package doctor

import "fmt"

// Core reports on herdr-core, the Herdr app's server: whether it answered
// (err), how many agents it has and how many spaces the app's layout has.
// Without a layout, no agent has a place in the sidebar and so no slot.
func Core(path string, agents, spaces int, err error) Result {
	r := Result{Name: "herdr-core"}
	switch {
	case err != nil:
		r.Level, r.Detail, r.Fix = Fail, err.Error(), "open the Herdr app (it starts herdr-core)"
	case spaces == 0:
		r.Level, r.Detail = Warn, fmt.Sprintf("reachable at %s, but the Herdr app has no layout yet, so no agent has a slot", path)
		r.Fix = "open the Herdr app once"
	default:
		r.Level, r.Detail = OK, fmt.Sprintf("reachable at %s, %d agent%s in %d space%s", path, agents, plural(agents), spaces, plural(spaces))
	}
	return r
}

// Window reports on the Herdr app's window, which focus, zoom and new tabs
// go through: open (err nil), closed, or answering badly.
func Window(path string, closed bool, err error) Result {
	r := Result{Name: "herdr window"}
	switch {
	case err == nil:
		r.Level, r.Detail = OK, "open at "+path+": focus marks, focus, zoom and new tabs work"
	case closed:
		r.Level, r.Detail = Info, "not open: the lights and keys work; focus marks, focus, zoom and new tabs need it"
		r.Fix = "open the Herdr app"
	default:
		r.Level, r.Detail, r.Fix = Warn, path+": "+err.Error(), "restart the Herdr app"
	}
	return r
}

// AlsoTUI notes a herdr TUI that answers alongside herdr-core. tally reads
// one herdr at a time, and its agents aren't the app's.
func AlsoTUI(agents int) Result {
	return Result{Name: "herdr tui", Level: Info,
		Detail: fmt.Sprintf("also running, with %d agent%s tally isn't showing: it reads herdr-core", agents, plural(agents)),
		Fix:    "to read the herdr TUI instead, run tally with --herdr tui (tally install-agent --herdr tui for the agent)"}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
