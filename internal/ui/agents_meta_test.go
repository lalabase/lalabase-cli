package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/agents"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

// C9b: where the meta bar stands (view.MetaFits), the ticket's agent has
// its line there, first (review round 1 of #36, F2: the bar does not
// scroll, and its end is cut on a low terminal): a faint label, then its
// glyph in its tone, its word and the age of its state; the head's line
// of C9a goes, as the facts' line does. Below that width the head keeps it.
func TestTheAgentStandsInTheMetaBar(t *testing.T) {
	h := newHarness(t)
	h.showLogin()
	h.m.app.agents.list = []agents.Agent{agentOn(1, agents.WaitingForYou, h.now.Add(-4*time.Minute-30*time.Second))}
	for _, tc := range []struct {
		width int
		inner int
	}{{157, 75}, {158, 76}, {200, 101}} {
		h.m.width, h.m.focus = tc.width, view.List
		if got := view.InnerWidth(h.m.geometry(), view.Object); got != tc.inner {
			t.Fatalf("width %d: inner %d", tc.width, got)
		}
		c := h.m.objectColumn()
		line, _ := h.headLine()
		if !view.MetaFits(tc.inner) {
			if line != "⣿ Agent waiting for you · for 4 min" || c.Meta != nil {
				t.Fatalf("width %d: head %q, meta %q", tc.width, line, metaText(c))
			}
			continue
		}
		if line != "" {
			t.Fatalf("width %d: the head keeps %q beside the bar", tc.width, line)
		}
		label, value, more, gap := c.Meta[0], c.Meta[1], c.Meta[2], c.Meta[3]
		if label.Text.String() != "Agent" || label.Style != present.Faint {
			t.Fatalf("width %d: label %+v", tc.width, label)
		}
		// Too long for the bar on one line: the age follows on the next,
		// under the word, so nothing of it is cut.
		if value.Lead.String() != "⣿" || value.LeadStyle != present.AgentWaiting || value.Text.String() != "waiting for you" || value.Style != present.Plain {
			t.Fatalf("width %d: value %+v", tc.width, value)
		}
		if more.Lead.String() != "" || more.Text.String() != "  · 4 min" || more.Style != present.Plain {
			t.Fatalf("width %d: the age %+v", tc.width, more)
		}
		if gap != (present.MetaLine{}) || c.Meta[4].Text.String() != "Status" {
			t.Fatalf("width %d: %q", tc.width, metaText(c))
		}
	}
	// Without an agent the bar starts with Status.
	h.m.app.agents.list = nil
	h.m.width = 158
	if got := metaText(h.m.objectColumn()); got[0] != "Status" || strings.Contains(strings.Join(got, "|"), "Agent") {
		t.Fatalf("%q", got)
	}
}

// Review round 1 of #36, F2: on a low terminal the bar is cut at its end;
// the agent's state stays in sight wherever the bar stands.
func TestTheAgentStaysInSightOnALowTerminal(t *testing.T) {
	h := newHarness(t)
	h.showLogin()
	h.m.app.agents.list = []agents.Agent{agentOn(1, agents.WaitingForYou, h.now)}
	for _, width := range []int{158, 200} {
		for height := 10; height <= 30; height++ {
			for _, focus := range []view.Pane{view.List, view.Object} {
				h.m.width, h.m.height, h.m.focus = width, height, focus
				if h.m.small() {
					continue
				}
				if screen := ansi.Strip(h.m.View().Content); !strings.Contains(screen, "waiting for you") {
					t.Fatalf("%dx%d, focus %d: the agent's state is not on the screen:\n%s", width, height, focus, screen)
				}
			}
		}
	}
}

// The agent's value fits the bar: on one line where it can, else broken
// at a space, the lines after the first under the word; never wider than
// the bar.
func TestTheAgentsValueFitsTheBar(t *testing.T) {
	h := newHarness(t)
	for _, c := range []struct {
		state agents.State
		since time.Duration
		want  []string
	}{
		{agents.Ended, 4 * time.Minute, []string{"ended · 4 min"}},
		{agents.Working, 59 * time.Minute, []string{"working · 59 min"}},
		{agents.Starting, 59 * time.Second, []string{"starting · 59 s"}},
		{agents.Lost, 23 * time.Hour, []string{"lost · 23 h"}},
		{agents.WaitingForYou, 4 * time.Minute, []string{"waiting for you", "  · 4 min"}},
		{agents.WaitingForApproval, 50 * 24 * time.Hour, []string{"waiting for", "  approval · 50 d"}},
	} {
		h.m.app.agents.list = []agents.Agent{agentOn(6, c.state, h.now.Add(-c.since))}
		all := h.m.app.agentMeta(ticketID(website, 6))
		lines := all[1 : len(all)-1]
		var got []string
		for i, l := range lines {
			got = append(got, l.Text.String())
			if w := 2*boolInt(i == 0) + len([]rune(l.Text.String())); w > view.MetaRoom {
				t.Errorf("%q is %d cells", l.Text.String(), w)
			}
		}
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("state %d: %q, want %q", c.state, got, c.want)
		}
	}
}
