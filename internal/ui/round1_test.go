package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/queue"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

// Review round 1, finding 1: paging back to page 1 of all organisations
// while a read of that page is still on the wire (the chooser's R) joins
// that read. Its request is older than the one page 2 was last shown
// from, and its answer must still arrive: the order of requests is kept
// per key, so a newer answer for another page holds nothing back.
func TestPagingBackJoinsThePendingGlobalRead(t *testing.T) {
	h := newHarness(t)
	h.f.set(func(q api.NotificationQuery) transport.Answer {
		if q.Page == 2 {
			return pageAnswer(2, q.PerPage, 33, 33, h.d.rows(orgB, 101, 3), map[string]int{orgA: 30, orgB: 3})
		}
		return h.d.defaultInbox(q)
	})
	h.start()
	h.drive(h.choose(""))
	h.drive(h.press("]"))
	a := h.m.app
	if a.active.page != 2 || len(a.active.rows) != 3 {
		t.Fatalf("page 2: page %d, %d rows", a.active.page, len(a.active.rows))
	}
	// Esc, R: the counters' read of page 1 goes out and is held on the wire.
	gate := make(chan struct{})
	h.f.gate, h.f.gateOrg, h.f.gatePage = gate, "", 1
	h.press("esc")
	before := len(h.f.inboxCalls())
	batch, ok := h.press("R")().(tea.BatchMsg) // the directory and the counters
	if !ok {
		t.Fatal("R in the chooser is not a batch")
	}
	var refresh []<-chan tea.Msg
	for _, c := range batch {
		refresh = append(refresh, h.background(c))
	}
	h.waitFor("page 1 on the wire", func() bool { return len(h.f.inboxCalls()) == before+1 })
	// Back to the inbox (no request of its own), R: page 2 is read anew by
	// a request sent after page 1's.
	h.press("enter")
	h.drive(h.press("R"))
	if a.active == nil || a.active.page != 2 || a.active.loading || a.active.cached {
		t.Fatalf("return: %+v", a.active)
	}
	// Page 1 joins the read on the wire: two sharers of one flight, one
	// request at the source.
	back := h.background(h.press("["))
	if a.active.page != 1 || !a.active.loading {
		t.Fatalf("back: page %d, loading %v", a.active.page, a.active.loading)
	}
	share := a.active.waiterFor(a)
	h.waitFor("the second sharer", func() bool { return h.q.Sharers(a.waiters[share].ticket.ShareKey()) == 2 })
	close(gate)
	var flights []queue.Flight
	for _, ch := range refresh {
		msg := h.receive(ch)
		if r, ok := msg.(gateway.InboxResult); ok {
			flights = append(flights, r.Flight)
		}
		h.feed(msg)
	}
	r := h.receive(back).(gateway.InboxResult)
	h.feed(r)
	// Two requests since the chooser's R: page 1, shared by both, and the
	// reload of page 2.
	if len(flights) != 1 || flights[0] != r.Flight || len(h.f.inboxCalls()) != before+2 {
		t.Fatalf("flights %v and %d, %d requests", flights, r.Flight, len(h.f.inboxCalls())-before)
	}
	if a.active.loading || len(a.active.rows) != 25 || a.active.page != 1 || a.active.cached {
		t.Fatalf("stuck: loading %v, %d rows, page %d, cached %v", a.active.loading, len(a.active.rows), a.active.page, a.active.cached)
	}
	if !h.m.allowed()[NextPage] {
		t.Fatal("] is dead after the return to page 1")
	}
}

// Review round 1, finding 2: a removal that comes through another consumer
// of the key (the counters' refresh of the global page) clears the view of
// the same key too: rows the server took away never stay on screen.
func TestARemovalThroughAnotherConsumerClearsTheView(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.drive(h.choose(""))
	h.drive(h.press("enter"))
	a := h.m.app
	if len(a.active.rows) != 25 || a.active.opened == "" {
		t.Fatal("the global inbox did not open")
	}
	h.press("left") // the list, the details stay open
	h.press("left") // the chooser
	if h.m.focus != view.Navigation || a.active.opened == "" {
		t.Fatalf("focus %d, opened %q: not the chooser with the details open", h.m.focus, a.active.opened)
	}
	h.f.set(func(api.NotificationQuery) transport.Answer { return failing(transport.NoAccess, 200) })
	h.drive(h.press("R")) // refreshes the directory and the counters: the global page 1
	if len(a.active.rows) != 0 || a.active.opened != "" || a.countOf("").known {
		t.Fatalf("the removal left %d rows, opened %q, all %+v", len(a.active.rows), a.active.opened, a.countOf(""))
	}
	if got := h.m.screen().List.Notice.String(); got != "Not found or no longer accessible." {
		t.Fatalf("notice %q", got)
	}
	if _, ok := a.store.Get(a.active.key); ok {
		t.Fatal("the removed page stayed cached")
	}
	// And the other way: a fresh page through the counters' consumer shows.
	h.f.set(h.d.defaultInbox)
	h.drive(h.press("R"))
	if len(a.active.rows) != 25 || a.active.err.String() != "" {
		t.Fatalf("the reload through the counters: %d rows, %q", len(a.active.rows), a.active.err.String())
	}
}

// Review round 1, finding 6: a canceled read of the global page holds back
// neither the counters' refresh nor the opening of all organisations.
func TestACanceledGlobalReadHoldsNothingBack(t *testing.T) {
	h := newHarness(t)
	h.start()
	first := h.choose("")   // the global page, its read not yet run
	h.drive(h.choose(orgA)) // leaves it: the read is canceled
	h.m.focus = view.Navigation
	counters := h.press("R") // the counters: the same key
	again := h.choose("")    // all organisations: the same key
	h.drive(first, counters, again)
	a := h.m.app
	if a.active == nil || a.active.org != "" || a.active.loading || len(a.active.rows) != 25 {
		t.Fatalf("all organisations: %+v", a.active)
	}
	if a.steps[stepCounters].state != ready || len(a.waiters) != 0 {
		t.Fatalf("counters %d, %d waiters", a.steps[stepCounters].state, len(a.waiters))
	}
}

// Review round 1, finding 8: the fallbacks judge the cleaned text. A name
// of invisible characters alone is no name.
func TestFallbacksJudgeTheCleanedText(t *testing.T) {
	invisible := "\u202e\u2066\u200b"
	h := newHarness(t)
	me := h.d.me
	me.User.DisplayName = invisible
	h.f.me = func() (api.Me, transport.Answer) {
		return me, transport.Answer{Class: transport.Object, Status: 200, Sent: true}
	}
	h.f.orgs = func() ([]api.Ref, transport.Answer) {
		return []api.Ref{{ID: orgA, Name: invisible}}, transport.Answer{Class: transport.Object, Status: 200, Sent: true}
	}
	h.f.set(func(q api.NotificationQuery) transport.Answer {
		rows := h.d.rows(orgA, 1, 1)
		rows[0].Message, rows[0].Actor.Name, rows[0].Ticket.Title, rows[0].Organisation.Name = invisible, invisible, invisible, invisible
		rows[0].Project = &api.Ref{ID: "p", Name: invisible}
		return pageAnswer(q.Page, q.PerPage, 1, 1, rows, map[string]int{orgA: 1})
	})
	h.start()
	h.drive(h.choose(orgA))
	h.drive(h.press("enter"))
	s := h.m.screen()
	if !strings.HasPrefix(s.Identity.String(), "Unknown user") {
		t.Errorf("identity %q", s.Identity.String())
	}
	if got := headingRow(h.m, orgA).Primary.String(); got != "Unnamed organisation" {
		t.Errorf("organisation %q", got)
	}
	if got := s.Context.String(); !strings.HasPrefix(got, "Unnamed organisation › Inbox") {
		t.Errorf("context %q", got)
	}
	if got := s.List.Rows[0].Primary.String(); got != "#1 No message" {
		t.Errorf("row %q", got)
	}
	if got := s.List.Rows[0].Secondary.String(); !strings.HasPrefix(got, "Unknown actor · Unnamed project") {
		t.Errorf("secondary %q", got)
	}
	text := s.Object.Text.String()
	for _, want := range []string{"No message", "Organisation: Unnamed organisation", "Ticket: #1 Untitled ticket"} {
		if !strings.Contains(text, want) {
			t.Errorf("details lack %q:\n%s", want, text)
		}
	}
}

// Review round 1, finding 9: the frame never exceeds the terminal. Where
// the help would leave no line for the body, only leaving is offered.
func TestTheFrameNeverExceedsTheTerminal(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.drive(h.choose(orgA))
	for _, size := range [][2]int{{24, 8}, {24, 9}, {30, 8}, {40, 10}, {80, 8}, {80, 24}, {120, 24}} {
		for _, focus := range []view.Pane{view.Navigation, view.List, view.Object} {
			h.m.width, h.m.height, h.m.focus = size[0], size[1], focus
			lines := strings.Split(h.m.View().Content, "\n")
			if len(lines) != size[1] {
				t.Errorf("%dx%d focus %d: %d lines:\n%s", size[0], size[1], focus, len(lines), ansi.Strip(h.m.View().Content))
			}
			for _, line := range lines {
				if w := h.m.widthMethod.StringWidth(line); w > size[0] {
					t.Errorf("%dx%d: a line of %d cells", size[0], size[1], w)
				}
			}
			if h.m.small() && len(h.m.actions()) != 2 {
				t.Errorf("%dx%d: small but %d actions", size[0], size[1], len(h.m.actions()))
			}
		}
	}
	h.m.width, h.m.height, h.m.focus = 24, 8, view.List
	if !h.m.small() {
		t.Fatal("24x8 with the inbox's help is not small")
	}
	if got := ansi.Strip(h.m.View().Content); !strings.Contains(got, "Terminal too small") {
		t.Fatalf("no notice:\n%s", got)
	}
}

// Review round 1, finding 10 and review 2, finding 6: the details scroll
// only as far as there is text, and the help offers scrolling only then.
func TestDetailScrollIsBoundedAndOfferedOnlyWhenNeeded(t *testing.T) {
	h := newHarness(t)
	h.f.set(func(q api.NotificationQuery) transport.Answer {
		rows := h.d.rows(orgA, 1, 2)
		rows[0].Message = strings.Repeat("A long message that wraps onto many lines of the details pane. ", 40)
		return pageAnswer(q.Page, q.PerPage, 2, 2, rows, map[string]int{orgA: 2})
	})
	h.start()
	h.drive(h.choose(orgA))
	h.drive(h.press("enter"))
	a := h.m.app
	if !h.m.allowed()[PageDown] || !strings.Contains(helpText(h.m), "pgup/pgdn scroll") {
		t.Fatal("scrolling is not offered for long details")
	}
	before := ansi.Strip(h.m.View().Content)
	for range 50 {
		h.press("pgdown")
	}
	g := h.m.geometry()
	c := h.m.objectColumn()
	top := view.Extent(c, g, view.Object, h.m.widthMethod) - view.Capacity(c, g)
	if a.active.detailOffset != top || top <= 0 {
		t.Fatalf("offset %d, want the top %d", a.active.detailOffset, top)
	}
	h.press("pgup")
	if a.active.detailOffset != max(0, top-view.Capacity(c, g)) {
		t.Fatalf("one page up: offset %d", a.active.detailOffset)
	}
	if ansi.Strip(h.m.View().Content) == before {
		t.Fatal("scrolling changed nothing on screen")
	}
	// Short details: no scroll in the help, the keys do nothing.
	h.press("esc")
	h.press("down")
	h.drive(h.press("enter"))
	if h.m.allowed()[PageDown] || strings.Contains(helpText(h.m), "scroll") {
		t.Fatal("scrolling offered for details that fit")
	}
	plain := ansi.Strip(h.m.View().Content)
	h.press("pgdown")
	if ansi.Strip(h.m.View().Content) != plain || a.active.detailOffset != 0 {
		t.Fatal("pgdown moved details that fit")
	}
}

// Review 2, finding 6: the page keys are offered one by one, each where its
// page exists.
func TestThePageKeysAreOfferedOneByOne(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.drive(h.choose(orgA))
	if help := helpText(h.m); !strings.Contains(help, "] next page") || strings.Contains(help, "[ prev page") {
		t.Fatalf("page 1 of 2: %q", help)
	}
	if h.m.allowed()[PrevPage] || !h.m.allowed()[NextPage] {
		t.Fatal("page 1: [ allowed or ] not")
	}
	h.drive(h.press("]"))
	if help := helpText(h.m); strings.Contains(help, "] next page") || !strings.Contains(help, "[ prev page") {
		t.Fatalf("page 2 of 2: %q", help)
	}
	if !h.m.allowed()[PrevPage] || h.m.allowed()[NextPage] {
		t.Fatal("page 2: [ not allowed or ] allowed")
	}
}

// Review round 1, finding 7: an organisation the directory lost keeps its
// name in the heading of its open inbox; only the entry for all is called
// so.
func TestADroppedOrganisationKeepsItsNameInTheHeading(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.drive(h.choose(orgA))
	h.press("esc")
	h.f.orgs = func() ([]api.Ref, transport.Answer) {
		return []api.Ref{{ID: orgB, Name: "Südwind AG"}}, transport.Answer{Class: transport.Object, Status: 200, Sent: true}
	}
	h.drive(h.press("R"))
	h.press("esc")
	if got := h.m.screen().Context.String(); !strings.HasPrefix(got, "Nordlicht GmbH › Inbox") {
		t.Fatalf("context %q", got)
	}
	if h.m.app.highlight != "" {
		t.Fatalf("the highlight stayed on a dropped organisation: %q", h.m.app.highlight)
	}
}

// A late result for a key whose cache holds a newer request's answer is
// not filed over it; the view keeps the newer rows.
func TestAnOlderRequestsAnswerIsNotFiledOverANewerOne(t *testing.T) {
	h := newHarness(t)
	counters := h.startHolding()
	old := messages(t, counters)[0] // the flight landed; the message waits
	h.f.set(func(q api.NotificationQuery) transport.Answer {
		return pageAnswer(q.Page, q.PerPage, 1, 1, h.d.rows(orgA, 401, 1), map[string]int{orgA: 1})
	})
	h.drive(h.choose(""))
	a := h.m.app
	if len(a.active.rows) != 1 {
		t.Fatalf("%d rows", len(a.active.rows))
	}
	h.feed(old)
	e, ok := a.store.Get(a.active.key)
	page, _ := api.DecodeNotifications(transport.Answer{Class: transport.Object, Payload: e.Payload, Status: 200})
	if !ok || page.Meta.Total != 1 || len(a.active.rows) != 1 || a.countOf(orgA).value != 1 {
		t.Fatalf("the older answer won: cached total %d, %d rows, A %+v", page.Meta.Total, len(a.active.rows), a.countOf(orgA))
	}
	if len(a.waiters) != 0 {
		t.Fatalf("%d waiters left", len(a.waiters))
	}
}

var _ = tea.Quit

// An older request's answer for one global page arrives after a newer
// request's answer for another page of the resource. The counters step
// and the counts follow the newer request, and an older removal does not
// take away what the newer request brought: the order kept per key does
// not see two keys, a removal speaks for the whole resource.
func TestAnOlderGlobalPageAnsweredAfterANewerOneChangesNothing(t *testing.T) {
	page2 := func(h *harness) func(api.NotificationQuery) transport.Answer {
		return func(q api.NotificationQuery) transport.Answer {
			return pageAnswer(2, q.PerPage, 33, 33, h.d.rows(orgB, 101, 3), map[string]int{orgA: 30, orgB: 3})
		}
	}
	older := map[string]func(h *harness) func(api.NotificationQuery) transport.Answer{
		"failure": func(*harness) func(api.NotificationQuery) transport.Answer {
			return func(api.NotificationQuery) transport.Answer { return failing(transport.Temporary, 0) }
		},
		"success": func(h *harness) func(api.NotificationQuery) transport.Answer {
			return func(q api.NotificationQuery) transport.Answer {
				return pageAnswer(1, q.PerPage, 30, 30, h.d.rows(orgA, 1, 25), map[string]int{orgA: 30})
			}
		},
		"removal": func(*harness) func(api.NotificationQuery) transport.Answer {
			return func(api.NotificationQuery) transport.Answer { return failing(transport.NoAccess, 200) }
		},
	}
	for name, answer := range older {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.f.set(func(q api.NotificationQuery) transport.Answer {
				if q.Page == 2 {
					return page2(h)(q)
				}
				return h.d.defaultInbox(q)
			})
			h.start()
			h.drive(h.choose(""))
			h.drive(h.press("]"))
			h.press("esc")
			// The chooser's R reads page 1 again: the request completes, its
			// answer waits undelivered.
			h.f.set(answer(h))
			old := messages(t, h.press("R"))
			// Back to the inbox, R: page 2 is read anew, by a newer request.
			h.f.set(page2(h))
			h.press("enter")
			h.drive(h.press("R"))
			a := h.m.app
			if a.active == nil || a.active.page != 2 || a.countOf("").value != 33 || a.steps[stepCounters].state != ready {
				t.Fatalf("return: page %d, global %+v, counters %d", a.active.page, a.countOf(""), a.steps[stepCounters].state)
			}
			h.feed(old...)
			if a.steps[stepCounters].state != ready || a.countOf("").value != 33 || !a.countOf(orgB).known || a.countOf(orgB).value != 3 {
				t.Fatalf("after the older answer: counters %d, global %+v, B %+v", a.steps[stepCounters].state, a.countOf(""), a.countOf(orgB))
			}
			if len(a.active.rows) != 3 || a.active.err.String() != "" {
				t.Fatalf("the view: %d rows, %q", len(a.active.rows), a.active.err.String())
			}
			if _, ok := a.store.Get(a.active.key); !ok {
				t.Fatal("the older answer took page 2 out of the cache")
			}
		})
	}
}
