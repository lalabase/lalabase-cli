package ui

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/cache"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

// startHolding runs the start up to the tools and returns the counters'
// command unrun, for tests that order the answers themselves.
func (h *harness) startHolding() tea.Cmd {
	h.t.Helper()
	msgs := messages(h.t, h.m.Init())
	cmds := h.feed(msgs...)
	results := messages(h.t, tea.Batch(cmds...))
	var held []tea.Cmd
	for _, msg := range results {
		r := msg.(gateway.StartupResult)
		if r.Step == gateway.StepOrganisations {
			h.feed(r)
		}
	}
	for _, msg := range results {
		r := msg.(gateway.StartupResult)
		if r.Step == gateway.StepInitialize {
			held = h.feed(r)
		}
	}
	toolMsgs := messages(h.t, tea.Batch(held...))
	cmds = h.feed(toolMsgs...)
	if len(cmds) != 1 {
		h.t.Fatalf("%d commands after the tools", len(cmds))
	}
	return cmds[0]
}

// Plan test 6: counters follow the request sequence, not the arrival
// order. A filtered answer touches its organisation alone; a global one is
// a snapshot in which absence is unknown; an explicit zero stays zero.
func TestCountersFollowTheRequestSequence(t *testing.T) {
	global := map[string]int{orgA: 5, orgB: 3}
	filteredA := 7
	serve := func(h *harness) {
		h.f.set(func(q api.NotificationQuery) transport.Answer {
			if q.OrganisationID == orgA {
				return pageAnswer(q.Page, q.PerPage, 7, 7, h.d.rows(orgA, 1, 7), map[string]int{orgA: filteredA})
			}
			return pageAnswer(q.Page, q.PerPage, 8, 8, h.d.rows(orgA, 1, 5), global)
		})
	}
	// The order of the requests is the order they were sent in (the
	// queue's flight), the global one first here; the answers arrive in
	// either order.
	for _, globalFirst := range []bool{false, true} {
		h := newHarness(t)
		serve(h)
		counters := h.startHolding()        // the global read
		inbox := h.choose(orgA)             // the filtered read
		globalMsgs := messages(t, counters) // sent first
		inboxMsgs := messages(t, inbox)     // sent second
		if globalFirst {
			h.feed(globalMsgs...)
			if c := h.m.app.countOf(orgA); !c.known || c.value != 5 {
				t.Fatalf("global first, A: %+v", c)
			}
			h.feed(inboxMsgs...)
		} else {
			h.feed(inboxMsgs...)
			if c := h.m.app.countOf(orgB); c.known {
				t.Fatalf("filtered A touched B: %+v", c)
			}
			h.feed(globalMsgs...)
		}
		a := h.m.app
		if c := a.countOf(orgA); !c.known || c.value != 7 {
			t.Fatalf("global first %v, A: %+v", globalFirst, c)
		}
		if c := a.countOf(orgB); !c.known || c.value != 3 || c.seq >= a.countOf(orgA).seq {
			t.Fatalf("global first %v, B: %+v", globalFirst, c)
		}
		if c := a.countOf(""); !c.known || c.value != 8 {
			t.Fatalf("global first %v, all: %+v", globalFirst, c)
		}
	}
	// Omission is unknown with its sequence; an explicit zero is zero; a
	// newer removal makes a counter unknown again.
	h := newHarness(t)
	h.f.set(func(q api.NotificationQuery) transport.Answer {
		return pageAnswer(q.Page, q.PerPage, 0, 0, nil, map[string]int{orgA: 0})
	})
	h.start()
	a := h.m.app
	if c := a.countOf(orgA); !c.known || c.value != 0 {
		t.Fatalf("zero: %+v", c)
	}
	if c := a.countOf(orgB); c.known || c.seq == 0 {
		t.Fatalf("omitted B: %+v", c)
	}
	if got := countShown(h.m, orgB); got != "?" {
		t.Fatalf("omitted B shows %q", got)
	}
	h.f.set(func(q api.NotificationQuery) transport.Answer {
		return transport.Answer{Class: transport.NotFound, Status: 200, Sent: true, Message: "gone"}
	})
	h.drive(h.choose(orgA))
	if c := a.countOf(orgA); c.known || c.seq <= a.countOf(orgB).seq {
		t.Fatalf("a removal left A known: %+v", c)
	}
	if got := h.m.screen().List.Notice.String(); got != "Not found or no longer accessible." {
		t.Fatalf("notice %q", got)
	}
}

// Plan test 7: highlighting sends nothing; Enter chooses, and the read is
// unread and scoped to the organisation.
func TestHighlightingSendsNothingAndEnterChooses(t *testing.T) {
	h := newHarness(t)
	h.start()
	before := len(h.f.inboxCalls())
	h.m.focus = view.Navigation
	// Adapted in C1c: the chooser has an inbox and a tickets entry per
	// organisation, so B's inbox is three steps down.
	for _, key := range []string{"down", "down", "down", "up", "down"} {
		if cmd := h.press(key); cmd != nil {
			t.Fatalf("%s issued a command", key)
		}
	}
	a := h.m.app
	if a.highlight != orgB || a.highlightSection != sectionInbox || a.active != nil || len(h.f.inboxCalls()) != before {
		t.Fatalf("highlight %q, active %v, reads %d", a.highlight, a.active != nil, len(h.f.inboxCalls()))
	}
	h.m.app.highlight = orgA
	h.drive(h.press("enter"))
	calls := h.f.inboxCalls()
	if len(calls) != before+1 || calls[before] != (api.NotificationQuery{Status: "unread", Page: 1, PerPage: 25, OrganisationID: orgA}) {
		t.Fatalf("reads %+v", calls)
	}
	if a.active == nil || a.active.org != orgA || len(a.active.rows) != 25 || h.m.focus != view.List {
		t.Fatalf("active %+v focus %d", a.active, h.m.focus)
	}
	if got := h.m.screen().Context.String(); got != "Nordlicht GmbH | Inbox | page 1 of 2" {
		t.Fatalf("context %q", got)
	}
	// Enter in the list opens the details from the row, without a request.
	h.drive(h.press("enter"))
	if a.active.opened != a.active.rows[0].ID || h.m.focus != view.Object || len(h.f.inboxCalls()) != before+1 {
		t.Fatalf("details: opened %q, focus %d, reads %d", a.active.opened, h.m.focus, len(h.f.inboxCalls()))
	}
}

// Plan test 10: the last page from the total, and invalid metadata never
// reaching the cache.
func TestPagingTotalsAndInvalidMetadata(t *testing.T) {
	for _, tc := range []struct {
		total, last int
		next        bool
	}{{0, 1, false}, {1, 1, false}, {25, 1, false}, {26, 2, true}, {50, 2, true}, {51, 3, true}} {
		h := newHarness(t)
		h.f.set(func(q api.NotificationQuery) transport.Answer {
			rows := slice(h.d.rows(orgA, 1, tc.total), q.Page, q.PerPage)
			return pageAnswer(q.Page, q.PerPage, tc.total, tc.total, rows, map[string]int{orgA: tc.total})
		})
		h.start()
		h.drive(h.choose(orgA))
		v := h.m.app.active
		if lastPage(v.total, v.perPage) != tc.last {
			t.Errorf("total %d: last page %d", tc.total, lastPage(v.total, v.perPage))
		}
		if h.m.allowed()[NextPage] != tc.next || h.m.allowed()[PrevPage] {
			t.Errorf("total %d: next %v prev %v", tc.total, h.m.allowed()[NextPage], h.m.allowed()[PrevPage])
		}
		if tc.total == 0 && h.m.screen().List.Notice.String() != "No unread notifications." {
			t.Errorf("empty: notice %q", h.m.screen().List.Notice.String())
		}
	}
	h := newHarness(t)
	h.start()
	h.f.set(func(q api.NotificationQuery) transport.Answer {
		return pageAnswer(q.Page, 0, 3, 3, nil, map[string]int{orgA: 3})
	})
	h.drive(h.choose(orgA))
	a := h.m.app
	if _, ok := a.store.Get(a.active.key); ok {
		t.Fatal("invalid metadata reached the cache")
	}
	if got := h.m.screen().List.Notice.String(); !strings.Contains(got, "Unexpected server response") {
		t.Fatalf("notice %q", got)
	}
	h.f.set(func(q api.NotificationQuery) transport.Answer {
		rows := h.d.rows(orgA, 1, 2)
		rows[1].ID = rows[0].ID
		return pageAnswer(q.Page, 25, 2, 2, rows, map[string]int{orgA: 2})
	})
	h.drive(h.press("R"))
	if _, ok := a.store.Get(a.active.key); ok || len(a.active.rows) != 0 {
		t.Fatal("a duplicate id reached the cache or the screen")
	}
}

// Plan test 11: the selection and the opened row are ids: a reorder keeps
// them, a resize keeps everything, a removal selects the first row and
// closes the details.
func TestSelectionIsByIDAcrossReorderAndResize(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.drive(h.choose(orgA))
	h.press("down")
	h.press("down")
	a := h.m.app
	third := a.active.rows[2].ID
	if a.active.selected != third {
		t.Fatalf("selected %q", a.active.selected)
	}
	h.drive(h.press("enter")) // details of the third row
	h.f.set(func(q api.NotificationQuery) transport.Answer {
		rows := h.d.rows(orgA, 1, 25)
		slices.Reverse(rows)
		return pageAnswer(q.Page, q.PerPage, 30, 30, rows, map[string]int{orgA: 30})
	})
	h.drive(h.press("R"))
	if a.active.selected != third || a.active.opened != third || a.active.index(third) != 22 {
		t.Fatalf("after the reorder: selected %q opened %q at %d", a.active.selected, a.active.opened, a.active.index(third))
	}
	if h.m.screen().List.Cursor != 22 {
		t.Fatalf("cursor %d", h.m.screen().List.Cursor)
	}
	reads := len(h.f.inboxCalls())
	for _, width := range []int{79, 80, 119, 120, 79} {
		if cmds := h.feed(tea.WindowSizeMsg{Width: width, Height: 24}); len(cmds) != 0 {
			t.Fatalf("resize to %d issued a command", width)
		}
		h.m.View()
		if a.active.page != 1 || a.active.selected != third || a.active.opened != third || h.m.focus != view.Object {
			t.Fatalf("resize to %d lost state", width)
		}
		if len(h.f.inboxCalls()) != reads {
			t.Fatalf("resize to %d asked the server", width)
		}
		if got := ansi.Strip(h.m.View().Content); !strings.Contains(got, "Message 0003") {
			t.Fatalf("resize to %d lost the details:\n%s", width, got)
		}
	}
	h.f.set(func(q api.NotificationQuery) transport.Answer {
		rows := slices.DeleteFunc(h.d.rows(orgA, 1, 25), func(n api.Notification) bool { return n.ID == third })
		return pageAnswer(q.Page, q.PerPage, 29, 29, rows, map[string]int{orgA: 29})
	})
	h.drive(h.press("R"))
	if a.active.selected != a.active.rows[0].ID || a.active.opened != "" || h.m.focus != view.List {
		t.Fatalf("after the removal: selected %q opened %q focus %d", a.active.selected, a.active.opened, h.m.focus)
	}
}

// Plan test 12: a page beyond the last after a shrink is corrected once;
// a second shrink stops with R; a total of zero adds no request.
func TestAShrunkInboxIsCorrectedOnce(t *testing.T) {
	h := newHarness(t)
	total := 60
	h.f.set(func(q api.NotificationQuery) transport.Answer {
		rows := slice(h.d.rows(orgA, 1, total), q.Page, q.PerPage)
		return pageAnswer(q.Page, q.PerPage, total, total, rows, map[string]int{orgA: total})
	})
	h.start()
	h.drive(h.choose(orgA))
	h.drive(h.press("]"))
	h.drive(h.press("]"))
	a := h.m.app
	if a.active.page != 3 || len(a.active.rows) != 10 {
		t.Fatalf("page %d, %d rows", a.active.page, len(a.active.rows))
	}
	total = 30
	before := len(h.f.inboxCalls())
	h.drive(h.press("R"))
	if a.active.page != 2 || len(a.active.rows) != 5 || len(h.f.inboxCalls()) != before+2 {
		t.Fatalf("after the shrink: page %d, %d rows, %d reads", a.active.page, len(a.active.rows), len(h.f.inboxCalls())-before)
	}
	if got := h.m.screen().Context.String(); got != "Nordlicht GmbH | Inbox | page 2 of 2" {
		t.Fatalf("context %q", got)
	}
	// Another shrink during the correction: stop, offer R.
	h.drive(h.press("]")) // nothing: page 2 is the last
	total = 60
	h.drive(h.press("R"))
	h.drive(h.press("]"))
	if a.active.page != 3 {
		t.Fatalf("page %d", a.active.page)
	}
	shrinks := 0
	h.f.set(func(q api.NotificationQuery) transport.Answer {
		shrinks++
		if shrinks == 1 {
			return pageAnswer(q.Page, q.PerPage, 30, 30, nil, map[string]int{orgA: 30}) // page 3 of 30: beyond
		}
		return pageAnswer(q.Page, q.PerPage, 10, 10, nil, map[string]int{orgA: 10}) // page 2 of 10: beyond again
	})
	before = len(h.f.inboxCalls())
	h.drive(h.press("R"))
	if len(h.f.inboxCalls()) != before+2 || a.active.page != 2 {
		t.Fatalf("second shrink: %d reads, page %d", len(h.f.inboxCalls())-before, a.active.page)
	}
	if got := h.m.screen().List.Notice.String(); got != "The inbox shrank again. R refresh." {
		t.Fatalf("notice %q", got)
	}
	if !h.m.allowed()[Refresh] {
		t.Fatal("R not offered")
	}
	// Zero: an empty page 1 without a request.
	h.f.set(func(q api.NotificationQuery) transport.Answer {
		return pageAnswer(q.Page, q.PerPage, 0, 0, nil, map[string]int{orgA: 0})
	})
	before = len(h.f.inboxCalls())
	h.drive(h.press("R"))
	if len(h.f.inboxCalls()) != before+1 || a.active.page != 1 || len(a.active.rows) != 0 {
		t.Fatalf("zero: %d reads, page %d, %d rows", len(h.f.inboxCalls())-before, a.active.page, len(a.active.rows))
	}
	if _, ok := a.store.Get(a.inboxKey(orgA, 1)); ok {
		t.Fatal("a page-2 payload was filed under page 1")
	}
}

// Plan test 13: returning to a cached page shows it at once and reloads it
// exactly once; rendering, resizing, details and R while pending add no
// request.
func TestACacheHitShowsAtOnceAndReloadsOnce(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.drive(h.choose(orgA))
	h.press("down")
	h.drive(h.press("]")) // page 2, remembered
	h.drive(h.choose(orgB))
	a := h.m.app
	before := len(h.f.inboxCalls())
	seqA := a.countOf(orgA).seq
	reload := h.choose(orgA)
	v := a.active
	if v.page != 2 || len(v.rows) != 5 || !v.cached || !v.loading {
		t.Fatalf("return: page %d, %d rows, cached %v, loading %v", v.page, len(v.rows), v.cached, v.loading)
	}
	if got := h.m.screen().List.Notice.String(); got != "Cached" {
		t.Fatalf("notice %q", got)
	}
	if a.countOf(orgA).seq != seqA {
		t.Fatal("a cache hit replayed the counters")
	}
	h.m.View()
	h.feed(tea.WindowSizeMsg{Width: 80, Height: 24})
	h.m.View()
	if cmd := h.press("enter"); cmd != nil {
		t.Fatal("details issued a command")
	}
	if cmd := h.press("R"); cmd != nil {
		t.Fatal("R while pending issued a command")
	}
	if len(h.f.inboxCalls()) != before {
		t.Fatalf("%d reads before the one reload", len(h.f.inboxCalls())-before)
	}
	h.feed(messages(t, reload)...)
	if len(h.f.inboxCalls()) != before+1 || v.cached || v.loading || h.m.screen().List.Notice.String() != "" {
		t.Fatalf("after the reload: %d reads, cached %v, loading %v, notice %q", len(h.f.inboxCalls())-before, v.cached, v.loading, h.m.screen().List.Notice.String())
	}
	// A cached page that does not read any more is forgotten and loaded.
	a.store.Apply(a.inboxKey(orgB, 1), cache.Replace, []byte(`{"data":"broken"}`), h.now)
	before = len(h.f.inboxCalls())
	cmd := h.choose(orgB)
	if a.active.known || a.active.cached {
		t.Fatal("a broken cache entry was shown")
	}
	if _, ok := a.store.Get(a.inboxKey(orgB, 1)); ok {
		t.Fatal("the broken entry was kept")
	}
	h.feed(messages(t, cmd)...)
	if len(h.f.inboxCalls()) != before+1 || len(a.active.rows) != 3 {
		t.Fatalf("after the broken entry: %d reads, %d rows", len(h.f.inboxCalls())-before, len(a.active.rows))
	}
}

// Plan test 14: the cache decisions on the screen: a temporary failure
// keeps the rows and the load time with "Possibly stale", a refusal keeps
// them with its message, a removal clears rows and details.
func TestTheCacheDecisionsOnTheScreen(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.drive(h.choose(orgA))
	h.drive(h.press("enter"))
	a := h.m.app
	v := a.active
	loaded := v.loadedAt
	h.now = h.now.Add(time.Minute)
	h.f.set(func(api.NotificationQuery) transport.Answer { return failing(transport.Temporary, 503) })
	h.drive(h.press("R"))
	if len(v.rows) != 25 || !v.stale || !v.loadedAt.Equal(loaded) || v.opened == "" {
		t.Fatalf("temporary: %d rows, stale %v, loaded %v, opened %q", len(v.rows), v.stale, v.loadedAt, v.opened)
	}
	if got := h.m.screen().List.Notice.String(); got != "Possibly stale" {
		t.Fatalf("notice %q", got)
	}
	h.f.set(func(api.NotificationQuery) transport.Answer {
		return transport.Answer{Class: transport.Refused, Reason: "archived", Message: "archived", Status: 200, Sent: true}
	})
	h.drive(h.press("R"))
	if len(v.rows) != 25 || h.m.screen().List.Notice.String() != "Refused: refused: archived" {
		t.Fatalf("refused: %d rows, notice %q", len(v.rows), h.m.screen().List.Notice.String())
	}
	h.f.set(h.d.defaultInbox)
	h.drive(h.press("R"))
	if v.stale || h.m.screen().List.Notice.String() != "" || v.loadedAt.Equal(loaded) {
		t.Fatalf("fresh: stale %v, notice %q", v.stale, h.m.screen().List.Notice.String())
	}
	h.f.set(func(api.NotificationQuery) transport.Answer { return failing(transport.NoAccess, 200) })
	h.drive(h.press("R"))
	if len(v.rows) != 0 || v.opened != "" || h.m.focus != view.List || a.countOf(orgA).known {
		t.Fatalf("removed: %d rows, opened %q, focus %d, count %+v", len(v.rows), v.opened, h.m.focus, a.countOf(orgA))
	}
	if _, ok := a.store.Get(v.key); ok {
		t.Fatal("the removed page stayed cached")
	}
}

// Plan test 15: two consumers share one request that answers a removal;
// after a newer reload, the late copy of the removal leaves the new rows,
// the counters and the counters step alone, and invalidates nothing again.
func TestALateCopyOfARemovalLeavesNewerDataAlone(t *testing.T) {
	h := newHarness(t)
	gone := failing(transport.NotFound, 200)
	h.f.set(func(api.NotificationQuery) transport.Answer { return gone })
	gate := make(chan struct{})
	h.f.gate = gate
	counters := h.startHolding()
	inbox := h.choose("") // joins the pending global read
	a := h.m.app
	if len(a.waiters) != 2 {
		t.Fatalf("%d waiters", len(a.waiters))
	}
	var share string
	for _, w := range a.waiters {
		share = w.ticket.ShareKey()
	}
	first := h.background(counters)
	h.waitFor("the first waiter", func() bool { return h.q.Sharers(share) == 1 })
	second := h.background(inbox)
	h.waitFor("the second waiter", func() bool { return h.q.Sharers(share) == 2 })
	close(gate)
	r1, r2 := h.receive(first).(gateway.InboxResult), h.receive(second).(gateway.InboxResult)
	if r1.Flight == 0 || r1.Flight != r2.Flight {
		t.Fatalf("flights %d %d", r1.Flight, r2.Flight)
	}
	inboxResult, copyResult := r2, r1
	if r1.Waiter == a.active.waiterFor(a) {
		inboxResult, copyResult = r1, r2
	}
	key := a.active.key
	epochBefore := a.scope.Begin(gateway.Inbox, key, 0).Epoch
	h.feed(inboxResult)
	if len(a.active.rows) != 0 || a.countOf("").known || h.m.screen().List.Notice.String() != "Not found or no longer accessible." {
		t.Fatal("the removal was not applied")
	}
	epoch := a.scope.Begin(gateway.Inbox, key, 0).Epoch
	if epoch != epochBefore+1 {
		t.Fatalf("the removal moved the epoch from %d to %d: invalidated more than once", epochBefore, epoch)
	}
	// R is held back while the other consumer's result is still out; a
	// return to the inbox reads it anew, under the new epoch.
	if cmd := h.press("R"); cmd != nil {
		t.Fatal("R issued a read while a consumer was still pending")
	}
	h.f.gate = nil
	h.f.set(h.d.defaultInbox)
	h.drive(h.choose(orgA))
	h.drive(h.choose(""))
	if len(a.active.rows) != 25 || !a.countOf("").known || a.countOf("").value != 33 {
		t.Fatalf("the reload: %d rows, all %+v", len(a.active.rows), a.countOf(""))
	}
	h.feed(copyResult)
	if len(a.active.rows) != 25 || !a.countOf("").known || a.countOf(orgA).value != 30 {
		t.Fatalf("the copy cleared newer data: %d rows, all %+v", len(a.active.rows), a.countOf(""))
	}
	if a.steps[stepCounters].state == failed {
		t.Fatal("the copy failed the counters step")
	}
	if e, ok := a.store.Get(key); !ok || len(e.Payload) == 0 {
		t.Fatal("the copy removed the reloaded page")
	}
	if got := a.scope.Begin(gateway.Inbox, key, 0).Epoch; got != epoch {
		t.Fatalf("the epoch moved from %d to %d: a second invalidation", epoch, got)
	}
}

// waiterFor is the waiter of the active view's pending read, for the test
// above to tell the two results apart.
func (v *inboxView) waiterFor(a *app) gateway.Waiter {
	for id, w := range a.waiters {
		if w.purpose == forInbox && w.ticket.Key == v.key {
			return id
		}
	}
	return 0
}

// Plan test 18: nullable references, blanks, an invalid time and the UTC
// conversion, in the row and in the details; Enter adds no request.
func TestRowsAndDetailsFallBackForMissingValues(t *testing.T) {
	h := newHarness(t)
	h.f.set(func(q api.NotificationQuery) transport.Answer {
		rows := h.d.rows(orgA, 1, 4)
		rows[0].Actor, rows[0].Project, rows[0].Ticket = nil, nil, nil
		rows[0].Message, rows[0].LastEventAt = " ", "not a time"
		rows[1].Actor = &api.Ref{ID: "u-1", Name: ""}
		rows[1].Project = &api.Ref{ID: "p-1", Name: " "}
		rows[1].Ticket.Title = ""
		rows[1].LastEventAt = "2026-01-05T10:30:00.123456+02:00"
		rows[1].Organisation.Name = ""
		rows[2].Read = true
		return pageAnswer(q.Page, q.PerPage, 4, 4, rows, map[string]int{orgA: 4})
	})
	h.start()
	h.drive(h.choose(orgA))
	s := h.m.screen()
	if got := s.List.Rows[0].Primary.String(); got != "No message" {
		t.Fatalf("row 0 primary %q", got)
	}
	if got := s.List.Rows[0].Secondary.String(); got != "Unknown actor | No project | Time unknown" {
		t.Fatalf("row 0 secondary %q", got)
	}
	if got := s.List.Rows[1].Secondary.String(); got != "Unknown actor | Unnamed project | 2026-01-05 08:30 UTC" {
		t.Fatalf("row 1 secondary %q", got)
	}
	before := len(h.f.inboxCalls())
	h.drive(h.press("enter"))
	if len(h.f.inboxCalls()) != before {
		t.Fatal("the details asked the server")
	}
	text := h.m.screen().Object.Text.String()
	for _, want := range []string{"No message", "Actor: Unknown actor", "Project: No project", "Ticket: No ticket", "Last event: Time unknown", "State: unread", "Event: ticket_commented"} {
		if !strings.Contains(text, want) {
			t.Errorf("details lack %q:\n%s", want, text)
		}
	}
	h.press("esc")
	h.press("down")
	h.drive(h.press("enter"))
	text = h.m.screen().Object.Text.String()
	for _, want := range []string{"Organisation: Unnamed organisation", "Project: Unnamed project", "Ticket: #2 Untitled ticket", "Last event: 2026-01-05 08:30:00 UTC"} {
		if !strings.Contains(text, want) {
			t.Errorf("details lack %q:\n%s", want, text)
		}
	}
	h.press("esc")
	h.press("down")
	h.drive(h.press("enter"))
	if !strings.Contains(h.m.screen().Object.Text.String(), "State: read") {
		t.Error("a read row is not shown as read")
	}
}

// Plan test 19: the help names exactly the keys that act, in every focus
// and state: a key that acts is in the help, and every help entry's own
// key acts (for a pair, one of its two keys). Every press starts from a
// fresh model in the state, so nothing of one press leaks into the next.
func TestHelpNamesExactlyTheKeysThatAct(t *testing.T) {
	// C1f adds o (the ticket a notification names) and r (mark as read).
	keys := map[string]Action{"up": Up, "down": Down, "pgup": PageUp, "pgdown": PageDown, "[": PrevPage, "]": NextPage, "R": Refresh, "enter": OpenList, "esc": Back, "left": Left, "right": Right, "o": Order, "r": MarkRead, "tab": NextArea, "e": Expand}
	acted := func(h *harness, key string) bool {
		before := ansi.Strip(h.m.View().Content)
		focus := h.m.focus
		m, cmd := step(t, h.m, press(key))
		h.m = m
		return cmd != nil || ansi.Strip(h.m.View().Content) != before || h.m.focus != focus
	}
	check := func(t *testing.T, state string, build func() *harness) {
		t.Helper()
		h := build()
		allowed := h.m.allowed()
		help := helpText(h.m)
		bindings := h.m.actions()
		for key, action := range keys {
			if acted(build(), key) {
				if !allowed[action] {
					t.Errorf("%s: %s acted but is not offered (%q)", state, key, help)
				}
				if !strings.Contains(help, keyLabel(key)) {
					t.Errorf("%s: %s acted but the help does not name it: %q", state, key, help)
				}
			}
		}
		for _, b := range bindings {
			if b.kind == Quit || b.kind == Interrupt {
				continue
			}
			acts := false
			for key, action := range keys {
				if action != b.kind && pairs[b.kind] != action {
					continue
				}
				if acted(build(), key) {
					acts = true
				}
			}
			if !acts {
				t.Errorf("%s: the help offers %v but no key of it acts", state, b.kind)
			}
		}
	}
	started := func() *harness {
		h := newHarness(t)
		h.m.width, h.m.height = 120, 24
		h.start()
		return h
	}
	check(t, "small", func() *harness { h := newHarness(t); h.m.width, h.m.height = 20, 5; return h })
	check(t, "before /me", func() *harness { return newHarness(t) })
	check(t, "/me failed", func() *harness {
		h := newHarness(t)
		h.f.me = func() (api.Me, transport.Answer) { return api.Me{}, failing(transport.Refused, 200) }
		h.start()
		return h
	})
	check(t, "chooser", func() *harness { h := started(); h.m.focus = view.Navigation; return h })
	check(t, "inbox page 1", func() *harness { h := started(); h.drive(h.choose(orgA)); return h })
	check(t, "inbox page 2", func() *harness { h := started(); h.drive(h.choose(orgA)); h.drive(h.press("]")); return h })
	check(t, "object without details", func() *harness { h := started(); h.drive(h.choose(orgA)); h.m.focus = view.Object; return h })
	check(t, "details", func() *harness { h := started(); h.drive(h.choose(orgA)); h.drive(h.press("enter")); return h })
	check(t, "long details", func() *harness {
		h := started()
		h.f.set(func(q api.NotificationQuery) transport.Answer {
			rows := h.d.rows(orgA, 1, 2)
			rows[0].Message = strings.Repeat("A long message that wraps onto many lines of the details pane. ", 40)
			return pageAnswer(q.Page, q.PerPage, 2, 2, rows, map[string]int{orgA: 2})
		})
		h.drive(h.choose(orgA))
		h.drive(h.press("enter"))
		return h
	})
	check(t, "chooser with an inbox", func() *harness { h := started(); h.drive(h.choose(orgA)); h.m.focus = view.Navigation; return h })
	check(t, "two columns", func() *harness { h := started(); h.drive(h.choose(orgA)); h.m.width = 80; return h })
	check(t, "one column", func() *harness { h := started(); h.drive(h.choose(orgA)); h.m.width = 79; return h })
	check(t, "inbox loading", func() *harness { h := started(); h.drive(h.choose(orgA)); h.press("R"); return h })
	check(t, "expired", func() *harness {
		h := started()
		h.events.Revoke()
		h.feed(gateway.Wake{Session: h.events.Session()})
		return h
	})
	// C1f: the ticket a notification names, and marking as read.
	linked := func() *harness { h := started(); h.openLinked(orgA, 0); return h }
	check(t, "details of a row with a ticket", func() *harness { h := started(); h.openDetails(orgA, 0); return h })
	check(t, "details of a row without a ticket", func() *harness {
		h := started()
		h.f.set(func(q api.NotificationQuery) transport.Answer {
			rows := h.d.rows(orgA, 1, 2)
			rows[0].Ticket = nil
			return pageAnswer(q.Page, q.PerPage, 2, 2, rows, map[string]int{orgA: 2})
		})
		h.openDetails(orgA, 0)
		return h
	})
	check(t, "ticket from a notification", linked)
	check(t, "ticket from a notification, comments", func() *harness { h := linked(); h.press("tab"); return h })
	check(t, "ticket from a notification, list focused", func() *harness { h := linked(); h.m.focus = view.List; return h })
	check(t, "ticket from a notification, one column", func() *harness { h := linked(); h.m.width = 79; return h })
	check(t, "read-only token", func() *harness {
		h := newHarness(t)
		h.setReadOnly(boolPtr(true))
		h.start()
		h.openDetails(orgA, 0)
		return h
	})
	check(t, "unknown permission", func() *harness { h := newHarness(t); h.setReadOnly(nil); h.start(); h.openDetails(orgA, 0); return h })
	check(t, "a running mark", func() *harness { h := started(); h.drive(h.choose(orgA)); h.press("r"); return h })
	check(t, "a refused mark", func() *harness {
		h := started()
		h.f.setMark(func(string, string) (api.NotificationState, transport.Answer) {
			return api.NotificationState{}, failing(transport.Refused, 200)
		})
		h.drive(h.choose(orgA))
		h.drive(h.press("r"))
		return h
	})
	check(t, "a marked row", func() *harness {
		h := started()
		h.f.setMark(func(id, _ string) (api.NotificationState, transport.Answer) { return markedAnswer(id) })
		h.drive(h.choose(orgA))
		h.drive(h.press("r"))
		return h
	})
}

func keyLabel(key string) string {
	switch key {
	case "up", "down":
		return "up/down"
	case "pgup", "pgdown":
		return "pgup/pgdn"
	case "[":
		return "[ prev page"
	case "]":
		return "] next page"
	case "left", "right":
		return "left/right"
	}
	return key
}

const hostileText = "\x1b[2J\x1b[31mCSI\x1b[0m\x1b]8;;https://evil.invalid\x07LINK\x1b]8;;\x1b\\\x1b]52;c;YQ==\x1b\\\x1bPpayload\x1b\\\x07\u202e\u2066\u2069\xff\u009b\n\tEND"

// Plan test 20: hostile text from the server and from the cache reaches
// the screen cleaned, in rows, details, counters, identity and errors.
func TestHostileTextFromServerAndCacheIsCleaned(t *testing.T) {
	h := newHarness(t)
	me := h.d.me
	me.User.DisplayName = "NAME_START" + hostileText
	h.f.me = func() (api.Me, transport.Answer) {
		return me, transport.Answer{Class: transport.Object, Status: 200, Sent: true}
	}
	orgs := []api.Ref{{ID: orgA, Name: "ORG_START" + hostileText}}
	h.f.orgs = func() ([]api.Ref, transport.Answer) {
		return orgs, transport.Answer{Class: transport.Object, Status: 200, Sent: true}
	}
	h.f.set(func(q api.NotificationQuery) transport.Answer {
		rows := h.d.rows(orgA, 1, 2)
		rows[0].Message = "MSG_START" + hostileText
		rows[0].Actor.Name = "ACTOR_START" + hostileText
		rows[0].Ticket.Title = "TITLE_START" + hostileText
		rows[0].Organisation.Name = "ORGNAME_START" + hostileText
		rows[0].EventType = "EVENT_START" + hostileText
		rows[0].ID = "ID_START" + strings.ReplaceAll(hostileText, "\n", "")
		return pageAnswer(q.Page, q.PerPage, 2, 2, rows, map[string]int{orgA: 2})
	})
	h.start()
	h.drive(h.choose(orgA))
	h.drive(h.press("enter"))
	for _, width := range []int{120, 80, 79} {
		h.feed(tea.WindowSizeMsg{Width: width, Height: 24})
		for _, focus := range []view.Pane{view.Navigation, view.List, view.Object} {
			h.m.focus = focus
			text := h.m.View().Content
			assertViewSafe(t, text)
			plain := ansi.Strip(text)
			if focus == view.List && !strings.Contains(plain, "MSG_START") {
				t.Fatalf("width %d: the message is missing:\n%s", width, plain)
			}
			if focus == view.Object && (!strings.Contains(plain, "ACTOR_START") || !strings.Contains(plain, "EVENT_START")) {
				t.Fatalf("width %d: the details are missing:\n%s", width, plain)
			}
		}
	}
	h.m.focus = view.Navigation
	if plain := ansi.Strip(h.m.View().Content); !strings.Contains(plain, "NAME_START") || !strings.Contains(plain, "ORG_START") {
		t.Fatalf("identity or organisation missing:\n%s", plain)
	}
	// A hostile error text.
	h.f.set(func(api.NotificationQuery) transport.Answer {
		return transport.Answer{Class: transport.Refused, Message: "ERR_START" + hostileText, Status: 200, Sent: true}
	})
	h.m.focus = view.List
	h.drive(h.press("R"))
	text := h.m.View().Content
	assertViewSafe(t, text)
	if !strings.Contains(ansi.Strip(text), "ERR_START") {
		t.Fatal("the error text is missing")
	}
	// The cache path: a hostile page filed directly, shown on the return.
	a := h.m.app
	var p api.NotificationPage
	p.Data = h.d.rows(orgB, 101, 1)
	p.Data[0].Message = "CACHED_START" + hostileText
	p.Meta.Page, p.Meta.PerPage, p.Meta.Total, p.Meta.UnreadTotal = 1, 25, 1, 1
	p.Meta.ByOrganisation = []api.OrganisationCount{{ID: orgB, Name: "B", Unread: 1}}
	payload, _ := json.Marshal(p)
	a.directory = append(a.directory, api.Ref{ID: orgB, Name: "B"})
	a.store.Apply(a.inboxKey(orgB, 1), cache.Replace, payload, h.now)
	h.choose(orgB) // the reload is not run: what is shown comes from the cache
	text = h.m.View().Content
	assertViewSafe(t, text)
	if plain := ansi.Strip(text); !strings.Contains(plain, "CACHED_START") || !strings.Contains(plain, "Cached") {
		t.Fatalf("the cached row is missing:\n%s", plain)
	}
}
