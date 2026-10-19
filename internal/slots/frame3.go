package slots

import (
	"encoding/json"
	"strings"
	"time"
)

// VersionV3 is the soft pad's WebSocket protocol version.
const VersionV3 = 3

// SpaceMaxV3 is the most characters of a space name a v3 frame carries.
const SpaceMaxV3 = 32

// FrameV3 is a protocol v3 state frame for the soft pad. Unlike v2 it names
// each slot's pane, so actions can target it, and carries how long blocked
// agents have waited.
type FrameV3 struct {
	T        string     `json:"t"`
	V        int        `json:"v"`
	Seq      uint32     `json:"seq"`
	Slots    []*SlotV3  `json:"slots"`
	Focus    *int       `json:"focus"`
	FPane    *string    `json:"fpane"`
	FState   *string    `json:"fstate"`
	Overflow OverflowV3 `json:"overflow"`
	Herdr    bool       `json:"herdr"`
	Space    string     `json:"space,omitempty"`
}

// SlotV3 is one occupied slot.
type SlotV3 struct {
	S     string `json:"s"`
	Pane  string `json:"pane"`
	Since *int   `json:"since,omitempty"` // seconds blocked; only for N
}

// OverflowV3 counts the agents beyond the slots by code and lists how long
// each blocked one has waited, in agent order. It encodes as one object:
// {"N":1,"W":2,"blocked_since":[45]}.
type OverflowV3 struct {
	Counts       map[string]int
	BlockedSince []int
}

// MarshalJSON flattens the counts and blocked_since into one object.
func (o OverflowV3) MarshalJSON() ([]byte, error) {
	m := make(map[string]any, len(o.Counts)+1)
	for k, v := range o.Counts {
		m[k] = v
	}
	if len(o.BlockedSince) > 0 {
		m["blocked_since"] = o.BlockedSince
	}
	return json.Marshal(m)
}

// FrameV3 turns the state into a v3 frame with sequence number seq, timing
// blocked agents from tr as of now.
func (st State) FrameV3(seq uint32, tr Tracked, now time.Time) FrameV3 {
	f := FrameV3{T: "state", V: VersionV3, Seq: seq, Focus: st.Focus, FPane: st.FPane, FState: st.FState,
		Slots: make([]*SlotV3, len(st.Slots)), Overflow: OverflowV3{Counts: map[string]int{}}, Herdr: true,
		Space: spaceText(st.Space)}
	waited := func(pane string) *int {
		since, ok := tr.BlockedSince[pane]
		if !ok {
			return nil
		}
		s := max(0, int(now.Sub(since)/time.Second))
		return &s
	}
	for i, s := range st.Slots {
		if s == nil {
			continue
		}
		f.Slots[i] = &SlotV3{S: s.Code, Pane: s.Agent.PaneID}
		if s.Code == "N" {
			f.Slots[i].Since = waited(s.Agent.PaneID)
		}
	}
	for _, s := range st.Overflow {
		f.Overflow.Counts[s.Code]++
		if s.Code == "N" {
			if w := waited(s.Agent.PaneID); w != nil {
				f.Overflow.BlockedSince = append(f.Overflow.BlockedSince, *w)
			}
		}
	}
	return f
}

// OfflineV3 is the v3 frame sent while herdr's socket is unreachable.
func OfflineV3(n int, seq uint32) FrameV3 {
	return FrameV3{T: "state", V: VersionV3, Seq: seq, Slots: make([]*SlotV3, n),
		Overflow: OverflowV3{Counts: map[string]int{}}, Herdr: false}
}

// spaceText keeps the first SpaceMaxV3 characters of a space name, with
// control characters replaced by spaces.
func spaceText(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < ' ' || r == 0x7f {
			return ' '
		}
		return r
	}, s)
	if r := []rune(s); len(r) > SpaceMaxV3 {
		s = string(r[:SpaceMaxV3])
	}
	return s
}

// JSON encodes the frame on one line.
func (f FrameV3) JSON() []byte {
	b, _ := json.Marshal(f) // only plain types: cannot fail
	return b
}
