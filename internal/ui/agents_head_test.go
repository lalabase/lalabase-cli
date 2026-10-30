package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/gboyke/lalabase-cli/internal/agents"
	"github.com/gboyke/lalabase-cli/internal/present"
)

// C9a: the ticket's head has a line for its most urgent agent: the glyph
// in its tone, then "Agent <word> · for <age>", by the model's clock;
// where the meta bar stands, the line is in the bar (C9b). The tests of
// the head run at 80 columns, below the bar.

// headLine is the agent's line of the pane and its style; "" for none.
func (h *harness) headLine() (string, present.LineStyle) {
	h.t.Helper()
	c := h.m.objectColumn()
	for i, line := range strings.Split(c.Text.String(), "\n") {
		if strings.Contains(line, " Agent ") {
			return line, c.Styles[i]
		}
		if line == "" {
			break // the head ends with an empty line
		}
	}
	return "", present.LineStyle{}
}

func TestTheTicketHeadShowsItsAgent(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		state agents.State
		want  string
		style present.Style
	}{
		{agents.WaitingForApproval, "! Agent waiting for approval · for 4 min", present.AgentApproval},
		{agents.WaitingForYou, "A Agent waiting for you · for 4 min", present.AgentWaiting},
		{agents.Lost, "✕ Agent lost · for 4 min", present.AgentLost},
		{agents.Working, "⠹ Agent working · for 4 min", present.AgentWorking},
		{agents.Starting, "⠂ Agent starting · for 4 min", present.AgentStarting},
		{agents.Ended, "✓ Agent ended · for 4 min", present.AgentEnded},
	} {
		for _, width := range []int{80, 160} {
			h := newHarness(t)
			h.openSix()
			h.m.width = width
			h.m.app.agents.list = []agents.Agent{agentOn(6, c.state, h.now.Add(-4*time.Minute-30*time.Second))}
			h.m.app.agents.frame = 2
			line, style := h.headLine()
			glyph, _, _ := strings.Cut(c.want, " ")
			if width >= 158 {
				// C9b: where the meta bar stands, the line is there.
				meta := h.m.objectColumn().Meta
				at := 1 // after the label, first in the bar
				var value []string
				for _, l := range meta[at:] {
					if l == (present.MetaLine{}) {
						break
					}
					value = append(value, strings.TrimSpace(l.Text.String()))
				}
				word := strings.TrimSuffix(strings.TrimPrefix(c.want, glyph+" Agent "), " · for 4 min")
				if line != "" || meta[at].Lead.String() != glyph || meta[at].LeadStyle != c.style || strings.Join(value, " ") != word+" · 4 min" {
					t.Errorf("width %d: head %q, bar %+v", width, line, meta[at:])
				}
				continue
			}
			if line != c.want {
				t.Errorf("width %d: %q, want %q", width, line, c.want)
			}
			if style.Lead != len(glyph) || style.LeadStyle != c.style || style.RestStyle != present.Plain {
				t.Errorf("width %d, %q: style %+v", width, c.want, style)
			}
		}
	}
}

func TestATicketWithoutAnAgentHasNoLine(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.openSix()
	h.m.width = 80
	h.m.app.agents.list = []agents.Agent{agentOn(7, agents.WaitingForYou, h.now)}
	if line, _ := h.headLine(); line != "" {
		t.Fatalf("%q", line)
	}
}

// The head shows the most urgent of the ticket's agents.
func TestTheHeadShowsTheMostUrgent(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.openSix()
	h.m.width = 80
	approval := agentOn(6, agents.WaitingForApproval, h.now)
	approval.ID = "1f8fad5b-d9cb-469f-a165-70867728950e"
	h.m.app.agents.list = []agents.Agent{agentOn(6, agents.Working, h.now), approval}
	if line, _ := h.headLine(); !strings.HasPrefix(line, "! Agent waiting for approval") {
		t.Fatalf("%q", line)
	}
}

// The age runs with the model's clock, in its largest whole unit.
func TestTheAgeOfAState(t *testing.T) {
	t.Parallel()

	for d, want := range map[time.Duration]string{
		-time.Minute:                    "0 s",
		0:                               "0 s",
		59 * time.Second:                "59 s",
		time.Minute:                     "1 min",
		59*time.Minute + 59*time.Second: "59 min",
		time.Hour:                       "1 h",
		23 * time.Hour:                  "23 h",
		24 * time.Hour:                  "1 d",
		50 * 24 * time.Hour:             "50 d",
	} {
		if got := age(d); got != want {
			t.Errorf("%v: %q, want %q", d, got, want)
		}
	}
	h := newHarness(t)
	h.openSix()
	h.m.width = 80
	h.m.app.agents.list = []agents.Agent{agentOn(6, agents.WaitingForYou, h.now)}
	h.now = h.now.Add(2 * time.Hour)
	if line, _ := h.headLine(); line != "A Agent waiting for you · for 2 h" {
		t.Fatalf("%q", line)
	}
}
