package slots

import (
	"testing"
	"time"
)

func TestTrackerTimesBlockedAgentsAcrossSnapshots(t *testing.T) {
	var tr Tracker
	t0 := time.Unix(1000, 0)
	tracked := tr.Observe(snap("p1", [][]string{{"p1=blocked", "p2=working"}}), t0)
	if got := tracked.BlockedSince["p1"]; !got.Equal(t0) {
		t.Fatalf("p1 since = %v", got)
	}
	// Still blocked: the time stays; p2 joins later.
	tracked = tr.Observe(snap("p1", [][]string{{"p1=blocked", "p2=blocked"}}), t0.Add(30*time.Second))
	if !tracked.BlockedSince["p1"].Equal(t0) || !tracked.BlockedSince["p2"].Equal(t0.Add(30*time.Second)) {
		t.Fatalf("since = %v", tracked.BlockedSince)
	}
	// p1 unblocks, then blocks again: the clock restarts.
	tr.Observe(snap("p1", [][]string{{"p1=working", "p2=blocked"}}), t0.Add(40*time.Second))
	tracked = tr.Observe(snap("p1", [][]string{{"p1=blocked", "p2=blocked"}}), t0.Add(50*time.Second))
	if !tracked.BlockedSince["p1"].Equal(t0.Add(50 * time.Second)) {
		t.Errorf("p1 since = %v, want restarted", tracked.BlockedSince["p1"])
	}
	// p2 closes: forgotten.
	tracked = tr.Observe(snap("p1", [][]string{{"p1=blocked"}}), t0.Add(60*time.Second))
	if _, ok := tracked.BlockedSince["p2"]; ok {
		t.Error("closed pane still tracked")
	}
}

func TestTrackerRemembersTheLastFocusedPane(t *testing.T) {
	var tr Tracker
	now := time.Unix(0, 0)
	if got := tr.Observe(snap("p1", [][]string{{"p1=idle", "sh"}}), now).LastPane; got != "" {
		t.Errorf("last = %q before any focus change", got)
	}
	if got := tr.Observe(snap("sh", [][]string{{"p1=idle", "sh"}}), now).LastPane; got != "p1" {
		t.Errorf("last = %q, want p1", got)
	}
	// No focus (e.g. a popup): last stays.
	if got := tr.Observe(snap("", [][]string{{"p1=idle", "sh"}}), now).LastPane; got != "p1" {
		t.Errorf("last = %q after focus went nowhere", got)
	}
	if got := tr.Observe(snap("p1", [][]string{{"p1=idle", "sh"}}), now).LastPane; got != "sh" {
		t.Errorf("last = %q, want sh", got)
	}
}
