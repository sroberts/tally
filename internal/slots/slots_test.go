package slots

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/sroberts/vibe-macropad/internal/herdr"
)

// snap builds a snapshot from a compact description. Each workspace is a
// list of tabs; each tab lists its panes in layout order as "id" (a shell) or
// "id=status" (an agent). Agents are listed in that walk order, as herdr's
// server builds them. Tabs are numbered from 1 in the order given.
func snap(focus string, workspaces ...[][]string) *herdr.Snapshot {
	s := &herdr.Snapshot{}
	if focus != "" {
		s.FocusedPaneID = &focus
	}
	for wi, tabs := range workspaces {
		w := herdr.Workspace{ID: string(rune('a' + wi)), Number: wi + 1}
		s.Workspaces = append(s.Workspaces, w)
		for ti, panes := range tabs {
			t := herdr.Tab{ID: w.ID + ":t" + string(rune('1'+ti)), WorkspaceID: w.ID, Number: ti + 1}
			s.Tabs = append(s.Tabs, t)
			for _, p := range panes {
				id, status := p, ""
				for i := range p {
					if p[i] == '=' {
						id, status = p[:i], p[i+1:]
					}
				}
				if status != "" {
					s.Agents = append(s.Agents, herdr.Agent{Kind: "claude", PaneID: id, TabID: t.ID, WorkspaceID: w.ID, Status: status})
				}
			}
		}
	}
	return s
}

func ptr[T any](v T) *T { return &v }

func TestSlotsFollowHerdrsAgentOrder(t *testing.T) {
	// herdr's agents list is the sidebar order; Compute must not re-sort it,
	// even when the workspace list seems to disagree.
	s := snap("", [][]string{{"p1=idle", "shell", "p2=done"}}, [][]string{{"p3=working"}})
	s.Agents[0], s.Agents[2] = s.Agents[2], s.Agents[0]
	s.Workspaces[0], s.Workspaces[1] = s.Workspaces[1], s.Workspaces[0]
	var got []string
	for _, sl := range Compute(s, DefaultSlots).Slots {
		if sl != nil {
			got = append(got, sl.Agent.PaneID)
		}
	}
	if !reflect.DeepEqual(got, []string{"p3", "p2", "p1"}) {
		t.Errorf("slots = %v", got)
	}
}

func TestShellsTakeNoSlot(t *testing.T) {
	f := Compute(snap("", [][]string{{"s1", "s2"}}, [][]string{{"s3", "p1=idle"}}), DefaultSlots).Frame(1)
	if *f.Slots[0] != "R" || f.Slots[1] != nil {
		t.Errorf("frame = %s", f.JSON())
	}
}

func TestComputeCodesAndFocus(t *testing.T) {
	s := snap("p2", [][]string{{"p1=blocked", "p2=done"}}, [][]string{{"p3=working", "p4=idle", "p5=unknown", "p6=weird"}})
	st := Compute(s, DefaultSlots)
	f := st.Frame(7)
	want := Frame{T: "state", V: 2, Seq: 7, Slots: []*string{ptr("N"), ptr("G"), ptr("W"), ptr("R"), ptr("U"), ptr("U")},
		Focus: ptr(2), FState: ptr("G"), Overflow: map[string]int{}, Herdr: true}
	if !reflect.DeepEqual(f, want) {
		t.Errorf("frame = %s", f.JSON())
	}
}

func TestComputeOverflowAndFocusBeyondSlots(t *testing.T) {
	var ws [][][]string
	for _, st := range []string{"idle", "idle", "idle", "idle", "idle", "idle", "blocked", "working", "blocked"} {
		ws = append(ws, [][]string{{"p" + st + string(rune('0'+len(ws))) + "=" + st}})
	}
	s := snap("pworking7", ws...)
	f := Compute(s, DefaultSlots).Frame(1)
	if *f.Focus != 0 || *f.FState != "W" {
		t.Errorf("focus = %v, fstate = %v", *f.Focus, *f.FState)
	}
	if !reflect.DeepEqual(f.Overflow, map[string]int{"N": 2, "W": 1}) {
		t.Errorf("overflow = %v", f.Overflow)
	}
}

func TestComputeFocusOnShell(t *testing.T) {
	f := Compute(snap("s1", [][]string{{"s1", "p1=idle"}}), DefaultSlots).Frame(1)
	if f.Focus != nil || f.FState != nil {
		t.Errorf("focus = %v, fstate = %v", f.Focus, f.FState)
	}
	if string(f.JSON()) != `{"t":"state","v":2,"seq":1,"slots":["R",null,null,null,null,null],"focus":null,"fstate":null,"overflow":{},"herdr":true}` {
		t.Errorf("json = %s", f.JSON())
	}
}

func TestOfflineFrame(t *testing.T) {
	if got := string(Offline(6, 3).JSON()); got != `{"t":"state","v":2,"seq":3,"slots":[null,null,null,null,null,null],"focus":null,"fstate":null,"overflow":{},"herdr":false}` {
		t.Errorf("offline = %s", got)
	}
}

func TestLiveSnapshot(t *testing.T) {
	b, err := os.ReadFile("../../testdata/snapshots/live-7-agents.json")
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Result struct {
			Snapshot herdr.Snapshot `json:"snapshot"`
		} `json:"result"`
	}
	if err := json.Unmarshal(b, &resp); err != nil {
		t.Fatal(err)
	}
	got := string(Compute(&resp.Result.Snapshot, DefaultSlots).Frame(1).JSON())
	want := `{"t":"state","v":2,"seq":1,"slots":["R","G","R","R","R","G"],"focus":0,"fstate":"W","overflow":{"W":1},"herdr":true,"space":"space-labe"}`
	if got != want {
		t.Errorf("frame = %s\nwant    %s", got, want)
	}
}

func TestFrameFitsOnOneDeviceLine(t *testing.T) {
	var ws [][][]string
	for i := 0; i < 40; i++ {
		ws = append(ws, [][]string{{"p" + string(rune('A'+i)) + "=blocked"}})
	}
	f := Compute(snap("pA", ws...), DefaultSlots).Frame(4294967295)
	if n := len(f.JSON()) + 1; n > 512 {
		t.Errorf("frame is %d bytes with its newline", n)
	}
}

func TestFrameCarriesTheFocusedSpaceName(t *testing.T) {
	s := snap("w1:p1", [][]string{{"w1:p1=idle"}})
	for _, tt := range []struct{ label, want string }{
		{"vibe", "vibe"},
		{"vibe-macropad", "vibe-macro"}, // SpaceMax characters
		{"café ☕ bar", "caf? ? bar"},    // the OLED font is ASCII only
		{"", ""},
	} {
		s.Workspaces[0].Label = tt.label
		f := Compute(s, DefaultSlots).Frame(1)
		if f.Space != tt.want {
			t.Errorf("label %q: space = %q, want %q", tt.label, f.Space, tt.want)
		}
	}
	s.FocusedPaneID = nil
	s.Workspaces[0].Label = "vibe"
	if f := Compute(s, DefaultSlots).Frame(1); f.Space != "" || strings.Contains(string(f.JSON()), "space") {
		t.Errorf("no focus: frame = %s", f.JSON())
	}
}
