// Package web serves the browser pad: a pad for the phone's home screen,
// as a web page.
//
// It is protocol v3 behind a password. A browser signs in once with the
// password set by tally web --set-password; tally keeps an argon2id hash of
// it and gives the browser a random cookie, storing only the cookie's
// SHA-256 in web.json. The page then opens a WebSocket to /ws and speaks v3
// minus the auth message: the cookie is the auth. Sessions run through the
// remote server's action table and write allowlist, so the browser can do
// what a paired client can and nothing more, and no titles, paths or
// terminal output leave the host.
//
// The listener is HTTPS only, with a Tailscale certificate for this
// machine's MagicDNS name by default, so browsers trust it without a
// warning. Failed sign-ins are limited per source address and overall,
// password checks run one at a time, and revoking a browser (or signing
// out, or changing the password) closes its session within a second.
package web

import (
	"bytes"
	"context"
	"crypto/tls"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/sroberts/tally/internal/remote"
)

// Port is the browser pad's default HTTPS port, next to the remote listener's 47824.
const Port = 47825

// Sign-in limits and cookie lifetime.
const (
	MaxFailures    = 5  // failed sign-ins per source address per FailWindow
	MaxAllFailures = 20 // failed sign-ins from everywhere per FailWindow
	FailWindow     = time.Minute
	CookieName     = "__Host-tally" // __Host-: Secure, Path=/, no Domain
	CookieAge      = 90 * 24 * time.Hour
	maxPassword    = 1024
)

//go:embed static/index.html static/login.html static/pad.css static/pad.js static/lighting.js
var static embed.FS

// Config holds the server's collaborators.
type Config struct {
	Store  Store
	Remote *remote.Server // runs the v3 sessions
	Certs  Certs
	Log    *slog.Logger

	RevokePoll time.Duration // reread web.json this often
	Now        func() time.Time
}

// Server is the browser pad's listener.
type Server struct {
	cfg     Config
	limiter *limiter
	login   *template.Template
	icons   map[string][]byte
	assets  http.Handler
	check   sync.Mutex // one argon2id check at a time: each takes 64 MiB

	mu    sync.Mutex
	conns map[*websocket.Conn]string // live sessions and their browser's token hash
}

// New returns a server.
func New(cfg Config) *Server {
	if cfg.RevokePoll == 0 {
		cfg.RevokePoll = time.Second
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	sub, _ := fs.Sub(static, "static")
	return &Server{
		cfg:     cfg,
		limiter: newLimiter(cfg.Now),
		login:   template.Must(template.ParseFS(static, "static/login.html")),
		icons:   map[string][]byte{"/icon-180.png": icon(180), "/icon-512.png": icon(512)},
		assets:  http.FileServer(http.FS(sub)),
		conns:   map[*websocket.Conn]string{},
	}
}

// Dir is the directory holding web.json.
func (s *Server) Dir() string { return s.cfg.Store.Dir }

// Handler routes every request. Pages and the WebSocket need a signed-in
// browser; the stylesheet, scripts and icons don't, so the sign-in page
// can use them.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.pad)
	mux.HandleFunc("GET /login", s.loginPage)
	mux.HandleFunc("POST /login", s.signIn)
	mux.HandleFunc("POST /logout", s.signOut)
	mux.HandleFunc("GET /ws", s.ws)
	mux.HandleFunc("GET /session", s.session)
	mux.HandleFunc("POST /install-code", s.installCode)
	mux.HandleFunc("GET /manifest.webmanifest", s.manifest)
	for path := range s.icons {
		mux.HandleFunc("GET "+path, s.icon)
	}
	for _, f := range []string{"/pad.css", "/pad.js", "/lighting.js"} {
		mux.Handle("GET "+f, s.assets)
	}
	return headers(mux)
}

// headers sets the security headers every response carries.
func headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; "+
			"manifest-src 'self'; connect-src 'self' wss://"+r.Host+"; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin") // no-referrer makes Chrome send Origin: null on our own POSTs
		// No HSTS: it would cover every port on the MagicDNS name, breaking
		// any plain-http service there, and this listener is HTTPS only anyway.
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// Serve accepts browsers on ln, a plain TCP listener, until ctx is done.
// It wraps ln in TLS itself.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	tc := &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: s.cfg.Certs.GetCertificate}
	srv := &http.Server{Handler: s.Handler(), TLSConfig: tc,
		ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 2 * time.Minute,
		ErrorLog:    slog.NewLogLogger(s.cfg.Log.Handler(), slog.LevelDebug),
		BaseContext: func(net.Listener) context.Context { return ctx }}
	go s.watchRevocations(ctx)

	done := make(chan error, 1)
	go func() { done <- srv.Serve(tls.NewListener(ln, tc)) }()
	select {
	case <-ctx.Done():
		s.closeWhere(func(string) bool { return true }, websocket.StatusGoingAway, "tally stopping")
		shut, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		srv.Shutdown(shut)
		return nil
	case err := <-done:
		return err
	}
}

// browser returns the signed-in browser making this request.
func (s *Server) browser(r *http.Request) (Browser, bool) {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return Browser{}, false
	}
	st, err := s.cfg.Store.Load()
	if err != nil {
		return Browser{}, false
	}
	b, ok := st.Match(c.Value)
	if !ok || b.Expired(s.cfg.Now()) {
		return Browser{}, false
	}
	return b, true
}

func (s *Server) setCookie(w http.ResponseWriter, value string, age time.Duration) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: value, Path: "/", MaxAge: int(age.Seconds()),
		Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
}

// pad serves the pad to a signed-in browser and sends anyone else to sign
// in. A ?code= from a QR code or a home screen launch signs the browser in
// first, then redirects so the code leaves the address bar.
func (s *Server) pad(w http.ResponseWriter, r *http.Request) {
	b, ok := s.browser(r)
	if code := r.URL.Query().Get("code"); code != "" {
		if !ok {
			switch s.redeem(w, r, code) {
			case redeemBlocked:
				s.renderLogin(w, http.StatusTooManyRequests, "too many tries. wait a minute")
				return
			case redeemRefused:
				http.Redirect(w, r, "/login?expired=1", http.StatusSeeOther)
				return
			}
		}
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if wrote, _ := s.cfg.Store.Touch(b.TokenSHA256, s.cfg.Now()); wrote {
		c, _ := r.Cookie(CookieName)
		s.setCookie(w, c.Value, CookieAge) // a pad in use stays signed in
	}
	s.serveFile(w, "static/index.html", "text/html; charset=utf-8")
}

type redeemResult int

const (
	redeemOK redeemResult = iota
	redeemRefused
	redeemBlocked
)

// redeem signs the browser in with a single-use code, counting a bad code
// as a failed sign-in from its address. Codes are 256 bits, so the overall
// limit doesn't apply: guessing one is hopeless, and counting them would
// let anyone lock everyone out.
func (s *Server) redeem(w http.ResponseWriter, r *http.Request, code string) redeemResult {
	ip := sourceIP(r)
	if s.limiter.blockedHere(ip) {
		return redeemBlocked
	}
	token, b, ok, err := s.cfg.Store.SignInWithCode(code, Label(r.UserAgent()), ip, s.cfg.Now())
	if err != nil {
		s.cfg.Log.Error("web.json", "err", err)
		return redeemRefused
	}
	if !ok {
		s.limiter.fail(ip)
		s.cfg.Log.Info("browser sign-in code refused", "from", ip)
		return redeemRefused
	}
	s.signedIn(w, b, token, ip, "code")
	return redeemOK
}

// signedIn logs a newly signed-in browser and sets its cookie.
func (s *Server) signedIn(w http.ResponseWriter, b Browser, token, ip, how string) {
	s.cfg.Log.Info("browser signed in", "browser", b.Label, "id", b.ID, "from", ip, "with", how)
	s.setCookie(w, token, CookieAge)
}

// installCode gives a signed-in page a code to put in its own address, so
// the copy saved to the home screen (which has its own cookies on iOS)
// signs itself in on first launch.
func (s *Server) installCode(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "cross-origin request refused", http.StatusForbidden)
		return
	}
	b, ok := s.browser(r)
	if !ok {
		http.Error(w, "sign in first", http.StatusUnauthorized)
		return
	}
	code, err := s.cfg.Store.IssueInstallCode(b.TokenSHA256, s.cfg.Now())
	if err != nil {
		http.Error(w, "can't save the code", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"code":%q}`, code)
}

func (s *Server) serveFile(w http.ResponseWriter, name, typ string) {
	b, err := static.ReadFile(name)
	if err != nil {
		http.Error(w, "missing", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", typ)
	w.Write(b)
}

type loginView struct {
	Error string
}

func (s *Server) renderLogin(w http.ResponseWriter, status int, msg string) {
	var buf bytes.Buffer
	if err := s.login.Execute(&buf, loginView{Error: msg}); err != nil {
		http.Error(w, "template", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	w.Write(buf.Bytes())
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.browser(r); ok {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	msg := ""
	if r.URL.Query().Get("expired") != "" {
		msg = "that sign-in code has expired or was used. run tally web for a new one"
	}
	s.renderLogin(w, http.StatusOK, msg)
}

// signIn checks the password and, when it matches, signs the browser in.
func (s *Server) signIn(w http.ResponseWriter, r *http.Request) {
	ip := sourceIP(r)
	if !sameOrigin(r) {
		http.Error(w, "cross-origin sign-in refused", http.StatusForbidden)
		return
	}
	if s.limiter.blocked(ip) {
		s.renderLogin(w, http.StatusTooManyRequests, "too many tries. wait a minute")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	password := r.PostFormValue("password")
	st, err := s.cfg.Store.Load()
	if err != nil {
		s.cfg.Log.Error("web.json", "err", err)
		s.renderLogin(w, http.StatusInternalServerError, "tally can't read its settings")
		return
	}
	if st.Password == "" {
		s.renderLogin(w, http.StatusServiceUnavailable, "no password set. run tally web --set-password")
		return
	}
	// Check the limit again once it's this request's turn, and count a
	// failure before the next check starts, so requests sent all at once
	// still get only MaxFailures guesses.
	s.check.Lock()
	if s.limiter.blocked(ip) {
		s.check.Unlock()
		s.renderLogin(w, http.StatusTooManyRequests, "too many tries. wait a minute")
		return
	}
	ok := password != "" && len(password) <= maxPassword && st.CheckPassword(password)
	if !ok {
		s.limiter.fail(ip)
	}
	s.check.Unlock()
	if !ok {
		s.cfg.Log.Info("browser sign-in failed", "from", ip)
		s.renderLogin(w, http.StatusUnauthorized, "wrong password")
		return
	}
	token, b, err := s.cfg.Store.SignInWithPassword(st.Password, Label(r.UserAgent()), ip, s.cfg.Now())
	if errors.Is(err, errPasswordChanged) {
		s.renderLogin(w, http.StatusConflict, "the password just changed. sign in again")
		return
	}
	if err != nil {
		s.cfg.Log.Error("web.json", "err", err)
		s.renderLogin(w, http.StatusInternalServerError, "tally can't save the sign-in")
		return
	}
	s.signedIn(w, b, token, ip, "password")
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) signOut(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "cross-origin sign-out refused", http.StatusForbidden)
		return
	}
	if b, ok := s.browser(r); ok {
		if err := s.cfg.Store.SignOut(b.TokenSHA256); err != nil {
			s.cfg.Log.Error("web.json", "err", err)
		}
		s.cfg.Log.Info("browser signed out", "browser", b.Label, "id", b.ID)
	}
	s.setCookie(w, "", -time.Second)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// session answers 204 for a signed-in browser and 401 otherwise, so the
// page can tell a refused WebSocket from a network failure.
func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.browser(r); !ok {
		http.Error(w, "sign in first", http.StatusUnauthorized)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ws upgrades a signed-in browser's WebSocket and runs a v3 session on it.
// websocket.Accept refuses an Origin other than this host.
func (s *Server) ws(w http.ResponseWriter, r *http.Request) {
	b, ok := s.browser(r)
	if !ok {
		http.Error(w, "sign in first", http.StatusUnauthorized)
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	s.mu.Lock()
	s.conns[conn] = b.TokenSHA256
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.conns, conn)
		s.mu.Unlock()
	}()
	s.cfg.Store.Touch(b.TokenSHA256, s.cfg.Now())
	ip := sourceIP(r)
	s.cfg.Log.Info("browser connected", "browser", b.Label, "id", b.ID, "from", ip)
	err = s.cfg.Remote.Attach(r.Context(), conn, "browser "+b.ID)
	s.cfg.Log.Info("browser disconnected", "browser", b.Label, "id", b.ID, "err", err)
}

// watchRevocations rereads web.json and closes the sessions of browsers
// that are no longer signed in.
func (s *Server) watchRevocations(ctx context.Context) {
	t := time.NewTicker(s.cfg.RevokePoll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		st, err := s.cfg.Store.Load()
		if err != nil {
			continue // a half-written file; next tick
		}
		s.closeWhere(func(h string) bool { return !st.Has(h) }, websocket.StatusPolicyViolation, "signed out")
	}
}

func (s *Server) closeWhere(match func(tokenSHA256 string) bool, code websocket.StatusCode, reason string) {
	s.mu.Lock()
	var gone []*websocket.Conn
	for c, h := range s.conns {
		if match(h) {
			gone = append(gone, c)
		}
	}
	s.mu.Unlock()
	for _, c := range gone {
		c.Close(code, reason)
	}
}

func (s *Server) manifest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/manifest+json")
	// No start_url: the home screen copy opens the address it was saved
	// from, which carries a sign-in code (see installCode).
	w.Write([]byte(`{"name":"tally","short_name":"tally","scope":"/","display":"standalone",` +
		`"orientation":"portrait","background_color":"#0f0e12","theme_color":"#000000",` +
		`"icons":[{"src":"/icon-180.png","sizes":"180x180","type":"image/png"},` +
		`{"src":"/icon-512.png","sizes":"512x512","type":"image/png"}]}`))
}

func (s *Server) icon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/png")
	w.Write(s.icons[r.URL.Path])
}

// sameOrigin reports whether a form post came from this site. Browsers send
// Origin on every POST; without one, Sec-Fetch-Site must say so.
func sameOrigin(r *http.Request) bool {
	if o := r.Header.Get("Origin"); o != "" {
		return o == "https://"+r.Host
	}
	site := r.Header.Get("Sec-Fetch-Site")
	return site == "same-origin" || site == "none"
}

func sourceIP(r *http.Request) string {
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	return ip
}

// Label names a browser from its User-Agent: "iphone safari", "mac chrome".
func Label(ua string) string {
	u := strings.ToLower(ua)
	device := "browser"
	for _, d := range []struct{ key, name string }{
		{"iphone", "iphone"}, {"ipad", "ipad"}, {"android", "android"},
		{"macintosh", "mac"}, {"windows", "windows"}, {"linux", "linux"},
	} {
		if strings.Contains(u, d.key) {
			device = d.name
			break
		}
	}
	app := ""
	switch {
	case strings.Contains(u, "firefox") || strings.Contains(u, "fxios"):
		app = "firefox"
	case strings.Contains(u, "edg"):
		app = "edge"
	case strings.Contains(u, "chrome") || strings.Contains(u, "crios"):
		app = "chrome"
	case strings.Contains(u, "safari"):
		app = "safari"
	}
	if app == "" {
		return device
	}
	return device + " " + app
}

// limiter counts failed sign-ins per source address, and in total, over
// FailWindow.
type limiter struct {
	now   func() time.Time
	mu    sync.Mutex
	fails map[string][]time.Time
	all   []time.Time
}

func newLimiter(now func() time.Time) *limiter {
	return &limiter{now: now, fails: map[string][]time.Time{}}
}

func (l *limiter) prune(ts []time.Time) []time.Time {
	cutoff := l.now().Add(-FailWindow)
	kept := ts[:0]
	for _, t := range ts {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	return kept
}

// blocked reports whether ip, or everyone, has used up its failures.
func (l *limiter) blocked(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.all = l.prune(l.all)
	return l.mine(ip) >= MaxFailures || len(l.all) >= MaxAllFailures
}

// blockedHere reports whether ip has used up its own failures, ignoring
// everyone else's.
func (l *limiter) blockedHere(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.mine(ip) >= MaxFailures
}

func (l *limiter) mine(ip string) int {
	mine := l.prune(l.fails[ip])
	if len(mine) == 0 {
		delete(l.fails, ip)
	} else {
		l.fails[ip] = mine
	}
	return len(mine)
}

func (l *limiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for other := range l.fails { // drop addresses whose failures have aged out
		if other != ip {
			l.mine(other)
		}
	}
	now := l.now()
	l.fails[ip] = append(l.prune(l.fails[ip]), now)
	l.all = append(l.prune(l.all), now)
}
