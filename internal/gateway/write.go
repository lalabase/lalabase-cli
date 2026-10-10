package gateway

import (
	"context"
	"errors"
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/queue"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/worker"
)

// The one write of C1 (C1f): a notification marked as read. A write is
// never shared and never joins a read: it runs as the person's job of
// kind Write, so the queue retries it only when the server cannot have
// acted (a 429, or nothing was sent), with the same idempotency key. The
// result carries the ticket of the operation, not of a selection: the
// model files it under the operation it started, wherever the cursor is
// by then.

// MarkReadTicket names one operation of marking a notification as read:
// the session it belongs to, the operation's number in it, the target
// captured when the key was pressed and the idempotency key of that press.
// It does not change after the key press.
type MarkReadTicket struct {
	Session      Session
	Op           uint64
	Notification string
	Key          string
}

// MarkReadResult is the outcome of one operation. State is filled only when
// Answer is an object that names the notification asked for as read.
type MarkReadResult struct {
	MarkReadTicket
	State  api.NotificationState
	Answer transport.Answer
}

// CheckMarkReadTicket refuses an operation without a target or a number
// before anything is sent. The idempotency key's form is the adapter's
// check (api.MarkNotificationRead): one rule, in one place.
func CheckMarkReadTicket(t MarkReadTicket) error {
	switch {
	case t.Op == 0:
		return errors.New("mark read: an operation without a number")
	case t.Notification == "":
		return errors.New("mark read: no notification id")
	}
	return nil
}

// CheckMarkedState says whether a successful answer confirms the write: it
// names the notification that was asked for, and says it is read. Anything
// else after a request that was sent is an outcome the client cannot
// place, which the model treats as unknown (the server may have acted).
func CheckMarkedState(t MarkReadTicket, s api.NotificationState) error {
	if s.ID != t.Notification {
		return errors.New("mark read: another notification answered")
	}
	if s.State != "read" {
		return fmt.Errorf("mark read: state %q answered for a notification marked as read", s.State)
	}
	return nil
}

// MarkRead is the command of one operation. The request runs through the
// queue as the person's write, observed for the mailbox (a 401 on it ends
// the session, whatever becomes of this result). The closure assigns the
// state on every execution, the queue's retry included; the result carries
// it only after a confirmed success.
func (g *Gateway) MarkRead(ctx context.Context, t MarkReadTicket) tea.Cmd {
	if err := CheckMarkReadTicket(t); err != nil {
		return func() tea.Msg { return MarkReadResult{MarkReadTicket: t, Answer: refusedTicket(err)} }
	}
	var state api.NotificationState
	job := queue.Job{Priority: queue.User, Kind: queue.Write, Run: g.events.Observe(func(ctx context.Context) transport.Answer {
		var ans transport.Answer
		state, ans = g.src.MarkNotificationRead(ctx, t.Notification, t.Key)
		return ans
	})}
	return func() tea.Msg {
		msg, ok := worker.Run(g.sup, "gateway: mark read", func() tea.Msg {
			ans, _ := g.queue.DoShared(ctx, job)
			if ans.OK() {
				if err := CheckMarkedState(t, state); err != nil {
					ans = contradiction(ans, err)
				}
			}
			if !ans.OK() {
				state = api.NotificationState{}
			}
			return MarkReadResult{MarkReadTicket: t, State: state, Answer: ans}
		})
		if !ok {
			if g.sup.Err() != nil {
				return worker.Fatal{}
			}
			// Not run: the session stopped before this command came. Nothing
			// was sent, so the answer says so.
			return MarkReadResult{MarkReadTicket: t, Answer: transport.Answer{Class: transport.Canceled, Message: "canceled before it was sent"}}
		}
		return msg
	}
}
