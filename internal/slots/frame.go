package slots

import "encoding/json"

// Version is the serial protocol version the frame follows.
const Version = 2

// SpaceMax is the most characters of a space name the pad's OLED shows.
const SpaceMax = 10

// Frame is a protocol v2 state frame for the device.
type Frame struct {
	T        string         `json:"t"`
	V        int            `json:"v"`
	Seq      uint32         `json:"seq"`
	Slots    []*string      `json:"slots"`
	Focus    *int           `json:"focus"`
	FState   *string        `json:"fstate"`
	Overflow map[string]int `json:"overflow"`
	Herdr    bool           `json:"herdr"`
	Space    string         `json:"space,omitempty"` // focused agent's space, ASCII, at most SpaceMax
}

// Frame turns the state into a frame with sequence number seq.
func (st State) Frame(seq uint32) Frame {
	f := Frame{T: "state", V: Version, Seq: seq, Focus: st.Focus, FState: st.FState,
		Slots: make([]*string, len(st.Slots)), Overflow: map[string]int{}, Herdr: true,
		Space: oledText(st.Space, SpaceMax)}
	for i, s := range st.Slots {
		if s != nil {
			f.Slots[i] = &s.Code
		}
	}
	for _, s := range st.Overflow {
		f.Overflow[s.Code]++
	}
	return f
}

// oledText keeps the first max printable ASCII characters of s, the only
// ones the OLED's font draws, replacing anything else with '?'.
func oledText(s string, max int) string {
	out := make([]byte, 0, max)
	for _, r := range s {
		if len(out) == max {
			break
		}
		if r < ' ' || r > '~' {
			r = '?'
		}
		out = append(out, byte(r))
	}
	return string(out)
}

// Offline is the frame sent while herdr's socket is unreachable: every slot
// empty and herdr false.
func Offline(n int, seq uint32) Frame {
	return Frame{T: "state", V: Version, Seq: seq, Slots: make([]*string, n),
		Overflow: map[string]int{}, Herdr: false}
}

// JSON encodes the frame on one line, without a trailing newline.
func (f Frame) JSON() []byte {
	b, _ := json.Marshal(f) // only plain types: cannot fail
	return b
}
