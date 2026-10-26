// Package remote serves paired phones: a TLS 1.3 WebSocket on port
// 47824 speaking protocol v3 (docs/protocol-v3.md).
//
// Trust comes from pairing, not the network: tally presents its pinned
// self-signed certificate, and the phone's first message must be auth with
// a token from tally pair. Nothing else is read before auth succeeds, auth
// must arrive within AuthTimeout, and each source IP gets MaxFailures
// failed attempts per minute. Revoking a phone drops its live session.
//
// The server subscribes to the daemon's updates exactly like the pad's link
// does: it keeps the latest, sends a state frame on change, on auth and
// every Heartbeat, and runs each session's actions in order through the
// action table, acking each one. Actions are never retried.
package remote

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/sroberts/tally/internal/actions"
	"github.com/sroberts/tally/internal/daemon"
	"github.com/sroberts/tally/internal/herdr"
	"github.com/sroberts/tally/internal/limit"
	"github.com/sroberts/tally/internal/pairing"
	"github.com/sroberts/tally/internal/slots"
)

// Protocol limits and timings.
const (
	MaxMessage  = 16 * 1024
	MaxFailures = 5 // failed auths per source IP per FailWindow
	FailWindow  = time.Minute
)

// Config holds the server's identity, collaborators and timings.
type Config struct {
	Store    pairing.Store
	Slots    int
	HostName string
	HostID   string
	Version  string // tally's version, for hello

	// Writer makes the herdr calls; Snapshot reads herdr for each action.
	Writer   actions.Writer
	Snapshot func() (*herdr.Snapshot, error)

	Bonjour bool // advertise _tally._tcp on the local network
	Log     *slog.Logger

	AuthTimeout time.Duration // auth must arrive this soon after connecting
	Heartbeat   time.Duration // resend state and ping this often
	RevokePoll  time.Duration // reread remote.json this often
	Now         func() time.Time
}

// Defaults fills the spec's timings into cfg.
func Defaults(cfg Config) Config {
	if cfg.AuthTimeout == 0 {
		cfg.AuthTimeout = 2 * time.Second
	}
	if cfg.Heartbeat == 0 {
		cfg.Heartbeat = 5 * time.Second
	}
	if cfg.RevokePoll == 0 {
		cfg.RevokePoll = time.Second
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Slots == 0 {
		cfg.Slots = slots.DefaultSlots
	}
	return cfg
}

// Server is the remote listener.
type Server struct {
	cfg     Config
	cert    tls.Certificate
	table   *actions.Table
	limiter *limiter

	mu       sync.Mutex
	latest   daemon.Update
	key      string // the latest frame minus its timers, to spot changes
	seq      uint32
	sessions map[*session]struct{}
}

// New returns a server presenting cert.
func New(cfg Config, cert tls.Certificate) *Server {
	cfg = Defaults(cfg)
	s := &Server{cfg: cfg, cert: cert, limiter: newLimiter(cfg.Now), sessions: map[*session]struct{}{}}
	s.table = &actions.Table{W: cfg.Writer, Snapshot: cfg.Snapshot, Tracked: s.tracked}
	return s
}

// Publish takes the daemon's latest update. It never blocks: sessions are
// poked and send the new frame from their own goroutines.
func (s *Server) Publish(u daemon.Update) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.latest = u
	f := s.frameLocked(time.Time{}) // timers read 0: a ticking wait is not a change
	f.Seq = 0
	key := string(f.JSON())
	if key == s.key {
		return
	}
	s.key = key
	s.seq++
	for ss := range s.sessions {
		ss.poke()
	}
}

// Store is where the server reads paired phones.
func (s *Server) Store() pairing.Store { return s.cfg.Store }

func (s *Server) tracked() slots.Tracked {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.latest.Tracked
}

// Frame is the current v3 state frame, timed as of now.
func (s *Server) Frame() slots.FrameV3 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.frameLocked(s.cfg.Now())
}

func (s *Server) frameLocked(now time.Time) slots.FrameV3 {
	if !s.latest.Herdr {
		return slots.OfflineV3(s.cfg.Slots, s.seq)
	}
	return s.latest.State.FrameV3(s.seq, s.latest.Tracked, now)
}

// TLSConfig is the listener's TLS configuration: TLS 1.3 only, tally's
// certificate, no client certificates.
func (s *Server) TLSConfig() *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{s.cert}}
}

// Serve accepts remote connections on ln, a plain TCP listener, until ctx
// is done. It wraps ln in TLS itself.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	srv := &http.Server{Handler: http.HandlerFunc(s.handle), TLSConfig: s.TLSConfig(),
		ReadHeaderTimeout: s.cfg.AuthTimeout, ErrorLog: slog.NewLogLogger(s.cfg.Log.Handler(), slog.LevelDebug),
		BaseContext: func(net.Listener) context.Context { return ctx }}

	if s.cfg.Bonjour {
		if stop, err := advertise(s.cfg.HostID, ln.Addr(), s.cfg.Log); err != nil {
			s.cfg.Log.Info("bonjour advertisement failed", "err", err)
		} else {
			defer stop()
		}
	}
	go s.watchRevocations(ctx)

	done := make(chan error, 1)
	go func() { done <- srv.Serve(tls.NewListener(ln, srv.TLSConfig)) }()
	select {
	case <-ctx.Done():
		s.closeAll(websocket.StatusGoingAway, "tally stopping")
		shut, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		srv.Shutdown(shut)
		return nil
	case err := <-done:
		return err
	}
}

// ListenAndServe listens on addr (":47824" for every interface) and serves.
func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s.cfg.Log.Info("remote listener", "addr", ln.Addr().String())
	return s.Serve(ctx, ln)
}

func (s *Server) closeAll(code websocket.StatusCode, reason string) {
	s.mu.Lock()
	all := make([]*session, 0, len(s.sessions))
	for ss := range s.sessions {
		all = append(all, ss)
	}
	s.mu.Unlock()
	for _, ss := range all {
		ss.conn.Close(code, reason)
	}
}

// watchRevocations rereads remote.json and drops sessions whose phone is
// no longer paired.
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
		s.mu.Lock()
		var gone []*session
		for ss := range s.sessions {
			if ss.tokenHash != "" && !st.Has(ss.tokenHash) { // "" is a browser; package web revokes those
				gone = append(gone, ss)
			}
		}
		s.mu.Unlock()
		for _, ss := range gone {
			s.cfg.Log.Info("phone revoked; closing its session", "phone", ss.label)
			ss.conn.Close(websocket.StatusPolicyViolation, "revoked")
		}
	}
}

// handle runs one connection: rate limit, upgrade, auth, then the session.
func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if s.limiter.blocked(ip) {
		http.Error(w, "too many failed attempts", http.StatusTooManyRequests)
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(MaxMessage)

	label, hash, ok := s.auth(r.Context(), conn)
	if !ok {
		s.limiter.fail(ip)
		s.cfg.Log.Info("auth failed", "from", ip)
		conn.Close(websocket.StatusPolicyViolation, "auth failed")
		return
	}
	s.cfg.Log.Info("phone connected", "phone", label, "from", ip)
	ss := &session{s: s, conn: conn, label: label, tokenHash: hash, wake: make(chan struct{}, 1)}
	err = ss.run(r.Context())
	s.cfg.Log.Info("phone disconnected", "phone", label, "err", err)
}

// Attach runs a session for a client that authenticated some other way:
// the browser pad, which signs in with a password and a cookie. It skips
// the auth message and starts with hello. The caller closes conn when the
// client's sign-in is revoked; watchRevocations leaves these sessions alone.
func (s *Server) Attach(ctx context.Context, conn *websocket.Conn, label string) error {
	conn.SetReadLimit(MaxMessage)
	ss := &session{s: s, conn: conn, label: label, wake: make(chan struct{}, 1)}
	return ss.run(ctx)
}

// auth reads the first message, which must be a valid auth within
// AuthTimeout.
func (s *Server) auth(ctx context.Context, conn *websocket.Conn) (label, hash string, ok bool) {
	actx, cancel := context.WithTimeout(ctx, s.cfg.AuthTimeout)
	defer cancel()
	typ, data, err := conn.Read(actx)
	if err != nil || typ != websocket.MessageText {
		return "", "", false
	}
	var msg struct {
		T     string `json:"t"`
		V     int    `json:"v"`
		Token string `json:"token"`
	}
	if json.Unmarshal(data, &msg) != nil || msg.T != "auth" || msg.V != pairing.ProtocolVersion {
		return "", "", false
	}
	st, err := s.cfg.Store.Load()
	if err != nil {
		return "", "", false
	}
	label, ok = st.Match(msg.Token)
	return label, pairing.TokenHash(msg.Token), ok
}

// Hello is the host's first message after auth.
type Hello struct {
	T      string `json:"t"`
	V      int    `json:"v"`
	Host   string `json:"host"`
	HostID string `json:"host_id"`
	Tally  string `json:"tally"`
}

// Ack answers one action.
type Ack struct {
	T   string `json:"t"`
	ID  int64  `json:"id"`
	OK  bool   `json:"ok"`
	Err string `json:"err,omitempty"`
	Msg string `json:"msg,omitempty"` // herdr's text for herdr_error
}

// Action is one action message from the phone.
type Action struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Target string `json:"target"`
	Slot   int    `json:"slot"`
	Text   string `json:"text"`
}

type session struct {
	s         *Server
	conn      *websocket.Conn
	label     string
	tokenHash string
	wake      chan struct{}
}

func (ss *session) poke() {
	select {
	case ss.wake <- struct{}{}:
	default:
	}
}

// run serves an authenticated session until the phone leaves or ctx ends.
// One goroutine reads, one runs actions in order, and this one writes.
func (ss *session) run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s := ss.s
	s.mu.Lock()
	s.sessions[ss] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.sessions, ss)
		s.mu.Unlock()
	}()

	queue := make(chan Action, 16)
	acks := make(chan Ack, 16)
	readErr := make(chan error, 1)
	go func() { readErr <- ss.read(ctx, queue, acks) }()
	go ss.work(ctx, queue, acks)

	write := func(v any) error {
		b, _ := json.Marshal(v)
		wctx, cancel := context.WithTimeout(ctx, s.cfg.Heartbeat)
		defer cancel()
		return ss.conn.Write(wctx, websocket.MessageText, b)
	}
	if err := write(Hello{T: "hello", V: pairing.ProtocolVersion, Host: s.cfg.HostName, HostID: s.cfg.HostID, Tally: s.cfg.Version}); err != nil {
		return err
	}
	if err := write(s.Frame()); err != nil {
		return err
	}

	tick := time.NewTicker(s.cfg.Heartbeat)
	defer tick.Stop()
	for {
		var err error
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err = <-readErr:
			return err
		case <-ss.wake:
			err = write(s.Frame())
		case a := <-acks:
			err = write(a)
		case <-tick.C:
			if err = write(s.Frame()); err == nil {
				go ss.ping(ctx)
			}
		}
		if err != nil {
			return err
		}
	}
}

// ping closes the session when the phone stops answering pings.
func (ss *session) ping(ctx context.Context) {
	pctx, cancel := context.WithTimeout(ctx, ss.s.cfg.Heartbeat)
	defer cancel()
	if err := ss.conn.Ping(pctx); err != nil && ctx.Err() == nil {
		ss.conn.Close(websocket.StatusGoingAway, "no pong")
	}
}

// read decodes the phone's messages, queueing actions. Unknown message
// types and fields are ignored, as the protocol requires.
func (ss *session) read(ctx context.Context, queue chan<- Action, acks chan<- Ack) error {
	for {
		typ, data, err := ss.conn.Read(ctx)
		if err != nil {
			return err
		}
		if typ != websocket.MessageText {
			continue
		}
		var msg struct {
			T string `json:"t"`
			Action
		}
		if json.Unmarshal(data, &msg) != nil || msg.T != "action" {
			continue
		}
		select {
		case queue <- msg.Action:
		default:
			// The phone drops anything older than 500 ms anyway. Once the
			// session ends nobody reads acks, so don't wait for it.
			select {
			case acks <- Ack{T: "ack", ID: msg.ID, Err: actions.HerdrError, Msg: "busy"}:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

// work runs queued actions one at a time, in arrival order.
func (ss *session) work(ctx context.Context, queue <-chan Action, acks chan<- Ack) {
	for {
		var a Action
		select {
		case <-ctx.Done():
			return
		case a = <-queue:
		}
		err := ss.s.table.Do(actions.Request{Name: a.Name, Target: a.Target, Slot: a.Slot, Text: a.Text})
		ack := Ack{T: "ack", ID: a.ID, OK: err == nil}
		var ae *actions.Error
		if errors.As(err, &ae) {
			ack.Err = ae.Code
			if ae.Code == actions.HerdrError {
				ack.Msg = ae.Message
			}
		} else if err != nil {
			ack.Err, ack.Msg = actions.HerdrError, actions.CallFailed
		}
		if err != nil && (ae == nil || ae.Err != nil) {
			// The detail stays here: the client gets fixed text.
			ss.s.cfg.Log.Info("action failed", "phone", ss.label, "name", a.Name, "err", err)
		}
		ss.s.cfg.Log.Debug("action", "phone", ss.label, "name", a.Name, "target", a.Target, "slot", a.Slot, "ok", ack.OK, "err", ack.Err)
		select {
		case acks <- ack:
		case <-ctx.Done():
			return
		}
	}
}

// limiter counts failed auths per source IP over FailWindow.
type limiter struct{ fails *limit.Window }

func newLimiter(now func() time.Time) *limiter {
	return &limiter{fails: limit.New(FailWindow, now)}
}

func (l *limiter) blocked(ip string) bool { return l.fails.Count(ip) >= MaxFailures }

func (l *limiter) fail(ip string) { l.fails.Add(ip) }

// port returns addr's TCP port.
func port(addr net.Addr) int {
	_, p, _ := net.SplitHostPort(addr.String())
	n, _ := strconv.Atoi(p)
	return n
}
