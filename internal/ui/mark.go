package ui

import (
	"context"
	"strconv"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/cache"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
	"github.com/gboyke/lalabase-cli/internal/uitext"
)

// Mark as read (C1f): one r on an unread notification, from the inbox or
// from the ticket opened out of its details. Nothing marks by itself: not
// opening, not previewing, not paging.
//
// Permission comes from /me alone: read_only false offers the key, true
// hides and refuses it, a missing field is unknown and hides it too. The
// profile's allow_write is for agent commands and never reaches here.
//
// An operation captures its target when the key is pressed and never
// changes it: the notification, its organisation and, when there is one,
// the ticket. The lock on that target is set here in Update, before the
// command exists, so a second press on the same target does nothing until
// the outcome is known. The result is filed under the operation, wherever
// the cursor is by then; the answer for A never shows B as read.
//
// Outcomes, from the class of the answer (cache.Decide with Write, so a
// refused write never removes a ticket): confirmed (the server named the
// notification as read), refused (read-only, missing, no access, archived,
// invalid), not sent (nothing was done: the server was not reached, or it
// rate-limited the queue's one retry too), unknown (sent, no usable answer:
// the server may have acted). Confirmed and unknown invalidate the inbox
// variants and counters the write touched and reload what is shown; nothing
// is written again by itself, and absence from an unread page proves
// nothing. An explicit r later is a new operation with a new key.

type markState uint8

const (
	markRunning markState = iota
	markConfirmed
	markRefused
	markNotSent
	markUnknown
)

// markOp is one write of the person's action.
type markOp struct {
	id      uint64
	session gateway.Session
	ns      cache.Namespace
	// The target, captured at the key press: the notification, its
	// organisation, and the ticket when the notification names one.
	notification    string
	org             string
	project, ticket string
	// key is the idempotency key of this action; the queue's retry sends
	// the same one.
	key    string
	state  markState
	err    present.Inline
	cancel context.CancelFunc
	// clarifiers are the reads afterMark issued for this operation: while
	// one of them is out, an unknown outcome is being cleared up and the
	// lock holds. A later read of the same inbox is not one of them.
	clarifiers []gateway.Waiter
}

// origin is the notification a ticket in the object pane was opened from.
// It is navigation state: a ticket opened from the tickets section has
// none, however it was reached before, and the cached ticket knows
// nothing of it.
type origin struct {
	notification    string
	org             string
	project, ticket string
	// unread: the row was unread when the ticket was opened; a row shown
	// as read offers no r.
	unread bool
}

// markTarget is what r would mark right now.
type markTarget struct {
	notification    string
	org             string
	project, ticket string
}

func targetOf(n api.Notification) markTarget {
	t := markTarget{notification: n.ID, org: n.Organisation.ID}
	if n.Ticket != nil {
		t.project, t.ticket = n.Ticket.ProjectID, n.Ticket.ID
	}
	return t
}

// writeAllowed: /me succeeded and said the token may write, and the
// server has the tool with its arguments.
func (a *app) writeAllowed() bool {
	return a.steps[stepMe].state == ready && a.me.Token.ReadOnly != nil && !*a.me.Token.ReadOnly && a.on("mark_read")
}

// writeBlocked names why r is not offered when it is not: the permission
// unknown, the token read-only, the tool missing. 0 when it is offered.
func (a *app) writeBlocked() uitext.ID {
	switch {
	case a.steps[stepMe].state != ready || a.me.Token.ReadOnly == nil:
		return uitext.WritePermissionUnknown
	case *a.me.Token.ReadOnly:
		return uitext.ReadOnlyToken
	case !a.on("mark_read"):
		return uitext.MarkUnsupported
	}
	return 0
}

// openedNotification is the row whose details the pane shows, if any.
func (a *app) openedNotification() (api.Notification, bool) {
	v := a.active
	if v == nil || v.opened == "" {
		return api.Notification{}, false
	}
	if i := v.index(v.opened); i >= 0 {
		return v.rows[i], true
	}
	return api.Notification{}, false
}

// selectedNotification is the row the cursor of the inbox is on, if any.
func (a *app) selectedNotification() (api.Notification, bool) {
	v := a.active
	if v == nil {
		return api.Notification{}, false
	}
	if i := v.index(v.selected); i >= 0 {
		return v.rows[i], true
	}
	return api.Notification{}, false
}

// markTarget is the target of r in the current state: the focused pane
// decides. The ticket opened from a notification marks that notification;
// the details mark the row they show; the list marks the selected row.
// Only an unread row, only with the permission, only in navigation, and
// not while an operation on the same target runs or is being cleared up.
func (m Model) markTarget() (markTarget, bool) {
	a := m.app
	if a == nil || a.expired || m.mode != Navigate || !a.writeAllowed() {
		return markTarget{}, false
	}
	var t markTarget
	switch {
	case a.origin != nil && m.focus == view.Object:
		o := a.origin
		if !o.unread {
			return markTarget{}, false
		}
		t = markTarget{notification: o.notification, org: o.org, project: o.project, ticket: o.ticket}
	case a.active != nil && m.focus == view.List:
		n, ok := a.selectedNotification()
		if !ok || n.Read {
			return markTarget{}, false
		}
		t = targetOf(n)
	case a.active != nil && m.focus == view.Object:
		n, ok := a.openedNotification()
		if !ok || n.Read {
			return markTarget{}, false
		}
		t = targetOf(n)
	default:
		return markTarget{}, false
	}
	if a.markBlocks(t.notification) {
		return markTarget{}, false
	}
	return t, true
}

// markBlocks: an operation on the notification holds the lock: it runs,
// it was confirmed (nothing to mark again), or its outcome is unknown and
// the reads that clear it up are still out (review round 1, finding 3:
// those reads, not any later read of the inbox). A refused or unsent one
// frees the key: the next r is the person's new action.
func (a *app) markBlocks(notification string) bool {
	op := a.marks[notification]
	if op == nil {
		return false
	}
	switch op.state {
	case markRunning, markConfirmed:
		return true
	case markUnknown:
		return a.clarifying(op)
	}
	return false
}

// clarifying: one of the reads issued for the operation's outcome is still
// out for somebody. A read the person canceled since (a page turn, another
// inbox) clears nothing up any more.
func (a *app) clarifying(op *markOp) bool {
	for _, id := range op.clarifiers {
		if w := a.waiters[id]; w != nil && !w.canceled {
			return true
		}
	}
	return false
}

// touchedResources are the inbox variants a write on a notification of
// org changes: the organisation's own inbox and the inbox of all
// organisations, every page of each.
func (a *app) touchedResources(op *markOp) []cache.Key {
	return []cache.Key{a.inboxKey(op.org, 1).Resource(), a.inboxKey("", 1).Resource()}
}

// markRead is r: the target captured, the lock set, the command made. The
// idempotency key is this action's and no other's; the queue's one retry
// sends the same.
func (m Model) markRead() (Model, tea.Cmd) {
	a := m.app
	t, ok := m.markTarget()
	if !ok || a.deps.Gateway == nil {
		return m, nil
	}
	a.nextOp++
	ctx, cancel := context.WithCancel(a.base)
	op := &markOp{
		id: a.nextOp, session: a.session, ns: a.ns,
		notification: t.notification, org: t.org, project: t.project, ticket: t.ticket,
		key: a.idempotencyKey(a.nextOp), state: markRunning, cancel: cancel,
	}
	a.marks[t.notification] = op
	return m, a.deps.Gateway.MarkRead(ctx, gateway.MarkReadTicket{Session: a.session, Op: op.id, Notification: op.notification, Key: op.key})
}

// idempotencyKey is the key of one action: the session and the operation
// number make it unique in this run, the clock across runs. Its form is
// what the server keeps unchanged (letters, digits and -, 8 to 128).
func (a *app) idempotencyKey(op uint64) string {
	return "lalabase-mark-" + strconv.FormatUint(uint64(a.session), 10) + "-" + strconv.FormatUint(op, 10) + "-" + strconv.FormatInt(a.now().UnixNano(), 10)
}

// receiveMark files the outcome of an operation under the operation it
// started, whatever is selected now. A 401 ends the session first, also
// for an operation whose target was left. A result of another session, of
// an ended one, or of an operation that was replaced is dropped.
func (m Model) receiveMark(r gateway.MarkReadResult) (Model, tea.Cmd) {
	a := m.app
	if a == nil || a.expired || r.Session != a.session {
		return m, nil
	}
	if r.Answer.Class == transport.Unauthenticated {
		m.expire()
		return m, nil
	}
	op := a.marks[r.Notification]
	if op == nil || op.id != r.Op || op.state != markRunning {
		return m, nil
	}
	op.cancel()
	ans := r.Answer
	switch cache.Decide(ans.Class, cache.Write) {
	case cache.Reload:
		// The gateway checked that the answer names the notification as
		// read.
		op.state = markConfirmed
		return m, a.afterMark(op)
	case cache.KeepStale:
		// Temporary: the server was not reached, or it answered 429 to the
		// queue's retry as well, so it did not act; anything else after
		// the request left may have been carried out.
		switch {
		case !ans.Sent:
			op.state, op.err = markNotSent, uitext.Inline(uitext.MarkNotSent)
		case ans.Status == 429:
			op.state, op.err = markNotSent, uitext.Inline(uitext.MarkRateLimited)
		default:
			op.state = markUnknown
			return m, a.afterMark(op)
		}
	case cache.Drop:
		// Canceled by the client: the session ended or the program stops.
		// Sent means the server may have acted.
		if ans.Sent {
			op.state = markUnknown
			return m, a.afterMark(op)
		}
		op.state, op.err = markNotSent, uitext.Inline(uitext.MarkNotSent)
	case cache.KeepShowMessage:
		// A refusal, or an answer the client cannot place. The latter after
		// a request that left is an unknown outcome, not a refusal: an
		// answer that contradicts the question says nothing about the
		// write.
		if ans.Class == transport.Protocol && ans.Sent {
			op.state = markUnknown
			return m, a.afterMark(op)
		}
		op.state, op.err = markRefused, uitext.Fill(uitext.MarkRefused, detail(ans))
	default:
		// Replace and Remove are decisions on a read; an answer to a write
		// never takes either. Taken as unknown, the safe side.
		op.state = markUnknown
		return m, a.afterMark(op)
	}
	return m, nil
}

// afterMark follows a confirmed write and an unknown outcome alike: the
// inbox variants the write touched leave the cache and their epochs move
// on, so a read begun before brings nothing old back; their counters are
// unknown until the server says; then the counters and the page shown are
// read again, each once, the person's priority. The ticket, if one is
// shown, stays as it is: the answer to a write replaces no record.
func (a *app) afterMark(op *markOp) tea.Cmd {
	if a.expired || a.scope == nil {
		return nil
	}
	now := a.now()
	for _, res := range a.touchedResources(op) {
		a.cancelResource(res)
		a.store.Apply(res, cache.Remove, nil, now)
		a.scope.Invalidate(res)
	}
	a.counters[op.org] = count{seq: a.counters[op.org].seq}
	a.global = count{seq: a.global.seq}
	before := make(map[gateway.Waiter]bool, len(a.waiters))
	for id := range a.waiters {
		before[id] = true
	}
	cmds := []tea.Cmd{a.issueCounters()}
	if v := a.active; v != nil && (v.org == op.org || v.org == "") {
		cmds = append(cmds, a.refreshActive())
	}
	// The reads just issued are the operation's clarifiers.
	op.clarifiers = nil
	for id := range a.waiters {
		if !before[id] {
			op.clarifiers = append(op.clarifiers, id)
		}
	}
	return tea.Batch(cmds...)
}

// cancelResource cancels every read of the resource still out: its answer
// predates the write, and the epoch refuses it anyway; canceling spares
// the request.
func (a *app) cancelResource(res cache.Key) {
	for _, w := range a.waiters {
		if !w.canceled && w.ticket.Key.Resource() == res {
			w.canceled = true
			w.cancel()
		}
	}
}

// forgetSettledMarks drops the records of operations that ended, on
// leaving the inbox: their state was shown where it belonged. A running
// operation keeps its record: its result still has to invalidate and
// reload.
func (a *app) forgetSettledMarks() {
	for id, op := range a.marks {
		if op.state != markRunning {
			delete(a.marks, id)
		}
	}
}

// --- the ticket opened from a notification -----------------------------------

// linkedTicket says whether the notification whose details are shown names
// a ticket the pane can open: a project and a ticket id, and the tool.
func (a *app) linkedTicket() (api.Notification, bool) {
	n, ok := a.openedNotification()
	if !ok || !a.ticketOn() || n.Ticket == nil || n.Ticket.ID == "" || n.Ticket.ProjectID == "" {
		return api.Notification{}, false
	}
	return n, true
}

// openLinkedTicket is o in the details of a notification: the ticket it
// names opens in the pane, from the cache at once when the cache holds it
// and read once, with the notification remembered as its origin. Nothing
// is marked by it.
func (m Model) openLinkedTicket() (Model, tea.Cmd) {
	a := m.app
	n, ok := a.linkedTicket()
	if !ok {
		return m, nil
	}
	a.origin = &origin{notification: n.ID, org: n.Organisation.ID, project: n.Ticket.ProjectID, ticket: n.Ticket.ID, unread: !n.Read}
	d := a.ensureDetail(n.Ticket.ProjectID, n.Ticket.ID)
	d.offset, d.follow, d.area, d.post = 0, anchorNone, areaDescription, ""
	m.focus = view.Object
	return m, a.readUnlessFresh(d)
}

// closeLinkedTicket takes the ticket out of the pane and restores where
// the person came from: the details of the notification when its row is
// still on the page, else the list (the row left with a reload).
func (m Model) closeLinkedTicket() Model {
	a := m.app
	if a.origin == nil {
		return m
	}
	a.dropDetail()
	a.origin = nil
	if _, ok := a.openedNotification(); ok {
		m.focus = view.Object
		return m
	}
	if v := a.active; v != nil {
		v.opened = ""
	}
	m.focus = view.List
	return m
}

// leaveLinkedTicket drops the ticket without a return: the inbox moves on
// (another row's details, another page, another organisation).
func (a *app) leaveLinkedTicket() {
	if a.origin == nil {
		return
	}
	a.dropDetail()
	a.origin = nil
}

// --- what is shown ----------------------------------------------------------

// markText is the line that says what became of marking a notification,
// or why it cannot be marked: the operation's state when there is one,
// else the reason r is not offered for an unread row. Empty when there is
// nothing to say.
func (a *app) markText(notification string, unread bool) string {
	if op := a.marks[notification]; op != nil {
		switch op.state {
		case markRunning:
			return uitext.Inline(uitext.Marking).String()
		case markConfirmed:
			return uitext.Inline(uitext.Marked).String()
		case markUnknown:
			if a.clarifying(op) {
				return uitext.Inline(uitext.MarkUnknownRefreshing).String()
			}
			return uitext.Inline(uitext.MarkUnknown).String()
		}
		return op.err.String()
	}
	if unread {
		if reason := a.writeBlocked(); reason != 0 {
			return uitext.Inline(reason).String()
		}
	}
	return ""
}

// markShort is the word at the end of an inbox row with an operation.
func (a *app) markShort(notification string) present.Inline {
	op := a.marks[notification]
	if op == nil {
		return present.Inline{}
	}
	switch op.state {
	case markRunning:
		return uitext.Inline(uitext.MarkingShort)
	case markConfirmed:
		return uitext.Inline(uitext.MarkedShort)
	case markUnknown:
		return uitext.Inline(uitext.UnknownShort)
	}
	return uitext.Inline(uitext.NotMarkedShort)
}
