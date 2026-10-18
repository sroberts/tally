package slots

import (
	"encoding/json"
	"testing"
	"time"
)

func TestFrameV3MatchesTheSpecExample(t *testing.T) {
	s := snap("w1:p2",
		[][]string{{"w1:p1=working", "w1:p2=blocked"}},
		[][]string{{"w2:p1=done", "w2:p3=idle", "w2:p4=blocked"}})
	s.Workspaces[0].Label = "vibe-macro"
	t0 := time.Unix(1000, 0)
	var tr Tracker
	tr.Observe(s, t0)
	st := Compute(s, 4)
	got := string(st.FrameV3(1042, tr.Observe(s, t0.Add(312*time.Second)), t0.Add(312*time.Second)).JSON())
	want := `{"t":"state","v":3,"seq":1042,` +
		`"slots":[{"s":"W","pane":"w1:p1"},{"s":"N","pane":"w1:p2","since":312},{"s":"G","pane":"w2:p1"},{"s":"R","pane":"w2:p3"}],` +
		`"focus":2,"fpane":"w1:p2","fstate":"N","overflow":{"N":1,"blocked_since":[312]},"herdr":true,"space":"vibe-macro"}`
	if got != want {
		t.Errorf("frame\n got %s\nwant %s", got, want)
	}
}

func TestFrameV3EmptySlotsAndOffline(t *testing.T) {
	f := Compute(snap("", [][]string{{"p1=idle"}}), 3).FrameV3(1, Tracked{}, time.Now())
	var back struct {
		Slots    []json.RawMessage
		Focus    *int
		FPane    *string
		Overflow map[string]any
	}
	if err := json.Unmarshal(f.JSON(), &back); err != nil {
		t.Fatal(err)
	}
	if len(back.Slots) != 3 || string(back.Slots[1]) != "null" || back.Focus != nil || back.FPane != nil || len(back.Overflow) != 0 {
		t.Errorf("frame = %s", f.JSON())
	}
	if got := string(OfflineV3(2, 7).JSON()); got != `{"t":"state","v":3,"seq":7,"slots":[null,null],"focus":null,"fpane":null,"fstate":null,"overflow":{},"herdr":false}` {
		t.Errorf("offline = %s", got)
	}
}

func TestFrameV3SpaceIsCappedAndClean(t *testing.T) {
	s := snap("p1", [][]string{{"p1=idle"}})
	s.Workspaces[0].Label = "tab\there ünïcode and a very long name that goes on"
	f := Compute(s, 1).FrameV3(1, Tracked{}, time.Now())
	if f.Space != "tab here ünïcode and a very long" {
		t.Errorf("space = %q", f.Space)
	}
}

func TestV2FrameIgnoresTheNewFields(t *testing.T) {
	// The pad's firmware reads v2; FPane must not leak into it.
	s := snap("p1", [][]string{{"p1=blocked"}})
	got := string(Compute(s, 2).Frame(5).JSON())
	want := `{"t":"state","v":2,"seq":5,"slots":["N",null],"focus":1,"fstate":"N","overflow":{},"herdr":true}`
	if got != want {
		t.Errorf("v2 frame\n got %s\nwant %s", got, want)
	}
}
