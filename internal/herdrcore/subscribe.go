package herdrcore

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/sroberts/tally/internal/herdr"
)

// Quiet is how long a subscription may go without any line before tally
// calls it dead. herdr-core sends a heartbeat every 15 s. Tests shorten it.
var Quiet = 45 * time.Second

// SubscribeAll holds one events.subscribe connection open until ctx is done
// or herdr-core ends it. herdr-core has a single stream for every terminal
// and document change, so unlike the herdr TUI there is nothing to pick.
//
// It calls notify once when herdr-core answers with its snapshot and once per
// event that may change a frame (see changes) or missed notice after that;
// heartbeats only keep the connection alive. Notifications are never decoded
// past their method name, event kind and any error, since events carry
// titles and labels and tally only needs to know that something changed. A missed notice is followed by a fresh snapshot on the same
// stream, so it needs nothing more than a notify.
//
// It returns ctx.Err() when ctx is done, a *herdr.Error when herdr-core
// rejects the request, and another error when the connection fails or goes
// quiet. It never returns nil.
func (c *Client) SubscribeAll(ctx context.Context, notify func()) error {
	if err := checkAllowed("events.subscribe"); err != nil {
		return err
	}
	conn, err := dial(c.Path, "events.subscribe", nil, Timeout)
	if err != nil {
		return err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()

	r := bufio.NewReaderSize(conn, 64*1024)
	// The answer carries every terminal, screens included; read past it,
	// with as long as any other line gets: a big session can take a while.
	conn.SetReadDeadline(time.Now().Add(Quiet))
	line, err := readLine(r)
	if err != nil {
		return ended(ctx, err)
	}
	if _, err := decode("events.subscribe", line); err != nil {
		return err
	}
	notify()

	for {
		conn.SetReadDeadline(time.Now().Add(Quiet))
		line, err := readLine(r)
		if err != nil {
			return ended(ctx, err)
		}
		var msg struct {
			Method string `json:"method"`
			Params struct {
				Type string `json:"type"` // an event's kind; no titles or labels
			} `json:"params"`
			Error *json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal(line, &msg); err != nil {
			return fmt.Errorf("herdr-core: bad notification: %w", err)
		}
		if msg.Error != nil && string(*msg.Error) != "null" {
			if _, err := decode("events.subscribe", line); err != nil {
				return err
			}
			return errors.New("herdr-core: the subscription answered with an error")
		}
		if changes(msg.Method, msg.Params.Type) {
			notify()
		}
	}
}

// quiet lists the event kinds that can't change a frame: a terminal's
// title, its size, and whether core keeps it running.
var quiet = map[string]bool{"title": true, "size": true, "keep": true}

// changes reports whether a notification may change what tally shows.
// heartbeat never does. event does unless its kind is in quiet, and missed
// always does. A notification or event kind tally doesn't know (a newer
// core's) counts as a change rather than be missed.
func changes(method, kind string) bool {
	switch method {
	case "heartbeat":
		return false
	case "event":
		return !quiet[kind]
	}
	return true
}

// ended explains why a subscription's read failed.
func ended(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, io.EOF) {
		return errors.New("herdr-core closed the subscription")
	}
	var off *herdr.OfflineError
	if errors.As(err, &off) {
		return err
	}
	return fmt.Errorf("herdr-core: subscription: %w", err)
}
