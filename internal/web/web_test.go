package web

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/sroberts/tally/internal/actions"
	"github.com/sroberts/tally/internal/daemon"
	"github.com/sroberts/tally/internal/herdr"
	"github.com/sroberts/tally/internal/pairing"
	"github.com/sroberts/tally/internal/remote"
	"github.com/sroberts/tally/internal/slots"
)

const password = "correct horse battery"

func TestPasswordHashAndCheck(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	if err := s.SetPassword("short"); err == nil {
		t.Fatal("a short password was accepted")
	}
	if err := s.SetPassword(password); err != nil {
		t.Fatal(err)
	}
	st, _ := s.Load()
	if !strings.HasPrefix(st.Password, "$argon2id$v=19$m=65536,t=3,p=4$") {
		t.Fatalf("hash %q", st.Password)
	}
	if strings.Contains(st.Password, password) {
		t.Fatal("password stored in the clear")
	}
	if !st.CheckPassword(password) || st.CheckPassword(password+"x") || st.CheckPassword("") {
		t.Fatal("check")
	}
	if (State{}).CheckPassword(password) {
		t.Fatal("no password set must match nothing")
	}
	fi, _ := os.Stat(s.File())
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("web.json mode %v", fi.Mode().Perm())
	}
}

func TestSettingThePasswordSignsEveryoneOut(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	s.SetPassword(password)
	tok, _, _ := s.SignIn("iphone safari", "100.64.0.2", time.Now())
	s.SetPassword(password + "2")
	st, _ := s.Load()
	if _, ok := st.Match(tok); ok {
		t.Fatal("old cookie still matches")
	}
}

func TestSignInMatchAndRevoke(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	a, ba, err := s.SignIn("iphone safari", "100.64.0.2", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	b, _, _ := s.SignIn("mac chrome", "100.64.0.3", time.Now())
	st, _ := s.Load()
	if got, ok := st.Match(a); !ok || got.Label != "iphone safari" || got.ID != ba.ID {
		t.Fatalf("match a: %+v %v", got, ok)
	}
	if strings.Contains(mustRead(t, s.File()), a) {
		t.Fatal("cookie value stored")
	}
	if _, ok := st.Match("not-a-token"); ok {
		t.Fatal("junk matched")
	}
	if _, err := s.Revoke(""); err == nil {
		t.Fatal("empty id revoked")
	}
	if _, err := s.Revoke("zzzz"); err == nil {
		t.Fatal("unknown id revoked")
	}
	if n, err := s.Revoke(ba.ID[:4]); err != nil || n != 1 {
		t.Fatalf("revoke: %d %v", n, err)
	}
	st, _ = s.Load()
	if _, ok := st.Match(a); ok {
		t.Fatal("revoked browser still matches")
	}
	if _, ok := st.Match(b); !ok {
		t.Fatal("other browser signed out")
	}
	if n, _ := s.Revoke("all"); n != 1 {
		t.Fatalf("revoke all: %d", n)
	}
}

func TestTouchWritesAtMostHourly(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	t0 := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	_, b, _ := s.SignIn("x", "y", t0)
	if wrote, _ := s.Touch(b.TokenSHA256, t0.Add(10*time.Minute)); wrote {
		t.Fatal("touched within the hour")
	}
	if wrote, _ := s.Touch(b.TokenSHA256, t0.Add(2*time.Hour)); !wrote {
		t.Fatal("not touched after two hours")
	}
	st, _ := s.Load()
	if !st.Browsers[0].LastSeen.Equal(t0.Add(2 * time.Hour)) {
		t.Fatalf("last seen %v", st.Browsers[0].LastSeen)
	}
}

func TestLabel(t *testing.T) {
	for ua, want := range map[string]string{
		"Mozilla/5.0 (iPhone; CPU iPhone OS 26_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.0 Mobile/15E148 Safari/604.1": "iphone safari",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Safari/537.36":                       "mac chrome",
		"Mozilla/5.0 (Linux; Android 15) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Mobile Safari/537.36":                                "android chrome",
		"curl/8.7.1": "browser",
	} {
		if got := Label(ua); got != want {
			t.Errorf("%q: got %q, want %q", ua, got, want)
		}
	}
}

func TestLimiter(t *testing.T) {
	now := time.Unix(0, 0)
	l := newLimiter(func() time.Time { return now })
	for range MaxFailures {
		l.fail("a")
	}
	if !l.blocked("a") || l.blocked("b") {
		t.Fatal("per-address limit")
	}
	now = now.Add(FailWindow + time.Second)
	if l.blocked("a") {
		t.Fatal("limit outlived its window")
	}
	for i := range MaxAllFailures {
		l.fail(string(rune('a' + i)))
	}
	if !l.blocked("fresh") {
		t.Fatal("overall limit")
	}
}

// --- HTTP ---------------------------------------------------------------------

type harness struct {
	t     *testing.T
	store Store
	srv   *Server
	rs    *remote.Server
	addr  string
	fp    string
	rec   *actions.Recorder
}

func start(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	ps := pairing.Store{Dir: dir}
	cert, fp, err := ps.Certificate("test")
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, store: Store{Dir: dir}, fp: fp, rec: &actions.Recorder{}}
	if err := h.store.SetPassword(password); err != nil {
		t.Fatal(err)
	}
	focus := "w1:p2"
	snap := &herdr.Snapshot{FocusedPaneID: &focus,
		Workspaces: []herdr.Workspace{{ID: "w1", Label: "vibe-macro"}},
		Agents: []herdr.Agent{
			{Kind: "claude", PaneID: "w1:p1", WorkspaceID: "w1", Status: "working"},
			{Kind: "claude", PaneID: "w1:p2", WorkspaceID: "w1", Status: "blocked"},
		}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h.rs = remote.New(remote.Config{Store: ps, Slots: 6, HostName: "mbp", HostID: "7f3c", Version: "test",
		Writer: h.rec, Snapshot: func() (*herdr.Snapshot, error) { return snap, nil }, Log: log,
		Heartbeat: time.Hour, RevokePoll: 20 * time.Millisecond}, cert)
	var tr slots.Tracker
	h.rs.Publish(daemon.Update{Herdr: true, Snapshot: snap, State: slots.Compute(snap, 6), Tracked: tr.Observe(snap, time.Now())})
	h.srv = New(Config{Store: h.store, Remote: h.rs, Certs: Static{Cert: cert}, Log: log, RevokePoll: 20 * time.Millisecond})

	ctx, cancel := context.WithCancel(context.Background())
	var done []chan struct{}
	serve := func(f func(context.Context, net.Listener) error) string {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		d := make(chan struct{})
		done = append(done, d)
		go func() { f(ctx, ln); close(d) }()
		return ln.Addr().String()
	}
	h.addr = serve(h.srv.Serve)
	serve(h.rs.Serve) // the phones' listener too: its revocation watch must leave browsers alone
	t.Cleanup(func() {
		cancel()
		for _, d := range done {
			<-d
		}
	})
	return h
}

func (h *harness) client() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, Transport: &http.Transport{TLSClientConfig: remote.PinnedTLS(h.fp)},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func (h *harness) url(path string) string { return "https://" + h.addr + path }

func (h *harness) login(c *http.Client, pw string, origin string) *http.Response {
	h.t.Helper()
	req, _ := http.NewRequest("POST", h.url("/login"), strings.NewReader(url.Values{"password": {pw}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 26_0 like Mac OS X) Safari/604.1")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	resp, err := c.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func (h *harness) get(c *http.Client, path string) *http.Response {
	h.t.Helper()
	resp, err := c.Get(h.url(path))
	if err != nil {
		h.t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func (h *harness) dial(c *http.Client, origin string) (*websocket.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	hdr := http.Header{}
	if origin != "" {
		hdr.Set("Origin", origin)
	}
	conn, _, err := websocket.Dial(ctx, "wss://"+h.addr+"/ws", &websocket.DialOptions{HTTPClient: c, HTTPHeader: hdr})
	return conn, err
}

func next(t *testing.T, c *websocket.Conn) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, b, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	json.Unmarshal(b, &m)
	return m
}

func TestSignedOutBrowsersGoToSignIn(t *testing.T) {
	h := start(t)
	c := h.client()
	if r := h.get(c, "/"); r.StatusCode != http.StatusSeeOther || r.Header.Get("Location") != "/login" {
		t.Fatalf("/: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	if r := h.get(c, "/session"); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("/session: %d", r.StatusCode)
	}
	if _, err := h.dial(c, ""); err == nil {
		t.Fatal("websocket without a cookie")
	}
	r := h.get(c, "/login")
	if r.StatusCode != 200 {
		t.Fatalf("/login: %d", r.StatusCode)
	}
	csp := r.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'self'") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("csp %q", csp)
	}
	for _, p := range []string{"/pad.css", "/pad.js", "/lighting.js", "/icon-180.png", "/manifest.webmanifest"} {
		if r := h.get(c, p); r.StatusCode != 200 {
			t.Errorf("%s: %d", p, r.StatusCode)
		}
	}
	if r := h.get(c, "/lighting_test.mjs"); r.StatusCode != http.StatusNotFound {
		t.Errorf("test file served: %d", r.StatusCode)
	}
}

func TestSignInRules(t *testing.T) {
	h := start(t)
	c := h.client()
	origin := "https://" + h.addr
	if r := h.login(c, password, ""); r.StatusCode != http.StatusForbidden {
		t.Fatalf("no origin: %d", r.StatusCode)
	}
	if r := h.login(c, password, "https://evil.example"); r.StatusCode != http.StatusForbidden {
		t.Fatalf("cross origin: %d", r.StatusCode)
	}
	for i := range MaxFailures {
		if r := h.login(c, "wrong password!", origin); r.StatusCode != http.StatusUnauthorized {
			t.Fatalf("wrong %d: %d", i, r.StatusCode)
		}
	}
	if r := h.login(c, password, origin); r.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("after %d failures: %d", MaxFailures, r.StatusCode)
	}
}

func TestSignInSetsAStrictCookie(t *testing.T) {
	h := start(t)
	c := h.client()
	r := h.login(c, password, "https://"+h.addr)
	if r.StatusCode != http.StatusSeeOther || r.Header.Get("Location") != "/" {
		t.Fatalf("login: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	set := r.Header.Get("Set-Cookie")
	for _, want := range []string{CookieName + "=", "Path=/", "Secure", "HttpOnly", "SameSite=Strict", "Max-Age=7776000"} {
		if !strings.Contains(set, want) {
			t.Errorf("Set-Cookie %q lacks %q", set, want)
		}
	}
	if strings.Contains(set, "Domain") {
		t.Error("cookie has a Domain")
	}
	if r := h.get(c, "/"); r.StatusCode != 200 {
		t.Fatalf("/ signed in: %d", r.StatusCode)
	}
	if r := h.get(c, "/session"); r.StatusCode != http.StatusNoContent {
		t.Fatalf("/session: %d", r.StatusCode)
	}
	st, _ := h.store.Load()
	if len(st.Browsers) != 1 || st.Browsers[0].Label != "iphone safari" || st.Browsers[0].From != "127.0.0.1" {
		t.Fatalf("browsers %+v", st.Browsers)
	}
}

func TestWebSocketSpeaksV3AndActs(t *testing.T) {
	h := start(t)
	c := h.client()
	h.login(c, password, "https://"+h.addr)
	if _, err := h.dial(c, "https://evil.example"); err == nil {
		t.Fatal("cross-origin websocket accepted")
	}
	conn, err := h.dial(c, "https://"+h.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	if m := next(t, conn); m["t"] != "hello" || m["host"] != "mbp" {
		t.Fatalf("hello: %v", m)
	}
	st := next(t, conn)
	if st["t"] != "state" || st["fpane"] != "w1:p2" {
		t.Fatalf("state: %v", st)
	}
	conn.Write(context.Background(), websocket.MessageText, []byte(`{"t":"action","id":1,"name":"approve_once","target":"w1:p2"}`))
	if m := next(t, conn); m["t"] != "ack" || m["ok"] != true {
		t.Fatalf("ack: %v", m)
	}
	if len(h.rec.Calls) == 0 {
		t.Fatal("no herdr call")
	}
	// The phones' revocation watch has polled many times by now; the browser is still here.
	time.Sleep(100 * time.Millisecond)
	conn.Write(context.Background(), websocket.MessageText, []byte(`{"t":"action","id":2,"name":"deny","target":"w9:p9"}`))
	if m := next(t, conn); m["t"] != "ack" || m["err"] != "target_gone" {
		t.Fatalf("ack 2: %v", m)
	}
}

func TestRevokingABrowserClosesItsSession(t *testing.T) {
	h := start(t)
	c := h.client()
	h.login(c, password, "https://"+h.addr)
	conn, err := h.dial(c, "https://"+h.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	next(t, conn)
	next(t, conn)
	if _, err := h.store.Revoke("all"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _, err = conn.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Fatalf("read after revoke: %v", err)
	}
	if r := h.get(c, "/"); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("/ after revoke: %d", r.StatusCode)
	}
}

func TestSignOut(t *testing.T) {
	h := start(t)
	c := h.client()
	h.login(c, password, "https://"+h.addr)
	req, _ := http.NewRequest("POST", h.url("/logout"), nil)
	req.Header.Set("Origin", "https://"+h.addr)
	r, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusSeeOther || r.Header.Get("Location") != "/login" {
		t.Fatalf("logout: %d", r.StatusCode)
	}
	if st, _ := h.store.Load(); len(st.Browsers) != 0 {
		t.Fatalf("still signed in: %+v", st.Browsers)
	}
}

func TestNoPasswordSet(t *testing.T) {
	h := start(t)
	os.WriteFile(h.store.File(), []byte(`{"browsers":[]}`), 0o600)
	if r := h.login(h.client(), password, "https://"+h.addr); r.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("login with no password set: %d", r.StatusCode)
	}
}

// TestLightingJS runs the browser pad's lighting tests (the same cases as
// vibepad's LightingTests.swift) when node is installed.
func TestLightingJS(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	out, err := exec.Command(node, "--test", filepath.Join("static", "lighting_test.mjs")).CombinedOutput()
	if err != nil {
		t.Fatalf("%s\n%v", out, err)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestEnsurePasswordOnlyWhenUnset(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	pw, err := s.EnsurePassword()
	if err != nil || len(pw) != 23 {
		t.Fatalf("generated %q, %v", pw, err)
	}
	st, _ := s.Load()
	if !st.CheckPassword(pw) {
		t.Fatal("generated password doesn't check")
	}
	if again, _ := s.EnsurePassword(); again != "" {
		t.Fatal("replaced an existing password")
	}
}

func TestCodesAreSingleUseAndExpire(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	code, err := s.IssueCode(QRCodeTTL, now)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(mustRead(t, s.File()), code) {
		t.Fatal("code stored in the clear")
	}
	if ok, _ := s.RedeemCode(code, now.Add(time.Minute)); !ok {
		t.Fatal("fresh code refused")
	}
	if ok, _ := s.RedeemCode(code, now.Add(time.Minute)); ok {
		t.Fatal("code used twice")
	}
	old, _ := s.IssueCode(QRCodeTTL, now)
	if ok, _ := s.RedeemCode(old, now.Add(QRCodeTTL+time.Second)); ok {
		t.Fatal("expired code accepted")
	}
	if ok, _ := s.RedeemCode("junk", now); ok {
		t.Fatal("junk accepted")
	}
	c, _ := s.IssueCode(QRCodeTTL, now)
	s.SetPassword(password)
	if ok, _ := s.RedeemCode(c, now); ok {
		t.Fatal("a new password must void outstanding codes")
	}
}

func TestCodeLinkSignsInOnce(t *testing.T) {
	h := start(t)
	code, _ := h.store.IssueCode(QRCodeTTL, time.Now())
	c := h.client()
	r := h.get(c, "/?code="+code)
	if r.StatusCode != http.StatusSeeOther || r.Header.Get("Location") != "/" {
		t.Fatalf("code link: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	if r := h.get(c, "/"); r.StatusCode != 200 {
		t.Fatalf("/ after code: %d", r.StatusCode)
	}
	other := h.client()
	if r := h.get(other, "/?code="+code); r.Header.Get("Location") != "/login?expired=1" {
		t.Fatalf("reused code: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
}

func TestInstallCodeForTheHomeScreen(t *testing.T) {
	h := start(t)
	post := func(c *http.Client) *http.Response {
		req, _ := http.NewRequest("POST", h.url("/install-code"), nil)
		req.Header.Set("Origin", "https://"+h.addr)
		r, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	if r := post(h.client()); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("signed out: %d", r.StatusCode)
	}
	safari := h.client()
	h.login(safari, password, "https://"+h.addr)
	r := post(safari)
	var body struct{ Code string }
	json.NewDecoder(r.Body).Decode(&body)
	r.Body.Close()
	if body.Code == "" {
		t.Fatalf("no code: %d", r.StatusCode)
	}
	// Safari reloading its own address keeps the code for the home screen copy.
	if r := h.get(safari, "/?code="+body.Code); r.Header.Get("Location") != "/" {
		t.Fatalf("signed-in reload: %s", r.Header.Get("Location"))
	}
	homeScreen := h.client() // its own cookie jar, as on iOS
	if r := h.get(homeScreen, "/?code="+body.Code); r.Header.Get("Location") != "/" {
		t.Fatalf("home screen launch: %s", r.Header.Get("Location"))
	}
	if r := h.get(homeScreen, "/"); r.StatusCode != 200 {
		t.Fatalf("home screen copy not signed in: %d", r.StatusCode)
	}
}
