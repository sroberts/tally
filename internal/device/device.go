// Package device finds the MacroPad's Tally data port and writes frames to it.
//
// The MacroPad exposes two USB CDC ports: the CircuitPython REPL console and
// the data port that boot.py enables. Only the data port answers a ping with
// hello, which is how discovery tells them apart.
package device

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime"
	"sort"
	"strings"
	"time"

	"go.bug.st/serial"
	"go.bug.st/serial/enumerator"
)

const (
	// AdafruitVID is Adafruit's USB vendor id.
	AdafruitVID = "239A"
	// MaxLine is the longest line either side accepts, newline included.
	MaxLine = 512
	// BaudRate is ignored by USB CDC; it is set for tooling compatibility.
	BaudRate = 115200
	// HandshakeTimeout is how long a port gets to answer ping with hello.
	HandshakeTimeout = time.Second
)

// ErrNoHello means the port never answered ping with hello.
var ErrNoHello = errors.New("no hello from device")

var ping = []byte(`{"t":"ping"}` + "\n")

// Hello is the device's reply to ping, also sent when it boots.
type Hello struct {
	V  int    `json:"v"`
	FW string `json:"fw"`
}

// EncodeFrame checks that raw is a JSON object with a "t" field and returns
// it compacted onto one newline-terminated line of at most MaxLine bytes.
func EncodeFrame(raw []byte) ([]byte, error) {
	var msg map[string]json.RawMessage
	if err := json.Unmarshal(raw, &msg); err != nil || msg == nil {
		return nil, errors.New("frame must be a JSON object")
	}
	var t string
	if err := json.Unmarshal(msg["t"], &t); err != nil || t == "" {
		return nil, errors.New(`frame needs a string "t" field`)
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return nil, err
	}
	buf.WriteByte('\n')
	if buf.Len() > MaxLine {
		return nil, fmt.Errorf("frame is %d bytes; the limit is %d including the newline", buf.Len(), MaxLine)
	}
	return buf.Bytes(), nil
}

// Handshake sends ping and waits up to timeout for a hello line, skipping
// anything else (the REPL console prints its own output). On ErrNoHello the
// caller must close rw, which also stops the reader goroutine.
func Handshake(rw io.ReadWriter, timeout time.Duration) (Hello, error) {
	lines := make(chan []byte)
	done := make(chan struct{})
	defer close(done)
	go func() {
		sc := bufio.NewScanner(rw)
		for sc.Scan() {
			line := append([]byte(nil), sc.Bytes()...)
			select {
			case lines <- line:
			case <-done:
				return
			}
		}
	}()

	if _, err := rw.Write(ping); err != nil {
		return Hello{}, err
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case line := <-lines:
			var msg struct {
				T string `json:"t"`
				Hello
			}
			if json.Unmarshal(bytes.TrimSpace(line), &msg) == nil && msg.T == "hello" {
				return msg.Hello, nil
			}
		case <-timer.C:
			return Hello{}, ErrNoHello
		}
	}
}

// Candidates lists serial ports that belong to Adafruit USB devices.
func Candidates() ([]string, error) {
	ports, err := enumerator.GetDetailedPortsList()
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, p := range ports {
		if p.IsUSB && strings.EqualFold(p.VID, AdafruitVID) {
			paths = append(paths, calloutPath(p.Name, runtime.GOOS))
		}
	}
	sort.Strings(paths)
	return paths, nil
}

// calloutPath swaps macOS's /dev/tty.* for /dev/cu.*: opening tty.* blocks
// waiting for carrier detect.
func calloutPath(name, goos string) string {
	if goos == "darwin" {
		return strings.Replace(name, "/dev/tty.", "/dev/cu.", 1)
	}
	return name
}

// Open opens a serial port at path.
func Open(path string) (serial.Port, error) {
	return serial.Open(path, &serial.Mode{BaudRate: BaudRate})
}

// Find opens each Adafruit port in turn and returns the first that answers
// ping with hello: the Tally data port.
func Find() (string, serial.Port, Hello, error) {
	paths, err := Candidates()
	if err != nil {
		return "", nil, Hello{}, err
	}
	if len(paths) == 0 {
		return "", nil, Hello{}, errors.New("no Adafruit USB serial ports found; is the MacroPad plugged in?")
	}
	for _, path := range paths {
		port, err := Open(path)
		if err != nil {
			continue
		}
		hello, err := Handshake(port, HandshakeTimeout)
		if err == nil {
			return path, port, hello, nil
		}
		port.Close()
	}
	return "", nil, Hello{}, fmt.Errorf("no Tally data port answered on %s; is boot.py installed and the pad hard-reset since?", strings.Join(paths, ", "))
}
