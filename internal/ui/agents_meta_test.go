package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/gboyke/lalabase-cli/internal/agents"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

// C9b: where the meta bar stands (view.MetaFits), the ticket's agent has
// its line there, after Updated: a faint label, then its glyph in its
// tone, its word and the age of its state; the head's line of C9a goes,
// as the facts' line does. Below that width the head keeps it.
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
		n := len(c.Meta)
		if n < 5 || c.Meta[n-5].Text.String() != "5 Jan 09:26" || c.Meta[n-4] != (present.MetaLine{}) {
			t.Fatalf("width %d: %q", tc.width, metaText(c))
		}
		label, value, more := c.Meta[n-3], c.Meta[n-2], c.Meta[n-1]
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
	}
	// Without an agent the bar ends with Updated.
	h.m.app.agents.list = nil
	h.m.width = 158
	if got := metaText(h.m.objectColumn()); got[len(got)-1] != "5 Jan 09:26" || strings.Contains(strings.Join(got, "|"), "Agent") {
		t.Fatalf("%q", got)
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
		lines := h.m.app.agentMeta(ticketID(website, 6))[2:]
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
