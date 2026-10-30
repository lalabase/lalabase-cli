package ui

// Reviews of #36 after the merge: the reviewer's four repro tests (on 726e7ee),
// the fresh title review's project removal and its test gaps.

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/agents"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/cache"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

func TestAnOlderListCannotUndoARemoval(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.withWatcher(agentIn(1, orgA, 6, agents.Working, h.now))
	h.openSix()
	h.m.focus = view.List
	older := messages(t, h.press("R"))
	if len(older) == 0 {
		t.Fatal("no list answer")
	}
	h.m.focus = view.Object
	h.f.setTicket(notFound)
	h.drive(h.press("R"))
	if got := h.areaRow(6).Primary.String(); got != "#6" {
		t.Fatalf("not removed: %q", got)
	}
	h.drive(h.feed(older...)...)
	if got := h.areaRow(6).Primary.String(); got != "#6" {
		t.Fatalf("older list undid removal: %q", got)
	}
}

func TestANewTitleAfterARemovalBeatsAnOldListRow(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.withWatcher(agentIn(1, orgA, 6, agents.Working, h.now))
	h.openSix()
	h.f.setTicket(notFound)
	h.drive(h.press("R"))
	if got := h.areaRow(6).Primary.String(); got != "#6" {
		t.Fatalf("removal failed: %q", got)
	}
	h.f.setTicket(func(project, id string, o api.TicketOptions) transport.Answer {
		r := h.d.ticketRecord(project, 6, o)
		r.Title = "Ticket six, renamed"
		return ticketAnswer(r)
	})
	h.drive(h.press("A"))
	h.drive(h.press("R"))
	if got := h.areaRow(6).Primary.String(); got != "#6 Ticket six, renamed" {
		t.Fatalf("new successful title hidden by old list row: %q; title=%+v", got, h.m.app.titles[ticketID(website, 6)])
	}
}
func TestTheNoticeFitsBesideTheAgents(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.withWatcher(agentIn(1, orgA, 6, agents.Working, h.now))
	h.start()
	h.m.width, h.m.height = 32, 24
	h.drive(h.press("A"))
	if h.m.focus != view.Agents {
		t.Fatal("agents not focused")
	}
	h.m.app.pause = h.now.Add(time.Minute)
	status, _ := h.m.status()
	got := ansi.Strip(h.m.View().Content)
	if !strings.Contains(got, status.String()) {
		t.Fatalf("status %q cut despite room; geometry %+v\n%s", status.String(), h.m.geometry(), got)
	}
}

func TestTheSameAgentFromTheListKeepsTheScroll(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.withWatcher(agentIn(1, orgA, 6, agents.Working, h.now))
	h.openSix()
	h.m.width, h.m.height = 60, 12
	h.drive(h.press("pgdown"))
	before := h.m.app.detail.offset
	if before == 0 {
		t.Fatal("PgDown did not scroll")
	}
	h.drive(h.press("A"))
	if h.m.focus != view.Agents {
		t.Fatal("agents not focused")
	}
	h.drive(h.press("enter"))
	if h.m.app.detail.offset != before {
		t.Fatalf("same ticket list path lost scroll: got %d, want %d", h.m.app.detail.offset, before)
	}
	if h.m.focus != view.Object {
		t.Fatalf("focus %v, want the ticket", h.m.focus)
	}
}

// A project's removal reaches a remembered title also while the agents'
// list is briefly empty (a failed read of the agents).
func TestAProjectRemovalReachesAnAgentBrieflyAbsent(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.withWatcher(agentIn(1, orgA, 6, agents.Working, h.now))
	h.start() // the title is read and remembered
	h.drive(h.chooseTickets(orgA))
	h.drive(h.openProject(handbuch)) // a list without #6
	h.m.focus = view.List
	h.press("esc") // back to the projects
	a := h.m.app
	saved := a.agents.list
	a.agents.list = nil
	h.drive(h.openProject(website))
	if a.tickets == nil || a.tickets.project != website {
		t.Fatal("the list of Website is not open")
	}
	h.m.focus = view.List
	h.f.setTickets(listGone)
	h.drive(h.press("R"))
	a.agents.list = saved
	if got := h.areaRow(6).Primary.String(); got != "#6" {
		t.Fatalf("the removal missed the absent agent: %q", got)
	}
}

// A title the memory cache holds is taken from it: no request.
func TestATitleInTheMemoryCacheCostsNoRequest(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.withWatcher()
	h.start()
	a := h.m.app
	key := a.detailKey(website, ticketID(website, 6))
	a.store.Apply(key, cache.Replace, ticketAnswer(h.d.ticketRecord(website, 6, gateway.DetailOptions(key))).Payload, h.now)
	before := h.titleReads(6)
	a.agents.list = []agents.Agent{agentIn(1, orgA, 6, agents.Working, h.now)}
	h.drive(a.requestTitles())
	if h.titleReads(6) != before || h.areaRow(6).Primary.String() != "#6 Ticket 6" {
		t.Fatalf("%d reads, row %q", h.titleReads(6)-before, h.areaRow(6).Primary.String())
	}
}

// The disk not open yet at the first reading of the agents: the title is
// looked up there once it is.
func TestATitleIsLookedUpOnTheDiskOnceItOpens(t *testing.T) {
	t.Parallel()

	h := newDiskHarness(t, t.TempDir(), nil)
	h.withWatcher()
	h.start()
	a := h.m.app
	opened := a.disk.cache
	a.disk.cache = nil // not open yet
	ag := agentIn(1, orgA, 6, agents.Working, h.now)
	a.agents.list = []agents.Agent{ag}
	a.requestTitles()
	if tt := a.titles[ag.Ticket]; tt == nil || tt.disk {
		t.Fatalf("the lookup counted as done before the disk opened: %+v", tt)
	}
	a.disk.cache = opened
	a.requestTitles()
	for _, r := range a.disk.reads {
		if r.purpose == forTitle && r.ticket.Key.Ticket == ag.Ticket {
			return
		}
	}
	t.Fatal("the disk was never asked once it opened")
}

// A removal while the title's read is out offers no R that would read
// nothing; the read's answer settles it.
func TestARemovalDuringATitleReadOffersNoR(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.withWatcher()
	h.start()
	a := h.m.app
	ag := agentIn(1, orgA, 6, agents.Working, h.now)
	a.agents.list = []agents.Agent{ag}
	held := a.requestTitles()
	a.forgetTitle(ag.Ticket, 0, ag.Project)
	if a.titlesFailed() {
		t.Fatal("R offered while the read is out")
	}
	h.f.setTicket(notFound)
	h.drive(held)
	if !a.titlesFailed() {
		t.Fatal("no R after the read failed")
	}
}

// A failed title that a list from the cache shows (no read accepted, so
// nothing renews it) offers no R.
func TestAFailedTitleShownByACachedListOffersNoR(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.withWatcher()
	h.openTickets(website) // the list is cached now
	h.press("esc")         // back to the projects; the list is left
	if h.m.app.tickets != nil {
		t.Fatal("the list is still open")
	}
	a := h.m.app
	ag := agentIn(1, orgA, 6, agents.Working, h.now)
	a.agents.list = []agents.Agent{ag}
	a.titles[ag.Ticket] = &title{failed: true, disk: true, project: website}
	if !a.titlesFailed() {
		t.Fatal("no failed title to begin with")
	}
	h.m.app.projects.selected = website
	h.m.focus = view.List
	h.press("enter") // the cached page at once, its reload not driven
	if a.tickets == nil || a.tickets.index(ag.Ticket) < 0 {
		t.Fatal("the cached list does not show #6")
	}
	if a.titlesFailed() {
		t.Fatal("R offered for a title the list shows")
	}
}

// Review of a8b2e0c: Enter on the agent whose ticket is open in the list
// keeps the place, but still reads a ticket that is not fresh (its first
// read or a refresh failed), as opening it from the list does.
func TestTheSameAgentFromTheListRetriesAFailedRead(t *testing.T) {
	t.Parallel()

	down := func(project, id string, o api.TicketOptions) transport.Answer {
		return transport.Answer{Class: transport.Temporary, Message: "down", Status: 503, Sent: true}
	}
	for _, c := range []struct {
		name  string
		setup func(h *harness)
	}{
		{"a refresh failed", func(h *harness) {
			h.openSix()
			h.m.width, h.m.height = 60, 12
			h.drive(h.press("pgdown"))
			h.f.setTicket(down)
			h.drive(h.press("R"))
		}},
		{"the first read failed", func(h *harness) {
			h.openTickets(website)
			h.f.setTicket(down)
			h.m.app.tickets.selected = ticketID(website, 6)
			h.m.focus = view.List
			h.drive(h.press("enter"))
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			h.withWatcher(agentIn(1, orgA, 6, agents.Working, h.now))
			c.setup(h)
			d := h.m.app.detail
			if d == nil || d.holds() {
				t.Fatalf("not the case: the pane holds a fresh ticket: %+v", d)
			}
			offset := d.offset
			h.f.setTicket(h.d.defaultTicket)
			reads := len(h.f.detailReads())
			h.drive(h.press("A"))
			h.drive(h.press("enter"))
			d = h.m.app.detail
			if len(h.f.detailReads()) == reads || d == nil || !d.holds() {
				t.Fatalf("no read of the ticket that is not fresh: %d reads, %+v", len(h.f.detailReads())-reads, d)
			}
			if d.offset != offset {
				t.Fatalf("the place is lost: %d, was %d", d.offset, offset)
			}
		})
	}
}

// Review of #37 on e37282a: mutants of this PR's code that no test killed.

// A ticket only previewed in the list (the pane shows it, the list has not
// opened it): Enter on its agent opens it, as Enter on its row does.
func TestAnAgentOpensItsPreviewedTicket(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.withWatcher(agentIn(1, orgA, 6, agents.Working, h.now))
	h.openTickets(website)
	v := h.m.app.tickets
	v.selected = ticketID(website, 6)
	h.m.focus = view.List
	h.m.app.ensureDetail(website, ticketID(website, 6))
	if v.opened != "" || h.m.app.detail == nil {
		t.Fatal("not a preview")
	}
	h.drive(h.press("A"))
	h.drive(h.press("enter"))
	if v.opened != ticketID(website, 6) {
		t.Fatalf("not opened: %q", v.opened)
	}
}

// A removal of the ticket sent before a list read that still shows it,
// answered after the list: the older removal does not take the title.
func TestAnOlderRemovalCannotUndoANewerList(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.withWatcher(agentIn(1, orgA, 6, agents.Working, h.now))
	h.openSix()
	if h.m.app.titles[ticketID(website, 6)] == nil {
		t.Fatal("no title remembered")
	}
	h.f.setTicket(notFound)
	stale := messages(t, h.press("R")) // the ticket's read, sent first
	if len(stale) == 0 {
		t.Fatal("no ticket read")
	}
	h.f.setTicket(h.d.defaultTicket)
	h.m.focus = view.List
	h.drive(h.press("R")) // the list, sent after, answered first
	if got := h.areaRow(6).Primary.String(); got != "#6 Ticket 6" {
		t.Fatalf("before the late removal: %q", got)
	}
	h.drive(h.feed(stale...)...)
	if got := h.areaRow(6).Primary.String(); got != "#6 Ticket 6" {
		t.Fatalf("an older removal won over a newer list: %q", got)
	}
}

// A title read sent after a list read and answered after it wins: the list
// answers first with the old name, the title read with the new one.
func TestANewerTitleReadWinsOverAnOlderList(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.withWatcher()
	h.start()
	a := h.m.app
	a.agents.list = []agents.Agent{agentIn(1, orgA, 6, agents.Working, h.now)}
	held := a.requestTitles() // its flight is numbered when it runs, below
	if held == nil {
		t.Fatal("no title read")
	}
	h.drive(h.chooseTickets(orgA))
	h.drive(h.openProject(website))
	if got := h.areaRow(6).Primary.String(); got != "#6 Ticket 6" {
		t.Fatalf("the list's title: %q", got)
	}
	h.f.setTicket(func(project, id string, o api.TicketOptions) transport.Answer {
		r := h.d.ticketRecord(project, 6, o)
		r.Title = "Ticket six, renamed"
		return ticketAnswer(r)
	})
	h.drive(held) // sent after the list
	if got := h.areaRow(6).Primary.String(); got != "#6 Ticket six, renamed" {
		t.Fatalf("a newer title read lost to an older list: %q", got)
	}
}

// The notice is measured with the agents' title alone: whatever their
// column holds, it takes as many lines as on the screen drawn with it.
func TestTheNoticeIsMeasuredWithTheAgentsTitle(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.withWatcher(agentIn(1, orgA, 6, agents.WaitingForApproval, h.now), agentIn(2, orgB, 7, agents.Working, h.now))
	h.start()
	h.m.app.pause = h.now.Add(time.Minute)
	measured := 0
	for _, focus := range []view.Pane{view.Navigation, view.List, view.Object, view.Agents} {
		for width := 24; width <= 140; width += 4 {
			h.m.focus, h.m.width, h.m.height = focus, width, 24
			status, tone := h.m.status()
			if status.String() == "" {
				t.Fatal("no notice")
			}
			g := h.m.geometry()
			g.NoticeHeight, g.BodyHeight = 0, g.BodyHeight+g.NoticeHeight
			full := present.Screen{Context: h.m.context(), Crumbs: h.m.crumbs(), Chooser: h.m.chooserColumn(), Agents: h.m.agentsColumn(), Status: status, StatusStyle: tone}
			want := view.NoticeLines(full, g, focus, h.m.widthMethod)
			if got := h.m.withNotice(g).NoticeHeight; want > 0 && got == 0 {
				t.Fatalf("focus %d, width %d: the screen needs %d notice lines, the measure gives none", focus, width, want)
			}
			measured += want
		}
	}
	if measured == 0 {
		t.Fatal("no geometry needed a notice line: nothing was measured")
	}
}
