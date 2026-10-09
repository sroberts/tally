package doctor

import "testing"

// Captured from herdr 0.9.3, trimmed.
const status = `pi: not installed (/Users/x/.pi/agent/extensions/herdr-agent-state.ts)
claude: current (v10) (/Users/x/.claude/hooks/herdr-agent-state.sh)
codex: current (v8) (/Users/x/.codex/herdr-agent-state.sh)
`

func TestClaudeIntegration(t *testing.T) {
	for _, tt := range []struct {
		out    string
		level  Level
		detail string
	}{
		{status, OK, "herdr's Claude Code integration is current (v10)"},
		{"claude: not installed (/Users/x/.claude/hooks/herdr-agent-state.sh)\n", Fail, ""},
		{"claude: outdated (v7, current v10) (/Users/x/.claude/hooks/h.sh)\n", Warn, "herdr's Claude Code integration is outdated (v7, current v10)"},
		{"codex: current (v8)\n", Warn, ""},
	} {
		r := ClaudeIntegration(tt.out)
		if r.Level != tt.level || (tt.detail != "" && r.Detail != tt.detail) {
			t.Errorf("%q: %v %q", tt.out, r.Level, r.Detail)
		}
		if r.Level != OK && r.Fix == "" {
			t.Errorf("%q: no fix", tt.out)
		}
	}
}

func TestPanelSort(t *testing.T) {
	for _, tt := range []struct {
		config string
		level  Level
	}{
		{"", OK},
		{"[keys]\nprefix = \"ctrl+b\"\n", OK},
		{"[ui]\nagent_panel_sort = \"spaces\"\n", OK},
		{"[ui]\nagent_panel_sort = \"priority\" # busiest first\n", Fail},
		{"ui.agent_panel_sort = 'priority'\n", Fail},
		{"[ui]\n# agent_panel_sort = \"priority\"\n", OK},
		{"[keys]\nagent_panel_sort = \"priority\"\n", OK}, // wrong table: herdr ignores it
		{"[ui]\ntheme = \"a#b\"\nagent_panel_sort = \"spaces\"\n", OK},
	} {
		if r := PanelSort(tt.config); r.Level != tt.level {
			t.Errorf("%q: %v %q", tt.config, r.Level, r.Detail)
		}
	}
}

func TestStripComment(t *testing.T) {
	for in, want := range map[string]string{
		`a = "x" # c`:    `a = "x" `,
		`a = "x#y"`:      `a = "x#y"`,
		`a = 'x#y' # c`:  `a = 'x#y' `,
		`a = "q\"#" # c`: `a = "q\"#" `,
		`# whole line`:   ``,
	} {
		if got := stripComment(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}
