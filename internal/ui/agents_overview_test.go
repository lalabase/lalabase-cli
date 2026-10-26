package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/agents"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
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

// The header's first line carries the summary at its right end, never
// wider than the terminal; below 120 columns without the word.
func TestTheHeaderSumsTheAgentsUp(t *testing.T) {
	h := newHarness(t)
	h.withWatcher(agentIn(1, orgA, 1, agents.WaitingForYou, h.now), agentIn(2, orgB, 2, agents.Ended, h.now))
	h.start()
	for _, c := range []struct {
		width int
		want  string
	}{{120, "Agents ⣿ 1 · ✓ 1"}, {119, " ⣿ 1 · ✓ 1"}, {80, " ⣿ 1 · ✓ 1"}} {
		h.m.width = c.width
		first := strings.Split(ansi.Strip(h.m.View().Content), "\n")[0]
		if !strings.HasSuffix(first, c.want) || (c.width < 120 && strings.Contains(first, "Agents")) {
			t.Errorf("%d: %q", c.width, first)
		}
		if w := ansi.WcWidth.StringWidth(first); w > c.width {
			t.Errorf("%d: %d cells", c.width, w)
		}
	}
}

// An organisation's heading carries the glyph of its most urgent agent,
// in the two cells a ticket list reserves, which every row of the chooser
// keeps while an agent exists (the sections stay indented under their
// heading); "All organisations" and the rows of the sections carry none.
// Without an agent nothing is reserved.
func TestAnOrganisationCarriesItsMostUrgentAgent(t *testing.T) {
	h := newHarness(t)
	f := h.withWatcher(
		agentIn(1, orgA, 1, agents.Working, h.now),
		agentIn(2, orgA, 2, agents.WaitingForApproval, h.now),
		agentIn(3, orgB, 3, agents.Ended, h.now),
	)
	h.start()
	headings := map[string]present.Row{}
	for _, r := range h.m.chooserColumn().Rows {
		name := strings.TrimSpace(r.Primary.String())
		switch name {
		case "Inbox", "Tickets":
			if !r.Marked || r.Mark.String() != "" {
				t.Errorf("a section row: %+v", r)
			}
		default:
			headings[name] = r
		}
	}
	all := headings["All organisations"]
	if !all.Marked || all.Mark.String() != "" {
		t.Errorf("All organisations: %+v", all)
	}
	for _, o := range h.d.orgs {
		r, ok := headings[o.Name]
		if !ok {
			t.Fatalf("no heading of %s in %v", o.Name, headings)
		}
		want, style := map[string]string{orgA: "!", orgB: "✓"}[o.ID], map[string]present.Style{orgA: present.AgentApproval, orgB: present.AgentEnded}[o.ID]
		if !r.Marked || r.Mark.String() != want || r.MarkStyle != style {
			t.Errorf("%s: %+v, want %q", o.Name, r, want)
		}
	}
	f.set()
	h.due()
	for _, r := range h.m.chooserColumn().Rows {
		if r.Marked {
			t.Fatalf("a row is marked without an agent: %+v", r)
		}
	}
}

// With agents in every place they show (the header, the organisations,
// the area, the list, the meta bar), no line of the screen is wider than
// the terminal, at any width from 80 to 200 and in every focus.
func TestNoLineIsWiderThanTheTerminalWithAgents(t *testing.T) {
	h := newHarness(t)
	h.withWatcher(
		agentIn(1, orgA, 6, agents.WaitingForApproval, h.now.Add(-50*24*time.Hour)),
		agentIn(2, orgB, 7, agents.WaitingForYou, h.now),
		agentIn(3, orgA, 8, agents.Lost, h.now),
		agentIn(4, orgA, 9, agents.Working, h.now),
		agentIn(5, orgA, 10, agents.Starting, h.now),
		agentIn(6, orgA, 11, agents.Ended, h.now),
	)
	h.openSix()
	for _, focus := range []view.Pane{view.Navigation, view.List, view.Object, view.Agents} {
		h.m.focus = focus
		for width := 80; width <= 200; width++ {
			h.m.width = width
			for i, line := range strings.Split(h.m.View().Content, "\n") {
				if w := ansi.WcWidth.StringWidth(line); w > width {
					t.Fatalf("focus %d, width %d, line %d: %d cells: %q", focus, width, i, w, ansi.Strip(line))
				}
			}
		}
	}
}
