package ui

import (
	"fmt"
	"testing"
	"time"

	"github.com/gboyke/lalabase-cli/internal/agents"
	"github.com/gboyke/lalabase-cli/internal/present"
)

// C9b: the agents of the profile across the organisations, in one order
// for the area and one summary for the header.

// agentIn is an agent of org at ticket #n of a project, with its own id.
func agentIn(id int, org string, n int, state agents.State, since time.Time) agents.Agent {
	a := agentOn(n, state, since)
	a.ID = fmt.Sprintf("0f8fad5b-d9cb-469f-a165-%012d", id)
	a.Organisation = org
	return a
}

// The order is the urgency, then the oldest change first; of two with the
// same state and time, the one read first.
func TestTheAgentsAreOrderedByUrgencyThenAge(t *testing.T) {
	h := newHarness(t)
	t0 := h.now.Add(-time.Hour)
	list := []agents.Agent{
		agentIn(1, orgA, 1, agents.Ended, t0),
		agentIn(2, orgA, 2, agents.Starting, t0.Add(time.Minute)),
		agentIn(3, orgB, 3, agents.Working, t0.Add(2*time.Minute)),
		agentIn(4, orgA, 4, agents.Lost, t0.Add(3*time.Minute)),
		agentIn(5, orgB, 5, agents.WaitingForYou, t0.Add(4*time.Minute)),
		agentIn(6, orgA, 6, agents.WaitingForApproval, t0.Add(5*time.Minute)),
		// A second approval, older: before the first.
		agentIn(7, orgB, 7, agents.WaitingForApproval, t0.Add(-time.Minute)),
		// A second working one, newer: after the first.
		agentIn(8, orgA, 8, agents.Working, t0.Add(10*time.Minute)),
		// Two waiting at the same time: in the order read.
		agentIn(9, orgA, 9, agents.WaitingForYou, t0.Add(4*time.Minute)),
	}
	h.withWatcher(list...)
	h.start()
	var got []int
	for _, a := range h.m.app.agentsInOrder() {
		got = append(got, a.Number)
	}
	want := []int{7, 6, 5, 9, 4, 3, 8, 2, 1}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order %v, want %v", got, want)
		}
	}
}

// The summary counts the agents per state, the most urgent first, and
// leaves out a state without one.
func TestTheSummaryCountsPerState(t *testing.T) {
	h := newHarness(t)
	h.withWatcher(
		agentIn(1, orgA, 1, agents.Ended, h.now),
		agentIn(2, orgB, 2, agents.Working, h.now),
		agentIn(3, orgA, 3, agents.WaitingForYou, h.now),
		agentIn(4, orgA, 4, agents.WaitingForYou, h.now),
		agentIn(5, orgA, 5, agents.Lost, h.now),
	)
	h.start()
	got := h.m.app.agentSummary()
	want := []struct {
		glyph string
		style present.Style
		count int
	}{
		{"⣿", present.AgentWaiting, 2},
		{"✕", present.AgentLost, 1},
		{spinner[h.m.app.agents.frame%len(spinner)], present.AgentWorking, 1},
		{"✓", present.AgentEnded, 1},
	}
	if len(got) != len(want) {
		t.Fatalf("%+v", got)
	}
	for i, w := range want {
		if got[i].Glyph.String() != w.glyph || got[i].Style != w.style || got[i].Count != w.count {
			t.Errorf("%d: %+v, want %+v", i, got[i], w)
		}
	}
	// No agent, no summary.
	h.m.app.agents.list = nil
	if got := h.m.app.agentSummary(); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
}
