package doctor

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestWebListener(t *testing.T) {
	if r := WebListener(nil, 47825); r.Level != OK {
		t.Errorf("listening: %+v", r)
	}
	if r := WebListener(errors.New("dial tcp: connect: connection refused"), 47825); r.Level != Info || r.Fix == "" {
		t.Errorf("off: %+v", r)
	}
	if r := WebListener(errors.New("unexpected status 404"), 47825); r.Level != Fail {
		t.Errorf("something else: %+v", r)
	}
}

func TestWebPassword(t *testing.T) {
	if r := WebPassword(false, 0); r.Level != Warn || r.Fix != "tally web --set-password" {
		t.Errorf("unset: %+v", r)
	}
	if r := WebPassword(true, 2); r.Level != OK {
		t.Errorf("set: %+v", r)
	}
}

func TestWebCert(t *testing.T) {
	now := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		notAfter time.Time
		err      error
		want     Level
	}{
		{"", time.Time{}, nil, Warn},
		{"mbp.tail1.ts.net", time.Time{}, errors.New("no such file"), Warn},
		{"mbp.tail1.ts.net", now.Add(-time.Hour), nil, Fail},
		{"mbp.tail1.ts.net", now.Add(5 * 24 * time.Hour), nil, Warn},
		{"mbp.tail1.ts.net", now.Add(60 * 24 * time.Hour), nil, OK},
	}
	for _, c := range cases {
		if r := WebCert(c.name, c.notAfter, c.err, now); r.Level != c.want {
			t.Errorf("%q %v %v: got %+v", c.name, c.notAfter, c.err, r)
		}
	}
}

func TestWebSelfCert(t *testing.T) {
	now := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	ca := now.Add(3000 * day) // when tally replaces the CA
	cases := []struct {
		what              string
		name              string
		server, caReplace time.Time
		err               error
		want              Level
	}{
		{"no tailscale", "", time.Time{}, time.Time{}, nil, Warn},
		{"no CA", "mbp.tail1.ts.net", time.Time{}, time.Time{}, errors.New("no such file"), Warn},
		{"no server certificate", "mbp.tail1.ts.net", time.Time{}, ca, errors.New("no such file"), Warn},
		{"doesn't verify", "mbp.tail1.ts.net", now.Add(300 * day), ca, errors.New("x509: certificate is valid for studio.tail1.ts.net"), Warn},
		{"CA replaced soon", "mbp.tail1.ts.net", now.Add(300 * day), now.Add(30 * day), nil, Warn},
		{"server expiring", "mbp.tail1.ts.net", now.Add(5 * day), ca, nil, Warn},
		{"ok", "mbp.tail1.ts.net", now.Add(300 * day), ca, nil, OK},
	}
	for _, c := range cases {
		r := WebSelfCert(c.name, c.server, c.caReplace, c.err, now)
		if r.Level != c.want || (r.Level != OK && r.Fix == "") {
			t.Errorf("%s: got %+v", c.what, r)
		}
	}
	if r := WebSelfCert("mbp.tail1.ts.net", now.Add(300*day), ca, nil, now); !strings.Contains(r.Detail, "tally web --ca") {
		t.Errorf("ok doesn't say how to trust the CA: %q", r.Detail)
	}
}
