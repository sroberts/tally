package doctor

import (
	"errors"
	"strings"
	"testing"
)

func TestListener(t *testing.T) {
	cases := []struct {
		cert, probe error
		want        Level
	}{
		{errors.New("no such file"), nil, Info},
		{nil, errors.New("dial tcp 127.0.0.1:47824: connect: connection refused"), Info},
		{nil, errors.New("certificate does not match the paired fingerprint"), Fail},
		{nil, nil, OK},
	}
	for _, c := range cases {
		if r := Listener(c.cert, c.probe, 47824, []string{"192.168.1.20"}); r.Level != c.want {
			t.Errorf("cert=%v probe=%v: %v %s", c.cert, c.probe, r.Level, r.Detail)
		}
	}
}

func TestPhones(t *testing.T) {
	if r := Phones(nil); r.Level != Warn || r.Fix != "tally pair" {
		t.Errorf("none: %+v", r)
	}
	if r := Phones([]string{"phone", "ipad"}); r.Level != OK || !strings.Contains(r.Detail, "2 paired: phone, ipad") {
		t.Errorf("two: %+v", r)
	}
}

func TestTailscale(t *testing.T) {
	cases := map[string]Level{
		`{"BackendState":"Running","Self":{"DNSName":"mbp.ts.net."}}`:      OK,
		"Warning: version skew\n" + `{"BackendState":"Running","Self":{}}`: Info,
		`{"BackendState":"Stopped"}`:                                       Info,
	}
	for out, want := range cases {
		if r := Tailscale([]byte(out), nil); r.Level != want {
			t.Errorf("%s: %v %s", out, r.Level, r.Detail)
		}
	}
	if r := Tailscale(nil, errors.New("not found")); r.Level != Info {
		t.Errorf("missing: %+v", r)
	}
}

func TestMacFirewall(t *testing.T) {
	const p = "/Users/me/.local/bin/tally"
	cases := []struct {
		global, app string
		want        Level
	}{
		{"Firewall is disabled. (State = 0)", "", OK},
		{"Firewall is enabled. (State = 1)", "Incoming connection to " + p + " is permitted.", OK},
		{"Firewall is enabled. (State = 1)", "Incoming connection to " + p + " is blocked.", Fail},
		{"Firewall is enabled. (State = 1)", "", Warn},
	}
	for _, c := range cases {
		if r := MacFirewall(c.global, c.app, p); r.Level != c.want {
			t.Errorf("%q %q: %v", c.global, c.app, r.Level)
		}
	}
}

func TestLinuxFirewall(t *testing.T) {
	if r := LinuxFirewall("Status: active\n\nTo Action From\n22/tcp ALLOW Anywhere\n", "", 47824); r.Level != Warn || r.Fix != "sudo ufw allow 47824/tcp" {
		t.Errorf("ufw without rule: %+v", r)
	}
	if r := LinuxFirewall("Status: active\n47824/tcp ALLOW Anywhere\n", "", 47824); r.Level != OK {
		t.Errorf("ufw with rule: %+v", r)
	}
	if r := LinuxFirewall("", "running\n", 47824); r.Level != Warn || !strings.Contains(r.Fix, "--add-port=47824/tcp") {
		t.Errorf("firewalld: %+v", r)
	}
	if r := LinuxFirewall("Status: inactive", "not running", 47824); r.Level != OK {
		t.Errorf("none: %+v", r)
	}
}
