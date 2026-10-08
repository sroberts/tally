package device

import (
	"bufio"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/term"
)

// serialPair returns two ends of a pty standing in for a serial link: host
// is what tally opens, dev is the MacroPad. The host end is in raw mode, as
// go.bug.st/serial leaves a real port.
func serialPair(t *testing.T) (host, dev *os.File) {
	t.Helper()
	dev, host, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := term.MakeRaw(int(host.Fd())); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { host.Close(); dev.Close() })
	return host, dev
}

func TestEncodeFrame(t *testing.T) {
	tests := []struct {
		name, in, want, err string
	}{
		{name: "compacts", in: "{\n  \"t\": \"state\",\n  \"v\": 2\n}", want: `{"t":"state","v":2}` + "\n"},
		{name: "keeps unknown fields", in: `{"t":"x","extra":[1,2]}`, want: `{"t":"x","extra":[1,2]}` + "\n"},
		{name: "not json", in: `{"t":`, err: "JSON object"},
		{name: "array", in: `[1]`, err: "JSON object"},
		{name: "null", in: `null`, err: "JSON object"},
		{name: "no t", in: `{"v":2}`, err: `"t"`},
		{name: "t not a string", in: `{"t":1}`, err: `"t"`},
		{name: "too long", in: `{"t":"x","pad":"` + strings.Repeat("a", 500) + `"}`, err: "limit is 512"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := EncodeFrame([]byte(tt.in))
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("err = %v, want containing %q", err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEncodeFrameExactLimit(t *testing.T) {
	// {"t":"x","p":"…"} plus newline is 17 bytes around the padding.
	in := `{"t":"x","p":"` + strings.Repeat("a", MaxLine-17) + `"}`
	got, err := EncodeFrame([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != MaxLine {
		t.Fatalf("len = %d, want %d", len(got), MaxLine)
	}
	if _, err := EncodeFrame([]byte(`{"t":"x","p":"` + strings.Repeat("a", MaxLine-16) + `"}`)); err == nil {
		t.Fatal("one byte over the limit was accepted")
	}
}

func TestHandshake(t *testing.T) {
	host, dev := serialPair(t)
	go func() {
		line, err := bufio.NewReader(dev).ReadString('\n')
		if err != nil || line != `{"t":"ping"}`+"\n" {
			t.Errorf("device got %q, %v; want a ping line", line, err)
			return
		}
		// REPL-style noise first, as the console port would print.
		dev.Write([]byte("Adafruit CircuitPython 10.3.1\r\n{\"t\":\"state\"}\n"))
		dev.Write([]byte(`{"t":"hello","v":2,"fw":"0.1.0"}` + "\n"))
	}()

	hello, err := Handshake(host, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if hello != (Hello{V: 2, FW: "0.1.0"}) {
		t.Errorf("hello = %+v", hello)
	}
}

func TestHandshakeTimeout(t *testing.T) {
	host, _ := serialPair(t)
	start := time.Now()
	_, err := Handshake(host, 100*time.Millisecond)
	if !errors.Is(err, ErrNoHello) {
		t.Fatalf("err = %v, want ErrNoHello", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("timed out after %v", d)
	}
}

func TestFrameArrivesAsOneLine(t *testing.T) {
	host, dev := serialPair(t)
	line, err := EncodeFrame([]byte("{\"t\": \"state\",\n \"v\": 2, \"slots\": [\"W\", null]}"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Write(line); err != nil {
		t.Fatal(err)
	}
	got, err := bufio.NewReader(dev).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"t":"state","v":2,"slots":["W",null]}` + "\n"; got != want {
		t.Errorf("device read %q, want %q", got, want)
	}
}

func TestCalloutPath(t *testing.T) {
	if got := calloutPath("/dev/tty.usbmodem1", "darwin"); got != "/dev/cu.usbmodem1" {
		t.Errorf("darwin: %q", got)
	}
	if got := calloutPath("/dev/ttyACM0", "linux"); got != "/dev/ttyACM0" {
		t.Errorf("linux: %q", got)
	}
}
