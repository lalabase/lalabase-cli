package ui

// C9c: the title rule of C9b holds for every agent's ticket, with or
// without a title entry of its own, and an outranked removal changes
// nothing (the backlog of #37's pre-landing reviews).

import (
	"testing"

	"github.com/gboyke/lalabase-cli/internal/agents"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

// renamedSix answers every read of #6 with a new title.
func renamedSix(h *harness) func(project, id string, o api.TicketOptions) transport.Answer {
	return func(project, id string, o api.TicketOptions) transport.Answer {
		r := h.d.ticketRecord(project, 6, o)
		r.Title = "Six, renamed"
		return ticketAnswer(r)
	}
}

// A removal of the project's list sent before a newer read of the ticket
// is outranked: it neither takes the title nor refuses a title read begun
// after the newer read.
func TestAnOutrankedListRemovalRefusesNoNewerRead(t *testing.T) {
	h := newHarness(t)
	h.withWatcher(agentIn(1, orgA, 6, agents.Working, h.now))
	h.openSix()
	h.m.focus = view.List
	h.f.setTickets(listGone)
	removal := messages(t, h.press("R")) // sent now, answered last
	// The ticket goes and comes back, both after the list's removal was
	// sent: its newest fact is its title, and a title read is out.
	h.m.focus = view.Object
	h.f.setTicket(notFound)
	h.drive(h.press("R"))
	if got := h.areaRow(6).Primary.String(); got != "#6" {
		t.Fatalf("not removed: %q", got)
	}
	h.f.setTicket(h.d.defaultTicket)
	h.drive(h.press("A"))
	read := messages(t, h.press("R"))
	if len(read) == 0 {
		t.Fatal("R read no title")
	}
	h.drive(h.feed(removal...)...)
	h.drive(h.feed(read...)...)
	if got := h.areaRow(6).Primary.String(); got != "#6 Ticket 6" {
		t.Fatalf("the outranked removal refused the newer read: %q", got)
	}
}

// An agent that appears while the list shows its ticket has no title of
// its own: the area takes the row's. A newer list read renames the
// ticket; once the list is left, the area keeps the new title, not the
// older copy of the ticket in the cache.
func TestANewerListTitleOutlivesTheListForANewAgent(t *testing.T) {
	h := newHarness(t)
	h.withWatcher()
	h.openSix() // #6 read and cached with its old title
	a := h.m.app
	a.agents.list = []agents.Agent{agentIn(1, orgA, 6, agents.Working, h.now)}
	h.drive(a.requestTitles())
	if a.titles[ticketID(website, 6)] != nil {
		t.Fatal("a title read though the list shows the title: not the case")
	}
	h.f.setTickets(func(q api.TicketQuery) transport.Answer {
		rows := h.d.ticketRows(website, 30)
		for i := range rows {
			if rows[i].Number == 6 {
				rows[i].Title = "Six, renamed"
			}
		}
		return ticketPageAnswer(1, 25, len(rows), rows[:25])
	})
	h.m.focus = view.List
	h.drive(h.press("R"))
	if got := h.areaRow(6).Primary.String(); got != "#6 Six, renamed" {
		t.Fatalf("the list shown: %q", got)
	}
	a.leaveTickets()
	h.drive(a.requestTitles())
	if got := h.areaRow(6).Primary.String(); got != "#6 Six, renamed" {
		t.Fatalf("after the list: %q", got)
	}
}

// The full text of the description is a read of the ticket too: its
// accepted answer renews the title.
func TestTheFullTextRenewsTheTitle(t *testing.T) {
	h := newHarness(t)
	h.withWatcher(agentIn(1, orgA, 6, agents.Working, h.now))
	h.start()
	h.drive(h.press("A"))
	h.drive(h.press("enter"))
	if got := h.areaRow(6).Primary.String(); got != "#6 Ticket 6" {
		t.Fatalf("%q", got)
	}
	h.f.setTicket(renamedSix(h))
	if !h.offers(Expand) {
		t.Fatal("e is not offered on #6")
	}
	h.drive(h.press("e"))
	if h.m.app.detail.expansion == nil {
		t.Fatal("nothing expanded")
	}
	if got := h.areaRow(6).Primary.String(); got != "#6 Six, renamed" {
		t.Fatalf("after the full text: %q", got)
	}
}

// Review of the C9c groundwork: an agent appears after the session
// accepted a newer read of its ticket (the pane, renamed) while an older
// list read was still out. The entry the older list answer makes starts
// from what the views hold, with the flight that brought it, so the older
// answer is outranked and the pane's newer title stays.
func TestAnOlderAnswerCannotSeedANewAgentsTitle(t *testing.T) {
	h := newHarness(t)
	h.withWatcher()
	h.openTickets(website)
	h.m.focus = view.List
	older := messages(t, h.press("R")) // the list, still saying "Ticket 6"
	if len(older) == 0 {
		t.Fatal("no list read")
	}
	h.f.setTicket(renamedSix(h))
	h.m.app.tickets.selected = ticketID(website, 6)
	h.drive(h.press("enter"))
	if d := h.m.app.detail; d == nil || d.record.Title != "Six, renamed" {
		t.Fatalf("the pane: %+v", d)
	}
	a := h.m.app
	a.agents.list = []agents.Agent{agentIn(1, orgA, 6, agents.Working, h.now)}
	h.drive(a.requestTitles())
	if got := h.areaRow(6).Primary.String(); got != "#6 Six, renamed" {
		t.Fatalf("before the older list: %q", got)
	}
	h.drive(h.feed(older...)...)
	if got := h.areaRow(6).Primary.String(); got != "#6 Six, renamed" {
		t.Fatalf("after the older list: %q", got)
	}
}

// Review round 2 of the C9c groundwork, finding 1: an agent appears while
// the pane holds an older read of its ticket than the list shown. The
// views keep the flight of their data, so the newer of the two decides,
// not the order pane before list.
func TestTheNewerViewDecidesForANewAgent(t *testing.T) {
	h := newHarness(t)
	h.withWatcher()
	h.openSix() // the pane: "Ticket 6"
	h.f.setTickets(func(q api.TicketQuery) transport.Answer {
		rows := h.d.ticketRows(website, 30)
		for i := range rows {
			if rows[i].Number == 6 {
				rows[i].Title = "Six, renamed"
			}
		}
		return ticketPageAnswer(1, 25, len(rows), rows[:25])
	})
	h.m.focus = view.List
	h.drive(h.press("R"))
	if d := h.m.app.detail; d == nil || d.record.Title != "Ticket 6" {
		t.Fatalf("the pane was read again: not the case: %+v", d)
	}
	a := h.m.app
	a.agents.list = []agents.Agent{agentIn(1, orgA, 6, agents.Working, h.now)}
	h.drive(a.requestTitles())
	if got := h.areaRow(6).Primary.String(); got != "#6 Six, renamed" {
		t.Fatalf("the pane's older title won: %q", got)
	}
}

// Review round 2 of the C9c groundwork, finding 2: a page of comments
// that comes back empty moves the pane to page 1 without a read. The
// pane's title keeps the flight of the answer that brought it, not the
// older one filed for page 1, so an older list answer cannot outrank it.
func TestACorrectedPageKeepsItsTitlesFlight(t *testing.T) {
	h := newHarness(t)
	h.withWatcher()
	h.openSix()
	h.press("tab")
	h.drive(h.press("]"))
	if k := h.m.app.detail.shown; k.PostsPage != 2 {
		t.Fatalf("not on page 2: %+v", k)
	}
	h.m.focus = view.List
	older := messages(t, h.press("R")) // the list, "Ticket 6", held
	if len(older) == 0 {
		t.Fatal("no list read")
	}
	h.f.setTicket(func(project, id string, o api.TicketOptions) transport.Answer {
		r := h.d.ticketRecord(project, 6, o)
		r.Title = "Six, renamed"
		r.Posts, r.PostsTotal, r.PostsPages, r.PostsTruncated = []api.Post{}, 0, 0, false
		return ticketAnswer(r)
	})
	h.m.focus = view.Object
	h.drive(h.press("R"))
	d := h.m.app.detail
	if d.record.Title != "Six, renamed" || d.shown.PostsPage != 1 {
		t.Fatalf("not corrected to page 1 with the new title: %q, %+v", d.record.Title, d.shown)
	}
	a := h.m.app
	a.agents.list = []agents.Agent{agentIn(1, orgA, 6, agents.Working, h.now)}
	h.drive(a.requestTitles())
	h.drive(h.feed(older...)...)
	if got := h.areaRow(6).Primary.String(); got != "#6 Six, renamed" {
		t.Fatalf("the older list outranked the corrected pane: %q", got)
	}
}

// Review round 3 of the C9c groundwork: a title taken from the cache (no
// flight) does not outrank a newer read a view shows again from the
// cache: the list, renamed, reopened from memory while its reload fails.
func TestACachedTitleLosesToANewerViewFromTheCache(t *testing.T) {
	h := newHarness(t)
	h.withWatcher()
	h.openSix() // the pane and the cache: "Ticket 6"
	h.f.setTickets(func(q api.TicketQuery) transport.Answer {
		rows := h.d.ticketRows(website, 30)
		for i := range rows {
			if rows[i].Number == 6 {
				rows[i].Title = "Six, renamed"
			}
		}
		return ticketPageAnswer(1, 25, len(rows), rows[:25])
	})
	h.m.focus = view.List
	h.drive(h.press("R"))
	a := h.m.app
	a.leaveTickets()
	a.agents.list = []agents.Agent{agentIn(1, orgA, 6, agents.Working, h.now)}
	h.drive(a.requestTitles())
	if tt := a.titles[ticketID(website, 6)]; tt == nil || tt.fact != titled || tt.flight != 0 {
		t.Fatalf("the title did not come from the cache: not the case: %+v", tt)
	}
	h.f.setTickets(func(api.TicketQuery) transport.Answer {
		return transport.Answer{Class: transport.Temporary, Message: "down", Status: 503, Sent: true}
	})
	h.drive(h.chooseTickets(orgA))
	h.drive(h.openProject(website))
	if v := a.tickets; v == nil || v.index(ticketID(website, 6)) < 0 || v.flight == 0 {
		t.Fatalf("the list is not shown from memory: %+v", v)
	}
	if got := h.areaRow(6).Primary.String(); got != "#6 Six, renamed" {
		t.Fatalf("the cache's title outranked the list's newer read: %q", got)
	}
}
