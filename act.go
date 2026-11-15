package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/sroberts/tally/internal/actions"
)

// act runs one action through the same table remote clients use. DRIVE
// actions default to the focused agent, as the phone's do.
func act(args []string) error {
	fs := flag.NewFlagSet("act", flag.ExitOnError)
	target := fs.String("target", "", "pane id for DRIVE actions (default: the focused agent)")
	slot := fs.Int("slot", 0, "slot for focus_slot, from 1")
	text := fs.String("text", "", "text for insert_text")
	dry := fs.Bool("dry-run", false, "print the herdr calls instead of making them")
	backend := fs.String("herdr", backendAuto, herdrFlagHelp)
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

	path, reader, err := openHerdr(*backend)
	if err != nil {
		return err
	}
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

	var w actions.Writer
	rec := &actions.Recorder{}
	if *dry {
		w, rec = newRecorder(reader)
	} else if w, err = newWriter(path, reader); err != nil {
		return err
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
