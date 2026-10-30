package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/present"
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
// Esc the same ends are told on the status line, wherever the person is. The
// line tells the end of each ticket side by side, until its deadline
// (bookNotice), and the next press on a ticket takes its end away: what it says
// is never older than what is known, and the end of one ticket never takes
// the end of another away. All of it is what writes on a ticket share
// (write.go), the comment's lock and ends too.
//
// A confirmed and an unknown booking both drop the time on the ticket and
// read it again. Nothing else: a booking writes no activity and no
// notification (the core's R6), so the inbox and the ticket stay as they are.

const (
	// bookDurationLimit and bookNoteLimit are the fields' limits, in runes:
	// the server has none, and these are more than either is for.
	bookDurationLimit = 32
	bookNoteLimit     = 200
)

// bookingWrites is the booking among the writes on a ticket (write.go). Any
// later read of the time clears an unknown end up as well: it left after the
// booking did.
func (a *app) bookingWrites() ticketWrites {
	return ticketWrites{
		kind: gateway.BookTime, name: "book", ops: a.bookings, busy: uitext.BookBusy,
		clearing: func(op *ticketOp) bool { return a.pending(a.timeKey(op.project, op.ticket)) },
		done: func(op *ticketOp, r gateway.WriteResult) (present.Inline, tea.Cmd) {
			return uitext.Fill(uitext.BookDone, hoursMinutes(r.Booking.Minutes), op.number), a.afterBooking(op)
		},
		unclear: a.afterBooking,
		held:    a.unclearLine,
		why: func(end writeEnd, r gateway.WriteResult) present.Inline {
			return notDone(notDoneWords{uitext.BookNotSent, uitext.BookRateLimited, uitext.BookForbidden}, end, r.Answer)
		},
		notDone: uitext.BookNotBooked, unclearNotice: uitext.BookUnclearNotice,
	}
}

// bookTarget is the ticket b would book on right now: the known ticket in
// the focused pane, while nothing holds its lock.
func (m Model) bookTarget() (DialogTarget, bool) {
	a := m.app
	if a == nil || !a.bookAllowed() {
		return DialogTarget{}, false
	}
	return m.freeTarget(a.bookingWrites())
}

// bookAllowed: the token may write and /me names the capability. There is
// no other way to book: the client never used the tool.
func (a *app) bookAllowed() bool {
	return a.tokenWrites() && a.on("time_book") && a.deps.Gateway != nil
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

// sendBooking is one press of Enter.
func (a *app) sendBooking(s Submission) tea.Cmd {
	return a.sendOnTicket(a.bookingWrites(), s, gateway.WriteTicket{Value: s.Values[0], Note: s.Values[1]})
}

// receiveBooking files what became of a booking.
func (m Model) receiveBooking(r gateway.WriteResult) (Model, tea.Cmd) {
	if m.app == nil {
		return m, nil
	}
	return m.endOnTicket(m.app.bookingWrites(), r)
}

// afterBooking follows a confirmed booking and an unknown one alike: the
// time on the ticket leaves the cache and its epoch moves on, so a read
// begun before brings nothing old back. In the pane the line goes, the old
// sum being wrong by now, and the time is read again, the person's priority,
// for as long as the pane still shows the ticket. Elsewhere the next open
// reads it.
func (a *app) afterBooking(op *ticketOp) tea.Cmd {
	a.dropResources(a.timeKey(op.project, op.ticket).Resource())
	d := a.detail
	if d == nil || d.id != op.ticket || d.project != op.project {
		return nil
	}
	d.booked = bookedTime{}
	return a.readTime(d, false)
}

// tellBooking puts a booking's end on the status line (tell).
func (a *app) tellBooking(ticket string, text present.Inline, tone present.Style, lasts time.Duration) tea.Cmd {
	return a.tell(gateway.BookTime, ticket, text, tone, lasts)
}

// unclearLine is what a box held on op says, as it is now: that the time is
// being read, then the time on the ticket to go by, when the pane still shows
// that ticket and the read brought one.
func (a *app) unclearLine(op *ticketOp) (present.Inline, present.Style) {
	if a.pending(a.timeKey(op.project, op.ticket)) {
		return uitext.Inline(uitext.BookUnclearReading), present.NoticeWaiting
	}
	if d := a.detail; d != nil && d.id == op.ticket && d.project == op.project && d.booked.known {
		return uitext.Fill(uitext.BookUnclearRead, hoursMinutes(d.booked.minutes)), present.NoticeProblem
	}
	return uitext.Inline(uitext.BookUnclear), present.NoticeProblem
}
