package ui

import (
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/cache"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/queue"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

// Mark as read (C1f). Every test names the change it would miss.

// 1. Permission: read_only false offers r, true hides and refuses it, a
// missing field is unknown and hides it, a server without the tool hides
// it; the profile's allow_write cannot reach the decision. Would miss: nil
// taken as false; a lock on the help alone.
func TestMarkReadNeedsAnExplicitWritePermission(t *testing.T) {
	cases := []struct {
		name     string
		readOnly *bool
		tools    func(h *harness)
		reason   string
	}{
		{"read_only true", boolPtr(true), nil, "Read-only token"},
		{"read_only missing", nil, nil, "Write permission unknown"},
		{"tool missing", boolPtr(false), func(h *harness) {
			tools := withoutTool(h.d.tools, "mark_notification_read")
			h.f.tools = func() ([]transport.Tool, transport.Answer) {
				return tools, transport.Answer{Class: transport.Object, Status: 200, Sent: true}
			}
		}, "Marking as read is not supported by this server."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.setReadOnly(tc.readOnly)
			if tc.tools != nil {
				tc.tools(h)
			}
			h.start()
			h.drive(h.choose(orgA))
			if h.m.allowed()[MarkRead] || strings.Contains(helpText(h.m), "r mark read") {
				t.Fatalf("r offered: %q", helpText(h.m))
			}
			// Not only the help: the update path refuses it too.
			if _, cmd := h.m.sessionKey(MarkRead); cmd != nil || len(h.m.app.marks) != 0 {
				t.Fatal("r acted through Update")
			}
			if cmd := h.press("r"); cmd != nil || len(h.f.markCalls()) != 0 {
				t.Fatal("r sent a write")
			}
			h.drive(h.press("enter"))
			if got := h.detailsText(); !strings.Contains(got, "Mark as read: "+tc.reason) {
				t.Fatalf("details:\n%s", got)
			}
			if _, cmd := h.m.sessionKey(MarkRead); cmd != nil || len(h.f.markCalls()) != 0 {
				t.Fatal("r acted in the details")
			}
		})
	}
	// read_only false: offered in the list, in the details and in the
	// ticket opened from them; the identity line says so.
	h := newHarness(t)
	h.start()
	h.drive(h.choose(orgA))
	if !h.m.allowed()[MarkRead] || !strings.Contains(helpText(h.m), "r mark read") {
		t.Fatalf("r not offered in the list: %q", helpText(h.m))
	}
	h.drive(h.press("enter"))
	if !h.m.allowed()[MarkRead] || strings.Contains(h.detailsText(), "Mark as read:") {
		t.Fatalf("details: offered %v, text:\n%s", h.m.allowed()[MarkRead], h.detailsText())
	}
	h.drive(h.press("o"))
	if !h.m.allowed()[MarkRead] || !strings.Contains(h.paneText(), "Notification: unread") {
		t.Fatalf("ticket: offered %v, text:\n%s", h.m.allowed()[MarkRead], h.paneText())
	}
	// In the chooser there is no target.
	h.m.focus = view.Navigation
	if h.m.allowed()[MarkRead] {
		t.Fatal("r offered in the chooser")
	}
	// The interface knows nothing of the profile's allow_write: the
	// session carries no such thing, and the decision reads /me alone.
	for _, typ := range []reflect.Type{reflect.TypeFor[Session](), reflect.TypeFor[app]()} {
		for i := range typ.NumField() {
			if name := strings.ToLower(typ.Field(i).Name); strings.Contains(name, "allow") || strings.Contains(name, "write") {
				t.Fatalf("%s carries %s", typ.Name(), typ.Field(i).Name)
			}
		}
	}
}

func boolPtr(b bool) *bool { return &b }

// 2. The target: a row of the inbox, with or without a ticket; the ticket
// opened from a row; never the same ticket opened from the tickets
// section, whatever an inbox visit cached; Esc returns to the details.
// Would miss: an origin taken from the ticket's cache entry.
func TestTheTargetIsTheRowOrTheTicketOpenedFromIt(t *testing.T) {
	h := newHarness(t)
	h.f.set(func(q api.NotificationQuery) transport.Answer {
		rows := slices2(h.d.rows(orgA, 1, 30), h.f.readIDs())
		for i := range rows {
			if rows[i].ID == idOf(1) {
				rows[i].Ticket, rows[i].Project = nil, nil
			}
		}
		return pageAnswer(q.Page, q.PerPage, len(rows), len(rows), slice(rows, q.Page, q.PerPage), map[string]int{orgA: len(rows)})
	})
	h.start()
	h.drive(h.choose(orgA))
	a := h.m.app
	first := a.active.rows[0].ID
	// A row without a ticket marks from the list.
	if !h.m.allowed()[MarkRead] {
		t.Fatal("r not offered for a row without a ticket")
	}
	h.drive(h.press("r"))
	if calls := h.f.markCalls(); len(calls) != 1 || calls[0].id != first {
		t.Fatalf("calls %+v", calls)
	}
	if op := a.marks[first]; op == nil || op.state != markConfirmed || op.ticket != "" {
		t.Fatalf("op %+v", op)
	}
	// The row is gone after the reload; the details of a row with a
	// ticket open the ticket, and r there marks that row.
	second := a.active.rows[0].ID
	h.drive(h.press("enter"))
	if !h.m.allowed()[Order] || !strings.Contains(helpText(h.m), "o open ticket") {
		t.Fatalf("o not offered: %q", helpText(h.m))
	}
	h.drive(h.press("o"))
	if a.origin == nil || a.origin.notification != second || a.origin.ticket != login || a.detail == nil || a.detail.id != login || h.m.focus != view.Object {
		t.Fatalf("origin %+v, detail %+v, focus %d", a.origin, a.detail, h.m.focus)
	}
	if got := h.m.crumbs()[2].String(); got != "Ticket" {
		t.Fatalf("crumb %q", got)
	}
	mustContain(t, h.paneText(), "#1 Login bricht ab", "Notification: unread")
	// Opening marked nothing.
	if len(h.f.markCalls()) != 1 {
		t.Fatal("opening the ticket wrote")
	}
	h.drive(h.press("r"))
	if calls := h.f.markCalls(); len(calls) != 2 || calls[1].id != second {
		t.Fatalf("calls %+v", calls)
	}
	if op := a.marks[second]; op == nil || op.state != markConfirmed || op.ticket != login || op.project != website {
		t.Fatalf("op %+v", op)
	}
	// The ticket stays open and usable after the mark; its row left the
	// page, so Esc goes to the list. The outcome stands in the head and in
	// the pane's notice, which stays in sight when the head is scrolled
	// away.
	if a.detail == nil || !a.detail.known || h.m.focus != view.Object {
		t.Fatal("the ticket closed with the mark")
	}
	mustContain(t, h.paneText(), "Notification: unread · Marked as read")
	if got := h.m.objectColumn().Notice.String(); got != "Marked as read" {
		t.Fatalf("notice %q", got)
	}
	h.drive(h.press("esc"))
	if a.origin != nil || a.detail != nil || h.m.focus != view.List || a.active.opened != "" {
		t.Fatalf("after esc: origin %v, detail %v, focus %d, opened %q", a.origin, a.detail, h.m.focus, a.active.opened)
	}
	// A row that stays: Esc restores its details.
	h.f.setMark(func(id, _ string) (api.NotificationState, transport.Answer) { return markedAnswer(id) })
	third := a.active.rows[0].ID
	h.drive(h.press("enter"))
	h.drive(h.press("o"))
	h.drive(h.press("r"))
	h.drive(h.press("esc"))
	if a.origin != nil || h.m.focus != view.Object || a.active.opened != third {
		t.Fatalf("after esc: origin %v, focus %d, opened %q", a.origin, h.m.focus, a.active.opened)
	}
	mustContain(t, h.detailsText(), "Mark as read: Marked as read")
	// The same ticket from the tickets section, straight from the open
	// linked ticket: the cache holds it from the inbox visit, and still
	// there is no target.
	h.drive(h.press("o"))
	if a.origin == nil {
		t.Fatal("setup: the linked ticket is not open")
	}
	h.drive(h.chooseTickets(orgA))
	h.drive(h.openProject(website))
	a.tickets.selected = login
	h.drive(h.press("enter"))
	if a.detail == nil || a.detail.id != login || !a.detail.cached && !a.detail.known {
		t.Fatalf("the ticket did not open from the section: %+v", a.detail)
	}
	if a.origin != nil || h.m.allowed()[MarkRead] {
		t.Fatal("r offered for a ticket of the tickets section")
	}
	before := len(h.f.markCalls())
	if _, cmd := h.m.sessionKey(MarkRead); cmd != nil || len(h.f.markCalls()) != before {
		t.Fatal("r acted for a ticket of the tickets section")
	}
	mustNotContain(t, h.paneText(), "Notification:")
}

// slices2 drops the rows marked so far, as the fakes' default inbox does.
func slices2(rows []api.Notification, read map[string]bool) []api.Notification {
	var out []api.Notification
	for _, n := range rows {
		if !read[n.ID] {
			out = append(out, n)
		}
	}
	return out
}

// 3. The answer belongs to the operation: started for A, the selection
// moved to B, the answer for A marks A. Would miss: the selection read when
// the request is sent or when the result lands.
func TestTheAnswerBelongsToTheOperationNotToTheSelection(t *testing.T) {
	h := newHarness(t)
	// The fakes answer the success without carrying it out, so both rows
	// stay on the page and the marks can be read off them.
	h.f.setMark(func(id, _ string) (api.NotificationState, transport.Answer) { return markedAnswer(id) })
	h.start()
	h.drive(h.choose(orgA))
	a := h.m.app
	rowA, rowB := a.active.rows[0].ID, a.active.rows[1].ID
	cmd := h.press("r")
	if cmd == nil {
		t.Fatal("no command")
	}
	h.press("down")
	if a.active.selected != rowB {
		t.Fatal("the selection did not move")
	}
	result := resultOf(h, cmd)
	if calls := h.f.markCalls(); len(calls) != 1 || calls[0].id != rowA {
		t.Fatalf("sent %+v", calls)
	}
	if result.Notification != rowA {
		t.Fatalf("result for %q", result.Notification)
	}
	// The details of B are open when the answer lands.
	h.drive(h.press("enter"))
	h.drive(h.feed(result)...)
	if op := a.marks[rowA]; op == nil || op.state != markConfirmed {
		t.Fatalf("A: %+v", op)
	}
	if a.marks[rowB] != nil {
		t.Fatal("B got an operation")
	}
	if h.rowTrailing(rowA) != "marked" || h.rowTrailing(rowB) != "" {
		t.Fatalf("trailing A %q, B %q", h.rowTrailing(rowA), h.rowTrailing(rowB))
	}
	if strings.Contains(h.detailsText(), "Marked") {
		t.Fatalf("B's details show A's mark:\n%s", h.detailsText())
	}
	if a.active.selected != rowB || a.active.opened != rowB {
		t.Fatal("the result moved the selection")
	}
	// The same with the row gone: the record stays with its id, no row
	// shows it, and B is never marked.
	h.f.setMark(h.f.defaultMark)
	h.press("esc")
	a.active.selected = rowB
	cmd = h.press("r")
	a.active.selected = a.active.rows[2].ID
	h.drive(cmd)
	if executed := h.f.executed(); len(executed) != 1 || executed[0] != rowB {
		t.Fatalf("executed %v", executed)
	}
	if a.active.index(rowB) >= 0 || a.marks[rowB].state != markConfirmed || a.marks[a.active.selected] != nil {
		t.Fatalf("after the reload: B at %d, marks %v", a.active.index(rowB), a.marks)
	}
}

// 4. The lock and the key: a second r on the same target does nothing
// before the command runs and while the request is out; the queue's one
// retry sends the same key, and there is only ever one operation for it.
// Would miss: the lock set in the command; a retry with a new key.
func TestASecondPressDoesNothingAndARetryKeepsTheKey(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.drive(h.choose(orgA))
	a := h.m.app
	row := a.active.rows[0].ID
	cmd := h.press("r")
	if cmd == nil || len(a.marks) != 1 || !keyFormOK(a.marks[row].key) {
		t.Fatalf("first press: cmd %v, marks %v", cmd != nil, a.marks)
	}
	if again := h.press("r"); again != nil || len(a.marks) != 1 {
		t.Fatal("a second press before the command ran made another operation")
	}
	if h.m.allowed()[MarkRead] || strings.Contains(helpText(h.m), "r mark read") {
		t.Fatal("r offered while the operation runs")
	}
	if h.rowTrailing(row) != "marking…" {
		t.Fatalf("trailing %q", h.rowTrailing(row))
	}
	gate := make(chan struct{})
	h.f.setMarkGate(gate)
	result := h.background(cmd)
	h.waitForWriteOnWire(1)
	if again := h.press("r"); again != nil || len(a.marks) != 1 {
		t.Fatal("a press while the request is out made another operation")
	}
	h.f.setMarkGate(nil)
	close(gate)
	msg := h.receive(result)
	h.drive(h.feed(msg)...)
	if len(h.f.markCalls()) != 1 || a.marks[row].state != markConfirmed {
		t.Fatalf("calls %d, state %v", len(h.f.markCalls()), a.marks[row].state)
	}
	// A retry of the queue sends the same key.
	var mu sync.Mutex
	attempts := 0
	h.f.setMark(func(id, key string) (api.NotificationState, transport.Answer) {
		mu.Lock()
		attempts++
		n := attempts
		mu.Unlock()
		if n == 1 {
			return api.NotificationState{}, transport.Answer{Class: transport.Temporary, Message: "dial failed"}
		}
		h.f.execute(id)
		return markedAnswer(id)
	})
	row = a.active.rows[0].ID
	before := len(h.f.markCalls())
	h.drive(h.press("r"))
	calls := h.f.markCalls()[before:]
	if len(calls) != 2 || calls[0].key != calls[1].key || calls[0].id != row || calls[1].id != row || !keyFormOK(calls[0].key) {
		t.Fatalf("retry: %+v", calls)
	}
	if op := a.marks[row]; op == nil || op.state != markConfirmed || op.key != calls[0].key {
		t.Fatalf("op %+v", op)
	}
	// Two operations never share a key.
	keys := map[string]bool{}
	for _, c := range h.f.markCalls() {
		keys[c.key] = true
	}
	if len(keys) != 2 {
		t.Fatalf("%d keys for 2 operations: %+v", len(keys), h.f.markCalls())
	}
}

// fakeClock fires After channels only when the test advances it, for the
// queue of the 429 case.
type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []fakeWaiter
}

type fakeWaiter struct {
	at time.Time
	ch chan time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	if d <= 0 {
		ch <- c.now
		return ch
	}
	c.waiters = append(c.waiters, fakeWaiter{at: c.now.Add(d), ch: ch})
	return ch
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	var keep []fakeWaiter
	for _, w := range c.waiters {
		if !w.at.After(c.now) {
			w.ch <- c.now
		} else {
			keep = append(keep, w)
		}
	}
	c.waiters = keep
}

// 5. The retry matrix of a write: not sent → one retry, same key; 429 →
// one retry after the window, same key; sent and broken off → no retry,
// unknown, reloads; a refusal → no retry, no reload. Would miss: a write
// sent again after Sent.
func TestAWriteIsRetriedOnlyWhenTheServerCannotHaveActed(t *testing.T) {
	cases := []struct {
		name    string
		answers []transport.Answer
		calls   int
		state   markState
		reloads bool
		text    string
	}{
		{"not sent, then ok", []transport.Answer{{Class: transport.Temporary, Message: "dial"}, {Class: transport.Object, Status: 200, Sent: true}}, 2, markConfirmed, true, "marked"},
		{"not sent twice", []transport.Answer{{Class: transport.Temporary}, {Class: transport.Temporary}}, 2, markNotSent, false, "not marked"},
		{"sent, then broken off", []transport.Answer{{Class: transport.Temporary, Sent: true, Message: "EOF"}}, 1, markUnknown, true, "unknown"},
		{"a 503 after sending", []transport.Answer{{Class: transport.Temporary, Status: 503, Sent: true}}, 1, markUnknown, true, "unknown"},
		{"read-only", []transport.Answer{{Class: transport.ReadOnly, Status: 200, Sent: true, Message: "This token is read-only; writes are not permitted."}}, 1, markRefused, false, "not marked"},
		{"not found", []transport.Answer{{Class: transport.NotFound, Status: 200, Sent: true, Message: "No notification found."}}, 1, markRefused, false, "not marked"},
		{"no access", []transport.Answer{{Class: transport.NoAccess, Status: 200, Sent: true}}, 1, markRefused, false, "not marked"},
		{"archived", []transport.Answer{{Class: transport.Refused, Reason: "archived", Status: 200, Sent: true, Message: "archived"}}, 1, markRefused, false, "not marked"},
		{"invalid input", []transport.Answer{{Class: transport.InvalidInput, Status: 200, Sent: true}}, 1, markRefused, false, "not marked"},
		{"a protocol error after sending", []transport.Answer{{Class: transport.Protocol, Status: 200, Sent: true, Message: "no JSON"}}, 1, markUnknown, true, "unknown"},
		{"a protocol error before sending", []transport.Answer{{Class: transport.Protocol, Message: "bad request"}}, 1, markRefused, false, "not marked"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.start()
			h.drive(h.choose(orgA))
			a := h.m.app
			row := a.active.rows[0].ID
			var mu sync.Mutex
			n := 0
			h.f.setMark(func(id, _ string) (api.NotificationState, transport.Answer) {
				mu.Lock()
				defer mu.Unlock()
				ans := tc.answers[min(n, len(tc.answers)-1)]
				n++
				if ans.OK() {
					h.f.execute(id)
					return markedAnswer(id)
				}
				return api.NotificationState{}, ans
			})
			reads := len(h.f.inboxCalls())
			cmd := h.press("r")
			result := resultOf(h, cmd)
			calls := h.f.markCalls()
			if len(calls) != tc.calls {
				t.Fatalf("%d attempts, want %d", len(calls), tc.calls)
			}
			for _, c := range calls {
				if c.key != calls[0].key {
					t.Fatalf("keys differ: %+v", calls)
				}
			}
			cmds := h.feed(result)
			op := a.marks[row]
			if op == nil || op.state != tc.state {
				t.Fatalf("state %v, want %v (%+v)", op.state, tc.state, op)
			}
			if (len(cmds) > 0) != tc.reloads {
				t.Fatalf("%d follow-up commands, reloads %v", len(cmds), tc.reloads)
			}
			if got := h.rowTrailing(row); got != tc.text {
				t.Fatalf("trailing %q, want %q", got, tc.text)
			}
			h.drive(cmds...)
			if tc.reloads && len(h.f.inboxCalls()) == reads {
				t.Fatal("no reload")
			}
			if !tc.reloads && len(h.f.inboxCalls()) != reads {
				t.Fatal("a reload after a refusal or an unsent write")
			}
			// Nothing is written again by itself.
			if len(h.f.markCalls()) != tc.calls {
				t.Fatalf("%d attempts after the follow-ups", len(h.f.markCalls()))
			}
		})
	}
	// 429: the first attempt is rate-limited, the queue waits for the
	// window and sends the same key once more; the write counts as done
	// only then.
	t.Run("429, then ok", func(t *testing.T) {
		h := newHarness(t)
		h.start()
		h.drive(h.choose(orgA))
		a := h.m.app
		clock := &fakeClock{now: h.now}
		q := queue.New(queue.WithLifetime(h.live), queue.WithRetryDelay(0), queue.WithClock(clock), queue.WithOnRateLimit(h.events.RateLimit))
		a.deps.Gateway = gateway.New(h.live, q, h.f, gateway.WithEvents(h.events))
		row := a.active.rows[0].ID
		var mu sync.Mutex
		n := 0
		h.f.setMark(func(id, _ string) (api.NotificationState, transport.Answer) {
			mu.Lock()
			defer mu.Unlock()
			n++
			if n == 1 {
				return api.NotificationState{}, transport.Answer{Class: transport.Temporary, Status: 429, RetryAt: clock.Now().Add(time.Second), Sent: true}
			}
			h.f.execute(id)
			return markedAnswer(id)
		})
		result := h.background(h.press("r"))
		h.waitForWriteOnWire(1)
		select {
		case <-result:
			t.Fatal("the write ended inside the window")
		case <-time.After(50 * time.Millisecond):
		}
		if len(h.f.markCalls()) != 1 {
			t.Fatal("a retry inside the window")
		}
		clock.advance(time.Second)
		msg := h.receive(result)
		calls := h.f.markCalls()
		if len(calls) != 2 || calls[0].key != calls[1].key {
			t.Fatalf("calls %+v", calls)
		}
		h.drive(h.feed(msg)...)
		if op := a.marks[row]; op.state != markConfirmed {
			t.Fatalf("state %v", op.state)
		}
		// 429 twice: the server did not act; the write is not retried a
		// third time and the person may try again.
		row = a.active.rows[0].ID
		h.f.setMark(func(string, string) (api.NotificationState, transport.Answer) {
			return api.NotificationState{}, transport.Answer{Class: transport.Temporary, Status: 429, RetryAt: clock.Now().Add(time.Second), Sent: true}
		})
		before := len(h.f.markCalls())
		result = h.background(h.press("r"))
		h.waitForWriteOnWire(before + 1)
		clock.advance(time.Second)
		h.waitForWriteOnWire(before + 2)
		clock.advance(time.Second)
		msg = h.receive(result)
		h.drive(h.feed(msg)...)
		if op := a.marks[row]; op.state != markNotSent || len(h.f.markCalls()) != before+2 {
			t.Fatalf("state %v, %d calls", op.state, len(h.f.markCalls())-before)
		}
		mustContain(t, h.m.listColumn().Rows[0].Trailing.String(), "not marked")
		if !h.m.allowed()[MarkRead] {
			t.Fatal("r not offered again after a rate-limited write")
		}
	})
}

// 6. An unknown outcome: the server acted and the answer was lost. One
// change, no second write, the reads that clear it up, and the state stays
// unknown after them: absence from the unread page proves nothing. Would
// miss: an empty unread page taken as a confirmation.
func TestAnUnknownOutcomeReloadsAndStaysUnknown(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.openLinked(orgA, 0)
	a := h.m.app
	row := a.origin.notification
	h.f.setMark(func(id, _ string) (api.NotificationState, transport.Answer) {
		h.f.execute(id)
		return api.NotificationState{}, transport.Answer{Class: transport.Temporary, Sent: true, Message: "unexpected EOF"}
	})
	reads := len(h.f.inboxCalls())
	result := resultOf(h, h.press("r"))
	cmds := h.feed(result)
	op := a.marks[row]
	if op == nil || op.state != markUnknown || len(h.f.executed()) != 1 || len(h.f.markCalls()) != 1 {
		t.Fatalf("op %+v, executed %v, calls %d", op, h.f.executed(), len(h.f.markCalls()))
	}
	// The reads are out: the lock holds, the text says so.
	if len(cmds) == 0 || h.m.allowed()[MarkRead] {
		t.Fatalf("%d commands, r offered %v", len(cmds), h.m.allowed()[MarkRead])
	}
	mustContain(t, h.paneText(), "Notification: unread · Result unknown; refreshing")
	if got := h.m.objectColumn().Notice.String(); got != "Result unknown; refreshing" {
		t.Fatalf("notice %q", got)
	}
	if _, cmd := h.m.sessionKey(MarkRead); cmd != nil {
		t.Fatal("r acted while the reads clear it up")
	}
	h.drive(cmds...)
	if got := h.f.inboxCalls()[reads:]; len(got) != 2 {
		t.Fatalf("reads after the write: %+v", got)
	}
	if a.active.index(row) >= 0 {
		t.Fatal("the row is still on the page")
	}
	// Unknown it stays: no success is claimed anywhere, nothing was
	// written again.
	if op.state != markUnknown || len(h.f.markCalls()) != 1 {
		t.Fatalf("state %v, calls %d", op.state, len(h.f.markCalls()))
	}
	mustContain(t, h.paneText(), "Notification: unread · Result unknown")
	mustNotContain(t, h.paneText(), "Marked", "refreshing")
	if strings.Contains(ansi.Strip(h.m.View().Content), "Marked") {
		t.Fatal("the screen claims a success")
	}
	// The ticket is still there; and a later r is the person's new
	// action, with a new key.
	if a.detail == nil || !a.detail.known {
		t.Fatal("the ticket went")
	}
	if !h.m.allowed()[MarkRead] {
		t.Fatal("r not offered after the reads landed")
	}
	h.f.setMark(h.f.defaultMark)
	h.drive(h.press("r"))
	calls := h.f.markCalls()
	if len(calls) != 2 || calls[0].key == calls[1].key || a.marks[row].state != markConfirmed || a.marks[row].id == op.id {
		t.Fatalf("the explicit retry: %+v, op %+v", calls, a.marks[row])
	}
	// R after an unknown outcome reads; it never writes.
	before := len(h.f.markCalls())
	h.drive(h.press("R"))
	if len(h.f.markCalls()) != before {
		t.Fatal("R wrote")
	}
}

// 7. Cache and counters: the write drops every cached page of the
// organisation's inbox and of the global one and moves their epochs on,
// so a read begun before cannot file old rows; the counters are unknown
// until the server says, never counted down by the client. Would miss: an
// epoch not moved; a variant left out.
func TestOldReadsCannotBringBackWhatTheWriteChanged(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.drive(h.choose(orgA))
	a := h.m.app
	h.drive(h.press("]")) // page 2 cached
	h.drive(h.press("["))
	keyA1, keyA2, keyG := a.inboxKey(orgA, 1), a.inboxKey(orgA, 2), a.inboxKey("", 1)
	for _, k := range []cache.Key{keyA1, keyA2, keyG} {
		if _, ok := a.store.Get(k); !ok {
			t.Fatalf("%v not cached before the write", k.Page)
		}
	}
	// Two reads begun before the write, of page 2 and of the global page,
	// whose requests go out only after it (held in the queue by a pause or
	// the budget, say): their flights are newer than the reloads', so the
	// order of flights cannot refuse them, only the epoch can. Their
	// results are built here from the pages as they were.
	old2 := gateway.InboxResult{Ticket: a.scope.Begin(gateway.Inbox, keyA2, queue.User), Flight: 1 << 40}
	oldGlobal := gateway.InboxResult{Ticket: a.scope.Begin(gateway.Organisations, keyG, queue.User), Flight: 1<<40 + 1}
	for _, r := range []*gateway.InboxResult{&old2, &oldGlobal} {
		a.waiters[r.Waiter] = &waiter{ticket: r.Ticket, cancel: func() {}, purpose: forInbox}
		e, _ := a.store.Get(r.Key)
		r.Page, r.Answer = api.DecodeNotifications(transport.Answer{Class: transport.Object, Payload: e.Payload, Status: 200, Sent: true})
	}
	epochA, epochG := old2.Epoch, oldGlobal.Epoch
	row := a.active.rows[0].ID
	result := resultOf(h, h.press("r"))
	cmds := h.feed(result)
	// Right after the result: the variants left the cache, the counters
	// are unknown, the epochs moved, nothing of B's changed.
	for _, k := range []cache.Key{keyA1, keyA2, keyG} {
		if _, ok := a.store.Get(k); ok {
			t.Fatalf("page %d of %q still cached after the write", k.Page, k.Organisation)
		}
	}
	if a.countOf(orgA).known || a.countOf("").known || !a.countOf(orgB).known {
		t.Fatalf("counts A %+v, all %+v, B %+v", a.countOf(orgA), a.countOf(""), a.countOf(orgB))
	}
	if got := a.scope.Begin(gateway.Inbox, keyA1, queue.User).Epoch; got != epochA+1 {
		t.Fatalf("A's epoch %d, was %d", got, epochA)
	}
	if got := a.scope.Begin(gateway.Organisations, keyG, queue.User).Epoch; got != epochG+1 {
		t.Fatalf("the global epoch %d, was %d", got, epochG)
	}
	if countShown(h.m, orgA) != "?" || countShown(h.m, "") != "?" || countShown(h.m, orgB) != "3" {
		t.Fatalf("shown A %q, all %q, B %q", countShown(h.m, orgA), countShown(h.m, ""), countShown(h.m, orgB))
	}
	// The reloads: the page shown and the counters, each once, from the
	// server; the old reads land afterwards and change nothing.
	reads := len(h.f.inboxCalls())
	h.drive(cmds...)
	got := h.f.inboxCalls()[reads:]
	if len(got) != 2 || got[0] != (api.NotificationQuery{Status: "unread", Page: 1, PerPage: 25}) || got[1] != (api.NotificationQuery{Status: "unread", Page: 1, PerPage: 25, OrganisationID: orgA}) {
		t.Fatalf("reloads %+v", got)
	}
	if c := a.countOf(orgA); !c.known || c.value != 29 {
		t.Fatalf("A %+v", c)
	}
	if c := a.countOf(""); !c.known || c.value != 32 {
		t.Fatalf("all %+v", c)
	}
	if len(a.active.rows) != 25 || a.active.index(row) >= 0 || a.active.total != 29 {
		t.Fatalf("%d rows, total %d, row at %d", len(a.active.rows), a.active.total, a.active.index(row))
	}
	h.feed(old2)
	if _, ok := a.store.Get(keyA2); ok {
		t.Fatal("an old read of page 2 was filed after the write")
	}
	h.feed(oldGlobal)
	if c := a.countOf(orgA); c.value != 29 || a.countOf("").value != 32 || !a.countOf("").known {
		t.Fatalf("an old global read set the counters back: A %+v, all %+v", c, a.countOf(""))
	}
	e, ok := a.store.Get(keyG)
	if !ok {
		t.Fatal("the reloaded global page is not cached")
	}
	if p, ans := api.DecodeNotifications(transport.Answer{Class: transport.Object, Payload: e.Payload, Status: 200, Sent: true}); !ans.OK() || p.Meta.Total != 32 {
		t.Fatalf("the cached global page has total %d", p.Meta.Total)
	}
	// B's inbox was not touched by a write on A: its count stands.
	if c := a.countOf(orgB); !c.known || c.value != 3 {
		t.Fatalf("B %+v", c)
	}
}

// 8. A refused write never removes the ticket: not found, no access,
// archived, read-only keep the ticket and its cached variants, show the
// refusal where the target is, and reload nothing. Would miss: the decision
// taken with Read.
func TestARefusedWriteNeverRemovesTheTicket(t *testing.T) {
	for _, tc := range []struct {
		name string
		ans  transport.Answer
	}{
		{"not found", transport.Answer{Class: transport.NotFound, Status: 200, Sent: true, Message: "No notification found for x."}},
		{"no access", transport.Answer{Class: transport.NoAccess, Status: 200, Sent: true, Message: "Not authorized."}},
		{"archived", transport.Answer{Class: transport.Refused, Reason: "archived", Status: 200, Sent: true, Message: "notification is archived; it cannot be marked as read"}},
		{"read-only", transport.Answer{Class: transport.ReadOnly, Status: 200, Sent: true, Message: "This token is read-only; writes are not permitted."}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.start()
			h.openLinked(orgA, 0)
			a := h.m.app
			key := a.detail.key
			if _, ok := a.store.Get(key); !ok {
				t.Fatal("the ticket is not cached")
			}
			h.f.setMark(func(string, string) (api.NotificationState, transport.Answer) { return api.NotificationState{}, tc.ans })
			reads, ticketReads := len(h.f.inboxCalls()), len(h.f.detailReads())
			h.drive(h.press("r"))
			op := a.marks[a.origin.notification]
			if op == nil || op.state != markRefused {
				t.Fatalf("op %+v", op)
			}
			if _, ok := a.store.Get(key); !ok {
				t.Fatal("the refused write removed the cached ticket")
			}
			if a.detail == nil || !a.detail.known || a.origin == nil || h.m.focus != view.Object {
				t.Fatalf("the ticket went: detail %+v, origin %+v, focus %d", a.detail, a.origin, h.m.focus)
			}
			mustContain(t, h.paneText(), "#1 Login bricht ab", "Notification: unread · Not marked: "+tc.ans.Class.String()+": "+tc.ans.Message)
			mustNotContain(t, h.paneText(), "Result unknown", "Marked as read")
			if len(h.f.inboxCalls()) != reads || len(h.f.detailReads()) != ticketReads {
				t.Fatal("a refusal reloaded")
			}
			// The row stays with its refusal, and r is the person's again.
			h.drive(h.press("esc"))
			mustContain(t, h.detailsText(), "Mark as read: Not marked: "+tc.ans.Class.String())
			if h.rowTrailing(a.active.opened) != "not marked" || !h.m.allowed()[MarkRead] {
				t.Fatalf("trailing %q, r offered %v", h.rowTrailing(a.active.opened), h.m.allowed()[MarkRead])
			}
			h.f.setMark(h.f.defaultMark)
			h.drive(h.press("r"))
			if calls := h.f.markCalls(); len(calls) != 2 || calls[0].key == calls[1].key {
				t.Fatalf("the explicit retry: %+v", calls)
			}
		})
	}
}

// 9. A 401 on a write ends the session, also when its target was left and
// another row is open; a later success fills nothing. Would miss: the
// operation of a left target sorted out before the 401 is seen.
func TestA401OnAWriteEndsTheSessionWhateverIsSelected(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.drive(h.choose(orgA))
	a := h.m.app
	rowA := a.active.rows[0].ID
	h.f.setMark(func(string, string) (api.NotificationState, transport.Answer) {
		return api.NotificationState{}, transport.Answer{Class: transport.Unauthenticated, Status: 401, Sent: true}
	})
	cmd := h.press("r")
	h.press("down")
	h.drive(h.press("enter")) // B's details
	h.drive(h.press("o"))     // and its ticket
	if a.origin == nil || a.origin.notification == rowA {
		t.Fatal("setup: the ticket of B is not open")
	}
	store := a.store
	result := resultOf(h, cmd)
	if !h.events.State().Revoked {
		t.Fatal("the mailbox did not record the 401")
	}
	// Reset the mailbox's say so the model's own path is what is tested.
	h.events = gateway.NewEvents(h.live)
	a.deps.Events = h.events
	a.session = h.events.Session()
	result.Session = a.session
	h.feed(result)
	if !a.expired || a.active != nil || a.store != nil || a.origin != nil || len(a.marks) != 0 {
		t.Fatalf("after the 401: expired %v, active %v, store %v, origin %v, marks %d", a.expired, a.active != nil, a.store != nil, a.origin != nil, len(a.marks))
	}
	if _, ok := store.Get(a.inboxKey(orgA, 1)); ok {
		t.Fatal("the cache kept the inbox")
	}
	if got := ansi.Strip(h.m.View().Content); !strings.Contains(got, "Session expired") || strings.Contains(got, "Message 0001") {
		t.Fatalf("screen:\n%s", got)
	}
	// A late success for another operation fills nothing.
	late := gateway.MarkReadResult{MarkReadTicket: gateway.MarkReadTicket{Session: a.session, Op: 1, Notification: rowA, Key: "lalabase-mark-late"}, State: api.NotificationState{ID: rowA, State: "read"}, Answer: transport.Answer{Class: transport.Object, Status: 200, Sent: true}}
	if cmds := h.feed(late); len(cmds) != 0 || a.active != nil || a.store != nil || len(a.marks) != 0 || !a.expired {
		t.Fatal("a late success filled the ended session")
	}
	for _, key := range []string{"r", "R", "enter", "o", "esc"} {
		if cmd := h.press(key); cmd != nil {
			t.Fatalf("%s acted after the expiry", key)
		}
	}
	if len(h.f.markCalls()) != 1 {
		t.Fatal("a write after the expiry")
	}
}

// Through the real mailbox: a 401 the request observed expires the
// session before the write's own result is handled, and the result then
// changes nothing.
func TestAn401ObservedOnAWriteExpiresBeforeItsResult(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.drive(h.choose(orgA))
	a := h.m.app
	h.f.setMark(func(string, string) (api.NotificationState, transport.Answer) {
		return api.NotificationState{}, transport.Answer{Class: transport.NoAccess, Status: 401, Sent: true}
	})
	result := resultOf(h, h.press("r"))
	if !h.events.State().Revoked {
		t.Fatal("the 401 was not observed")
	}
	h.feed(result)
	if !a.expired || len(a.marks) != 0 {
		t.Fatal("the session survived an observed 401")
	}
}

// 10. Output: a hostile refusal text reaches the screen cleaned, in the
// row, the details and the ticket's head; r in the search line is text.
func TestHostileRefusalTextIsCleanedAndRInTheSearchLineIsText(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.openLinked(orgA, 0)
	a := h.m.app
	h.f.setMark(func(string, string) (api.NotificationState, transport.Answer) {
		return api.NotificationState{}, transport.Answer{Class: transport.Refused, Reason: "archived", Status: 200, Sent: true, Message: "ERR_START" + hostileText}
	})
	h.drive(h.press("r"))
	for _, width := range []int{120, 80, 79} {
		h.feed(tea.WindowSizeMsg{Width: width, Height: 30})
		h.m.focus = view.Object
		text := h.m.View().Content
		assertViewSafe(t, text)
		if !strings.Contains(ansi.Strip(text), "ERR_START") {
			t.Fatalf("width %d: the refusal is missing from the ticket:\n%s", width, ansi.Strip(text))
		}
	}
	h.drive(h.press("esc"))
	text := h.m.View().Content
	assertViewSafe(t, text)
	if !strings.Contains(ansi.Strip(text), "ERR_START") {
		t.Fatalf("the refusal is missing from the details:\n%s", ansi.Strip(text))
	}
	h.press("esc")
	assertViewSafe(t, h.m.View().Content)
	if h.rowTrailing(a.active.selected) != "not marked" {
		t.Fatalf("trailing %q", h.rowTrailing(a.active.selected))
	}
	// r in the search line is text.
	h.press("esc")
	h.drive(h.chooseTickets(orgA))
	h.drive(h.openProject(website))
	before := len(h.f.markCalls())
	h.press("/")
	if cmd := h.press("r"); cmd != nil || h.m.mode != TextEntry || a.tickets.entry != "r" || len(h.f.markCalls()) != before || len(a.marks) != 0 {
		t.Fatalf("r in the search line: mode %d, entry %q, calls %d, marks %d", h.m.mode, a.tickets.entry, len(h.f.markCalls())-before, len(a.marks))
	}
}

// The ticket opened from a notification is the ticket of C1d: its
// sections, pages, order and expansion work, and nothing of it marks.
func TestTheTicketOpenedFromANotificationPagesLikeAnyOther(t *testing.T) {
	h := newHarness(t)
	six := ticketID(website, 6)
	h.f.set(func(q api.NotificationQuery) transport.Answer {
		rows := slices2(h.d.rows(orgA, 1, 30), h.f.readIDs())
		for i := range rows {
			rows[i].Ticket.ID, rows[i].Ticket.Number = six, 6
		}
		return pageAnswer(q.Page, q.PerPage, len(rows), len(rows), slice(rows, q.Page, q.PerPage), map[string]int{orgA: len(rows)})
	})
	h.start()
	h.openLinked(orgA, 0)
	a := h.m.app
	if a.detail.id != six || h.lastRead() != first {
		t.Fatalf("opened %q with %+v", a.detail.id, h.lastRead())
	}
	mustContain(t, h.paneText(), "Comments · newest first · page 1 of 2 · 30 readable comments", "Notification: unread")
	h.press("tab")
	h.drive(h.press("]"))
	mustContain(t, h.paneText(), "page 2 of 2", "Six comment 1")
	h.drive(h.press("o"))
	mustContain(t, h.paneText(), "oldest first · page 1 of 2")
	h.press("tab")
	h.drive(h.press("]"))
	mustContain(t, h.paneText(), "Attachments · page 2 of 2")
	h.press("tab")
	h.drive(h.press("e"))
	mustContain(t, h.paneText(), "Description · whole text", sixDescriptionWhole)
	h.press("down") // a comment selection in the description does nothing to the inbox
	if a.active.selected != a.active.rows[0].ID {
		t.Fatal("a key of the ticket moved the inbox")
	}
	// Esc closes the expansion first, then returns to the details.
	h.press("esc")
	if a.detail.expansion != nil || a.origin == nil {
		t.Fatal("esc did not close the expansion first")
	}
	h.press("esc")
	if a.origin != nil || h.m.focus != view.Object || a.active.opened == "" {
		t.Fatalf("esc: origin %v, focus %d, opened %q", a.origin, h.m.focus, a.active.opened)
	}
	if len(h.f.markCalls()) != 0 {
		t.Fatal("paging wrote")
	}
	// R on the ticket reads the ticket, not the inbox.
	h.drive(h.press("o"))
	reads, inbox := len(h.f.detailReads()), len(h.f.inboxCalls())
	h.drive(h.press("R"))
	if len(h.f.detailReads()) != reads+1 || len(h.f.inboxCalls()) != inbox {
		t.Fatalf("R: %d ticket reads, %d inbox reads", len(h.f.detailReads())-reads, len(h.f.inboxCalls())-inbox)
	}
	// Enter on another row closes the ticket with the details it came from.
	h.m.focus = view.List
	h.press("down")
	h.drive(h.press("enter"))
	if a.origin != nil || a.detail != nil || a.active.opened != a.active.rows[1].ID || h.m.focus != view.Object {
		t.Fatalf("enter: origin %v, detail %v, opened %q", a.origin, a.detail, a.active.opened)
	}
	// A page turn and another inbox drop it too.
	h.drive(h.press("o"))
	h.m.focus = view.List
	h.drive(h.press("]"))
	if a.origin != nil || a.detail != nil {
		t.Fatal("the page turn kept the ticket")
	}
	h.drive(h.press("["))
	h.drive(h.press("enter"))
	h.drive(h.press("o"))
	h.drive(h.choose(orgB))
	if a.origin != nil || a.detail != nil {
		t.Fatal("another inbox kept the ticket")
	}
}

// The outcome is shown where the target is, and the states are told apart:
// running, confirmed, refused, unknown.
func TestTheOutcomesAreToldApart(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.drive(h.choose(orgA))
	a := h.m.app
	rows := a.active.rows
	h.f.setMark(func(id, _ string) (api.NotificationState, transport.Answer) {
		switch id {
		case rows[0].ID:
			return markedAnswer(id)
		case rows[1].ID:
			return api.NotificationState{}, transport.Answer{Class: transport.Refused, Reason: "archived", Status: 200, Sent: true, Message: "archived"}
		case rows[2].ID:
			return api.NotificationState{}, transport.Answer{Class: transport.Temporary, Status: 503, Sent: true}
		}
		return api.NotificationState{}, transport.Answer{Class: transport.Temporary}
	})
	for i := range 4 {
		a.active.selected = rows[i].ID
		h.drive(h.press("r"))
	}
	a.active.selected = rows[4].ID
	running := h.press("r")
	if running == nil {
		t.Fatal("no fifth operation")
	}
	want := []string{"marked", "not marked", "unknown", "not marked", "marking…"}
	for i, w := range want {
		if got := h.rowTrailing(rows[i].ID); got != w {
			t.Errorf("row %d: %q, want %q", i, got, w)
		}
	}
	texts := map[int]string{0: "Mark as read: Marked as read", 1: "Mark as read: Not marked: refused: archived", 2: "Mark as read: Result unknown", 3: "Mark as read: Not marked; the server was not reached. r try again", 4: "Mark as read: Marking as read…"}
	for i, w := range texts {
		a.active.selected = rows[i].ID
		h.drive(h.press("enter"))
		mustContain(t, h.detailsText(), w)
		h.press("esc")
	}
	_ = running
}

// The page correction after a mark keeps the ticket: the only row of the
// last page is marked from the ticket opened out of it, the inbox moves to
// the new last page, and the ticket stays open and focused with its
// outcome; Esc then lands in the list.
func TestThePageCorrectionAfterAMarkKeepsTheTicket(t *testing.T) {
	h := newHarness(t)
	h.f.set(func(q api.NotificationQuery) transport.Answer {
		rows := slices2(h.d.rows(orgA, 1, 26), h.f.readIDs())
		return pageAnswer(q.Page, q.PerPage, len(rows), len(rows), slice(rows, q.Page, q.PerPage), map[string]int{orgA: len(rows)})
	})
	h.start()
	h.drive(h.choose(orgA))
	h.drive(h.press("]"))
	a := h.m.app
	if a.active.page != 2 || len(a.active.rows) != 1 {
		t.Fatalf("page %d with %d rows", a.active.page, len(a.active.rows))
	}
	h.drive(h.press("enter"))
	h.drive(h.press("o"))
	if a.origin == nil || a.detail == nil || !a.detail.known {
		t.Fatal("the linked ticket is not open")
	}
	h.drive(h.press("r"))
	if a.active.page != 1 || len(a.active.rows) != 25 {
		t.Fatalf("after the correction: page %d with %d rows", a.active.page, len(a.active.rows))
	}
	if a.origin == nil || a.detail == nil || !a.detail.known || h.m.focus != view.Object {
		t.Fatalf("the correction took the ticket: origin %v, detail %v, focus %d", a.origin, a.detail != nil, h.m.focus)
	}
	mustContain(t, h.paneText(), "#1 Login bricht ab", "Notification: unread · Marked as read")
	h.drive(h.press("esc"))
	if a.origin != nil || h.m.focus != view.List || a.active.opened != "" || a.active.selected == "" {
		t.Fatalf("after esc: origin %v, focus %d, opened %q, selected %q", a.origin, h.m.focus, a.active.opened, a.active.selected)
	}
}
