package web

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
)

// argon2id parameters: RFC 9106's second recommended option.
const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // KiB
	argonThreads = 4
	argonKeyLen  = 32
)

// MinPassword is the shortest password tally web --set-password accepts.
const MinPassword = 12

// touchEvery limits how often a session's last-seen time is written.
const touchEvery = time.Hour

// Store keeps the browser pad's password hash and signed-in browsers in
// web.json, next to remote.json.
type Store struct {
	Dir string
}

// Browser is one signed-in browser. Its cookie is never stored, only the
// SHA-256 of the cookie's 32 random bytes.
type Browser struct {
	ID          string    `json:"id"` // the hash's first 8 characters, for --revoke
	Label       string    `json:"label"`
	TokenSHA256 string    `json:"token_sha256"`
	From        string    `json:"from"` // the address it signed in from
	Created     time.Time `json:"created"`
	LastSeen    time.Time `json:"last_seen"`
}

// State is web.json.
type State struct {
	Password string    `json:"password,omitempty"` // argon2id, PHC string format
	Browsers []Browser `json:"browsers"`
}

// File is web.json's path.
func (s Store) File() string { return filepath.Join(s.Dir, "web.json") }

// Load reads web.json. A missing file is an empty state.
func (s Store) Load() (State, error) {
	var st State
	b, err := os.ReadFile(s.File())
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return st, fmt.Errorf("%s: %w", s.File(), err)
	}
	return st, nil
}

func (s Store) save(st State) error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.File() + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil { // WriteFile keeps an old file's mode
		return err
	}
	return os.Rename(tmp, s.File())
}

// SetPassword stores a new password and signs every browser out.
func (s Store) SetPassword(password string) error {
	if len([]rune(password)) < MinPassword {
		return fmt.Errorf("use at least %d characters", MinPassword)
	}
	st, err := s.Load()
	if err != nil {
		return err
	}
	st.Password = hashPassword(password)
	st.Browsers = nil
	return s.save(st)
}

func hashPassword(password string) string {
	salt := make([]byte, 16)
	rand.Read(salt)
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads,
		b64.EncodeToString(salt), b64.EncodeToString(key))
}

// CheckPassword reports whether password matches the stored hash. With no
// password set nothing matches.
func (st State) CheckPassword(password string) bool {
	parts := strings.Split(st.Password, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var v int
	var m uint32
	var t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[2], "v=%d", &v); err != nil || v != argon2.Version {
		return false
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil || m == 0 || t == 0 || p == 0 {
		return false
	}
	b64 := base64.RawStdEncoding
	salt, err1 := b64.DecodeString(parts[4])
	want, err2 := b64.DecodeString(parts[5])
	if err1 != nil || err2 != nil || len(want) == 0 {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// SignIn records a new browser and returns its cookie value, base64url
// without padding. The value is shown to the browser once and never stored.
func (s Store) SignIn(label, from string, now time.Time) (string, Browser, error) {
	st, err := s.Load()
	if err != nil {
		return "", Browser{}, err
	}
	raw := make([]byte, 32)
	rand.Read(raw)
	h := hash(raw)
	now = now.UTC().Truncate(time.Second)
	b := Browser{ID: h[:8], Label: label, TokenSHA256: h, From: from, Created: now, LastSeen: now}
	st.Browsers = append(st.Browsers, b)
	if err := s.save(st); err != nil {
		return "", Browser{}, err
	}
	return base64.RawURLEncoding.EncodeToString(raw), b, nil
}

// Match returns the browser this cookie value belongs to, comparing hashes
// in constant time.
func (st State) Match(token string) (Browser, bool) {
	h := TokenHash(token)
	if h == "" {
		return Browser{}, false
	}
	var found Browser
	ok := false
	for _, b := range st.Browsers {
		if subtle.ConstantTimeCompare([]byte(h), []byte(b.TokenSHA256)) == 1 {
			found, ok = b, true
		}
	}
	return found, ok
}

// Has reports whether a browser with this token hash is still signed in.
func (st State) Has(tokenSHA256 string) bool {
	for _, b := range st.Browsers {
		if b.TokenSHA256 == tokenSHA256 {
			return true
		}
	}
	return false
}

// Touch records that a browser was seen, at most once per touchEvery. It
// reports whether it wrote.
func (s Store) Touch(tokenSHA256 string, now time.Time) (bool, error) {
	st, err := s.Load()
	if err != nil {
		return false, err
	}
	for i, b := range st.Browsers {
		if b.TokenSHA256 == tokenSHA256 {
			if now.Sub(b.LastSeen) < touchEvery {
				return false, nil
			}
			st.Browsers[i].LastSeen = now.UTC().Truncate(time.Second)
			return true, s.save(st)
		}
	}
	return false, nil
}

// SignOut removes the browser with this token hash, if any.
func (s Store) SignOut(tokenSHA256 string) error {
	st, err := s.Load()
	if err != nil {
		return err
	}
	kept := st.Browsers[:0]
	for _, b := range st.Browsers {
		if b.TokenSHA256 != tokenSHA256 {
			kept = append(kept, b)
		}
	}
	st.Browsers = kept
	return s.save(st)
}

// Revoke signs out the browser whose id starts with prefix, or every
// browser for "all". A running tally serve closes its session within a
// second.
func (s Store) Revoke(prefix string) (int, error) {
	st, err := s.Load()
	if err != nil {
		return 0, err
	}
	if prefix == "all" {
		n := len(st.Browsers)
		st.Browsers = nil
		return n, s.save(st)
	}
	if prefix == "" {
		return 0, errors.New("give a browser id from tally web --list, or all")
	}
	var match []int
	for i, b := range st.Browsers {
		if strings.HasPrefix(b.ID, prefix) {
			match = append(match, i)
		}
	}
	switch len(match) {
	case 0:
		return 0, fmt.Errorf("no signed-in browser has id %q", prefix)
	case 1:
	default:
		return 0, fmt.Errorf("%q matches %d browsers; give more of the id", prefix, len(match))
	}
	st.Browsers = append(st.Browsers[:match[0]], st.Browsers[match[0]+1:]...)
	return 1, s.save(st)
}

func hash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// TokenHash returns the hash web.json keeps for a cookie value, or "" for a
// malformed one.
func TokenHash(token string) string {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 {
		return ""
	}
	return hash(raw)
}
