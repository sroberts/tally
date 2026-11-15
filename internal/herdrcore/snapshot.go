package herdrcore

import (
	"strconv"
	"strings"

	"github.com/sroberts/tally/internal/herdr"
)

// LayoutDoc is the herdr-core document where the Herdr app keeps its spaces,
// tabs and splits.
const LayoutDoc = "herdr-app"

// The labels the Herdr app puts on terminals it shows. Tally reads no other
// label: they are free-form and may carry anything.
type labels struct {
	Space string `json:"herdr.space"`
	Tab   string `json:"herdr.tab"`
	Seen  string `json:"herdr.seen"` // the last turn a person looked at
}

// terminal is the part of a herdr-core terminal tally declares. Cwd, argv,
// title, screen, name, the status message and every other label are not
// declared, so they are dropped while decoding.
type terminal struct {
	Address string `json:"address"` // t7; machine/t7 on a connected machine
	State   string `json:"state"`   // live or finished
	Labels  labels `json:"labels"`
	Status  *struct {
		State string `json:"state"` // idle, working, done, blocked or error
		App   string `json:"app"`   // the program's own name, claude-code
	} `json:"status"` // nil when the program never reported one
	Turn uint64 `json:"turn"` // goes up each time the status leaves working
}

// layout is the part of the app's layout document tally declares: ids,
// order and space names. Folders and tab names are dropped while decoding.
type layout struct {
	Spaces []struct {
		ID   string `json:"id"`
		Name string `json:"name"` // the space's name in the app's sidebar
		Tabs []struct {
			ID   string `json:"id"`
			Root node   `json:"root"`
		} `json:"tabs"`
	} `json:"spaces"`
}

// node is one node of a tab's layout tree: a row or column of children, or
// a block showing one thing.
type node struct {
	ID       string `json:"id"` // the pane id when this block shows a terminal
	Children []node `json:"children"`
	Content  *struct {
		Terminal string `json:"terminal"`
	} `json:"content"`
}

// find returns the first block under n, in layout order, that match accepts.
func (n node) find(match func(node) bool) *node {
	if n.Content != nil && n.Content.Terminal != "" && match(n) {
		return &n
	}
	for _, c := range n.Children {
		if f := c.find(match); f != nil {
			return f
		}
	}
	return nil
}

// find returns the first block in the layout, in sidebar order, that match
// accepts.
func (l layout) find(match func(node) bool) *node {
	for _, sp := range l.Spaces {
		for _, tab := range sp.Tabs {
			if f := tab.Root.find(match); f != nil {
				return f
			}
		}
	}
	return nil
}

// terminalAt is the terminal the pane shows, or "".
func (l layout) terminalAt(pane string) string {
	if f := l.find(func(n node) bool { return n.ID == pane }); f != nil {
		return f.Content.Terminal
	}
	return ""
}

// paneOf is the first pane showing the terminal, or "".
func (l layout) paneOf(terminal string) string {
	if f := l.find(func(n node) bool { return n.Content.Terminal == terminal }); f != nil {
		return f.ID
	}
	return ""
}

// terminals appends the terminals under n in layout order.
func (n node) terminals(out []string) []string {
	if n.Content != nil && n.Content.Terminal != "" {
		out = append(out, n.Content.Terminal)
	}
	for _, c := range n.Children {
		out = c.terminals(out)
	}
	return out
}

// Snapshot reads herdr-core's terminals and the app's layout and returns
// them as a herdr.Snapshot. Agents come in the app's sidebar order: spaces,
// then tabs, then each tab's terminals in layout order. A terminal the app
// labels but hasn't placed yet goes after those. Focus belongs to the app
// window, not to herdr-core: FocusedPaneID is the terminal in the window's
// focused pane when App is set and the window answers, else nil.
func (c *Client) Snapshot() (*herdr.Snapshot, error) {
	var list struct {
		Terminals []terminal `json:"terminals"`
	}
	if err := c.Call("terminal.list", nil, &list); err != nil {
		return nil, err
	}
	l, err := c.layout()
	if err != nil {
		return nil, err
	}
	s := build(list.Terminals, l)
	if c.App != nil {
		// A closed window only means no focus; the lights still work.
		if st, err := c.App.Status(); err == nil && st.Pane != "" {
			if t := l.terminalAt(st.Pane); t != "" {
				s.FocusedPaneID = &t
			}
		}
	}
	return s, nil
}

// layout reads the app's layout document; empty before the app first runs.
func (c *Client) layout() (layout, error) {
	var doc struct {
		Value *layout `json:"value"` // null before the app first runs
	}
	if err := c.Call("doc.get", struct {
		Name string `json:"name"`
	}{LayoutDoc}, &doc); err != nil {
		return layout{}, err
	}
	if doc.Value == nil {
		return layout{}, nil
	}
	return *doc.Value, nil
}

// build is Snapshot without the socket.
func build(terms []terminal, l layout) *herdr.Snapshot {
	agents := make(map[string]terminal, len(terms))
	for _, t := range terms {
		if t.State == "live" && t.Status != nil && !strings.Contains(t.Address, "/") {
			agents[t.Address] = t
		}
	}

	s := &herdr.Snapshot{Workspaces: []herdr.Workspace{}, Tabs: []herdr.Tab{}, Agents: []herdr.Agent{}}
	placed := map[string]bool{}
	spaces := map[string]bool{}
	tabs := map[string]string{} // tab id -> its space
	for i, sp := range l.Spaces {
		spaces[sp.ID] = true
		s.Workspaces = append(s.Workspaces, herdr.Workspace{ID: sp.ID, Number: i + 1, Label: sp.Name})
		for j, tab := range sp.Tabs {
			tabs[tab.ID] = sp.ID
			s.Tabs = append(s.Tabs, herdr.Tab{ID: tab.ID, WorkspaceID: sp.ID, Number: j + 1})
			for _, addr := range tab.Root.terminals(nil) {
				t, ok := agents[addr]
				if !ok || placed[addr] {
					continue
				}
				placed[addr] = true
				s.Agents = append(s.Agents, agent(t, sp.ID, tab.ID))
			}
		}
	}
	for _, t := range terms {
		a, ok := agents[t.Address]
		if ok && !placed[t.Address] && spaces[a.Labels.Space] {
			placed[t.Address] = true
			tab := a.Labels.Tab
			if tabs[tab] != a.Labels.Space {
				tab = "" // a tab that isn't in that space, or is gone
			}
			s.Agents = append(s.Agents, agent(a, a.Labels.Space, tab))
		}
	}
	return s
}

func agent(t terminal, space, tab string) herdr.Agent {
	return herdr.Agent{
		Kind:        t.Status.App,
		PaneID:      t.Address,
		TabID:       tab,
		WorkspaceID: space,
		Status:      status(t),
	}
}

// status applies the Herdr app's rule: blocked and error need a person,
// working is working, and otherwise an agent is done when it has finished a
// turn nobody has looked at yet, else idle. A terminal without a seen label
// has nothing unread.
func status(t terminal) string {
	switch t.Status.State {
	case "blocked", "error":
		return "blocked"
	case "working":
		return "working"
	}
	if seen, err := strconv.ParseUint(t.Labels.Seen, 10, 64); err == nil && t.Turn > seen {
		return "done"
	}
	return "idle"
}
