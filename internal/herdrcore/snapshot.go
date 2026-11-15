package herdrcore

import (
	"sort"
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
// Each machine keeps its own document; world joins them, with a connected
// machine's ids prefixed by its name the way the app shows them (mm/t7).
type layout struct {
	Spaces []space `json:"spaces"`
}

type space struct {
	ID   string `json:"id"`
	Name string `json:"name"` // the space's name in the app's sidebar
	Tabs []tab  `json:"tabs"`
}

type tab struct {
	ID   string `json:"id"`
	Root node   `json:"root"`
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

// under returns n with every id and terminal prefixed by machine.
func (n node) under(machine string) node {
	out := node{ID: prefixed(machine, n.ID)}
	if n.Content != nil {
		out.Content = &struct {
			Terminal string `json:"terminal"`
		}{prefixed(machine, n.Content.Terminal)}
	}
	for _, c := range n.Children {
		out.Children = append(out.Children, c.under(machine))
	}
	return out
}

// under returns a connected machine's layout in the app's terms: its space,
// tab and pane ids and its terminals prefixed by the machine's name.
func (l layout) under(machine string) layout {
	var out layout
	for _, sp := range l.Spaces {
		ns := space{ID: prefixed(machine, sp.ID), Name: sp.Name}
		for _, t := range sp.Tabs {
			ns.Tabs = append(ns.Tabs, tab{ID: prefixed(machine, t.ID), Root: t.Root.under(machine)})
		}
		out.Spaces = append(out.Spaces, ns)
	}
	return out
}

// prefixed names id on machine as the app does: machine/id, or id alone for
// this machine (machine "").
func prefixed(machine, id string) string {
	if machine == "" || id == "" {
		return id
	}
	return machine + "/" + id
}

// machineOf is the machine an address such as mm/t7 lives on, "" for this one.
func machineOf(address string) string {
	if i := strings.LastIndex(address, "/"); i >= 0 {
		return address[:i]
	}
	return ""
}

// ordered returns l with its spaces in the order of ids (the window's
// sidebar). Spaces ids doesn't name keep their order after those.
func (l layout) ordered(ids []string) layout {
	if len(ids) == 0 {
		return l
	}
	rank := make(map[string]int, len(ids))
	for i, id := range ids {
		rank[id] = i + 1
	}
	out := layout{Spaces: append([]space(nil), l.Spaces...)}
	sort.SliceStable(out.Spaces, func(i, j int) bool {
		ri, rj := rank[out.Spaces[i].ID], rank[out.Spaces[j].ID]
		if ri == 0 || rj == 0 {
			return ri != 0 && rj == 0
		}
		return ri < rj
	})
	return out
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
// labels but hasn't placed yet goes after those. Terminals and spaces on a
// connected machine count too, with the app's machine/ prefix, so tally on
// the computer running the app sees what its window shows. Focus belongs to
// the app window, not to herdr-core: FocusedPaneID is the terminal in the
// window's focused pane when App is set and the window answers, else nil.
func (c *Client) Snapshot() (*herdr.Snapshot, error) {
	var list struct {
		Terminals []terminal `json:"terminals"`
		Machines  []machine  `json:"machines"` // added here, connected or not
	}
	if err := c.Call("terminal.list", nil, &list); err != nil {
		return nil, err
	}
	l, machines, err := c.worldOf(list.Machines)
	if err != nil {
		return nil, err
	}
	// A closed window only means no focus; the lights still work. One that
	// answers also knows the sidebar's order across machines; one that
	// doesn't isn't asked twice, so a hung window costs one short timeout.
	focus := ""
	if c.App != nil {
		if st, err := c.App.Status(); err == nil {
			if st.Pane != "" {
				focus = l.terminalAt(st.Pane)
			}
			if ids, err := c.App.Spaces(); err == nil {
				l = l.ordered(ids)
			}
		}
	}
	s := buildWith(list.Terminals, l, machines)
	if focus != "" {
		s.FocusedPaneID = &focus
	}
	return s, nil
}

// machine is the part of a herdr-core machine record tally declares.
type machine struct {
	Name  string `json:"name"`  // what this core calls it: mm in mm/t7
	State string `json:"state"` // connecting, connected, needs_login or unreachable
}

// world is worldOf for the machines herdr-core lists now. A core that can't
// list machines is read alone.
func (c *Client) world() (layout, map[string]bool, error) {
	var ms struct {
		Machines []machine `json:"machines"`
	}
	if err := c.Call("machine.list", nil, &ms); err != nil {
		ms.Machines = nil
	}
	return c.worldOf(ms.Machines)
}

// worldOf reads the layout across machines: this core's document, then each
// connected machine's in the order given, prefixed. It also returns the
// connected machines' names. A machine whose document can't be read is left
// out rather than failing the whole read.
func (c *Client) worldOf(ms []machine) (layout, map[string]bool, error) {
	l, err := c.layoutOn("")
	if err != nil {
		return layout{}, nil, err
	}
	machines := map[string]bool{}
	for _, m := range ms {
		if m.State != "connected" || m.Name == "" {
			continue
		}
		machines[m.Name] = true
		if ml, err := c.layoutOn(m.Name); err == nil {
			l.Spaces = append(l.Spaces, ml.under(m.Name).Spaces...)
		}
	}
	return l, machines, nil
}

// layoutOn reads the app's layout document on machine ("" for this core);
// empty before the app first lays anything out there.
func (c *Client) layoutOn(machine string) (layout, error) {
	var doc struct {
		Value *layout `json:"value"` // null before the app first runs
	}
	if err := c.Call("doc.get", struct {
		Name string `json:"name"`
		On   string `json:"on,omitempty"`
	}{LayoutDoc, machine}, &doc); err != nil {
		return layout{}, err
	}
	if doc.Value == nil {
		return layout{}, nil
	}
	return *doc.Value, nil
}

// HasLayout reports whether the Herdr app has laid out any space, here or on
// a connected machine, reading only layout documents. This core's comes
// first, so other machines are asked only when it has none: an app whose
// spaces all live on a connected machine still counts.
func (c *Client) HasLayout() (bool, error) {
	l, err := c.layoutOn("")
	if err != nil || len(l.Spaces) > 0 {
		return len(l.Spaces) > 0, err
	}
	l, _, err = c.world()
	return len(l.Spaces) > 0, err
}

// build is buildWith for this machine's terminals alone.
func build(terms []terminal, l layout) *herdr.Snapshot { return buildWith(terms, l, nil) }

// buildWith is Snapshot without the socket. machines names the connected
// machines whose terminals (machine/t7) count.
func buildWith(terms []terminal, l layout, machines map[string]bool) *herdr.Snapshot {
	agents := make(map[string]terminal, len(terms))
	for _, t := range terms {
		m := machineOf(t.Address)
		if t.State == "live" && t.Status != nil && (m == "" || machines[m]) {
			// Labels name ids local to the terminal's machine.
			t.Labels.Space, t.Labels.Tab = prefixed(m, t.Labels.Space), prefixed(m, t.Labels.Tab)
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
