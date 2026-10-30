package ui

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/cache"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/uitext"
)

// What every write of the interface shares: who may, the key of an action,
// and the one reading of what the answer says of it; and what a write on a
// ticket through a dialog shares, the booking (K6) and the comment (K3a): its
// lock, per ticket and kind, its ends and the status line that tells them.

// writeEnd is what an answer says became of a write.
type writeEnd uint8

const (
	// writeConfirmed: the server did it, and the gateway saw it in the answer.
	writeConfirmed writeEnd = iota + 1
	// writeRefused: the server said no.
	writeRefused
	// writeNotSent: it never reached the server.
	writeNotSent
	// writeRateLimited: the server rate-limited the queue's one retry too;
	// neither attempt was acted on.
	writeRateLimited
	// writeUnknown: it left and no usable answer came back: the server may
	// have acted. It is reloaded, never sent again.
	writeUnknown
)

// placeWrite reads an answer to a write, from its class alone (cache.Decide
// with Write, so a refused write never removes a record). An answer the
// client cannot place, after a request that left, says nothing about the
// write; a decision a write never takes counts as unknown, the safe side.
func placeWrite(ans transport.Answer) writeEnd {
	switch cache.Decide(ans.Class, cache.Write) {
	case cache.Reload:
		return writeConfirmed
	case cache.KeepStale:
		// The server was not reached, or it answered 429 to the retry as
		// well; anything else after the request left may have been carried
		// out.
		switch {
		case !ans.Sent:
			return writeNotSent
		case ans.Status == 429:
			return writeRateLimited
		}
	case cache.Drop:
		// Canceled by the client: the session ended or the program stops.
		if !ans.Sent {
			return writeNotSent
		}
	case cache.KeepShowMessage:
		if ans.Class != transport.Protocol || !ans.Sent {
			return writeRefused
		}
	}
	return writeUnknown
}

// tokenWrites: /me succeeded and said the token may write. A token that does
// not say (a server before MCP 0.7.0) does not write. The profile's
// allow_write is for agent commands and never reaches here.
func (a *app) tokenWrites() bool {
	return a.steps[stepMe].state == ready && a.me.Token.ReadOnly != nil && !*a.me.Token.ReadOnly
}

// idempotencyKey is the key of one action: the session and the operation
// number make it unique in this run, the clock across runs. Its form is
// what the server keeps unchanged (letters, digits and -, 8 to 128).
func (a *app) idempotencyKey(kind string, op uint64) string {
	return "lalabase-" + kind + "-" + strconv.FormatUint(uint64(a.session), 10) + "-" + strconv.FormatUint(op, 10) + "-" + strconv.FormatInt(a.now().UnixNano(), 10)
}

const (
	// maxRefusal bounds a server's text in the box or on the status line,
	// whose three lines it is wrapped into at every frame.
	maxRefusal = 240
	// problemFor is how long a problem with a write stands on the status
	// line: a hint's hintFor is too short to come back to.
	problemFor = 30 * time.Second
)

// ticketOp is one write on a ticket on its way, or one nobody knows the end
// of.
type ticketOp struct {
	id uint64
	// The target, taken from the dialog: it does not move with the pane.
	project, ticket, org string
	number               int
	// dialog is the box it was sent from, which may be gone by the end.
	dialog uint64
	cancel context.CancelFunc
	// unclear: it ended, and nobody knows how.
	unclear bool
	// clarifiers are the reads issued for an end nobody knows, where the
	// kind goes by them.
	clarifiers []gateway.Waiter
}

// ticketWrites is what tells one kind of write on a ticket from another.
type ticketWrites struct {
	kind gateway.WriteKind
	// name stands in the kind's idempotency keys.
	name string
	// ops are the operations of the kind, by ticket, each the newest for its
	// ticket: the kind's lock.
	ops map[string]*ticketOp
	// clearing: the end nobody knows of op is still being cleared up.
	clearing func(op *ticketOp) bool
	// busy is what a send says that finds the lock held.
	busy uitext.ID
	// done says a confirmed write, and acts on it; unclear acts on an end
	// nobody knows, held is the line of a box held on it.
	done    func(op *ticketOp, r gateway.WriteResult) (present.Inline, tea.Cmd)
	unclear func(op *ticketOp) tea.Cmd
	held    func(op *ticketOp) (present.Inline, present.Style)
	// why says what kept a write from happening; notDone (the number, why)
	// and unclearNotice (the number) tell an end on the status line.
	why                    func(end writeEnd, r gateway.WriteResult) present.Inline
	notDone, unclearNotice uitext.ID
}

// blocks is the ticket's lock of the kind: a write is out, or its end is
// unknown and being cleared up.
func (w ticketWrites) blocks(ticket string) bool {
	op := w.ops[ticket]
	return op != nil && (!op.unclear || w.clearing(op))
}

// freeTarget is the ticket a key of the kind would write on right now: the
// known ticket in the focused pane, while nothing holds its lock.
func (m Model) freeTarget(w ticketWrites) (DialogTarget, bool) {
	t, ok := m.ticketTarget()
	if !ok || w.blocks(t.Ticket) {
		return DialogTarget{}, false
	}
	return t, true
}

// sendOnTicket is one send: the ticket's lock first, then the operation with
// a key of its own, then the command of the write, whose payload is t's.
func (a *app) sendOnTicket(w ticketWrites, s Submission, t gateway.WriteTicket) tea.Cmd {
	target := s.Target
	if w.blocks(target.Ticket) {
		return func() tea.Msg { return DialogOutcome{Dialog: s.Dialog, Refusal: uitext.Inline(w.busy)} }
	}
	a.forgetNotice(w.kind, target.Ticket)
	a.nextOp++
	ctx, cancel := context.WithCancel(a.base)
	op := &ticketOp{id: a.nextOp, project: target.Project, ticket: target.Ticket, org: target.Organisation, number: target.Number, dialog: s.Dialog, cancel: cancel}
	w.ops[target.Ticket] = op
	t.Session, t.Op, t.Kind, t.Key, t.Project, t.Ticket = a.session, op.id, w.kind, a.idempotencyKey(w.name, op.id), op.project, op.ticket
	return a.deps.Gateway.Write(ctx, t)
}

// endOnTicket files what became of a write under the operation it started,
// whatever the pane shows now. A 401 ends the session first. A result of
// another session or of no operation running is dropped.
func (m Model) endOnTicket(w ticketWrites, r gateway.WriteResult) (Model, tea.Cmd) {
	a := m.app
	if a.expired || r.Session != a.session {
		return m, nil
	}
	if r.Answer.Class == transport.Unauthenticated {
		m.expire()
		return m, nil
	}
	op := w.ops[r.Ticket]
	if op == nil || op.id != r.Op || op.unclear {
		return m, nil
	}
	op.cancel()
	inBox := m.dialog != nil && m.dialog.id == op.dialog
	switch end := placeWrite(r.Answer); end {
	case writeConfirmed:
		delete(w.ops, op.ticket)
		text, read := w.done(op, r)
		told := a.tell(w.kind, op.ticket, text, present.Plain, hintFor)
		var closed tea.Cmd
		m, closed = m.receiveOutcome(DialogOutcome{Dialog: op.dialog, Done: true})
		return m, tea.Batch(told, read, closed)
	case writeUnknown:
		op.unclear = true
		read := w.unclear(op)
		if inBox {
			m, _ = m.receiveOutcome(DialogOutcome{Dialog: op.dialog, Held: func() (present.Inline, present.Style) { return w.held(op) }})
			return m, read
		}
		return m, tea.Batch(read, a.tell(w.kind, op.ticket, uitext.Fill(w.unclearNotice, op.number), present.NoticeProblem, problemFor))
	default:
		delete(w.ops, op.ticket)
		why := w.why(end, r)
		if inBox {
			return m.receiveOutcome(DialogOutcome{Dialog: op.dialog, Refusal: why})
		}
		return m, a.tell(w.kind, op.ticket, uitext.Fill(w.notDone, op.number, why.String()), present.NoticeProblem, problemFor)
	}
}

// notDoneWords are a kind's words for what kept a write from happening.
type notDoneWords struct{ notSent, rateLimited, forbidden uitext.ID }

// notDone is why a write did not happen: what stopped it from reaching the
// server, else the server's text, cleaned here where it is shown; the API's
// 403 and 404 carry none, and their class says it.
func notDone(words notDoneWords, end writeEnd, ans transport.Answer) present.Inline {
	switch end {
	case writeNotSent:
		return uitext.Inline(words.notSent)
	case writeRateLimited:
		return uitext.Inline(words.rateLimited)
	}
	if text := ans.RefusalText(); text != "" {
		return present.NewInline(bounded(text, maxRefusal))
	}
	switch ans.Class {
	case transport.NoAccess:
		return uitext.Inline(words.forbidden)
	case transport.ReadOnly:
		return uitext.Inline(uitext.ReadOnlyToken)
	case transport.NotFound:
		return uitext.Inline(uitext.BookGone)
	}
	return uitext.Inline(uitext.DialogRefused)
}

// dropResources takes what a write changed out of the cache and moves its
// epochs on, so a read begun before brings nothing old back.
func (a *app) dropResources(resources ...cache.Key) {
	now := a.now()
	for _, res := range resources {
		a.cancelResource(res)
		a.store.Apply(res, cache.Remove, nil, now)
		a.scope.Invalidate(res)
	}
}

// issuedBy runs read and names the reads it issued: an operation's
// clarifiers.
func (a *app) issuedBy(read func() tea.Cmd) (tea.Cmd, []gateway.Waiter) {
	before := make(map[gateway.Waiter]bool, len(a.waiters))
	for id := range a.waiters {
		before[id] = true
	}
	cmd := read()
	var issued []gateway.Waiter
	for id := range a.waiters {
		if !before[id] {
			issued = append(issued, id)
		}
	}
	return cmd, issued
}

// clarifying: one of the reads is still out for somebody. A read the person
// canceled since (a page turn, another inbox) clears nothing up any more.
func (a *app) clarifying(reads []gateway.Waiter) bool {
	for _, id := range reads {
		if w := a.waiters[id]; w != nil && !w.canceled {
			return true
		}
	}
	return false
}

// bookNotice says what became of a write of a kind on ticket, on the status
// line. It stands until its deadline, wherever the person went meanwhile, or
// until the person sends that kind on that ticket again.
type bookNotice struct {
	kind   gateway.WriteKind
	ticket string
	text   present.Inline
	tone   present.Style
	until  time.Time
}

// tell puts the end of a write on the status line, beside the ends of other
// writes: the press it came from took the ticket's own away. The Events wake
// the model at its deadline, so the line is drawn again.
func (a *app) tell(kind gateway.WriteKind, ticket string, text present.Inline, tone present.Style, lasts time.Duration) tea.Cmd {
	now := a.now()
	n := bookNotice{kind: kind, ticket: ticket, text: text, tone: tone, until: now.Add(lasts)}
	a.bookNotices = append(a.bookNotices, n)
	if a.deps.Events == nil {
		return nil
	}
	return a.deps.Events.Expiry(n.until, now)
}

// forgetNotice takes what the line says of a kind on a ticket away: a new
// press on it is the person's answer to that, and it must never stand over
// the end of the write that follows. A write of another kind is another
// matter.
func (a *app) forgetNotice(kind gateway.WriteKind, ticket string) {
	a.bookNotices = slices.DeleteFunc(a.bookNotices, func(n bookNotice) bool { return n.kind == kind && n.ticket == ticket })
}

// writeStatus is what the line says of writes now: the ends whose deadline
// has not come, oldest first, in the tone of a problem when one is among them.
// shown is false when none is.
func (a *app) writeStatus() (text present.Inline, tone present.Style, shown bool) {
	now := a.now()
	var ends []string
	for _, n := range a.bookNotices {
		if n.until.After(now) {
			ends = append(ends, n.text.String())
			if n.tone == present.NoticeProblem {
				tone = n.tone
			}
		}
	}
	return present.NewInline(strings.Join(ends, uitext.Inline(uitext.PathDetail).String())), tone, len(ends) > 0
}
