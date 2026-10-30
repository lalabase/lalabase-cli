package view

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/present"
)

// C9b: the header's right end sums the agents up per state, the most
// urgent first; the identity on the left gives way first.

func counts() []present.AgentCount {
	return []present.AgentCount{
		{Glyph: present.NewInline("!"), Style: present.AgentApproval, Count: 1},
		{Glyph: present.NewInline("A"), Style: present.AgentWaiting, Count: 2},
		{Glyph: present.NewInline("✕"), Style: present.AgentLost, Count: 1},
		{Glyph: present.NewInline("⠹"), Style: present.AgentWorking, Count: 1},
		{Glyph: present.NewInline("✓"), Style: present.AgentEnded, Count: 12},
	}
}

func headerScreen(identity string, c []present.AgentCount) present.Screen {
	return present.Screen{Identity: present.NewInline(identity), AgentCounts: c}
}

const fullSummary = "Agents ! 1 · A 2 · ✕ 1 · ⠹ 1 · ✓ 12"

func TestTheSummaryStandsRightInTheHeader(t *testing.T) {
	t.Parallel()

	s := headerScreen("Ada Example | Token can write: yes", counts())
	got := headerLine(s, 120, ansi.WcWidth)
	plain := ansi.Strip(got)
	if w := ansi.WcWidth.StringWidth(got); w != 120 {
		t.Fatalf("%d cells: %q", w, plain)
	}
	if !strings.HasPrefix(plain, "lalabase | default | Ada Example | Token can write: yes ") || !strings.HasSuffix(plain, fullSummary) {
		t.Fatalf("%q", plain)
	}
	// Each glyph in its tone, the counts and the word plain.
	for _, c := range []struct {
		glyph string
		sgr   int
	}{{"!", 35}, {"A", 35}, {"✕", 31}, {"⠹", 36}, {"✓", 32}} {
		cells := inTone(got, c.sgr)
		if !strings.Contains(cells, c.glyph) {
			t.Errorf("%q is not in its tone: %q", c.glyph, cells)
		}
		if strings.ContainsAny(cells, "0123456789gs") {
			t.Errorf("a count or the word in a tone: %q", cells)
		}
	}
	// Without an agent the header is as before.
	if got := ansi.Strip(headerLine(headerScreen("Ada Example", nil), 120, ansi.WcWidth)); got != "lalabase | default | Ada Example" {
		t.Fatalf("%q", got)
	}
}

// Below 120 columns the word goes; a state without agents is never
// listed (the model leaves it out).
func TestBelow120ColumnsTheWordGoes(t *testing.T) {
	t.Parallel()

	s := headerScreen("Ada", counts())
	if got := ansi.Strip(headerLine(s, 119, ansi.WcWidth)); !strings.HasSuffix(got, "! 1 · A 2 · ✕ 1 · ⠹ 1 · ✓ 12") || strings.Contains(got, "Agents") {
		t.Fatalf("%q", got)
	}
	if got := ansi.Strip(headerLine(s, 120, ansi.WcWidth)); !strings.HasSuffix(got, fullSummary) {
		t.Fatalf("%q", got)
	}
}

// The order in which a narrow header gives way: the identity is cut
// first, then the word goes, then the least urgent count, one by one; the
// app and the profile stay whole. The line is never wider than the
// terminal, whatever the width.
func TestANarrowHeaderGivesWayInOrder(t *testing.T) {
	t.Parallel()

	s := headerScreen("A rather long display name | Token can write: yes", counts())
	for width := 1; width <= 200; width++ {
		got := headerLine(s, width, ansi.WcWidth)
		if w := ansi.WcWidth.StringWidth(got); w > width {
			t.Fatalf("width %d: %d cells: %q", width, w, ansi.Strip(got))
		}
	}
	at := func(width int) string { return ansi.Strip(headerLine(s, width, ansi.WcWidth)) }
	// 120: the identity cut, the word kept.
	if got := at(120); !strings.HasSuffix(got, fullSummary) || !strings.Contains(got, "lalabase | default | A rather") {
		t.Fatalf("120: %q", got)
	}
	// 90: the word gone, the identity cut with an ellipsis.
	got := at(90)
	if strings.Contains(got, "Agents") || !strings.HasSuffix(got, "! 1 · A 2 · ✕ 1 · ⠹ 1 · ✓ 12") || !strings.Contains(got, "…") {
		t.Fatalf("90: %q", got)
	}
	// Narrower: the counts go from the least urgent on, the most urgent
	// stays longest; the app and the profile are never cut for them.
	last := 5
	for width := 89; width >= 1; width-- {
		got := at(width)
		n := strings.Count(got, " · ") + 1
		if !strings.Contains(got, "! 1") {
			n = 0
		}
		if n > last {
			t.Fatalf("width %d: a count came back: %q", width, got)
		}
		last = n
		if n > 0 && !strings.HasPrefix(got, "lalabase | default") {
			t.Fatalf("width %d: the profile was cut for the summary: %q", width, got)
		}
		if n > 0 && n < 5 && strings.Contains(got, "✓ 12") {
			t.Fatalf("width %d: the least urgent stayed: %q", width, got)
		}
	}
	if got := at(18); strings.Contains(got, "!") {
		t.Fatalf("18: a summary without room for the profile: %q", got)
	}
}

// inTone is the text of a line's cells drawn in the colour sgr (each
// cell one rune here).
func inTone(line string, sgr int) string {
	on, _ := cellsWith(line, sgr, 39)
	var b strings.Builder
	for i, r := range []rune(ansi.Strip(line)) {
		if i < len(on) && on[i] {
			b.WriteRune(r)
		}
	}
	return b.String()
}
