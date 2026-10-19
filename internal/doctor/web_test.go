package doctor

import (
	"errors"
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
