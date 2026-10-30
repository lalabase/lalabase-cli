package ui

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/cache"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
	"github.com/gboyke/lalabase-cli/internal/uitext"
)

// K6: time booked on a ticket from the terminal. Every test names the
// change it would miss.

const (
	offerB     = "b book time"
	durationIs = "Duration"
)

func six() string { return ticketID(website, 6) }

// 1. b needs all three: a ticket in the focused pane, the capability, a
// token that says it may write. Unknown is no: a server before MCP 0.7.0
// does not say. Would miss: nil taken as false; a lock on the help alone.
func TestBIsOfferedWithTheCapabilityAndAWritingTokenAlone(t *testing.T) {
	t.Parallel()

	writes := boolPtr(false)
	for _, tc := range []struct {
		name  string
		token func(h *harness)
		want  bool
	}{
		{"both", func(h *harness) { h.setToken(writes) }, true},
		{"no capability", func(h *harness) { h.setToken(writes, "time_sessions.book") }, false},
		{"no capabilities at all, the tool beside them", func(h *harness) { h.setReadOnly(writes) }, false},
		{"a read-only token", func(h *harness) { h.setToken(boolPtr(true)) }, false},
		{"a token that does not say", func(h *harness) { h.setToken(nil) }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newRESTHarness(t)
			tc.token(h)
			h.openSix()
			if got := strings.Contains(h.helpText(), offerB); got != tc.want {
				t.Fatalf("offered %v: %s", got, h.helpText())
			}
			if h.offers(BookTime) != tc.want {
				t.Fatalf("allowed %v", h.offers(BookTime))
			}
			if tc.want {
				return
			}
			// Not only the help: neither the key nor the path behind it acts.
			h.press("b")
			if next, _ := h.m.sessionKey(BookTime); next.dialog != nil || h.m.dialog != nil {
				t.Fatal("b opened a dialog")
			}
		})
	}
}

// The ticket in the pane is the target only with the focus on it: not from
// the list beside it, not while it is unknown, not in a notification's
// details, which name a ticket they do not show. The ticket opened from a
// notification and the one an agent opened are tickets like any other.
func TestBActsOnTheTicketInTheFocusedPaneAlone(t *testing.T) {
	t.Parallel()

	h := bookingHarness(t)
	h.m.focus = view.List
	if h.offers(BookTime) || strings.Contains(h.helpText(), offerB) {
		t.Fatalf("b from the list: %s", h.helpText())
	}
	h.press("b")
	if h.m.dialog != nil {
		t.Fatal("a dialog from the list")
	}
	h.m.focus = view.Object
	h.m.app.detail.known = false
	if h.offers(BookTime) {
		t.Fatal("b on a ticket that is not known")
	}
	h.m.app.detail.known = true

	h = newRESTHarness(t)
	h.start()
	h.openDetails(orgA, 5)
	if h.offers(BookTime) {
		t.Fatalf("b in the details of a notification: %s", h.helpText())
	}
	h.drive(h.press("o"))
	if !h.offers(BookTime) {
		t.Fatalf("no b on the ticket opened from a notification: %s", h.helpText())
	}
}

// In a dialog b is text like every key, and in the search line too: there
// the keys write, and no dialog opens from an open line (the check M1 left
// to K6). Would miss: b routed before the mode.
func TestBIsTextWhereKeysWrite(t *testing.T) {
	t.Parallel()

	h := bookingHarness(t)
	h.press("b")
	h.press("b")
	if got := dialogValues(h.m); got[0] != "b" {
		t.Fatalf("values %q", got)
	}

	h = newRESTHarness(t)
	h.openTickets(website)
	h.press("/")
	h.press("b")
	if h.m.mode != TextEntry || h.m.app.tickets.entry != "b" || h.m.dialog != nil {
		t.Fatalf("mode %d, entry %q, dialog %v", h.m.mode, h.m.app.tickets.entry, h.m.dialog != nil)
	}
	// Whatever brought the focus there, an open line is no place for one.
	h = bookingHarness(t)
	h.m.mode = TextEntry
	if _, ok := h.m.ticketTarget(); ok {
		t.Fatal("a target from an open search line")
	}
}

// The dialog: the action, the ticket's number and title, the fields and
// their hint, the help. Would miss: another key's name in its help.
func TestTheBookingDialogSaysWhatItBooksOn(t *testing.T) {
	t.Parallel()

	h := bookingHarness(t)
	h.press("b")
	text := boxed(h.m)
	for _, want := range []string{"Book time · #6 Ticket 6", durationIs, "30, 1:30 or 1.5", "Note", "enter book", "tab next field", "esc cancel"} {
		if !strings.Contains(text, want) {
			t.Fatalf("%q is missing:\n%s", want, text)
		}
	}
}

// 2. The whole booking: one write with what was typed, on the ticket; the
// box closes; the notice has the minutes the server read, not what was
// typed; the time is read again and shows; nothing else is read, since a
// booking writes no activity and no notification (R6). Would miss: a
// second write, minutes from the input, a reload of the inbox or the ticket.
func TestABookingBooksOnceAndReadsTheTimeAgain(t *testing.T) {
	t.Parallel()

	h := bookingHarness(t)
	times, inbox, tickets, lists := len(h.f.bookedReads()), len(h.f.inboxCalls()), len(h.f.restCalls()), len(h.f.ticketListReads())
	h.setBooked(func(_, id string) transport.Answer { return bookedAnswer(id, 180) })
	h.f.setBook(func(c bookCall) (api.TimeBooking, transport.Answer) { return bookedOf(c.ticket, 90) })
	send := h.sendBooking("1,5", "Vertrag geprüft")
	send()
	calls := h.f.bookCalls()
	if len(calls) != 1 || calls[0].ticket != six() || calls[0].duration != "1,5" || calls[0].note != "Vertrag geprüft" || !transport.ValidKey(calls[0].key) {
		t.Fatalf("calls %+v", calls)
	}
	if h.m.dialog != nil || h.m.mode != Navigate {
		t.Fatalf("the box stayed: mode %d", h.m.mode)
	}
	if got := h.status(); !strings.Contains(got, "1:30 booked") || !strings.Contains(got, "#6") {
		t.Fatalf("status %q", got)
	}
	if got := h.f.bookedReads(); len(got) != times+1 || got[times] != six() {
		t.Fatalf("time reads %v", got)
	}
	if got := h.metaText(); !strings.Contains(got, "Booked by you\n3:00") {
		t.Fatalf("meta bar:\n%s", got)
	}
	if len(h.f.inboxCalls()) != inbox || len(h.f.restCalls()) != tickets || len(h.f.ticketListReads()) != lists {
		t.Fatalf("reads besides the time: inbox %d, ticket %d, lists %d", len(h.f.inboxCalls())-inbox, len(h.f.restCalls())-tickets, len(h.f.ticketListReads())-lists)
	}
}

// The notice goes: it is a hint with a deadline. Would miss: a notice for
// the rest of the session.
func TestTheNoticeOfABookingGoesAtItsDeadline(t *testing.T) {
	t.Parallel()

	h := bookingHarness(t)
	h.sendBooking("30", "")()
	notices := h.m.app.bookNotices
	if len(notices) != 1 {
		t.Fatalf("notices %+v", notices)
	}
	until := notices[0].until
	if _, tone := h.m.status(); tone != present.Plain || !until.Equal(h.now.Add(5*time.Second)) {
		t.Fatalf("tone %v, until %v", tone, until)
	}
	h.now = until
	if got := h.status(); got != "" {
		t.Fatalf("status %q past its deadline", got)
	}
	h.feed(gateway.Expired{Session: h.m.app.session, Until: until})
	if got := h.m.app.bookNotices; len(got) != 0 {
		t.Fatalf("notices kept %+v", got)
	}
}

// A second booking is a booking: nothing of the first holds it up, and it
// has a key of its own.
func TestASecondBookingIsANewOne(t *testing.T) {
	t.Parallel()

	h := bookingHarness(t)
	h.sendBooking("30", "")()
	if !h.offers(BookTime) {
		t.Fatalf("no b after a booking: %s", h.helpText())
	}
	h.sendBooking("15", "")()
	calls := h.f.bookCalls()
	if len(calls) != 2 || calls[0].key == calls[1].key || calls[1].duration != "15" {
		t.Fatalf("calls %+v", calls)
	}
}

// While the time is read again the old sum is gone: after a booking it is
// wrong, and a read that fails does not bring it back, neither to the bar
// nor from the cache. Would miss: the line left as it was; the entry left
// in the cache, which the next open would show.
func TestTheOldTimeGoesWithABooking(t *testing.T) {
	t.Parallel()

	h := bookingHarness(t)
	if !strings.Contains(h.metaText(), "Booked by you\n2:30") {
		t.Fatalf("meta bar:\n%s", h.metaText())
	}
	h.setBooked(func(string, string) transport.Answer {
		return transport.Answer{Class: transport.Temporary, Message: "down", Status: 503, Sent: true}
	})
	h.sendBooking("30", "")()
	if strings.Contains(h.metaText(), "Booked") {
		t.Fatalf("the old sum stayed:\n%s", h.metaText())
	}
	if _, ok := h.m.app.store.Get(h.m.app.timeKey(website, six())); ok {
		t.Fatal("the old sum stayed in the cache")
	}
	h.setBooked(func(_, id string) transport.Answer { return bookedAnswer(id, 180) })
	h.m.focus = view.Object
	h.drive(h.press("R"))
	if !strings.Contains(h.metaText(), "Booked by you\n3:00") {
		t.Fatalf("R brought no time:\n%s", h.metaText())
	}
}

// A time read that left before the answer brings the old sum: it does not
// count. Would miss: an entry dropped without moving the epoch on.
func TestATimeReadBegunBeforeTheBookingDoesNotCount(t *testing.T) {
	t.Parallel()

	h := bookingHarness(t)
	h.m.focus = view.Object
	stale := h.m.app.readTime(h.m.app.detail, false) // 2:30, out, not landed
	if stale == nil {
		t.Fatal("no read out")
	}
	h.sendBooking("30", "")()
	h.setBooked(func(_, id string) transport.Answer { return bookedAnswer(id, 180) })
	h.drive(h.m.app.readTime(h.m.app.detail, false)) // the one after the booking
	h.drive(stale)
	if got := h.metaText(); !strings.Contains(got, "Booked by you\n3:00") {
		t.Fatalf("meta bar:\n%s", got)
	}
}

// What the disk is still reading comes too late as well: canceling reads
// reaches the network's alone, and only the epoch stops this one. Without
// the tool nothing is read back here, so the store would take it. Would
// miss: an entry dropped without moving the epoch on.
func TestATimeOnItsWayFromTheDiskDoesNotCount(t *testing.T) {
	t.Parallel()

	h := bookingHarness(t)
	a := h.m.app
	a.report.Enabled["booked_time"] = false
	key := a.timeKey(website, six())
	late := a.scope.Begin(gateway.Time, key, 0)
	h.sendBooking("30", "")()
	if a.scope.Promote(a.store, late, cache.Entry{Payload: bookedAnswer(six(), 150).Payload, LoadedAt: h.now}) {
		t.Fatal("a time from before the booking was filed")
	}
	if _, ok := a.store.Get(key); ok {
		t.Fatal("the old time is in the cache")
	}
}

// 3. A refusal: the box stays, the server's text in its error line, the
// input as it was. The next try is the person's: nothing goes out until
// enter, and then with a new key. Would miss: a key per dialog; an
// automatic retry; the input cleared.
func TestARefusalStaysInTheDialogAndTheNextTryHasANewKey(t *testing.T) {
	t.Parallel()

	h := bookingHarness(t)
	refused := recordedRefusal(t, "invalid_input_over_a_day", transport.InvalidInput)
	h.f.bookFails(refused)
	times := len(h.f.bookedReads())
	h.sendBooking("24:01", "Vertrag")()
	if h.m.dialog == nil || h.m.dialog.sending {
		t.Fatalf("no dialog waiting: %+v", h.m.dialog)
	}
	if got := h.dialogLine(); got != "Dauer darf höchstens 1440 Minuten (24 Stunden) betragen" {
		t.Fatalf("line %q", got)
	}
	if got := dialogValues(h.m); got[0] != "24:01" || got[1] != "Vertrag" {
		t.Fatalf("values %q", got)
	}
	if got := h.f.bookCalls(); len(got) != 1 {
		t.Fatalf("calls %+v", got)
	}
	if got := len(h.f.bookedReads()); got != times {
		t.Fatalf("%d time reads after a refusal", got-times)
	}
	if h.status() != "" {
		t.Fatalf("status %q while the box says it", h.status())
	}
	h.f.setBook(nil)
	h.drive(h.press("enter"))
	calls := h.f.bookCalls()
	if len(calls) != 2 || calls[0].key == calls[1].key || calls[1].duration != "24:01" || h.m.dialog != nil {
		t.Fatalf("calls %+v, dialog %v", calls, h.m.dialog != nil)
	}
}

// A server's text is foreign: what reaches the terminal is cleaned, and a
// very long one is cut before it is worked on at every frame. Would miss:
// the raw text in the line.
func TestARefusalsTextIsCleaned(t *testing.T) {
	t.Parallel()

	h := bookingHarness(t)
	body, _ := json.Marshal(map[string]any{"error": "validation_failed", "code": "invalid_input", "errors": []string{"SAFE" + dialogHostile + strings.Repeat("x", 100_000)}})
	h.f.bookFails(transport.Answer{Class: transport.InvalidInput, Message: string(body), Status: 422, Sent: true})
	h.sendBooking("1,5x", "")()
	content := h.m.View().Content
	if !strings.Contains(boxed(h.m), "SAFE") {
		t.Fatalf("no text:\n%s", boxed(h.m))
	}
	for _, bad := range []string{"\x1b[2J", "\x1b]8", "\x1b]52", "\u202e", "\u2066", "\x07", "\u009b"} {
		if strings.Contains(content, bad) {
			t.Fatalf("%q reached the terminal", bad)
		}
	}
	if len([]rune(h.dialogLine())) > maxRefusal+1 {
		t.Fatalf("a line of %d", len([]rune(h.dialogLine())))
	}
}

// The server's forbidden has no text of its own, nor has a 404 or a read-only
// token's refusal: the class says it. The dialog stays, b is shown whatever
// the project's rights are, and nothing is derived from them. Would miss:
// the raw body in the line, or a success's close.
func TestARefusalWithoutATextIsSaidByItsClass(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		class transport.Class
		want  string
	}{
		{"forbidden", transport.NoAccess, "You may not book time on this ticket"},
		{"read_only", transport.ReadOnly, "Read-only token"},
		{"not_found", transport.NotFound, "This ticket is gone, or no longer yours to see"},
		{"hidden", transport.NotFound, "This ticket is gone, or no longer yours to see"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := bookingHarness(t)
			h.f.bookFails(recordedRefusal(t, tc.name, tc.class))
			h.sendBooking("30", "")()
			if h.m.dialog == nil || h.dialogLine() != tc.want {
				t.Fatalf("dialog %v, line %q", h.m.dialog != nil, h.dialogLine())
			}
		})
	}
}

// A refusal that says nothing the client can read still is one.
func TestAnAnswerNoRuleNamesIsARefusal(t *testing.T) {
	t.Parallel()

	h := bookingHarness(t)
	h.f.bookFails(transport.Answer{Class: transport.Refused, Reason: "stale_object", Message: `{"error":"stale_object","code":"refused"}`, Status: 422, Sent: true})
	h.sendBooking("30", "")()
	if h.m.dialog == nil || h.dialogLine() == "" {
		t.Fatalf("dialog %v, line %q", h.m.dialog != nil, h.dialogLine())
	}
}

// 4. An unclear outcome: nothing goes out again, the time is read, and the
// box says so with what was read. Enter stays dead in this box: right after
// a fast loss a second press would be a second booking. The way on is esc,
// and b, a new action with a new key. Would miss: a resend; a dialog that
// frees enter; a time not read.
func TestAnUnclearBookingIsReadBackNeverSentAgain(t *testing.T) {
	t.Parallel()

	h := bookingHarness(t)
	h.f.bookFails(transport.Answer{Class: transport.Temporary, Message: "connection lost", Sent: true})
	times := len(h.f.bookedReads())
	h.setBooked(func(_, id string) transport.Answer { return bookedAnswer(id, 180) })
	h.sendBooking("30", "Vertrag")()
	if len(h.f.bookCalls()) != 1 {
		t.Fatalf("calls %+v", h.f.bookCalls())
	}
	if len(h.f.bookedReads()) != times+1 {
		t.Fatalf("no time read: %v", h.f.bookedReads())
	}
	if h.m.dialog == nil {
		t.Fatal("the box closed by itself")
	}
	if got := h.dialogLine(); !strings.Contains(got, "Not clear whether it was booked") || !strings.Contains(got, "3:00") {
		t.Fatalf("line %q", got)
	}
	if got := dialogValues(h.m); got[0] != "30" || got[1] != "Vertrag" {
		t.Fatalf("values %q", got)
	}
	if cmd := h.press("enter"); cmd != nil || len(h.f.bookCalls()) != 1 {
		t.Fatalf("enter acted: %+v", h.f.bookCalls())
	}
	first := h.f.bookCalls()[0].key
	h.press("esc")
	if h.m.dialog != nil || !h.offers(BookTime) {
		t.Fatalf("no way on: %s", h.helpText())
	}
	h.f.setBook(nil)
	h.sendBooking("30", "")()
	calls := h.f.bookCalls()
	if len(calls) != 2 || calls[1].key == first {
		t.Fatalf("calls %+v", calls)
	}
}

// What the box says follows what is known: while the read is out it says
// so and b stays locked; a read that fails says that too. Would miss: a
// line that stays at reading; the lock freed before the read landed.
func TestTheBoxSaysWhatIsKnownOfAnUnclearBooking(t *testing.T) {
	t.Parallel()

	h := bookingHarness(t)
	h.f.bookFails(transport.Answer{Class: transport.Temporary, Message: "connection lost", Sent: true})
	h.setBooked(func(string, string) transport.Answer {
		return transport.Answer{Class: transport.Temporary, Message: "down", Status: 503, Sent: true}
	})
	h.fillBooking("30", "")
	reads := h.feed(messages(t, h.press("enter"))...)
	if got := h.dialogLine(); !strings.Contains(got, "reading") {
		t.Fatalf("line %q", got)
	}
	h.press("esc")
	if h.offers(BookTime) {
		t.Fatal("b offered while the outcome is being cleared up")
	}
	h.press("b")
	if h.m.dialog != nil {
		t.Fatal("a dialog while the outcome is being cleared up")
	}
	h.drive(reads...)
	if !h.offers(BookTime) {
		t.Fatalf("b stayed locked: %s", h.helpText())
	}

	h = bookingHarness(t)
	h.f.bookFails(transport.Answer{Class: transport.Temporary, Message: "connection lost", Sent: true})
	h.setBooked(func(string, string) transport.Answer {
		return transport.Answer{Class: transport.Temporary, Message: "down", Status: 503, Sent: true}
	})
	h.sendBooking("30", "")()
	if got := h.dialogLine(); got != "Not clear whether it was booked" {
		t.Fatalf("line %q", got)
	}
}

// A success that does not confirm is unclear, whatever its class says: the
// gateway makes it a protocol answer, and it never reads "booked". Would
// miss: a confirmation loosened in the gateway.
func TestASuccessThatDoesNotConfirmIsNoBooking(t *testing.T) {
	t.Parallel()

	for name, booking := range map[string]api.TimeBooking{
		"another ticket": {ID: "x", TicketID: ticketID(website, 7), Minutes: 30},
		"no id":          {TicketID: ticketID(website, 6), Minutes: 30},
		"no minutes":     {ID: "x", TicketID: ticketID(website, 6)},
		"minus minutes":  {ID: "x", TicketID: ticketID(website, 6), Minutes: -1},
	} {
		t.Run(name, func(t *testing.T) {
			h := bookingHarness(t)
			h.f.setBook(func(bookCall) (api.TimeBooking, transport.Answer) {
				return booking, transport.Answer{Class: transport.Object, Status: 201, Sent: true}
			})
			h.sendBooking("30", "")()
			if h.m.dialog == nil || h.m.dialog.held == nil || strings.Contains(h.status(), "booked") {
				t.Fatalf("dialog %+v, status %q", h.m.dialog, h.status())
			}
			if !strings.Contains(h.dialogLine(), "Not clear") {
				t.Fatalf("line %q", h.dialogLine())
			}
		})
	}
}

// keysOf are the keys of the attempts, each once: a retry of the queue
// carries the key of its action.
func keysOf(calls []bookCall) []string {
	var keys []string
	for _, c := range calls {
		if !slices.Contains(keys, c.key) {
			keys = append(keys, c.key)
		}
	}
	return keys
}

// Nothing left: the server did not act, so nothing is unclear. The box
// stays ready, the time is not read, and the next try is a new one, however
// often the queue tried this one. Would miss: such an answer held as
// unclear, or taken as booked.
func TestNotSentIsNoUnclearOutcome(t *testing.T) {
	t.Parallel()

	for name, ans := range map[string]transport.Answer{
		"not reached": {Class: transport.Temporary, Message: "refused"},
		"canceled":    {Class: transport.Canceled, Message: "canceled"},
	} {
		t.Run(name, func(t *testing.T) {
			h := bookingHarness(t)
			h.f.bookFails(ans)
			times := len(h.f.bookedReads())
			h.sendBooking("30", "")()
			if h.m.dialog == nil || h.m.dialog.held != nil || h.m.dialog.sending {
				t.Fatalf("dialog %+v", h.m.dialog)
			}
			if got := h.dialogLine(); !strings.Contains(got, "the server was not reached") {
				t.Fatalf("line %q", got)
			}
			if got := len(h.f.bookedReads()); got != times {
				t.Fatalf("%d time reads", got-times)
			}
			if got := keysOf(h.f.bookCalls()); len(got) != 1 {
				t.Fatalf("keys %q", got)
			}
			h.f.setBook(nil)
			h.drive(h.press("enter"))
			if got := keysOf(h.f.bookCalls()); len(got) != 2 || h.m.dialog != nil {
				t.Fatalf("keys %q", got)
			}
		})
	}
}

// A 429 to the queue's one retry too: neither attempt reached the
// application, so the outcome is known and the box is ready again. The
// answer is fed by hand: the queue's pause would hold the test for ten
// seconds, and it has its own (gateway).
func TestARateLimitTwiceIsNoUnclearOutcome(t *testing.T) {
	t.Parallel()

	h := bookingHarness(t)
	h.fillBooking("30", "")
	h.press("enter")
	a := h.m.app
	op := a.bookings[six()]
	h.feed(gateway.WriteResult{
		WriteTicket: gateway.WriteTicket{Session: a.session, Op: op.id, Kind: gateway.BookTime, Project: website, Ticket: six()},
		Answer:      transport.Answer{Class: transport.Temporary, Status: 429, Sent: true},
	})
	if h.m.dialog == nil || h.m.dialog.held != nil || h.m.dialog.sending || !strings.Contains(h.dialogLine(), "rate limited") {
		t.Fatalf("dialog %+v, line %q", h.m.dialog, h.dialogLine())
	}
	if len(a.bookings) != 0 {
		t.Fatalf("the lock stayed: %d", len(a.bookings))
	}
}

// A request that may have left and was given up is unclear.
func TestACanceledBookingThatLeftIsUnclear(t *testing.T) {
	t.Parallel()

	h := bookingHarness(t)
	h.f.bookFails(transport.Answer{Class: transport.Canceled, Message: "canceled", Sent: true})
	h.sendBooking("30", "")()
	if h.m.dialog == nil || h.m.dialog.held == nil {
		t.Fatalf("dialog %+v", h.m.dialog)
	}
}

// 5. Enter twice: one send. Would miss: the lock taken off the dialog.
func TestASecondEnterSendsNothing(t *testing.T) {
	t.Parallel()

	h := bookingHarness(t)
	h.fillBooking("30", "")
	first := h.press("enter")
	if second := h.press("enter"); second != nil {
		t.Fatal("the second enter made a command")
	}
	h.drive(first)
	if got := h.f.bookCalls(); len(got) != 1 {
		t.Fatalf("calls %+v", got)
	}
}

// The lock is the ticket's, not the box's: a send that reaches the sender
// while another runs on the ticket does not go out. Would miss: a lock on
// the dialog alone.
func TestABookingRunningOnTheTicketHoldsTheNext(t *testing.T) {
	t.Parallel()

	h := bookingHarness(t)
	h.fillBooking("30", "")
	first := h.press("enter")
	send := h.m.dialog.spec.Send
	second := send(Submission{Dialog: h.m.dialog.id, Target: h.m.dialog.spec.Target, Values: []string{"15", ""}})
	h.drive(second)
	h.drive(first)
	if got := h.f.bookCalls(); len(got) != 1 || got[0].duration != "30" {
		t.Fatalf("calls %+v", got)
	}
}

// The selection moves behind the box: the booking goes where the person
// pressed b. Would miss: the target read from the pane when enter comes.
func TestABookingGoesToTheTicketOfTheKeyPress(t *testing.T) {
	t.Parallel()

	h := bookingHarness(t)
	h.press("b")
	h.m.app.tickets.selected = ticketID(website, 7)
	h.m.app.ensureDetail(website, ticketID(website, 7))
	if d := h.m.app.detail; d == nil || d.id != ticketID(website, 7) {
		t.Fatalf("the pane did not move: %+v", d)
	}
	h.typeText("30")
	h.drive(h.press("enter"))
	if got := h.f.bookCalls(); len(got) != 1 || got[0].ticket != six() {
		t.Fatalf("calls %+v", got)
	}
}

// 6. Esc leaves the send under way, and its end is told all the same, on
// the ticket it was for, wherever the person is by then: the pane of
// another ticket neither takes the booked time nor reads it. Until then b
// waits on that ticket and acts on another. Would miss: a result filed
// under the pane's ticket; a refusal nobody hears of.
func TestABookingLeftWithEscIsStillTold(t *testing.T) {
	t.Parallel()

	h := bookingHarness(t)
	h.fillBooking("30", "")
	send := h.press("enter")
	h.press("esc")
	if h.m.dialog != nil {
		t.Fatal("esc did not close the box")
	}
	if h.offers(BookTime) {
		t.Fatalf("b offered while a booking runs: %s", h.helpText())
	}
	h.reopen(7)
	if !h.offers(BookTime) {
		t.Fatalf("no b on another ticket: %s", h.helpText())
	}
	seven := h.metaText()
	times := len(h.f.bookedReads())
	h.drive(send)
	if got := h.status(); !strings.Contains(got, "0:30 booked") || !strings.Contains(got, "#6") {
		t.Fatalf("status %q", got)
	}
	if h.metaText() != seven {
		t.Fatalf("#7's bar changed:\n%s", h.metaText())
	}
	if got := h.f.bookedReads(); len(got) != times {
		t.Fatalf("time reads %v", got)
	}
	if _, ok := h.m.app.store.Get(h.m.app.timeKey(website, six())); ok {
		t.Fatal("#6's old time stayed in the cache")
	}
}

func TestARefusalOfABookingLeftWithEscIsAProblemOnTheLine(t *testing.T) {
	t.Parallel()

	h := bookingHarness(t)
	h.f.bookFails(recordedRefusal(t, "invalid_input_over_a_day", transport.InvalidInput))
	h.fillBooking("24:01", "")
	send := h.press("enter")
	h.press("esc")
	h.drive(send)
	got := h.status()
	if !strings.Contains(got, "Not booked on #6") || !strings.Contains(got, "Dauer darf") {
		t.Fatalf("status %q", got)
	}
	h.isAProblemOnTheLine()
	if !h.offers(BookTime) {
		t.Fatalf("b stayed locked: %s", h.helpText())
	}
}

func TestAnUnclearBookingLeftWithEscIsAProblemOnTheLine(t *testing.T) {
	t.Parallel()

	h := bookingHarness(t)
	h.f.bookFails(transport.Answer{Class: transport.Temporary, Message: "lost", Sent: true})
	times := len(h.f.bookedReads())
	h.fillBooking("30", "")
	send := h.press("enter")
	h.press("esc")
	h.drive(send)
	if got := h.status(); !strings.Contains(got, "Not clear") || !strings.Contains(got, "#6") {
		t.Fatalf("status %q", got)
	}
	h.isAProblemOnTheLine()
	if got := len(h.f.bookedReads()); got != times+1 {
		t.Fatalf("%d time reads", got-times)
	}
}

// isAProblemOnTheLine: a notice in the tone of a problem, which stands long
// enough to come back to, not a hint that goes.
func (h *harness) isAProblemOnTheLine() {
	h.t.Helper()
	if _, tone := h.m.status(); tone != present.NoticeProblem {
		h.t.Fatalf("tone %v", tone)
	}
	notices := h.m.app.bookNotices
	if len(notices) != 1 || !notices[0].until.Equal(h.now.Add(30*time.Second)) {
		h.t.Fatalf("notices %+v", notices)
	}
}

// 7. A 401 ends the session, box and all, and a later result finds nothing.
func TestA401OnABookingEndsTheSession(t *testing.T) {
	t.Parallel()

	h := bookingHarness(t)
	h.f.bookFails(transport.Answer{Class: transport.Unauthenticated, Status: 401, Sent: true})
	h.sendBooking("30", "")()
	if !h.m.app.expired || h.m.dialog != nil || h.m.mode != Navigate {
		t.Fatalf("expired %v, dialog %v", h.m.app.expired, h.m.dialog != nil)
	}
	if len(h.m.app.bookings) != 0 {
		t.Fatalf("bookings %d", len(h.m.app.bookings))
	}
	if got := h.status(); !strings.Contains(got, "Session expired") {
		t.Fatalf("status %q", got)
	}
}

// The result says so itself, where the mailbox did not hear of it: the
// session ends all the same, also when the ticket was left meanwhile.
func TestA401TheMailboxDidNotHearOfEndsTheSession(t *testing.T) {
	t.Parallel()

	h := bookingHarness(t)
	h.fillBooking("30", "")
	h.press("enter")
	a := h.m.app
	op := a.bookings[six()]
	h.feed(gateway.WriteResult{
		WriteTicket: gateway.WriteTicket{Session: a.session, Op: op.id, Kind: gateway.BookTime, Project: website, Ticket: six()},
		Answer:      transport.Answer{Class: transport.Unauthenticated, Status: 401, Sent: true},
	})
	if !a.expired || h.m.dialog != nil {
		t.Fatalf("expired %v, dialog %v", a.expired, h.m.dialog != nil)
	}
}

// A result that belongs to no booking of this session changes nothing.
func TestAResultOfNoBookingChangesNothing(t *testing.T) {
	t.Parallel()

	h := bookingHarness(t)
	h.fillBooking("30", "")
	send := h.press("enter")
	a := h.m.app
	op := a.bookings[six()]
	answer := transport.Answer{Class: transport.Object, Status: 201, Sent: true}
	for name, w := range map[string]gateway.WriteTicket{
		"another session":   {Session: a.session + 1, Op: op.id, Kind: gateway.BookTime, Ticket: six()},
		"another operation": {Session: a.session, Op: op.id + 1, Kind: gateway.BookTime, Ticket: six()},
		"another ticket":    {Session: a.session, Op: op.id, Kind: gateway.BookTime, Ticket: ticketID(website, 7)},
	} {
		h.feed(gateway.WriteResult{WriteTicket: w, Answer: answer})
		if h.m.dialog == nil || !h.m.dialog.sending || a.bookings[six()] != op {
			t.Fatalf("%s: dialog %+v", name, h.m.dialog)
		}
	}
	h.drive(send)
	if h.m.dialog != nil {
		t.Fatal("the real result did not end it")
	}
}

// One operation has one end: the same result again reads nothing and tells
// nothing, once a booking is unclear. Would miss: a second round of reads.
func TestAnUnclearBookingEndsOnce(t *testing.T) {
	t.Parallel()

	h := bookingHarness(t)
	h.f.bookFails(transport.Answer{Class: transport.Temporary, Message: "lost", Sent: true})
	h.fillBooking("30", "")
	msgs := messages(t, h.press("enter"))
	h.drive(h.feed(msgs...)...)
	h.press("esc")
	reads := len(h.f.bookedReads())
	h.drive(h.feed(msgs...)...)
	if got := len(h.f.bookedReads()); got != reads {
		t.Fatalf("%d more reads", got-reads)
	}
	if got := h.status(); got != "" {
		t.Fatalf("status %q", got)
	}
}

// 8. The duration is the person's: empty sends nothing, and the note is
// not asked for. Would miss: a key used up by nothing; a note required.
func TestAnEmptyDurationSendsNothing(t *testing.T) {
	t.Parallel()

	h := bookingHarness(t)
	h.press("b")
	if cmd := h.press("enter"); cmd != nil {
		t.Fatal("enter sent an empty duration")
	}
	if len(h.f.bookCalls()) != 0 || h.m.dialog == nil || !strings.Contains(h.dialogLine(), "Duration is required") {
		t.Fatalf("calls %+v, line %q", h.f.bookCalls(), h.dialogLine())
	}
	if len(h.m.app.bookings) != 0 {
		t.Fatalf("an operation out of nothing: %d", len(h.m.app.bookings))
	}
}

// An empty note goes as empty, which the adapter leaves out (api); what was
// pasted into a field is what goes. Would miss: a duration changed on its way.
func TestWhatIsTypedIsWhatGoes(t *testing.T) {
	t.Parallel()

	h := bookingHarness(t)
	h.sendBooking("  1,5", "")()
	got := h.f.bookCalls()
	if len(got) != 1 || got[0].duration != "  1,5" || got[0].note != "" {
		t.Fatalf("calls %+v", got)
	}
}

// 9. Without the tool of the sum there is nothing to read back: the booking
// works, and an unclear one says so without a number.
func TestABookingWithoutTheBookedTimeTool(t *testing.T) {
	t.Parallel()

	h := bookingHarness(t)
	h.m.app.report.Enabled["booked_time"] = false
	times := len(h.f.bookedReads())
	h.sendBooking("30", "")()
	if got := len(h.f.bookedReads()); got != times || !strings.Contains(h.status(), "0:30 booked") {
		t.Fatalf("%d reads, status %q", got-times, h.status())
	}
	h = bookingHarness(t)
	h.m.app.report.Enabled["booked_time"] = false
	h.f.bookFails(transport.Answer{Class: transport.Temporary, Message: "lost", Sent: true})
	h.sendBooking("30", "")()
	if got := h.dialogLine(); got != "Not clear whether it was booked" {
		t.Fatalf("line %q", got)
	}
}

// The time of the ticket held is what the box shows, never another's, when
// the pane moved behind it.
func TestTheBoxShowsNoOtherTicketsTime(t *testing.T) {
	t.Parallel()

	h := bookingHarness(t)
	h.f.bookFails(transport.Answer{Class: transport.Temporary, Message: "lost", Sent: true})
	h.fillBooking("30", "")
	send := h.press("enter")
	a := h.m.app
	d := a.ensureDetail(website, ticketID(website, 7))
	d.booked = bookedTime{known: true, minutes: 7}
	h.drive(send)
	if got := h.dialogLine(); got != "Not clear whether it was booked" {
		t.Fatalf("line %q", got)
	}
}

// Every operation of a session ends with it.
func TestTheSessionsEndCancelsARunningBooking(t *testing.T) {
	t.Parallel()

	h := bookingHarness(t)
	h.fillBooking("30", "")
	h.press("enter")
	op := h.m.app.bookings[six()]
	canceled := false
	cancel := op.cancel
	op.cancel = func() { canceled = true; cancel() }
	h.m.expire()
	if !canceled {
		t.Fatal("the session ended, the booking did not")
	}
}

// The keys of two tickets are apart, so are their locks: a booking running
// on one holds nothing on the other.
func TestLocksAreByTicket(t *testing.T) {
	t.Parallel()

	h := bookingHarness(t)
	h.fillBooking("30", "")
	first := h.press("enter")
	h.press("esc")
	h.reopen(7)
	second := h.sendBooking("15", "")
	second()
	h.drive(first)
	calls := h.f.bookCalls()
	tickets := []string{calls[0].ticket, calls[1].ticket}
	slices.Sort(tickets)
	want := []string{six(), ticketID(website, 7)}
	slices.Sort(want)
	if len(calls) != 2 || !slices.Equal(tickets, want) {
		t.Fatalf("calls %+v", calls)
	}
}

// Review round 1 of #50, finding 1: what became of a booking is the
// person's to know, whatever else stands on the line. A lasting notice (the
// disk in use by another run, too many agents) does not go, so one below it
// would never be read, and after an unclear end b is free again.
func TestALastingNoticeHidesNoBooking(t *testing.T) {
	t.Parallel()

	for name, set := range map[string]func(a *app){
		"the disk":        func(a *app) { a.disk.notice = uitext.DiskBusy },
		"too many agents": func(a *app) { a.agents.tooMany = true },
	} {
		t.Run(name, func(t *testing.T) {
			h := bookingHarness(t)
			set(h.m.app)
			h.sendBooking("30", "")()
			if got := h.status(); !strings.Contains(got, "0:30 booked on #6") {
				t.Fatalf("a booking told nothing: %q", got)
			}
			h.f.bookFails(transport.Answer{Class: transport.Temporary, Message: "lost", Sent: true})
			h.fillBooking("30", "")
			send := h.press("enter")
			h.press("esc")
			h.drive(send)
			if got := h.status(); !strings.Contains(got, "Not clear") {
				t.Fatalf("an unclear booking told nothing: %q", got)
			}
		})
	}
}

// Finding 2: a pause outlasts a problem's deadline, and the booking it held
// up was refused by the same limit: told first, the pause after it.
func TestAPauseHidesNoBooking(t *testing.T) {
	t.Parallel()

	h := bookingHarness(t)
	h.m.app.pause = h.now.Add(time.Minute)
	h.fillBooking("30", "")
	h.press("enter")
	h.press("esc")
	a := h.m.app
	op := a.bookings[six()]
	h.feed(gateway.WriteResult{
		WriteTicket: gateway.WriteTicket{Session: a.session, Op: op.id, Kind: gateway.BookTime, Project: website, Ticket: six()},
		Answer:      transport.Answer{Class: transport.Temporary, Status: 429, Sent: true},
	})
	if got := h.status(); !strings.Contains(got, "Not booked on #6") {
		t.Fatalf("status %q", got)
	}
	h.now = a.bookNotices[0].until
	if got := h.status(); !strings.Contains(got, "Rate limited") {
		t.Fatalf("status %q after the notice", got)
	}
}

// Review round 2 of #50, finding 1: the line tells the newest end of a
// ticket. "Not booked on #6" over the booking that went through is a lie that
// books a third time. Would miss: an old end kept for being a problem.
func TestANewEndOfATicketReplacesItsOldOne(t *testing.T) {
	t.Parallel()

	for name, old := range map[string]struct {
		ans  transport.Answer
		want string
	}{
		"refused": {recordedRefusal(t, "invalid_input_over_a_day", transport.InvalidInput), "Not booked on #6"},
		"unclear": {transport.Answer{Class: transport.Temporary, Message: "lost", Sent: true}, "Not clear"},
	} {
		t.Run(name, func(t *testing.T) {
			h := bookingHarness(t)
			h.f.bookFails(old.ans)
			h.fillBooking("30", "")
			send := h.press("enter")
			h.press("esc")
			h.drive(send)
			if got := h.status(); !strings.Contains(got, old.want) {
				t.Fatalf("status %q", got)
			}
			h.now = h.now.Add(2 * time.Second)
			h.f.setBook(nil)
			h.sendBooking("30", "")()
			got, tone := h.m.status()
			if got.String() != "0:30 booked on #6" || tone != present.Plain {
				t.Fatalf("status %q, tone %v", got.String(), tone)
			}
		})
	}
}

// A press on the ticket is the person's answer to what the line said of it:
// the old end goes at once, whatever the new one brings. Would miss: "Not
// booked" standing over "Sending" and over a box held on the new booking.
func TestAPressTakesTheOldEndOfItsTicketAway(t *testing.T) {
	t.Parallel()

	h := bookingHarness(t)
	h.f.bookFails(recordedRefusal(t, "invalid_input_over_a_day", transport.InvalidInput))
	h.fillBooking("24:01", "")
	send := h.press("enter")
	h.press("esc")
	h.drive(send)
	h.fillBooking("30", "")
	if got := h.status(); !strings.Contains(got, "Not booked on #6") {
		t.Fatalf("the box alone took it: %q", got)
	}
	h.press("enter")
	if got := h.status(); got != "" {
		t.Fatalf("status %q while the new send is out", got)
	}
}

// Finding 2: the ends of two tickets stand side by side, each with its tone
// and its deadline: a refusal of #7 does not take the only warning about #6
// away, nor does a problem swallow a success. Would miss: one place for all.
func TestTheEndsOfTwoTicketsStandSideBySide(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		second bookCall
		answer func(c bookCall) (api.TimeBooking, transport.Answer)
		want   string
		until  time.Duration
	}{
		"a success beside a problem": {
			answer: func(c bookCall) (api.TimeBooking, transport.Answer) { return bookedOf(c.ticket, 30) },
			want:   "0:30 booked on #7", until: 5 * time.Second,
		},
		"a problem beside a problem": {
			answer: func(bookCall) (api.TimeBooking, transport.Answer) {
				return api.TimeBooking{}, recordedRefusal(t, "invalid_input_over_a_day", transport.InvalidInput)
			},
			want: "Not booked on #7", until: 30 * time.Second,
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := bookingHarness(t)
			h.f.setBook(func(c bookCall) (api.TimeBooking, transport.Answer) {
				if c.ticket == six() {
					return api.TimeBooking{}, transport.Answer{Class: transport.Temporary, Message: "lost", Sent: true}
				}
				return tc.answer(c)
			})
			h.fillBooking("30", "")
			unclear := h.press("enter")
			h.press("esc")
			h.drive(unclear)
			h.reopen(7)
			h.now = h.now.Add(time.Second)
			h.fillBooking("15", "")
			second := h.press("enter")
			h.press("esc")
			h.drive(second)
			got, tone := h.m.status()
			if !strings.Contains(got.String(), "Not clear whether it was booked on #6") || !strings.Contains(got.String(), tc.want) || tone != present.NoticeProblem {
				t.Fatalf("status %q, tone %v", got.String(), tone)
			}
			// Each goes at its own deadline, the second a second after the
			// first if it is a problem too.
			first := h.m.app.bookNotices[0].until
			h.now = h.now.Add(5 * time.Second)
			if text := h.status(); !strings.Contains(text, "#6") || strings.Contains(text, tc.want) != (tc.until > 5*time.Second) {
				t.Fatalf("status %q after 5 s", text)
			}
			h.now = first
			if text := h.status(); strings.Contains(text, "#6") || strings.Contains(text, tc.want) != (tc.until > 5*time.Second) {
				t.Fatalf("status %q at the first's deadline", text)
			}
			h.feed(gateway.Expired{Session: h.m.app.session, Until: first})
			if got := h.m.app.bookNotices; len(got) != 1 || strings.Contains(got[0].text.String(), "#6") {
				t.Fatalf("notices %+v", got)
			}
		})
	}
}

// Finding 5: the box open on one ticket is no box of a booking on another:
// the end of the first is told on the line, whichever it is, and the box
// stays as it is. Would miss: any open box taken for the booking's own.
func TestTheEndOfABookingLeavesAnotherTicketsBoxAlone(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		ans  transport.Answer
		want string
	}{
		"confirmed": {transport.Answer{Class: transport.Object, Status: 201, Sent: true}, "0:30 booked on #6"},
		"refused":   {recordedRefusal(t, "invalid_input_over_a_day", transport.InvalidInput), "Not booked on #6"},
		"unclear":   {transport.Answer{Class: transport.Temporary, Message: "lost", Sent: true}, "Not clear whether it was booked on #6"},
	} {
		t.Run(name, func(t *testing.T) {
			h := bookingHarness(t)
			h.f.setBook(func(c bookCall) (api.TimeBooking, transport.Answer) {
				if tc.ans.OK() {
					return bookedOf(c.ticket, 30)
				}
				return api.TimeBooking{}, tc.ans
			})
			h.fillBooking("30", "")
			send := h.press("enter")
			h.press("esc")
			h.reopen(7)
			h.press("b")
			h.typeText("15")
			box := h.m.dialog
			h.drive(send)
			if h.m.dialog != box || box.sending || box.held != nil || h.dialogLine() != "" {
				t.Fatalf("the box of #7: %+v, line %q", h.m.dialog, h.dialogLine())
			}
			if got := dialogValues(h.m); got[0] != "15" {
				t.Fatalf("values %q", got)
			}
			if got := h.status(); !strings.Contains(got, tc.want) {
				t.Fatalf("status %q", got)
			}
		})
	}
}

// Finding 5: the close of the box is the close esc gives: a preview that
// fell due behind it is set again, and its timer comes back from the
// booking's command. Would miss: the command dropped, or another in its place.
func TestTheCloseOfABookingSetsAPreviewAgain(t *testing.T) {
	t.Parallel()

	h := newRESTHarness(t)
	h.openTickets(website)
	a := h.m.app
	h.drive(h.feed(gateway.Due{Session: a.session, Token: a.previewToken})...)
	h.press("right")
	if !h.offers(BookTime) || a.tickets.opened != "" {
		t.Fatalf("no b on the preview: %s", h.helpText())
	}
	h.press("b")
	h.typeText("30")
	h.press("enter")
	a.tickets.selected = ticketID(website, 7)
	op := a.bookings[a.detail.id]
	booking, ans := bookedOf(op.ticket, 30)
	a.deps.Events = gateway.NewEvents(h.live) // a mailbox whose timers run
	_, cmd := h.m.receiveBooking(gateway.WriteResult{
		WriteTicket: gateway.WriteTicket{Session: a.session, Op: op.id, Kind: gateway.BookTime, Project: op.project, Ticket: op.ticket},
		Booking:     booking, Answer: ans,
	})
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatal("one command came back")
	}
	due := make(chan gateway.Due, len(batch))
	for _, c := range batch {
		h.background(func() tea.Msg {
			if d, ok := c().(gateway.Due); ok {
				due <- d
			}
			return nil
		})
	}
	select {
	case d := <-due:
		if d.Token != a.previewToken {
			t.Fatalf("token %d, want %d", d.Token, a.previewToken)
		}
	case <-time.After(wall):
		t.Fatal("no timer of a preview came back")
	}
}

// Finding 5: a ticket is its id in its project: the pane on the same id of
// another project neither takes the time nor reads it, nor does the box
// held on a booking show it.
func TestTheSameIDInAnotherProjectIsAnotherTicket(t *testing.T) {
	t.Parallel()

	h := bookingHarness(t)
	h.f.bookFails(transport.Answer{Class: transport.Temporary, Message: "lost", Sent: true})
	h.fillBooking("30", "")
	send := h.press("enter")
	a := h.m.app
	d := a.detail
	d.project, d.booked = handbuch, bookedTime{known: true, minutes: 7}
	reads := len(h.f.bookedReads())
	h.drive(send)
	if !d.booked.known || d.booked.minutes != 7 {
		t.Fatalf("the other project's time went: %+v", d.booked)
	}
	if got := len(h.f.bookedReads()); got != reads {
		t.Fatalf("%d reads", got-reads)
	}
	if got := h.dialogLine(); got != "Not clear whether it was booked" {
		t.Fatalf("line %q", got)
	}
}
