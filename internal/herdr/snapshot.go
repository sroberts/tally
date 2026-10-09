package herdr

// Snapshot is the part of herdr's session.snapshot that Tally keeps: ids,
// order, focus, agent status, and each space's name (for the pad's OLED).
// The structs declare no other fields, so tab and pane titles, cwds, tokens
// and agent sessions are dropped while decoding and never reach memory or
// logs.
type Snapshot struct {
	FocusedPaneID *string     `json:"focused_pane_id"`
	Workspaces    []Workspace `json:"workspaces"`
	Tabs          []Tab       `json:"tabs"`
	Agents        []Agent     `json:"agents"` // in Agent sidebar order
}

// Workspace is one herdr workspace (a Space in the sidebar).
type Workspace struct {
	ID     string `json:"workspace_id"`
	Number int    `json:"number"`
	Label  string `json:"label"` // the space's name in herdr's sidebar
}

// Tab is one tab within a workspace.
type Tab struct {
	ID          string `json:"tab_id"`
	WorkspaceID string `json:"workspace_id"`
	Number      int    `json:"number"`
}

// Agent is herdr's record for a pane that hosts a coding agent.
type Agent struct {
	Kind        string `json:"agent"` // claude, codex, ...
	PaneID      string `json:"pane_id"`
	TabID       string `json:"tab_id"`
	WorkspaceID string `json:"workspace_id"`
	Status      string `json:"agent_status"` // idle, working, blocked, done, unknown
}
