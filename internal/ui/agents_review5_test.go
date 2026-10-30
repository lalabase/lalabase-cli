package ui

import (
	"testing"

	"github.com/gboyke/lalabase-cli/internal/agents"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

// Review of #36 on bdded98: three findings, each with its failing case.

// A ticket the server removes (not found, no access) takes its
// remembered title with it: the row says #n again, and R reads it.
func TestARemovedTicketForgetsItsTitle(t *testing.T) {
	h := newHarness(t)
	h.withWatcher(agentIn(1, orgA, 6, agents.Working, h.now))
	h.start()
	h.drive(h.press("A"))
	h.drive(h.press("enter"))
	if got := h.areaRow(6).Primary.String(); got != "#6 Ticket 6" {
		t.Fatalf("%q", got)
	}
	h.press("right") // into the ticket beside the agents' list
	h.f.setTicket(func(project, id string, o api.TicketOptions) transport.Answer {
		return transport.Answer{Class: transport.NotFound, Message: "No ticket found.", Status: 200, Sent: true}
	})
	h.drive(h.press("R"))
	if got := h.areaRow(6).Primary.String(); got != "#6" {
		t.Fatalf("after the removal: %q", got)
	}
	if !h.m.app.titlesFailed() {
		t.Fatal("the removed title counts as read")
	}
}

// A title from an accepted list read wins over an older title read still
// out: its late answer does not bring the old name back. The title read is
// sent first (its answer held), the list after it, so the flights are the
// real ones.
func TestANewerTitleWinsOverAReadStillOut(t *testing.T) {
	h := newHarness(t)
	h.withWatcher()
	h.start()
	a := h.m.app
	ag := agentIn(1, orgA, 6, agents.Working, h.now)
	a.agents.list = []agents.Agent{ag}
	h.f.setTicket(func(project, id string, o api.TicketOptions) transport.Answer {
		r := h.d.ticketRecord(project, 6, o)
		r.Title = "Old name"
		return ticketAnswer(r)
	})
	late := messages(t, a.requestTitles()) // sent now, answered below
	if len(late) == 0 || a.titles[ag.Ticket] == nil || !a.titles[ag.Ticket].asked {
		t.Fatal("no title read out")
	}
	h.f.setTicket(h.d.defaultTicket)
	h.drive(h.chooseTickets(orgA))
	h.drive(h.openProject(website)) // sent after the title read
	h.drive(h.feed(late...)...)
	if got := h.areaRow(6).Primary.String(); got != "#6 Ticket 6" {
		t.Fatalf("an older title read won: %q", got)
	}
}

// At 120x9 with a notice in the chooser, the Agents area leaves the
// chooser a row: its entries stay in sight, and the keys measure the
// chooser as drawn.
func TestTheAreaLeavesTheChooserARow(t *testing.T) {
	h := newHarness(t)
	h.withWatcher(agentIn(1, orgA, 6, agents.Working, h.now))
	h.start()
	h.m.app.report.Enabled["inbox"] = false // "Inbox is not supported."
	split := 0
	for height := 9; height <= 16; height++ {
		h.m.width, h.m.height, h.m.focus = 120, height, view.Navigation
		c, ag := h.m.chooserColumn(), h.m.agentsColumn()
		if c.Notice.String() == "" {
			t.Fatal("no notice in the chooser")
		}
		g := h.m.geometry()
		got := view.ChooserCapacity(c, ag, g.BodyHeight)
		if got < 1 {
			t.Fatalf("%d rows: chooser capacity %d in a body of %d", height, got, g.BodyHeight)
		}
		if mine := h.m.focusedCapacity(g); mine != got {
			t.Fatalf("%d rows: the keys measure %d, the screen draws %d", height, mine, got)
		}
		if view.AgentsHeight(c, ag, g.BodyHeight, false) > 0 {
			split++
		}
	}
	if split == 0 {
		t.Fatal("no height showed the area: the split was never measured")
	}
}
