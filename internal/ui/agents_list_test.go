package ui

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/agents"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

// C9a: every ticket list reserves two cells for an agent's glyph, and the
// most urgent agent at a ticket gives it.

func (h *harness) rowOf(n int) present.Row {
	h.t.Helper()
	for _, r := range h.m.ticketsColumn().Rows {
		if strings.Contains(r.Primary.String(), "#"+strconv.Itoa(n)+" ") {
			return r
		}
	}
	h.t.Fatalf("no row of #%d", n)
	return present.Row{}
}

func TestEveryStateHasItsGlyphInTheList(t *testing.T) {
	for _, c := range []struct {
		state agents.State
		glyph string
		style present.Style
	}{
		{agents.WaitingForApproval, "!", present.AgentApproval},
		{agents.WaitingForYou, "⣿", present.AgentWaiting},
		{agents.Lost, "✕", present.AgentLost},
		{agents.Working, "⠋", present.AgentWorking},
		{agents.Starting, "⠂", present.AgentStarting},
		{agents.Ended, "✓", present.AgentEnded},
	} {
		h := newHarness(t)
		h.openTickets(website)
		h.m.app.agents.list = []agents.Agent{agentOn(6, c.state, h.now)}
		r := h.rowOf(6)
		if !r.Marked || r.Mark.String() != c.glyph || r.MarkStyle != c.style {
			t.Errorf("state %d: %+v", c.state, r)
		}
		if other := h.rowOf(7); !other.Marked || other.Mark.String() != "" {
			t.Errorf("a ticket without an agent: %+v", other)
		}
	}
}

// The spinner's frame is the reads' count.
func TestTheWorkingGlyphIsTheSpinnersFrame(t *testing.T) {
	h := newHarness(t)
	h.openTickets(website)
	h.m.app.agents.list = []agents.Agent{agentOn(6, agents.Working, h.now)}
	seen := map[string]bool{}
	for i := range len(spinner) + 1 {
		h.m.app.agents.frame = i
		seen[h.rowOf(6).Mark.String()] = true
	}
	if len(seen) != len(spinner) {
		t.Fatalf("frames %v", seen)
	}
}

// Every glyph is one cell wide, by either way of counting widths.
func TestTheGlyphsAreOneCellWide(t *testing.T) {
	glyphs := append([]string(nil), spinner...)
	for _, g := range agentGlyphs {
		glyphs = append(glyphs, g)
	}
	for _, g := range glyphs {
		for _, method := range []ansi.Method{ansi.WcWidth, ansi.GraphemeWidth} {
			if w := method.StringWidth(g); w != 1 {
				t.Errorf("%q is %d cells wide", g, w)
			}
		}
	}
	if len(agentGlyphs) != 5 || len(agentStyles) != 6 {
		t.Fatalf("%d glyphs, %d tones", len(agentGlyphs), len(agentStyles))
	}
}

// In one, two and three columns every line is as wide as the terminal,
// with an agent and without, and the glyph stands before the number.
func TestTheColumnFitsEveryLayout(t *testing.T) {
	glyphBeforeSix := regexp.MustCompile("⣿ +#6 ")
	for _, width := range []int{60, 100, 160} {
		for _, list := range [][]agents.Agent{nil, {}} {
			h := newHarness(t)
			h.openTickets(website)
			h.m.width = width
			if list != nil {
				list = []agents.Agent{agentOn(6, agents.WaitingForYou, h.now)}
			}
			h.m.app.agents.list = list
			want := map[int]int{60: 1, 100: 2, 160: 3}[width]
			if g := h.m.geometry(); g.Columns != want || h.m.focus != view.List {
				t.Fatalf("width %d: %d columns, focus %v", width, g.Columns, h.m.focus)
			}
			screen := ansi.Strip(h.m.View().Content)
			g := h.m.geometry()
			lines := strings.Split(screen, "\n")
			top := 2 + g.NoticeHeight
			for i := top; i < top+g.BodyHeight; i++ {
				// The panes' lines: the header, the path and the help are
				// as long as their text.
				if w := ansi.WcWidth.StringWidth(lines[i]); w != width {
					t.Errorf("width %d, line %d is %d cells: %q", width, i, w, lines[i])
				}
			}
			if got := glyphBeforeSix.MatchString(screen); got != (list != nil) {
				t.Errorf("width %d, agent %v: glyph before #6 %v\n%s", width, list != nil, got, screen)
			}
		}
	}
}
