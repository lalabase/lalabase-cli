package ui

import (
	"testing"

	"github.com/gboyke/lalabase-cli/internal/agents"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

// Review of #36 on 1c9fd9f: three findings, each with its failing case.

func listGone(q api.TicketQuery) transport.Answer {
	return transport.Answer{Class: transport.NotFound, Message: "Project not found or not accessible.", Status: 200, Sent: true}
}

// A project's removal outweighs a title read begun before it: its late
// answer brings no title back.
func TestAProjectRemovalOutweighsATitleReadBeforeIt(t *testing.T) {
	h := newHarness(t)
	h.withWatcher()
	h.start()
	a := h.m.app
	ag := agentIn(1, orgA, 6, agents.Working, h.now)
	a.agents.list = []agents.Agent{ag}
	held := a.requestTitles() // begun before the removal, answered after
	if held == nil {
		t.Fatal("no title read")
	}
	h.drive(h.chooseTickets(orgA))
	h.drive(h.openProject(website))
	h.f.setTickets(listGone)
	h.drive(h.press("R"))
	if got := h.areaRow(6).Primary.String(); got != "#6" {
		t.Fatalf("after the removal: %q", got)
	}
	h.drive(held)
	if got := h.areaRow(6).Primary.String(); got != "#6" {
		t.Fatalf("the read from before the removal brought the title back: %q", got)
	}
}

// After a project's removal a failed retry brings no title back from
// what the cache held before it.
func TestAFailedRetryAfterARemovalTakesNothingFromBefore(t *testing.T) {
	h := newHarness(t)
	h.withWatcher(agentIn(1, orgA, 6, agents.Working, h.now))
	h.start() // the title read succeeds and is cached
	h.drive(h.chooseTickets(orgA))
	h.drive(h.openProject(website))
	h.f.setTickets(listGone)
	h.drive(h.press("R"))
	h.f.setTicket(func(project, id string, o api.TicketOptions) transport.Answer {
		return transport.Answer{Class: transport.Temporary, Message: "down", Status: 503, Sent: true}
	})
	h.drive(h.press("A"))
	h.drive(h.press("R"))
	if got := h.areaRow(6).Primary.String(); got != "#6" {
		t.Fatalf("a failed retry restored the title: %q", got)
	}
}

// A title whose read failed before the disk cache opened is still looked
// up on the disk once it is open.
func TestAFailedTitleIsStillLookedUpOnTheDisk(t *testing.T) {
	h := newDiskHarness(t, t.TempDir(), nil)
	h.withWatcher()
	h.start()
	a := h.m.app
	ag := agentIn(1, orgA, 6, agents.Working, h.now)
	a.agents.list = []agents.Agent{ag}
	// The read failed while the disk was not open yet.
	a.titles[ag.Ticket] = &title{failed: true}
	a.requestTitles()
	for _, r := range a.disk.reads {
		if r.purpose == forTitle && r.ticket.Key.Ticket == ag.Ticket {
			return
		}
	}
	t.Fatal("the disk was never asked for the failed title")
}

// The agents' list closing by itself (the last agent gone) resumes the
// preview of the cached ticket behind it, as esc and A do.
func TestTheAgentsClosingByThemselvesResumeThePreview(t *testing.T) {
	h := newHarness(t)
	f := h.withWatcher(agentIn(1, orgA, 6, agents.Working, h.now))
	h.openSix()
	h.m.app.tickets.opened = ""
	d := h.m.app.detail
	d.cached, d.loading = true, false
	h.m.width = 60
	h.m.focus = view.Object
	h.drive(h.press("A"))
	before := h.m.app.previewToken
	f.set()
	h.m.app.agents.until = h.now
	h.due()
	if h.m.focus != view.Object || h.m.app.previewToken == before {
		t.Fatalf("focus %d, token %d → %d", h.m.focus, before, h.m.app.previewToken)
	}
}
