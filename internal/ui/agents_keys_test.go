package ui

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/agents"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/queue"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

// C9b: A opens the agents, in every layout; Enter on an agent opens its
// ticket, in its organisation.

func (h *harness) helpText() string {
	var parts []string
	for _, b := range h.m.help() {
		parts = append(parts, b.Key.String()+" "+b.Help.String())
	}
	return strings.Join(parts, " | ")
}

func TestAIsOfferedOnlyWithAnAgent(t *testing.T) {
	h := newHarness(t)
	f := h.withWatcher()
	h.start()
	if strings.Contains(h.helpText(), "A agents") || h.offers(OpenAgents) {
		t.Fatalf("A without an agent: %s", h.helpText())
	}
	h.press("A")
	if h.m.focus == view.Agents {
		t.Fatal("A opened the agents without one")
	}
	f.set(agentIn(1, orgA, 6, agents.Working, h.now))
	h.m.app.agents.until = h.now
	h.due()
	for _, focus := range []view.Pane{view.Navigation, view.List} {
		h.m.focus = focus
		if !strings.Contains(h.helpText(), "A agents") {
			t.Fatalf("focus %d: %s", focus, h.helpText())
		}
	}
}

// Three columns: the focus moves into the area; esc and a second A go
// back to where it was. Two: the agents in the list's place. One: a
// screen of their own.
func TestAInEveryLayout(t *testing.T) {
	for _, c := range []struct {
		width   int
		columns int
		visible []view.Pane
	}{
		{120, 3, []view.Pane{view.Navigation, view.List, view.Object}},
		{100, 2, []view.Pane{view.Agents, view.Object}},
		{60, 1, []view.Pane{view.Agents}},
	} {
		for _, back := range []string{"esc", "A"} {
			h := newHarness(t)
			h.withWatcher(agentIn(1, orgA, 6, agents.Working, h.now), agentIn(2, orgB, 7, agents.Ended, h.now))
			h.start()
			h.m.width = c.width
			h.drive(h.choose(orgA))
			before := h.m.focus
			h.drive(h.press("A"))
			g := h.m.geometry()
			if h.m.focus != view.Agents || g.Columns != c.columns || !slices.Equal(view.Visible(g, h.m.focus), c.visible) {
				t.Fatalf("%d: focus %d, %d columns, visible %v", c.width, h.m.focus, g.Columns, view.Visible(g, h.m.focus))
			}
			if got := h.m.screen().Agents.Cursor; got != 0 {
				t.Fatalf("%d: cursor %d", c.width, got)
			}
			help := h.helpText()
			for _, want := range []string{"enter open", "esc back", "up/down select"} {
				if !strings.Contains(help, want) {
					t.Fatalf("%d: %q not in %s", c.width, want, help)
				}
			}
			if strings.Contains(help, "left/right") || strings.Contains(help, "A agents") {
				t.Fatalf("%d: %s", c.width, help)
			}
			h.press("down")
			if got := h.m.screen().Agents.Cursor; got != 1 {
				t.Fatalf("%d: cursor %d after down", c.width, got)
			}
			h.drive(h.press(back))
			if h.m.focus != before {
				t.Fatalf("%d, %s: focus %d, want %d", c.width, back, h.m.focus, before)
			}
		}
	}
}

// The agents gone while their list is open: the list closes.
func TestTheLastAgentGoneClosesTheAgents(t *testing.T) {
	h := newHarness(t)
	f := h.withWatcher(agentIn(1, orgA, 6, agents.Working, h.now))
	h.start()
	h.m.focus = view.List
	h.press("A")
	f.set()
	h.due()
	if h.m.focus != view.List {
		t.Fatalf("focus %d", h.m.focus)
	}
}

// Enter on an agent of another organisation: its organisation's tickets,
// the ticket in the pane, read as the person's; an ended agent is seen
// after it, a working one is not.
func TestEnterOpensTheAgentsTicketInItsOrganisation(t *testing.T) {
	h := newHarness(t)
	ended := agentIn(1, orgA, 6, agents.Ended, h.now)
	working := agentIn(2, orgA, 7, agents.Working, h.now)
	f := h.withWatcher(working, ended)
	h.withLauncher()
	h.start()
	h.drive(h.choose(orgB))
	h.drive(h.press("A"))
	// The working agent is the first: more urgent.
	h.drive(h.press("enter"))
	a := h.m.app
	if a.projects == nil || a.projects.org != orgA || a.active != nil || a.highlight != orgA || a.highlightSection != sectionTickets {
		t.Fatalf("organisation not switched: projects %+v, inbox %v, highlight %s", a.projects, a.active != nil, a.highlight)
	}
	if a.detail == nil || a.detail.id != working.Ticket || !a.detail.known || h.m.focus != view.Object {
		t.Fatalf("ticket not open: %+v, focus %d", a.detail, h.m.focus)
	}
	if got := h.m.context().String(); got != "Nordlicht GmbH › Tickets" {
		t.Fatalf("path %q", got)
	}
	if len(f.seen()) != 0 {
		t.Fatalf("a working agent was marked: %v", f.seen())
	}
	// The ticket's own keys act on it: x knows its organisation.
	if tk, ok := h.m.launchTarget(); !ok || tk.Org != orgA {
		t.Fatalf("x target %+v %v", tk, ok)
	}
	// Back: the projects, the ticket stays in the pane.
	h.press("esc")
	if h.m.focus != view.List || a.detail == nil {
		t.Fatalf("esc: focus %d", h.m.focus)
	}
	// The ended one.
	h.drive(h.press("A"))
	h.press("down")
	if h.areaRow(6).Quiet {
		t.Fatal("faint before it was opened")
	}
	h.drive(h.press("enter"))
	if a.detail == nil || a.detail.id != ended.Ticket {
		t.Fatalf("%+v", a.detail)
	}
	if got := f.seen(); len(got) != 1 || got[0] != ended.ID {
		t.Fatalf("marks %v", got)
	}
	if !h.areaRow(6).Quiet {
		t.Fatal("not faint after it was opened")
	}
}

// Enter is not offered where the ticket cannot open: an organisation the
// tickets cannot open in, before the tools are known.
func TestEnterOnlyWhereTheTicketOpens(t *testing.T) {
	h := newHarness(t)
	h.withWatcher(agentIn(1, orgA, 6, agents.Working, h.now))
	h.start()
	h.drive(h.press("A"))
	h.m.app.report.Enabled["ticket"] = false
	if h.offers(OpenList) {
		t.Fatal("enter offered without the ticket tool")
	}
}

// "Seen" survives a restart: a new model over the same directory draws
// the ended agent faint.
func TestSeenSurvivesARestart(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a")
	if real, err := filepath.EvalSymlinks(filepath.Dir(dir)); err == nil {
		dir = filepath.Join(real, "a")
	}
	if err := agents.Prepare(dir); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t)
	ag := agentIn(1, orgA, 6, agents.Ended, h.now)
	if err := agents.WriteLaunch(dir, agents.Launch{Agent: ag.ID, Organisation: orgA, Project: website, Ticket: ag.Ticket, Number: 6,
		Session: "7c9e6679-7425-40de-944b-e07fc1f90ae7", At: h.now}); err != nil {
		t.Fatal(err)
	}
	if err := agents.Hook(agents.HookCall{Dir: dir, Event: agents.SessionEnd, Agent: ag.ID, Now: h.now}); err != nil {
		t.Fatal(err)
	}
	h.m.app.deps.Agents = agents.NewWatcher(h.live, dir)
	h.start()
	if r := h.areaRow(6); r.Quiet || r.Mark.String() != "✓" {
		t.Fatalf("%+v", r)
	}
	h.drive(h.press("A"))
	h.drive(h.press("enter"))
	again := newHarness(t)
	again.m.app.deps.Agents = agents.NewWatcher(again.live, dir)
	again.start()
	if r := again.areaRow(6); !r.Quiet {
		t.Fatalf("not faint after a restart: %+v", r)
	}
}

// R in the agents' list reads the failed titles again, and nothing of
// the inbox behind it.
func TestRInTheAgentsReadsTheTitles(t *testing.T) {
	h := newHarness(t)
	h.f.setTicket(func(project, id string, o api.TicketOptions) transport.Answer {
		return transport.Answer{Class: transport.Temporary, Message: "down", Status: 503, Sent: true}
	})
	h.withWatcher(agentIn(1, orgA, 6, agents.Working, h.now))
	h.start()
	h.drive(h.choose(orgA))
	h.drive(h.press("A"))
	if !h.offers(Refresh) {
		t.Fatal("R not offered with a failed title")
	}
	inbox := len(h.f.inboxCalls())
	reads := h.titleReads(6)
	h.f.setTicket(h.d.defaultTicket)
	h.drive(h.press("R"))
	if h.titleReads(6) <= reads || len(h.f.inboxCalls()) != inbox {
		t.Fatalf("title reads %d to %d, inbox reads %d to %d", reads, h.titleReads(6), inbox, len(h.f.inboxCalls()))
	}
	if h.offers(Refresh) {
		t.Fatal("R offered with every title known")
	}
}

// Review round 1 of #36, F3: a lost agent is seen after enter too.
func TestEnterSeesALostAgent(t *testing.T) {
	h := newHarness(t)
	lost := agentIn(1, orgA, 6, agents.Lost, h.now)
	f := h.withWatcher(lost)
	h.start()
	h.drive(h.press("A"))
	h.drive(h.press("enter"))
	if got := f.seen(); len(got) != 1 || got[0] != lost.ID {
		t.Fatalf("marks %v", got)
	}
	// Seen, but not faint: only an ended agent is.
	if h.areaRow(6).Quiet {
		t.Fatal("a seen lost agent is faint")
	}
}

// Review round 1 of #36, F4: R in the chooser reads the failed titles
// also while a step of the start failed.
func TestRWithAFailedStepReadsTheTitlesToo(t *testing.T) {
	h := newHarness(t)
	h.f.setTicket(func(project, id string, o api.TicketOptions) transport.Answer {
		return transport.Answer{Class: transport.Temporary, Message: "down", Status: 503, Sent: true}
	})
	h.f.set(func(q api.NotificationQuery) transport.Answer {
		return transport.Answer{Class: transport.Temporary, Message: "down", Status: 503, Sent: true}
	})
	h.withWatcher(agentIn(1, orgA, 6, agents.Working, h.now))
	h.start()
	if !h.m.app.anyFailed() || !h.m.app.titlesFailed() {
		t.Fatalf("failed step %v, failed title %v", h.m.app.anyFailed(), h.m.app.titlesFailed())
	}
	reads := h.titleReads(6)
	h.f.setTicket(h.d.defaultTicket)
	h.m.focus = view.Navigation
	h.drive(h.press("R"))
	if h.titleReads(6) <= reads || h.areaRow(6).Primary.String() != "#6 Ticket 6" {
		t.Fatalf("%d reads, row %q", h.titleReads(6), h.areaRow(6).Primary.String())
	}
}

// Review round 1 of #36, F6: the agents' list keeps the focus when the
// organisation behind it is removed, and closes to a pane that still
// shows something.
func TestTheAgentsKeepTheFocusThroughARemoval(t *testing.T) {
	h := newHarness(t)
	h.withWatcher(agentIn(1, orgA, 6, agents.Working, h.now))
	h.openSix()
	h.drive(h.press("A"))
	h.f.setProjects(func(org string) transport.Answer {
		return transport.Answer{Class: transport.NotFound, Message: "gone", Status: 404, Sent: true}
	})
	h.m.focus = view.Agents
	h.m.agentsFrom = view.Object
	h.m.app.projects.loading = true
	h.drive(h.m.app.issue(gateway.Projects, h.m.app.projects.key, forProjects, queue.User))
	if h.m.focus != view.Agents {
		t.Fatalf("a removal took the focus: %d", h.m.focus)
	}
	if h.m.app.detail != nil {
		t.Fatal("the ticket stayed")
	}
	h.press("esc")
	if h.m.focus != view.List {
		t.Fatalf("back to a pane that shows nothing: %d", h.m.focus)
	}
}

// Review round 1 of #36, F7: Enter again on the agent whose ticket is
// shown reads nothing.
func TestEnterAgainReadsNothing(t *testing.T) {
	h := newHarness(t)
	h.withWatcher(agentIn(1, orgA, 6, agents.Working, h.now))
	h.start()
	h.drive(h.press("A"))
	h.drive(h.press("enter"))
	reads := h.titleReads(6)
	h.drive(h.press("A"))
	h.drive(h.press("enter"))
	if h.titleReads(6) != reads {
		t.Fatalf("%d reads, %d before", h.titleReads(6), reads)
	}
}
