package slots

import "encoding/json"

// Version is the serial protocol version the frame follows.
const Version = 2

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
}

// Frame turns the state into a frame with sequence number seq.
func (st State) Frame(seq uint32) Frame {
	f := Frame{T: "state", V: Version, Seq: seq, Focus: st.Focus, FState: st.FState,
		Slots: make([]*string, len(st.Slots)), Overflow: map[string]int{}, Herdr: true}
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
