package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/sroberts/tally/internal/actions"
	"github.com/sroberts/tally/internal/herdr"
)

// act runs one action through the same table the soft pad uses. DRIVE
// actions default to the focused agent, as the phone's do.
func act(args []string) error {
	fs := flag.NewFlagSet("act", flag.ExitOnError)
	target := fs.String("target", "", "pane id for DRIVE actions (default: the focused agent)")
	slot := fs.Int("slot", 0, "slot for focus_slot, from 1")
	text := fs.String("text", "", "text for insert_text")
	dry := fs.Bool("dry-run", false, "print the herdr calls instead of making them")
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	if len(args) == 0 || args[0] == "" || args[0][0] == '-' {
		fs.Usage()
		os.Exit(2)
	}
	name := args[0]
	fs.Parse(args[1:])
	if !actions.Known(name) {
		return fmt.Errorf("unknown action %q", name)
	}

	path, err := herdr.SocketPath()
	if err != nil {
		return err
	}
	reader := herdr.New(path)
	if actions.IsDrive(name) && *target == "" {
		snap, err := reader.Snapshot()
		if err != nil {
			return err
		}
		if snap.FocusedPaneID == nil {
			return fmt.Errorf("%s needs --target: no pane has focus", name)
		}
		*target = *snap.FocusedPaneID
	}

	var w actions.Writer = herdr.NewWriter(path)
	rec := &actions.Recorder{}
	if *dry {
		w = rec
	}
	t := &actions.Table{W: w, Snapshot: reader.Snapshot}
	if err := t.Do(actions.Request{Name: name, Target: *target, Slot: *slot, Text: *text}); err != nil {
		return err
	}
	for _, c := range rec.Calls {
		fmt.Println(c)
	}
	return nil
}
