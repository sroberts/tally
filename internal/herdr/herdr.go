// Package herdr is a read-only client for herdr's local socket API.
//
// herdr serves newline-delimited JSON requests and responses on a Unix
// socket. Tally only reads: Call refuses any method outside an allowlist, so
// a bug here can light the wrong key but never type into a pane.
package herdr

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Timeout bounds a whole request/response exchange.
const Timeout = 2 * time.Second

// allowed lists every method Tally may call. Everything here only reads.
var allowed = map[string]bool{
	"ping":             true,
	"session.snapshot": true,
	"events.subscribe": true,
}

// SocketPath resolves herdr's socket the way the herdr CLI does:
// HERDR_SOCKET_PATH, then HERDR_SESSION, then the default session.
func SocketPath() (string, error) {
	if p := os.Getenv("HERDR_SOCKET_PATH"); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".config", "herdr")
	if s := os.Getenv("HERDR_SESSION"); s != "" {
		return filepath.Join(dir, "sessions", s, "herdr.sock"), nil
	}
	return filepath.Join(dir, "herdr.sock"), nil
}

// Error is an error response from herdr.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return "herdr: " + e.Code + ": " + e.Message }

// OfflineError means herdr's socket did not accept a connection or the
// request written to it.
type OfflineError struct {
	Path string
	Err  error
}

func (e *OfflineError) Error() string {
	return fmt.Sprintf("herdr is not reachable at %s: %v", e.Path, e.Err)
}

func (e *OfflineError) Unwrap() error { return e.Err }

// Client calls herdr at one socket path. herdr answers one request per
// connection and then closes it, so every call dials afresh.
type Client struct {
	Path string
}

// New returns a client for the socket at path.
func New(path string) *Client { return &Client{Path: path} }

// Call sends one request and decodes the response's result into out.
func (c *Client) Call(method string, params any, out any) error {
	conn, err := c.send(method, params)
	if err != nil {
		return err
	}
	defer conn.Close()
	line, err := bufio.NewReaderSize(conn, 64*1024).ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("herdr: reading %s response: %w", method, err)
	}
	result, err := decode(method, line)
	if err != nil || out == nil {
		return err
	}
	return json.Unmarshal(result, out)
}

// send checks method against the allowlist, dials herdr, and writes one
// request. The connection's deadline is Timeout from now.
func (c *Client) send(method string, params any) (net.Conn, error) {
	if !allowed[method] {
		return nil, fmt.Errorf("herdr: method %q is not on Tally's read-only allowlist", method)
	}
	return dial(c.Path, method, params)
}

// dial connects to herdr at path and writes one request. Callers check
// their allowlist first.
func dial(path, method string, params any) (net.Conn, error) {
	if params == nil {
		params = struct{}{}
	}
	req, err := json.Marshal(struct {
		ID     string `json:"id"`
		Method string `json:"method"`
		Params any    `json:"params"`
	}{"tally", method, params})
	if err != nil {
		return nil, err
	}

	conn, err := net.DialTimeout("unix", path, Timeout)
	if err != nil {
		return nil, &OfflineError{Path: path, Err: err}
	}
	conn.SetDeadline(time.Now().Add(Timeout))
	if _, err := conn.Write(append(req, '\n')); err != nil {
		conn.Close()
		return nil, &OfflineError{Path: path, Err: err}
	}
	return conn, nil
}

// decode returns a response line's result, or its error as an *Error.
func decode(method string, line []byte) (json.RawMessage, error) {
	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *Error          `json:"error"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("herdr: bad %s response: %w", method, err)
	}
	if resp.Error != nil {
		return nil, resp.Error
	}
	return resp.Result, nil
}

// Ping checks that herdr answers.
func (c *Client) Ping() error {
	var res struct {
		Type string `json:"type"`
	}
	if err := c.Call("ping", nil, &res); err != nil {
		return err
	}
	if res.Type != "pong" {
		return errors.New("herdr: ping answered with " + strconv.Quote(res.Type))
	}
	return nil
}

// Snapshot reads one authoritative session.snapshot.
func (c *Client) Snapshot() (*Snapshot, error) {
	var res struct {
		Snapshot Snapshot `json:"snapshot"`
	}
	if err := c.Call("session.snapshot", nil, &res); err != nil {
		return nil, err
	}
	return &res.Snapshot, nil
}
