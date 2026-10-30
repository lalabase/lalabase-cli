package ui

import (
	"testing"

	"github.com/gboyke/lalabase-cli/internal/agents"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

// Review of #36 after round 3: four findings, each with its failing case.

// P2-1: Enter again on the agent whose ticket is shown keeps the comment
// selected; a ticket read again from nothing would settle it, a ticket
// kept must not lose it.
func TestEnterAgainKeepsTheSelectedComment(t *testing.T) {
	h := newHarness(t)
	h.withWatcher(agentIn(1, orgA, 7, agents.Working, h.now))
	h.start()
	h.drive(h.press("A"))
	h.drive(h.press("enter"))
	d := h.m.app.detail
	if d == nil || !d.known || len(d.record.Posts) == 0 || d.post == "" {
		t.Fatalf("no comment selected: %+v", d)
	}
	selected := d.post
	// Into the ticket and back, then Enter on the same agent again.
	h.press("right")
	h.press("esc")
	h.drive(h.press("enter"))
	if d := h.m.app.detail; d == nil || d.post != selected {
		t.Fatalf("selection %q, want %q", h.m.app.detail.post, selected)
	}
}

// P2-2: closing the agents in one column resumes the preview of the
// cached ticket behind them (its timer fired while they were open), with
// the focus it returns to (esc and a second A).
func TestClosingTheAgentsResumesThePreview(t *testing.T) {
	for _, key := range []string{"esc", "A"} {
		h := newHarness(t)
		h.withWatcher(agentIn(1, orgA, 6, agents.Working, h.now))
		h.openSix()
		// The pane shows #6 as a cached preview awaiting its one reload.
		h.m.app.tickets.opened = ""
		d := h.m.app.detail
		d.cached, d.loading = true, false
		h.m.width = 60
		h.m.focus = view.Object
		h.drive(h.press("A"))
		if h.m.focus != view.Agents {
			t.Fatalf("%s: focus %d", key, h.m.focus)
		}
		before := h.m.app.previewToken
		cmd := h.press(key)
		if h.m.focus != view.Object || cmd == nil || h.m.app.previewToken == before {
			t.Fatalf("%s: focus %d, no preview scheduled (token %d → %d)", key, h.m.focus, before, h.m.app.previewToken)
		}
	}
}

// P2-3: a title read earlier gives way to the newer one of an accepted
// ticket read: the agent's row keeps the renamed title after the ticket
// leaves the pane.
func TestANewerTicketReadRenamesTheAgent(t *testing.T) {
	h := newHarness(t)
	h.withWatcher(agentIn(1, orgA, 6, agents.Working, h.now))
	h.start()
	if got := h.areaRow(6).Primary.String(); got != "#6 Ticket 6" {
		t.Fatalf("%q", got)
	}
	h.f.setTicket(func(project, id string, o api.TicketOptions) transport.Answer {
		r := h.d.ticketRecord(project, 6, o)
		r.Title = "Ticket six, renamed"
		return ticketAnswer(r)
	})
	h.drive(h.press("A"))
	h.drive(h.press("enter"))
	if got := h.areaRow(6).Primary.String(); got != "#6 Ticket six, renamed" {
		t.Fatalf("while shown: %q", got)
	}
	// The ticket leaves the pane: the projects, another project's list.
	h.press("esc")
	h.drive(h.openProject(handbuch))
	if h.m.app.detail != nil && h.m.app.detail.id == ticketID(website, 6) {
		t.Fatal("the ticket is still shown")
	}
	if got := h.areaRow(6).Primary.String(); got != "#6 Ticket six, renamed" {
		t.Fatalf("after: %q", got)
	}
}

// P2-4: A is offered only where the agents can be shown; and where a
// resize leaves them no room, esc still goes back.
func TestAKeepsAWayBackWhereTheAreaCannotFit(t *testing.T) {
	h := newHarness(t)
	h.withWatcher(agentIn(1, orgA, 6, agents.Working, h.now))
	h.start()
	h.m.width, h.m.height, h.m.focus = 120, 8, view.Navigation
	if h.m.small() {
		t.Fatal("the chooser itself is too small here")
	}
	if h.offers(OpenAgents) {
		h.drive(h.press("A"))
		if h.m.small() {
			t.Fatal("A led into a screen too small for anything but quitting")
		}
	}
	// A resize to that height with the agents open: esc goes back.
	h.m.height = 24
	h.drive(h.press("A"))
	if h.m.focus != view.Agents {
		t.Fatalf("focus %d", h.m.focus)
	}
	h.m.height = 8
	if !h.offers(Back) {
		t.Fatal("no way back from the agents")
	}
	h.press("esc")
	if h.m.focus != view.Navigation || h.m.small() {
		t.Fatalf("focus %d, small %v", h.m.focus, h.m.small())
	}
}
