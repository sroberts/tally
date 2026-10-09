package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/sroberts/vibe-macropad/internal/herdr"
	"github.com/sroberts/vibe-macropad/internal/slots"
)

var words = map[string]string{
	"N": "NEEDS YOU", "G": "done", "W": "working", "R": "ready", "U": "unknown",
}

func status(args []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "print the state frame instead of the table")
	n := fs.Int("slots", slots.DefaultSlots, "number of Fleet slot keys")
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	fs.Parse(args)

	path, err := herdr.SocketPath()
	if err != nil {
		return err
	}
	c := herdr.New(path)
	if err := c.Ping(); err != nil {
		if *asJSON {
			fmt.Printf("%s\n", slots.Offline(*n, 1).JSON())
		}
		return err
	}
	snap, err := c.Snapshot()
	if err != nil {
		return err
	}
	st := slots.Compute(snap, *n)

	if *asJSON {
		fmt.Printf("%s\n", st.Frame(1).JSON())
		return nil
	}
	printStatus(os.Stdout, path, snap, st)
	return nil
}

func printStatus(w io.Writer, path string, snap *herdr.Snapshot, st slots.State) {
	spaces := make(map[string]int, len(snap.Workspaces))
	for _, ws := range snap.Workspaces {
		spaces[ws.ID] = ws.Number
	}
	tabs := make(map[string]int, len(snap.Tabs))
	for _, t := range snap.Tabs {
		tabs[t.ID] = t.Number
	}
	focused := ""
	if snap.FocusedPaneID != nil {
		focused = *snap.FocusedPaneID
	}
	row := func(key string, s *slots.Slot) {
		if s == nil {
			fmt.Fprintf(w, "  %-3s  -\n", key)
			return
		}
		mark := ""
		if s.Agent.PaneID == focused {
			mark = "  <- focused"
		}
		fmt.Fprintf(w, "  %-3s  %-9s  %-7s  space %d, tab %d, pane %s%s\n", key, words[s.Code],
			s.Agent.Kind, spaces[s.Agent.WorkspaceID], tabs[s.Agent.TabID], s.Agent.PaneID, mark)
	}

	total := len(st.Overflow)
	for _, s := range st.Slots {
		if s != nil {
			total++
		}
	}
	fmt.Fprintf(w, "herdr %s: %d agent%s\n\n", path, total, plural(total))
	for i, s := range st.Slots {
		row(fmt.Sprintf("A%d", i+1), s)
	}
	for i := range st.Overflow {
		row("+", &st.Overflow[i])
	}
	if len(st.Overflow) > 0 {
		fmt.Fprintf(w, "\n  + = beyond the %d keys; shown on the OLED and counted by ATTN\n", len(st.Slots))
	}
	if st.Focus == nil {
		fmt.Fprintln(w, "\nfocus: not on an agent")
	}
	fmt.Fprintf(w, "\nframe: %s\n", strings.TrimSpace(string(st.Frame(1).JSON())))
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
