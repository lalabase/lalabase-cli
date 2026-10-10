package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

// Review round 2 of C1f (another AI, mutation probes): one test per
// mutation that survived the suite as it was. The code was right in every
// case; the tests pin it.

// S1 + S3: a running operation survives the switch to another inbox and
// to the tickets section; its result still invalidates A's pages, makes
// the counters unknown and reloads them, and reloads nothing of B's.
func TestARunningOperationSurvivesLeavingTheInbox(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.drive(h.choose(orgA))
	a := h.m.app
	row := a.active.rows[0].ID
	cmd := h.press("r")
	if cmd == nil {
		t.Fatal("no command")
	}
	h.drive(h.choose(orgB))
	if a.active == nil || a.active.org != orgB {
		t.Fatal("setup: B's inbox is not open")
	}
	if op := a.marks[row]; op == nil || op.state != markRunning {
		t.Fatalf("the switch forgot the running operation: %+v", op)
	}
	reads := len(h.f.inboxCalls())
	cmds := h.feed(resultOf(h, cmd))
	if op := a.marks[row]; op == nil || op.state != markConfirmed {
		t.Fatalf("op %+v", op)
	}
	if a.countOf(orgA).known || a.countOf("").known || !a.countOf(orgB).known {
		t.Fatalf("counts A %+v, all %+v, B %+v", a.countOf(orgA), a.countOf(""), a.countOf(orgB))
	}
	if _, ok := a.store.Get(a.inboxKey(orgA, 1)); ok {
		t.Fatal("A's page is still cached")
	}
	if len(cmds) == 0 {
		t.Fatal("no reload")
	}
	h.drive(cmds...)
	got := h.f.inboxCalls()[reads:]
	if len(got) != 1 || got[0] != (api.NotificationQuery{Status: "unread", Page: 1, PerPage: 25}) {
		t.Fatalf("reads after the result %+v, want the counters alone", got)
	}
	if c := a.countOf(orgA); !c.known || c.value != 29 {
		t.Fatalf("A %+v", c)
	}
	// The same into the tickets section.
	h.drive(h.choose(orgA))
	row = a.active.rows[0].ID
	cmd = h.press("r")
	h.drive(h.chooseTickets(orgA))
	if a.active != nil || a.projects == nil {
		t.Fatal("setup: the tickets section is not open")
	}
	if op := a.marks[row]; op == nil || op.state != markRunning {
		t.Fatalf("leaving the inbox forgot the running operation: %+v", op)
	}
	reads = len(h.f.inboxCalls())
	cmds = h.feed(resultOf(h, cmd))
	if len(cmds) == 0 || a.countOf(orgA).known {
		t.Fatalf("%d commands, A %+v", len(cmds), a.countOf(orgA))
	}
	h.drive(cmds...)
	if got := h.f.inboxCalls()[reads:]; len(got) != 1 || got[0].OrganisationID != "" {
		t.Fatalf("reads after the result %+v", got)
	}
	if c := a.countOf(orgA); !c.known || c.value != 28 {
		t.Fatalf("A %+v", c)
	}
}

// S2: a mark from page 2 of the global inbox reloads that page, not only
// the counters.
func TestAMarkFromTheGlobalInboxReloadsTheShownPage(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.drive(h.choose(""))
	a := h.m.app
	h.drive(h.press("]"))
	if a.active.page != 2 || len(a.active.rows) != 8 {
		t.Fatalf("page %d with %d rows", a.active.page, len(a.active.rows))
	}
	row := a.active.rows[0].ID
	reads := len(h.f.inboxCalls())
	h.drive(h.press("r"))
	if op := a.marks[row]; op == nil || op.state != markConfirmed {
		t.Fatalf("op %+v", op)
	}
	got := h.f.inboxCalls()[reads:]
	want := []api.NotificationQuery{{Status: "unread", Page: 1, PerPage: 25}, {Status: "unread", Page: 2, PerPage: 25}}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("reads after the write %+v, want %+v", got, want)
	}
	if a.active.index(row) >= 0 || len(a.active.rows) != 7 || a.active.total != 32 {
		t.Fatalf("the shown page was not reloaded: row at %d, %d rows, total %d", a.active.index(row), len(a.active.rows), a.active.total)
	}
}

// S4: the lock of an unknown outcome holds until BOTH clarifying reads
// landed, the global one included.
func TestTheLockHoldsUntilBothClarifyingReadsLanded(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.openLinked(orgA, 0)
	a := h.m.app
	row := a.origin.notification
	h.f.setMark(func(id, _ string) (api.NotificationState, transport.Answer) {
		h.f.execute(id)
		return api.NotificationState{}, transport.Answer{Class: transport.Temporary, Sent: true, Message: "unexpected EOF"}
	})
	cmds := h.feed(resultOf(h, h.press("r")))
	if len(cmds) != 1 || a.marks[row].state != markUnknown {
		t.Fatalf("%d commands, state %v", len(cmds), a.marks[row].state)
	}
	// The global read is held; A's page comes.
	gate := make(chan struct{})
	h.f.mu.Lock()
	h.f.gate, h.f.gateOrg, h.f.gatePage = gate, "", 0
	h.f.mu.Unlock()
	batch, ok := cmds[0]().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("%T with %d commands", cmds[0](), len(batch))
	}
	chans := []<-chan tea.Msg{h.background(batch[0]), h.background(batch[1])}
	recv := func() tea.Msg {
		select {
		case msg := <-chans[0]:
			return msg
		case msg := <-chans[1]:
			return msg
		case <-time.After(wall):
			t.Fatal("no message")
		}
		return nil
	}
	first := recv()
	if r, ok := first.(gateway.InboxResult); !ok || r.Key.Global {
		t.Fatalf("the first message is %T global %v", first, ok && r.Key.Global)
	}
	h.drive(h.feed(first)...)
	if a.active.index(row) >= 0 {
		t.Fatal("A's page did not reload")
	}
	if h.m.allowed()[MarkRead] {
		t.Fatal("r offered while the global read is still out")
	}
	mustContain(t, h.paneText(), "Result unknown; refreshing")
	if got := h.m.objectColumn().Notice.String(); got != "Result unknown; refreshing" {
		t.Fatalf("notice %q", got)
	}
	h.f.mu.Lock()
	h.f.gate = nil
	h.f.mu.Unlock()
	close(gate)
	h.drive(h.feed(recv())...)
	if !h.m.allowed()[MarkRead] {
		t.Fatal("r not offered after both reads landed")
	}
	mustContain(t, h.paneText(), "Result unknown")
	mustNotContain(t, h.paneText(), "refreshing")
}

// S5 + S6: the classes Update tells apart without the gateway: a 429 that
// stood after the retry is "rate limited", a request canceled after it was
// sent is unknown and reloads, one canceled before is not sent.
func TestTheClassesUpdateTellsApart(t *testing.T) {
	cases := []struct {
		name    string
		ans     transport.Answer
		state   markState
		reloads bool
		text    string
	}{
		{"429 after the retry", transport.Answer{Class: transport.Temporary, Status: 429, Sent: true}, markNotSent, false, "Not marked; rate limited. r try again"},
		{"canceled after sending", transport.Answer{Class: transport.Canceled, Sent: true, Message: "canceled"}, markUnknown, true, "Result unknown; refreshing"},
		{"canceled before sending", transport.Answer{Class: transport.Canceled, Message: "canceled before it was sent"}, markNotSent, false, "Not marked; the server was not reached. r try again"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.start()
			h.drive(h.choose(orgA))
			a := h.m.app
			row := a.active.rows[0].ID
			if cmd := h.press("r"); cmd == nil {
				t.Fatal("no command")
			}
			op := a.marks[row]
			cmds := h.feed(gateway.MarkReadResult{MarkReadTicket: gateway.MarkReadTicket{Session: a.session, Op: op.id, Notification: row, Key: op.key}, Answer: tc.ans})
			if op.state != tc.state || (len(cmds) > 0) != tc.reloads {
				t.Fatalf("state %v, %d commands", op.state, len(cmds))
			}
			h.drive(h.press("enter"))
			mustContain(t, h.detailsText(), "Mark as read: "+tc.text)
		})
	}
}

// S7: the pane's notice belongs to the ticket when the ticket has
// something to say; the mark's outcome fills it only when it is empty.
func TestAMarkOutcomeDoesNotHideTheTicketsOwnNotice(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.openLinked(orgA, 0)
	h.f.setMark(func(id, _ string) (api.NotificationState, transport.Answer) { return markedAnswer(id) })
	h.drive(h.press("r"))
	if got := h.m.objectColumn().Notice.String(); got != "Marked as read" {
		t.Fatalf("setup: notice %q", got)
	}
	h.f.setTicket(func(string, string, api.TicketOptions) transport.Answer { return failing(transport.Refused, 200) })
	h.drive(h.press("R"))
	notice := h.m.objectColumn().Notice.String()
	if notice == "Marked as read" || !strings.Contains(notice, "Refused") {
		t.Fatalf("the mark hides the ticket's notice: %q", notice)
	}
	mustContain(t, h.paneText(), "Notification: unread · Marked as read")
}

// S8: a row shown as read offers no r from its ticket either, and the head
// says "read".
func TestAReadRowOffersNoMarkFromItsTicket(t *testing.T) {
	h := newHarness(t)
	h.f.set(func(q api.NotificationQuery) transport.Answer {
		rows := slices2(h.d.rows(orgA, 1, 30), h.f.readIDs())
		rows[1].Read = true
		return pageAnswer(q.Page, q.PerPage, len(rows), len(rows), slice(rows, q.Page, q.PerPage), map[string]int{orgA: len(rows)})
	})
	h.start()
	h.openLinked(orgA, 1)
	a := h.m.app
	if a.origin.unread {
		t.Fatal("the origin says unread for a read row")
	}
	mustContain(t, h.paneText(), "Notification: read")
	mustNotContain(t, h.paneText(), "Notification: unread")
	if h.m.allowed()[MarkRead] || strings.Contains(helpText(h.m), "r mark read") {
		t.Fatalf("r offered for a read row: %q", helpText(h.m))
	}
	if _, cmd := h.m.sessionKey(MarkRead); cmd != nil || len(h.f.markCalls()) != 0 || len(a.marks) != 0 {
		t.Fatal("r acted for a read row")
	}
}

// S9: the key carries the clock: the same session number and operation
// number in another run give another key.
func TestTheIdempotencyKeyCarriesTheClock(t *testing.T) {
	h := newHarness(t)
	h.start()
	a := h.m.app
	k1 := a.idempotencyKey(1)
	h.now = h.now.Add(time.Second)
	k2 := a.idempotencyKey(1)
	if k1 == k2 || !keyFormOK(k1) || !keyFormOK(k2) {
		t.Fatalf("keys %q and %q", k1, k2)
	}
}

// S10: a result that names a replaced operation is dropped, also when a
// newer operation on the same notification is running.
func TestAResultOfAReplacedOperationIsDropped(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.drive(h.choose(orgA))
	a := h.m.app
	row := a.active.rows[0].ID
	h.f.setMark(func(string, string) (api.NotificationState, transport.Answer) {
		return api.NotificationState{}, transport.Answer{Class: transport.Refused, Reason: "archived", Status: 200, Sent: true, Message: "archived"}
	})
	first := resultOf(h, h.press("r"))
	h.feed(first)
	if a.marks[row].state != markRefused {
		t.Fatalf("setup: %+v", a.marks[row])
	}
	h.f.setMark(h.f.defaultMark)
	cmd := h.press("r")
	second := a.marks[row]
	if cmd == nil || second.id == first.Op || second.state != markRunning {
		t.Fatalf("setup: %+v", second)
	}
	if cmds := h.feed(first); len(cmds) != 0 || second.state != markRunning || second.err.String() != "" {
		t.Fatalf("a result of the replaced operation was taken for the running one: %+v", second)
	}
	h.drive(h.feed(resultOf(h, cmd))...)
	if second.state != markConfirmed {
		t.Fatalf("op %+v", second)
	}
}

// S11: the counters forget their value after the write, not their place
// in the order of observations.
func TestTheCountersKeepTheirSequenceAcrossTheWrite(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.drive(h.choose(orgA))
	a := h.m.app
	seqA, seqG := a.counters[orgA].seq, a.global.seq
	if seqA == 0 || seqG == 0 {
		t.Fatalf("setup: seq A %d, global %d", seqA, seqG)
	}
	h.feed(resultOf(h, h.press("r")))
	if c := a.counters[orgA]; c.known || c.seq != seqA {
		t.Fatalf("A %+v, seq was %d", c, seqA)
	}
	if a.global.known || a.global.seq != seqG {
		t.Fatalf("global %+v, seq was %d", a.global, seqG)
	}
}

// S12: a row confirmed as read that is still on the page is not marked
// again: not from the list, the details or its ticket.
func TestAConfirmedRowStillOnThePageIsNotMarkedAgain(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.drive(h.choose(orgA))
	a := h.m.app
	h.f.setMark(func(id, _ string) (api.NotificationState, transport.Answer) { return markedAnswer(id) })
	row := a.active.rows[0].ID
	h.drive(h.press("r"))
	if a.marks[row].state != markConfirmed || a.active.index(row) < 0 || a.active.selected != row {
		t.Fatalf("setup: op %+v, row at %d, selected %q", a.marks[row], a.active.index(row), a.active.selected)
	}
	if h.m.allowed()[MarkRead] || strings.Contains(helpText(h.m), "r mark read") {
		t.Fatalf("r offered for a row marked as read: %q", helpText(h.m))
	}
	if cmd := h.press("r"); cmd != nil || len(h.f.markCalls()) != 1 {
		t.Fatalf("r wrote again: %d calls", len(h.f.markCalls()))
	}
	h.drive(h.press("enter"))
	if h.m.allowed()[MarkRead] {
		t.Fatal("r offered in the details of a row marked as read")
	}
	mustContain(t, h.detailsText(), "Mark as read: Marked as read")
	h.drive(h.press("o"))
	if h.m.allowed()[MarkRead] {
		t.Fatal("r offered in the ticket of a row marked as read")
	}
	if len(h.f.markCalls()) != 1 {
		t.Fatal("a second write")
	}
}

// S13: an inbox the server took away (not found) takes the ticket opened
// from one of its rows with it.
func TestARemovedInboxTakesTheLinkedTicketWithIt(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.openLinked(orgA, 0)
	a := h.m.app
	h.f.set(func(q api.NotificationQuery) transport.Answer {
		if q.OrganisationID == orgA {
			return transport.Answer{Class: transport.NotFound, Message: "Organisation not found or not accessible.", Sent: true, Status: 200}
		}
		return h.d.inboxWithout(q, h.f.readIDs())
	})
	h.drive(h.press("r"))
	if a.origin != nil || a.detail != nil || h.m.focus != view.Object && h.m.focus != view.List {
		t.Fatalf("after the removal: origin %+v, detail %v, focus %d", a.origin, a.detail != nil, h.m.focus)
	}
	if got := h.m.crumbs()[2].String(); got == "Ticket" {
		t.Fatal("the crumbs still name the ticket")
	}
	mustNotContain(t, h.paneText(), "#1 Login bricht ab")
}
