package ui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/cache"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/uitext"
)

// Booking time on a ticket (K6): b on the known ticket in the focused pane
// opens a dialog, Enter books over REST. Offered when /me names the
// capability and the token may write; a project's rights are not guessed
// from anything: without them the server's forbidden stands in the box.
//
// The duration goes as it was typed: the server's parser is the one parser,
// and the minutes shown come from its answer. Every press of Enter is one
// operation with its own idempotency key, on the ticket taken when b was
// pressed. It holds the ticket's lock until its end is known: a second booking
// waits for the first, on that ticket alone, and Esc leaves it running.
//
// Ends, from placeWrite: a confirmed one closes the box and says the minutes;
// a refused or unsent one stays in the box with the input, ready for the
// person's next press, which is a new operation with a new key. An unknown one
// is never sent again: it holds the box, with the time read again to go by.
// The way on from there is Esc and b, the person's new action; Enter in that
// box could be the second half of a double press after a quick loss. After
// Esc the same ends are told on the status line, wherever the person is.
//
// A confirmed and an unknown booking both drop the time on the ticket and
// read it again. Nothing else: a booking writes no activity and no
// notification (the core's R6), so the inbox and the ticket stay as they are.

const (
	// bookDurationLimit and bookNoteLimit are the fields' limits, in runes:
	// the server has none, and these are more than either is for.
	bookDurationLimit = 32
	bookNoteLimit     = 200
	// maxRefusal bounds a server's text in the box or on the status line,
	// whose three lines it is wrapped into at every frame.
	maxRefusal = 240
	// problemFor is how long a problem with a booking stands on the status
	// line: a hint's hintFor is too short to come back to.
	problemFor = 30 * time.Second
)

// bookOp is one booking on its way, or one nobody knows the end of.
type bookOp struct {
	id uint64
	// The target, taken from the dialog: it does not move with the pane.
	project, ticket string
	number          int
	// dialog is the box it was sent from, which may be gone by the end.
	dialog uint64
	cancel context.CancelFunc
	// unclear: it ended, and nobody knows how.
	unclear bool
}

// bookNotice says what became of a booking on the status line. It stands
// until its deadline, wherever the person went meanwhile.
type bookNotice struct {
	text  present.Inline
	tone  present.Style
	until time.Time
}

// bookTarget is the ticket b would book on right now: the known ticket in
// the focused pane, while nothing holds its lock.
func (m Model) bookTarget() (DialogTarget, bool) {
	a := m.app
	if a == nil || !a.bookAllowed() {
		return DialogTarget{}, false
	}
	t, ok := m.ticketTarget()
	if !ok || a.bookBlocks(t.Ticket) {
		return DialogTarget{}, false
	}
	return t, true
}

// bookAllowed: the token may write and /me names the capability. There is
// no other way to book: the client never used the tool.
func (a *app) bookAllowed() bool {
	return a.tokenWrites() && a.on("time_book") && a.deps.Gateway != nil
}

// bookBlocks is the ticket's lock: a booking on it is out, or its end is
// unknown and the read that clears it up has not landed. Any later read of
// the time clears it up as well: it left after the booking did.
func (a *app) bookBlocks(ticket string) bool {
	op := a.bookings[ticket]
	if op == nil {
		return false
	}
	return !op.unclear || a.pending(a.timeKey(op.project, op.ticket))
}

// pressB is b: the dialog on the ticket in the pane, which it holds from
// here on.
func (m Model) pressB() Model {
	if _, ok := m.bookTarget(); !ok {
		return m
	}
	return m.openTicketDialog(DialogSpec{
		Action:   uitext.Inline(uitext.BookAction),
		SendHelp: uitext.Inline(uitext.BookSendHelp),
		Fields: []DialogField{
			{Label: uitext.Inline(uitext.BookDuration), Hint: uitext.Inline(uitext.BookDurationHint), Limit: bookDurationLimit, Required: true},
			{Label: uitext.Inline(uitext.BookNote), Limit: bookNoteLimit},
		},
		Send: m.app.sendBooking,
	})
}

// sendBooking is one press of Enter: the ticket's lock first, then the
// operation with a key of its own, then the command of the write.
func (a *app) sendBooking(s Submission) tea.Cmd {
	t := s.Target
	if a.bookBlocks(t.Ticket) {
		return func() tea.Msg { return DialogOutcome{Dialog: s.Dialog, Refusal: uitext.Inline(uitext.BookBusy)} }
	}
	a.nextOp++
	ctx, cancel := context.WithCancel(a.base)
	op := &bookOp{id: a.nextOp, project: t.Project, ticket: t.Ticket, number: t.Number, dialog: s.Dialog, cancel: cancel}
	a.bookings[t.Ticket] = op
	return a.deps.Gateway.Write(ctx, gateway.WriteTicket{
		Session: a.session, Op: op.id, Kind: gateway.BookTime, Key: a.idempotencyKey("book", op.id),
		Project: t.Project, Ticket: t.Ticket, Value: s.Values[0], Note: s.Values[1],
	})
}

// receiveBooking files what became of a booking under the operation it
// started, whatever the pane shows now. A 401 ends the session first. A
// result of another session or of no operation running is dropped.
func (m Model) receiveBooking(r gateway.WriteResult) (Model, tea.Cmd) {
	a := m.app
	if a == nil || a.expired || r.Session != a.session {
		return m, nil
	}
	if r.Answer.Class == transport.Unauthenticated {
		m.expire()
		return m, nil
	}
	op := a.bookings[r.Ticket]
	if op == nil || op.id != r.Op || op.unclear {
		return m, nil
	}
	op.cancel()
	inBox := m.dialog != nil && m.dialog.id == op.dialog
	switch end := placeWrite(r.Answer); end {
	case writeConfirmed:
		delete(a.bookings, op.ticket)
		told := a.tellBooking(uitext.Fill(uitext.BookDone, hoursMinutes(r.Booking.Minutes), op.number), present.Plain, hintFor)
		read := a.afterBooking(op)
		var closed tea.Cmd
		m, closed = m.receiveOutcome(DialogOutcome{Dialog: op.dialog, Done: true})
		return m, tea.Batch(told, read, closed)
	case writeUnknown:
		op.unclear = true
		read := a.afterBooking(op)
		if inBox {
			m, _ = m.receiveOutcome(DialogOutcome{Dialog: op.dialog, Held: func() (present.Inline, present.Style) { return a.unclearLine(op) }})
			return m, read
		}
		return m, tea.Batch(read, a.tellBooking(uitext.Fill(uitext.BookUnclearNotice, op.number), present.NoticeProblem, problemFor))
	default:
		delete(a.bookings, op.ticket)
		why := notBooked(end, r.Answer)
		if inBox {
			return m.receiveOutcome(DialogOutcome{Dialog: op.dialog, Refusal: why})
		}
		return m, a.tellBooking(uitext.Fill(uitext.BookNotBooked, op.number, why.String()), present.NoticeProblem, problemFor)
	}
}

// notBooked is why a booking did not happen: what stopped it from reaching
// the server, else the server's text, cleaned here where it is shown; the
// API's 403 and 404 carry none, and their class says it.
func notBooked(end writeEnd, ans transport.Answer) present.Inline {
	switch end {
	case writeNotSent:
		return uitext.Inline(uitext.BookNotSent)
	case writeRateLimited:
		return uitext.Inline(uitext.BookRateLimited)
	}
	if text := ans.RefusalText(); text != "" {
		return present.NewInline(bounded(text, maxRefusal))
	}
	switch ans.Class {
	case transport.NoAccess:
		return uitext.Inline(uitext.BookForbidden)
	case transport.ReadOnly:
		return uitext.Inline(uitext.ReadOnlyToken)
	case transport.NotFound:
		return uitext.Inline(uitext.BookGone)
	}
	return uitext.Inline(uitext.DialogRefused)
}

// afterBooking follows a confirmed booking and an unknown one alike: the
// time on the ticket leaves the cache and its epoch moves on, so a read
// begun before brings nothing old back. In the pane the line goes, the old
// sum being wrong by now, and the time is read again, the person's priority,
// for as long as the pane still shows the ticket. Elsewhere the next open
// reads it.
func (a *app) afterBooking(op *bookOp) tea.Cmd {
	res := a.timeKey(op.project, op.ticket).Resource()
	a.cancelResource(res)
	a.store.Apply(res, cache.Remove, nil, a.now())
	a.scope.Invalidate(res)
	d := a.detail
	if d == nil || d.id != op.ticket || d.project != op.project {
		return nil
	}
	d.booked = bookedTime{}
	return a.readTime(d, false)
}

// tellBooking puts a booking's end on the status line, which has one place
// for it: a hint does not take the place of a problem still standing, which
// is what the person has to come back to. The Events wake the model at the
// deadline, so the line is drawn again.
func (a *app) tellBooking(text present.Inline, tone present.Style, lasts time.Duration) tea.Cmd {
	now := a.now()
	if tone == present.Plain && a.bookNotice.tone == present.NoticeProblem && a.bookNotice.until.After(now) {
		return nil
	}
	a.bookNotice = bookNotice{text: text, tone: tone, until: now.Add(lasts)}
	if a.deps.Events == nil {
		return nil
	}
	return a.deps.Events.Expiry(a.bookNotice.until, now)
}

// unclearLine is what a box held on op says, as it is now: that the time is
// being read, then the time on the ticket to go by, when the pane still shows
// that ticket and the read brought one.
func (a *app) unclearLine(op *bookOp) (present.Inline, present.Style) {
	if a.pending(a.timeKey(op.project, op.ticket)) {
		return uitext.Inline(uitext.BookUnclearReading), present.NoticeWaiting
	}
	if d := a.detail; d != nil && d.id == op.ticket && d.project == op.project && d.booked.known {
		return uitext.Fill(uitext.BookUnclearRead, hoursMinutes(d.booked.minutes)), present.NoticeProblem
	}
	return uitext.Inline(uitext.BookUnclear), present.NoticeProblem
}
