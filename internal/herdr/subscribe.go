package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// ErrEventsLost means herdr dropped events before this subscriber read them
// and closed the subscription. Resubscribe, then take a fresh snapshot.
var ErrEventsLost = errors.New("herdr: events lost")

// Subscription is one entry of an events.subscribe request.
type Subscription struct {
	Type   string `json:"type"`
	PaneID string `json:"pane_id,omitempty"`
}

// Lifecycle is every event that can add, remove, reorder or refocus an
// agent, which is everything that can change a frame except an agent's
// status (see AgentStatus).
var Lifecycle = subs(
	"workspace.created", "workspace.moved", "workspace.reordered", "workspace.closed", "workspace.focused",
	"tab.created", "tab.closed", "tab.moved", "tab.focused",
	"pane.created", "pane.closed", "pane.moved", "pane.exited", "pane.focused", "pane.agent_detected",
	"layout.updated",
)

func subs(types ...string) []Subscription {
	s := make([]Subscription, len(types))
	for i, t := range types {
		s[i] = Subscription{Type: t}
	}
	return s
}

// AgentStatus subscribes to pane.agent_status_changed for each pane. herdr
// needs one entry per pane and rejects the whole request if any pane is gone.
func AgentStatus(paneIDs []string) []Subscription {
	s := make([]Subscription, len(paneIDs))
	for i, id := range paneIDs {
		s[i] = Subscription{Type: "pane.agent_status_changed", PaneID: id}
	}
	return s
}

// Subscribe holds one events.subscribe connection open until ctx is done or
// herdr ends it. It calls notify once when herdr acknowledges the request and
// once per pushed event after that. Event payloads are never decoded, since
// they carry pane titles and Tally only needs to know that something changed.
//
// It returns ctx.Err() when ctx is done, ErrEventsLost on an overrun, an
// *Error when herdr rejects the request, and another error when the
// connection fails. It never returns nil.
func (c *Client) Subscribe(ctx context.Context, subs []Subscription, notify func()) error {
	conn, err := c.send("events.subscribe", struct {
		Subscriptions []Subscription `json:"subscriptions"`
	}{subs})
	if err != nil {
		return err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()

	r := bufio.NewReaderSize(conn, 64*1024)
	line, err := r.ReadBytes('\n')
	if err != nil {
		return ended(ctx, err)
	}
	result, err := decode("events.subscribe", line)
	if err != nil {
		return err
	}
	var ack struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(result, &ack); ack.Type != "subscription_started" {
		return fmt.Errorf("herdr: events.subscribe answered with %q", ack.Type)
	}
	conn.SetDeadline(time.Time{})
	notify()

	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			return ended(ctx, err)
		}
		var msg struct {
			Error *Error `json:"error"`
		}
		if json.Unmarshal(line, &msg) == nil && msg.Error != nil {
			if msg.Error.Code == "events_lost" {
				return ErrEventsLost
			}
			return msg.Error
		}
		notify()
	}
}

// ended explains why a subscription's read failed.
func ended(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, io.EOF) {
		return errors.New("herdr closed the subscription")
	}
	return fmt.Errorf("herdr: subscription: %w", err)
}
