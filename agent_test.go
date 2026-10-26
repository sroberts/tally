package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/sroberts/tally/internal/pairing"
	"github.com/sroberts/tally/internal/web"
)

func TestServeArgs(t *testing.T) {
	cases := []struct {
		o    agentOptions
		want []string
	}{
		{agentOptions{slots: 6}, nil},
		{agentOptions{slots: 6, web: true}, []string{"--web"}},
		{agentOptions{slots: 9, port: "/dev/cu.usbmodem1", noDevice: true},
			[]string{"--port", "/dev/cu.usbmodem1", "--slots", "9", "--no-device"}},
		{agentOptions{slots: 6, remote: true, listen: "100.64.0.1:48000", web: true, webListen: ":48001", webCert: "self"},
			[]string{"--remote", "--listen", "100.64.0.1:48000", "--web", "--web-listen", ":48001", "--web-cert", "self"}},
	}
	for _, c := range cases {
		if got := c.o.serveArgs(); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%+v: %q, want %q", c.o, got, c.want)
		}
	}
}

func TestAgentServing(t *testing.T) {
	if got := (agentOptions{}).serving(); got != (web.Serving{Listen: ":47825", Cert: "tailscale"}) {
		t.Errorf("defaults: %+v", got)
	}
	if got := (agentOptions{webListen: "100.64.0.1:9", webCert: "self"}).serving(); got != (web.Serving{Listen: "100.64.0.1:9", Cert: "self"}) {
		t.Errorf("flags: %+v", got)
	}
}

func TestAgentEnvKeepsOnlyHerdr(t *testing.T) {
	env := map[string]string{"HERDR_SOCKET_PATH": "/tmp/h.sock", "HOME": "/home/u", "HERDR_SESSION": ""}
	got := agentEnv(func(k string) string { return env[k] })
	if !reflect.DeepEqual(got, map[string]string{"HERDR_SOCKET_PATH": "/tmp/h.sock"}) {
		t.Errorf("env %v", got)
	}
}

// A password install-agent generated is shown once; when installing fails
// before the sign-in prints it, the error path shows it instead.
func TestShowPasswordOnError(t *testing.T) {
	var b strings.Builder
	fail := errors.New("launchctl bootstrap failed")
	if err := showPasswordOnError(&b, "correct horse battery", fail); err != fail {
		t.Errorf("err = %v", err)
	}
	if !strings.Contains(b.String(), "correct horse battery") || !strings.Contains(b.String(), "shown once") {
		t.Errorf("printed %q", b.String())
	}

	b.Reset()
	showPasswordOnError(&b, "correct horse battery", nil) // the sign-in showed it
	showPasswordOnError(&b, "", fail)                     // there was one already
	if b.Len() != 0 {
		t.Errorf("printed %q", b.String())
	}
}

func TestListenPorts(t *testing.T) {
	for addr, want := range map[string]int{"": 47824, ":48000": 48000, "100.64.0.1:9": 9, "nonsense": 47824, "[::1]:7": 7} {
		if got := listenPort(addr, pairing.Port); got != want {
			t.Errorf("listenPort(%q) = %d, want %d", addr, got, want)
		}
	}
	if remoteListen("") != ":47824" || remoteListen("100.64.0.1:9") != "100.64.0.1:9" {
		t.Error("remoteListen")
	}
	if remotePort(pairing.State{}) != 47824 || remotePort(pairing.State{Listen: ":48000"}) != 48000 {
		t.Error("remotePort")
	}
}
