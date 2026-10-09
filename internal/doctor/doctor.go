// Package doctor holds tally doctor's checks that read herdr's CLI output
// and config. They are pure so they can be tested without herdr.
package doctor

import (
	"bufio"
	"strconv"
	"strings"
)

// Level is a check's outcome.
type Level int

const (
	OK   Level = iota
	Info       // could not be checked; nothing known to be wrong
	Warn       // works, but something is off
	Fail       // Tally will misbehave until this is fixed
)

func (l Level) String() string { return [...]string{"ok", "--", "warn", "FAIL"}[l] }

// Result is one line of tally doctor's report.
type Result struct {
	Name   string
	Level  Level
	Detail string
	Fix    string // a command or step, when Level isn't OK
}

// ClaudeIntegration reads `herdr integration status` output. herdr learns
// Claude Code's working, blocked and idle states from this hook.
func ClaudeIntegration(out string) Result {
	r := Result{Name: "claude hook"}
	for _, line := range strings.Split(out, "\n") {
		state, ok := strings.CutPrefix(strings.TrimSpace(line), "claude:")
		if !ok {
			continue
		}
		state, _, _ = strings.Cut(strings.TrimSpace(state), " (/") // drop the hook's path
		switch {
		case strings.HasPrefix(state, "current"):
			r.Level, r.Detail = OK, "herdr's Claude Code integration is "+state
		case strings.HasPrefix(state, "not installed"):
			r.Level, r.Detail = Fail, "herdr's Claude Code integration is not installed, so agents never leave unknown"
			r.Fix = "herdr integration install claude"
		default:
			r.Level, r.Detail = Warn, "herdr's Claude Code integration is "+state
			r.Fix = "herdr integration install claude"
		}
		return r
	}
	r.Level, r.Detail = Warn, "herdr integration status did not mention claude"
	r.Fix = "herdr integration status"
	return r
}

// PanelSort reads ui.agent_panel_sort from herdr's config.toml. Fleet keys
// follow herdr's agent list order, which only the default "spaces" sort
// draws as-is; any other sort makes the sidebar and the keys disagree.
func PanelSort(config string) Result {
	r := Result{Name: "sidebar sort"}
	sort, found := "", false
	table := ""
	sc := bufio.NewScanner(strings.NewReader(config))
	for sc.Scan() {
		line := strings.TrimSpace(stripComment(sc.Text()))
		if strings.HasPrefix(line, "[") {
			table = strings.TrimSpace(strings.Trim(line, "[]"))
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if (table == "ui" && key == "agent_panel_sort") || (table == "" && key == "ui.agent_panel_sort") {
			if v, err := strconv.Unquote(strings.TrimSpace(value)); err == nil {
				sort, found = v, true
			} else {
				sort, found = strings.TrimSpace(value), true
			}
		}
	}
	switch {
	case !found:
		r.Level, r.Detail = OK, `ui.agent_panel_sort is unset (default "spaces")`
	case sort == "spaces":
		r.Level, r.Detail = OK, `ui.agent_panel_sort is "spaces"`
	default:
		r.Level, r.Detail = Fail, "ui.agent_panel_sort is "+strconv.Quote(sort)+", so A1-A6 and the sidebar order disagree"
		r.Fix = `set agent_panel_sort = "spaces" under [ui] in herdr's config.toml, then herdr server reload-config`
	}
	return r
}

// stripComment drops a # comment that is outside a quoted string.
func stripComment(line string) string {
	quote := byte(0)
	for i := 0; i < len(line); i++ {
		switch c := line[i]; {
		case quote != 0:
			if c == '\\' && quote == '"' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '#':
			return line[:i]
		}
	}
	return line
}
