// Package herdrcore is a read-only client for herdr-core, the server behind
// the Herdr desktop app.
//
// herdr-core (the hc program) serves JSON-RPC 2.0 on a Unix socket, one
// message per line. Tally only reads: Call refuses any method outside an
// allowlist. Snapshot turns herdr-core's terminals and the app's layout
// document into a herdr.Snapshot, so the rest of tally sees the same model
// whichever herdr it runs against.
package herdrcore

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

	"github.com/sroberts/tally/internal/herdr"
)

// Timeout bounds a whole request/response exchange.
const Timeout = 2 * time.Second

// Generation is the herdr-core protocol generation tally speaks.
const Generation = 1

// maxLine bounds one response line. terminal.list carries every terminal's
// screen, which tally never declares but still has to read past.
const maxLine = 16 << 20

// allowed lists every method Tally may call. Everything here only reads.
var allowed = map[string]bool{
	"hello":            true,
	"terminal.list":    true,
	"doc.get":          true,
	"events.subscribe": true,
}

// SocketPath resolves herdr-core's client socket the way hc does: HC_SOCKET,
// then the session's socket (HC_SESSION, default "default") under
// $XDG_RUNTIME_DIR/herdr-core, /run/user/<uid>/herdr-core when it exists, or
// else the session's state directory.
func SocketPath() (string, error) {
	return socketPath(os.Getenv, os.Getuid(), dirExists)
}

func socketPath(getenv func(string) string, uid int, exists func(string) bool) (string, error) {
	if p := getenv("HC_SOCKET"); p != "" {
		return p, nil
	}
	session := getenv("HC_SESSION")
	if session == "" {
		session = "default"
	}
	name := session + ".sock"
	if d := getenv("XDG_RUNTIME_DIR"); d != "" {
		return filepath.Join(d, "herdr-core", name), nil
	}
	if d := filepath.Join("/run/user", strconv.Itoa(uid)); exists(d) {
		return filepath.Join(d, "herdr-core", name), nil
	}
	home := getenv("HOME")
	if home == "" {
		return "", errors.New("herdr-core: $HOME is not set, so its socket can't be found (set HC_SOCKET)")
	}
	return filepath.Join(home, ".local", "state", "herdr-core", session, name), nil
}

func dirExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// Client calls herdr-core at one socket path. Every call dials afresh, so a
// core restart never leaves tally holding a dead connection.
type Client struct {
	Path string
}

// New returns a client for the socket at path.
func New(path string) *Client { return &Client{Path: path} }

// dialSocket opens herdr-core's socket; tests replace it.
var dialSocket = func(path string) (net.Conn, error) { return net.DialTimeout("unix", path, Timeout) }

// Call sends one request and decodes the response's result into out.
// Failures are a *herdr.OfflineError when the socket doesn't answer and a
// *herdr.Error when herdr-core refuses, so callers treat both herdrs alike.
func (c *Client) Call(method string, params any, out any) error {
	if !allowed[method] {
		return fmt.Errorf("herdr-core: method %q is not on Tally's read-only allowlist", method)
	}
	return call(c.Path, method, params, out)
}

func call(path, method string, params any, out any) error {
	conn, err := dial(path, method, params)
	if err != nil {
		return err
	}
	defer conn.Close()
	line, err := readLine(bufio.NewReaderSize(conn, 64*1024))
	if err != nil {
		return fmt.Errorf("herdr-core: reading %s response: %w", method, err)
	}
	result, err := decode(method, line)
	if err != nil || out == nil {
		return err
	}
	return json.Unmarshal(result, out)
}

// dial connects to herdr-core at path and writes one request. The
// connection's deadline is Timeout from now. Callers check their allowlist
// first.
func dial(path, method string, params any) (net.Conn, error) {
	if params == nil {
		params = struct{}{}
	}
	req, err := json.Marshal(struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Method  string `json:"method"`
		Params  any    `json:"params"`
	}{"2.0", 1, method, params})
	if err != nil {
		return nil, err
	}
	conn, err := dialSocket(path)
	if err != nil {
		return nil, &herdr.OfflineError{Path: path, Err: err}
	}
	conn.SetDeadline(time.Now().Add(Timeout))
	if _, err := conn.Write(append(req, '\n')); err != nil {
		conn.Close()
		return nil, &herdr.OfflineError{Path: path, Err: err}
	}
	return conn, nil
}

func readLine(r *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		chunk, err := r.ReadSlice('\n')
		line = append(line, chunk...)
		if len(line) > maxLine {
			return nil, fmt.Errorf("response longer than %d bytes", maxLine)
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		return line, err
	}
}

// decode returns a response line's result, or its error as a *herdr.Error
// whose Code is herdr-core's error kind (not_found, unknown_method, ...).
func decode(method string, line []byte) (json.RawMessage, error) {
	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Data    struct {
				Kind string `json:"kind"`
			} `json:"data"`
		} `json:"error"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("herdr-core: bad %s response: %w", method, err)
	}
	if e := resp.Error; e != nil {
		code := e.Data.Kind
		if code == "" {
			code = strconv.Itoa(e.Code)
		}
		return nil, &herdr.Error{Code: code, Message: e.Message}
	}
	return resp.Result, nil
}

// Ping checks that herdr-core answers and speaks tally's generation.
func (c *Client) Ping() error {
	var res struct {
		Generation int `json:"generation"`
	}
	if err := c.Call("hello", nil, &res); err != nil {
		return err
	}
	if res.Generation != Generation {
		return fmt.Errorf("herdr-core speaks protocol generation %d; tally speaks %d", res.Generation, Generation)
	}
	return nil
}
