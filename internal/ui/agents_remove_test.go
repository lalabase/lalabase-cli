package ui

// C9c: d removes an ended or lost agent, in the agents' list and in the
// pane of its ticket, without asking (owner, 2026-10-28); the result and a
// seen mark that could not be written are told in the status line.

import (
	"errors"
	"strings"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/agents"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

// d is offered on an ended or lost agent alone, removes it through the
// watcher, takes it out of the area at once and says so.
func TestDRemovesAnEndedAgentFromTheList(t *testing.T) {
	for _, state := range []agents.State{agents.Ended, agents.Lost} {
		h := newHarness(t)
		f := h.withWatcher(agentIn(1, orgA, 6, state, h.now), agentIn(2, orgA, 7, agents.Working, h.now))
		h.start()
		h.drive(h.press("A"))
		if ag, _ := h.m.app.agentUnderCursor(); ag.Number != 6 {
			// The working #7 is the more urgent of the two: d on it is
			// not offered, so the cursor moves to #6.
			if h.offers(RemoveAgent) {
				t.Fatalf("%v: d offered on the working #7", state)
			}
			h.drive(h.press("down"))
		}
		if ag, _ := h.m.app.agentUnderCursor(); ag.Number != 6 {
			t.Fatalf("cursor on #%d", ag.Number)
		}
		if !h.offers(RemoveAgent) {
			t.Fatalf("%v: d is not offered", state)
		}
		h.drive(h.press("d"))
		if got := f.removals(); len(got) != 1 || got[0] != agentIn(1, orgA, 6, state, h.now).ID {
			t.Fatalf("%v: removed %v", state, got)
		}
		for _, r := range h.m.agentsColumn().Rows {
			if strings.HasPrefix(r.Primary.String(), "#6") {
				t.Fatalf("%v: #6 still shown", state)
			}
		}
		if got := h.status(); got != "Agent #6 removed." {
			t.Fatalf("%v: status %q", state, got)
		}
		if h.m.focus != view.Agents {
			t.Fatalf("%v: the list closed with an agent left", state)
		}
	}
}

// An agent that still runs, waits or starts is not removed: d is not
// offered, and pressing it does nothing.
func TestDLeavesARunningAgentAlone(t *testing.T) {
	for _, state := range []agents.State{agents.Working, agents.WaitingForYou, agents.WaitingForApproval, agents.Starting} {
		h := newHarness(t)
		f := h.withWatcher(agentIn(1, orgA, 6, state, h.now))
		h.start()
		h.drive(h.press("A"))
		if h.offers(RemoveAgent) {
			t.Fatalf("%v: d offered", state)
		}
		h.drive(h.press("d"))
		if len(f.removals()) != 0 {
			t.Fatalf("%v: removed", state)
		}
	}
}

// In the pane d removes the ticket's agent when that one ended; the
// last agent removed from the agents' list closes it.
func TestDInTheTicketsPaneAndTheLastAgent(t *testing.T) {
	h := newHarness(t)
	f := h.withWatcher(agentIn(1, orgA, 6, agents.Ended, h.now))
	h.start()
	h.drive(h.press("A"))
	h.drive(h.press("enter")) // the ticket in the pane
	if h.m.focus != view.Object || !h.offers(RemoveAgent) {
		t.Fatalf("focus %v, d offered %v", h.m.focus, h.offers(RemoveAgent))
	}
	h.drive(h.press("d"))
	if len(f.removals()) != 1 || len(h.m.app.agents.list) != 0 {
		t.Fatalf("removed %v, left %v", f.removals(), h.m.app.agents.list)
	}
	h2 := newHarness(t)
	h2.withWatcher(agentIn(1, orgA, 6, agents.Ended, h2.now))
	h2.start()
	h2.drive(h2.press("A"))
	h2.drive(h2.press("d"))
	if h2.m.focus == view.Agents {
		t.Fatal("the empty agents' list stayed open")
	}
}

// A removal that fails keeps the agent and says why, as a problem; a seen
// mark that could not be written says so too (C9b left it silent).
func TestAFailedRemovalOrSeenMarkIsTold(t *testing.T) {
	h := newHarness(t)
	f := h.withWatcher(agentIn(1, orgA, 6, agents.Ended, h.now))
	f.removeErr = errors.New("disk gone")
	f.seeErr = errors.New("disk gone")
	h.start()
	h.drive(h.press("A"))
	h.drive(h.press("d"))
	if len(h.m.app.agents.list) != 1 {
		t.Fatal("the agent left though its files stayed")
	}
	if got := h.status(); !strings.Contains(got, "#6") || !strings.Contains(got, "disk gone") {
		t.Fatalf("status %q", got)
	}
	h.drive(h.press("enter")) // seen: an ended agent
	if got := h.status(); !strings.Contains(got, "seen") || !strings.Contains(got, "disk gone") {
		t.Fatalf("status after the open %q", got)
	}
}
