package gateway

import (
	"context"
	"errors"
	"fmt"
	"slices"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/queue"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/worker"
)

// The writes of the interface run through one path (C1f built it for the
// one write of C1, a notification marked as read; C2 adds the comment,
// the status, the assignment and the title of a ticket). A write is never
// shared and never joins a read: it runs as the person's job of kind
// Write, so the queue retries it only when the server cannot have acted
// (a 429, or nothing was sent), with the same idempotency key. The result
// carries the target of the operation, not of a selection: the model
// files it under the operation it started, wherever the cursor is by
// then. A success counts only when the answer names the target asked for
// with the value sent; anything else after a request that left is an
// outcome the client cannot place, which the model treats as unknown.

// WriteKind names what a write does.
type WriteKind uint8

const (
	// MarkRead marks the notification as read (C1f).
	MarkRead WriteKind = iota + 1
	// Comment posts Value as a plain text comment on the ticket (C2b).
	Comment
	// Status sets the ticket's status to the key in Value (C2c).
	Status
	// Assign adds the person the token belongs to to the ticket's
	// assignees (C2c). The request sends "me", which the server resolves;
	// Value carries that person's id from /me for the confirmation alone.
	Assign
	// Title sets the ticket's title to Value (C2c).
	Title
)

func (k WriteKind) String() string {
	switch k {
	case MarkRead:
		return "mark read"
	case Comment:
		return "comment"
	case Status:
		return "status"
	case Assign:
		return "assign"
	case Title:
		return "title"
	}
	return "write"
}

// WriteTicket names one write: the session it belongs to, the operation's
// number in it, the target captured when the key was pressed, the
// idempotency key of that press and the payload. It does not change after
// the key press.
type WriteTicket struct {
	Session Session
	Op      uint64
	Kind    WriteKind
	Key     string
	// The target: a notification for MarkRead, a ticket in its project for
	// every other kind.
	Notification string
	Project      string
	Ticket       string
	// The payload: the body of a comment, the status key, the new title,
	// or the person's own id for Assign (see WriteKind).
	Value string
}

// WriteResult is the outcome of one write. The value of its kind is filled
// only when Answer is an object that confirms the write.
type WriteResult struct {
	WriteTicket
	State   api.NotificationState // MarkRead
	Summary api.TicketRow         // Status, Assign, Title
	Comment api.Comment           // Comment
	Answer  transport.Answer
}

// CheckWriteTicket refuses a write without a number, without its target or
// without the payload its kind needs before anything is sent. The
// idempotency key's form is the adapter's check (api.checkKey): one rule,
// in one place.
func CheckWriteTicket(t WriteTicket) error {
	if t.Op == 0 {
		return fmt.Errorf("%s: an operation without a number", t.Kind)
	}
	switch t.Kind {
	case MarkRead:
		if t.Notification == "" {
			return errors.New("mark read: no notification id")
		}
	case Comment, Status, Assign, Title:
		if t.Project == "" || t.Ticket == "" {
			return fmt.Errorf("%s: no ticket", t.Kind)
		}
		if t.Value == "" {
			if t.Kind == Assign {
				return errors.New("assign: no user id to confirm the answer with")
			}
			return fmt.Errorf("%s: nothing to send", t.Kind)
		}
	default:
		return fmt.Errorf("write: unknown kind %d", t.Kind)
	}
	return nil
}

// checkMarkedState says whether a successful answer confirms a mark as
// read: it names the notification asked for, and says it is read.
func checkMarkedState(t WriteTicket, s api.NotificationState) error {
	if s.ID != t.Notification {
		return errors.New("mark read: another notification answered")
	}
	if s.State != "read" {
		return fmt.Errorf("mark read: state %q answered for a notification marked as read", s.State)
	}
	return nil
}

// CheckUpdatedTicket says whether a ticket summary confirms the write of
// its kind: it names the ticket asked for and carries the status or the
// title sent, or the person among the assignees. The server's summary is
// the state after the write, so a value other than the one sent means
// the write did not take, whatever the answer's kind says.
func CheckUpdatedTicket(t WriteTicket, s api.TicketRow) error {
	if s.ID != t.Ticket {
		return fmt.Errorf("%s: another ticket answered", t.Kind)
	}
	switch t.Kind {
	case Status:
		if s.Status == nil || *s.Status != t.Value {
			return errors.New("status: the answer does not carry the status sent")
		}
	case Title:
		if s.Title != t.Value {
			return errors.New("title: the answer does not carry the title sent")
		}
	case Assign:
		if !slices.ContainsFunc(s.Assignees, func(r api.Ref) bool { return r.ID == t.Value }) {
			return errors.New("assign: the answer does not name you among the assignees")
		}
	default:
		return fmt.Errorf("%s: a ticket summary confirms no such write", t.Kind)
	}
	return nil
}

// CheckPostedComment says whether a comment confirms the write: it is a
// comment of the ticket asked for, with an id. The body is not compared:
// the server keeps a plain body as sent, but whether it did is not what
// the client needs to know.
func CheckPostedComment(t WriteTicket, c api.Comment) error {
	if t.Kind != Comment {
		return fmt.Errorf("%s: a comment confirms no such write", t.Kind)
	}
	if c.TicketID != t.Ticket {
		return errors.New("comment: a comment of another ticket answered")
	}
	if c.ID == "" {
		return errors.New("comment: a comment without an id answered")
	}
	return nil
}

// confirm picks the confirmation of a kind.
func confirm(t WriteTicket, r WriteResult) error {
	switch t.Kind {
	case MarkRead:
		return checkMarkedState(t, r.State)
	case Comment:
		return CheckPostedComment(t, r.Comment)
	}
	return CheckUpdatedTicket(t, r.Summary)
}

// Write is the command of one write. The request runs through the queue
// as the person's write, observed for the mailbox (a 401 on it ends the
// session, whatever becomes of this result). The closure assigns the
// decoded value on every execution, the queue's retry included; the
// result carries it only after a confirmed success. A comment goes out as
// plain text (the feature comment_plain is the model's to check before
// the key is offered).
func (g *Gateway) Write(ctx context.Context, t WriteTicket) tea.Cmd {
	if err := CheckWriteTicket(t); err != nil {
		return func() tea.Msg { return WriteResult{WriteTicket: t, Answer: refusedTicket(err)} }
	}
	var r WriteResult
	send := func(ctx context.Context) transport.Answer {
		var ans transport.Answer
		switch t.Kind {
		case MarkRead:
			r.State, ans = g.src.MarkNotificationRead(ctx, t.Notification, t.Key)
		case Comment:
			r.Comment, ans = g.src.PostComment(ctx, t.Project, t.Ticket, t.Value, api.CommentOptions{Plain: true}, t.Key)
		case Status:
			r.Summary, ans = g.src.UpdateTicket(ctx, t.Project, t.Ticket, api.TicketChange{Status: t.Value}, t.Key)
		case Assign:
			r.Summary, ans = g.src.UpdateTicket(ctx, t.Project, t.Ticket, api.TicketChange{AddMe: true}, t.Key)
		case Title:
			r.Summary, ans = g.src.UpdateTicket(ctx, t.Project, t.Ticket, api.TicketChange{Title: t.Value}, t.Key)
		}
		return ans
	}
	job := queue.Job{Priority: queue.User, Kind: queue.Write, Run: g.events.Observe(send)}
	return func() tea.Msg {
		msg, ok := worker.Run(g.sup, "gateway: "+t.Kind.String(), func() tea.Msg {
			ans, _ := g.queue.DoShared(ctx, job)
			if ans.OK() {
				if err := confirm(t, r); err != nil {
					ans = contradiction(ans, err)
				}
			}
			if !ans.OK() {
				r = WriteResult{}
			}
			r.WriteTicket, r.Answer = t, ans
			return r
		})
		if !ok {
			if g.sup.Err() != nil {
				return worker.Fatal{}
			}
			// Not run: the session stopped before this command came. Nothing
			// was sent, so the answer says so.
			return WriteResult{WriteTicket: t, Answer: transport.Answer{Class: transport.Canceled, Message: "canceled before it was sent"}}
		}
		return msg
	}
}

// --- mark as read (C1f), the first caller of Write ---------------------------

// MarkReadTicket names one operation of marking a notification as read:
// the session it belongs to, the operation's number in it, the target
// captured when the key was pressed and the idempotency key of that press.
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

func (t MarkReadTicket) write() WriteTicket {
	return WriteTicket{Session: t.Session, Op: t.Op, Kind: MarkRead, Key: t.Key, Notification: t.Notification}
}

// CheckMarkReadTicket refuses an operation without a target or a number
// before anything is sent.
func CheckMarkReadTicket(t MarkReadTicket) error { return CheckWriteTicket(t.write()) }

// CheckMarkedState says whether a successful answer confirms the write: it
// names the notification that was asked for, and says it is read.
func CheckMarkedState(t MarkReadTicket, s api.NotificationState) error {
	return checkMarkedState(t.write(), s)
}

// MarkRead is the command of one mark as read, through Write.
func (g *Gateway) MarkRead(ctx context.Context, t MarkReadTicket) tea.Cmd {
	cmd := g.Write(ctx, t.write())
	return func() tea.Msg {
		r, ok := cmd().(WriteResult)
		if !ok {
			return worker.Fatal{}
		}
		return MarkReadResult{MarkReadTicket: t, State: r.State, Answer: r.Answer}
	}
}
