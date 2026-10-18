// Package slots maps herdr agents to the MacroPad's Fleet keys.
//
// Fleet keys A1..A6 send herdr's focus_agent 1..6, which indexes the Agent
// sidebar. Under the default ui.agent_panel_sort = "spaces", herdr draws the
// sidebar straight from session.snapshot's agents list, which the server
// builds by walking workspaces, then tabs, then each tab's panes in layout
// order (herdr v0.9.3: src/app/agents.rs collect_agent_infos and
// src/client/shell/agent_sidebar.rs ordered_agent_pane_ids). So slot n is
// simply agents[n-1], and the key that lights is the key that jumps there.
package slots

import "github.com/sroberts/tally/internal/herdr"

// DefaultSlots is the number of Fleet keys.
const DefaultSlots = 6

// Codes maps herdr's effective agent status to the frame's state codes.
var Codes = map[string]string{
	"blocked": "N", // needs you
	"done":    "G", // finished, not yet seen
	"working": "W",
	"idle":    "R", // ready
	"unknown": "U",
}

// Slot is one agent in sidebar order.
type Slot struct {
	Agent herdr.Agent
	Code  string
}

// State is the result of one snapshot, ready to become a frame.
type State struct {
	Slots    []*Slot // len = slot count; nil when empty
	Overflow []Slot  // agents beyond the slot count, in order
	Focus    *int    // 1-based slot; 0 beyond the slots; nil when the focus is not an agent
	FState   *string // focused agent's code, even beyond the slots
	FPane    *string // focused agent's pane id, even beyond the slots
	Space    string  // name of the focused agent's space; "" when none
}

// Compute maps a snapshot onto n slots.
func Compute(s *herdr.Snapshot, n int) State {
	st := State{Slots: make([]*Slot, n)}
	labels := make(map[string]string, len(s.Workspaces))
	for _, w := range s.Workspaces {
		labels[w.ID] = w.Label
	}
	for i, a := range s.Agents {
		slot := Slot{Agent: a, Code: code(a.Status)}
		if i < n {
			st.Slots[i] = &slot
		} else {
			st.Overflow = append(st.Overflow, slot)
		}
		if s.FocusedPaneID != nil && a.PaneID == *s.FocusedPaneID {
			focus := 0
			if i < n {
				focus = i + 1
			}
			st.Focus = &focus
			st.FState = &slot.Code
			st.FPane = &a.PaneID
			st.Space = labels[a.WorkspaceID]
		}
	}
	return st
}

func code(status string) string {
	if c, ok := Codes[status]; ok {
		return c
	}
	return "U"
}
