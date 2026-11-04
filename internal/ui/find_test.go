package ui

import (
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

// F1: the find in a pane, its matcher and its spans, apart from any pane.

// hitTexts are the texts of hits in lines, each "line:text".
func hitTexts(lines []string, hits []hit) []string {
	var out []string
	for _, h := range hits {
		out = append(out, string(rune('0'+h.line))+":"+lines[h.line][h.from:h.to])
	}
	return out
}

// A hit is folded rune by rune and its bytes are those of the line as
// shown: a rune whose lower case has another byte length (the dotted
// capital I, the capital sharp s) moves no hit after it, and the dotted I
// is no plain i (simple folding, as strings.EqualFold folds).
func TestFindFoldsRuneByRuneAndKeepsTheLinesBytes(t *testing.T) {
	t.Parallel()

	lines := []string{
		"\u0130\u0130 abc \u0130",
		"GRO\u1e9eE Stra\u00dfe",
		"\u0130stanbul",
	}
	for _, c := range []struct {
		query string
		want  []hit
	}{
		{"ABC", []hit{{0, 5, 8}}},
		{"\u0130", []hit{{0, 0, 2}, {0, 2, 4}, {0, 9, 11}, {2, 0, 2}}},
		{"gro\u00dfe", []hit{{1, 0, 7}}},
		{"stra\u00dfe", []hit{{1, 8, 15}}},
		{"i", nil},
		{"STANBUL", []hit{{2, 2, 9}}},
	} {
		if got := findHits(lines, c.query); !slices.Equal(got, c.want) {
			t.Errorf("%q: %v (%q), want %v", c.query, got, hitTexts(lines, got), c.want)
		}
	}
}

// Hits at the start and the end of a line, two in one line, one after
// another: the next is looked for behind a hit, so "aa" in "aaa" is one.
func TestFindHitsDoNotOverlap(t *testing.T) {
	t.Parallel()

	lines := []string{"abc x abc", "aaa", "aaaa", "Logo und logo und LOGO"}
	for _, c := range []struct {
		query string
		want  []string
	}{
		{"abc", []string{"0:abc", "0:abc"}},
		{"aa", []string{"1:aa", "2:aa", "2:aa"}},
		{"logo", []string{"3:Logo", "3:logo", "3:LOGO"}},
		{"o und l", []string{"3:o und l", "3:o und L"}},
	} {
		if got := hitTexts(lines, findHits(lines, c.query)); !slices.Equal(got, c.want) {
			t.Errorf("%q: %q, want %q", c.query, got, c.want)
		}
	}
	if got := findHits(lines, "aa"); got[1] != (hit{2, 0, 2}) || got[2] != (hit{2, 2, 4}) {
		t.Fatalf("the bytes of the hits in a row: %v", got)
	}
}

// No hit crosses a line, an umlaut is not written out, and no query finds
// nothing.
func TestFindStaysInALineAndAsWritten(t *testing.T) {
	t.Parallel()

	lines := []string{"Start im", "M\u00e4rz", "\u00c4rger"}
	for _, query := range []string{"im M", "Maerz", "aerger", ""} {
		if got := findHits(lines, query); len(got) != 0 {
			t.Errorf("%q: %v", query, got)
		}
	}
	if got := hitTexts(lines, findHits(lines, "\u00e4")); !slices.Equal(got, []string{"1:\u00e4", "2:\u00c4"}) {
		t.Fatalf("the umlaut in either case: %q", got)
	}
}

// The place in words: the current hit of how many, kept within the hits
// when a reload leaves fewer, and no match; Enter starts it again at the
// first hit and ends the find on a blank line.
func TestTheFindSaysWhereItStands(t *testing.T) {
	t.Parallel()

	f := finder{query: "Logo", current: 2, round: 1}
	if got := f.place(2).String(); got != "2 of 2, from the top" || f.at(2) != 1 || f.at(0) != -1 || f.place(0).String() != "no match" {
		t.Fatalf("clamped: %q, at %d", got, f.at(2))
	}
	f.entry = " Logo  "
	if !f.edit(Submit, "") || f.query != "Logo" || f.current != 0 || f.round != 0 || f.place(3).String() != "1 of 3" {
		t.Fatalf("Enter: %+v", f)
	}
	f.entry = "   "
	if !f.edit(Submit, "") || f.query != "" {
		t.Fatalf("a blank line kept the find: %q", f.query)
	}
}

// The spans of the hits come first in a copy of the styles: a hit inside
// a tab or a lead is drawn as a hit, and the styles given stay as they
// were.
func TestHitsLeadTheSpansOfACopy(t *testing.T) {
	t.Parallel()

	styles := []present.LineStyle{
		{Spans: []present.Span{{From: 0, To: 6, Style: present.Tab}}},
		{Lead: 11, LeadStyle: present.Author, RestStyle: present.Author},
	}
	before := slices.Clone(styles[0].Spans)
	got := markHits(styles, []hit{{0, 2, 4}, {1, 5, 11}, {2, 0, 1}}, present.Match)
	if !slices.Equal(styles[0].Spans, before) || len(styles[1].Spans) != 0 {
		t.Fatal("the styles given changed")
	}
	if len(got) != 3 || got[0].StyleAt(2) != present.Match || got[0].StyleAt(1) != present.Tab || got[0].StyleAt(4) != present.Tab {
		t.Fatalf("the tab's line: %+v", got[0])
	}
	if got[1].StyleAt(5) != present.Match || got[1].StyleAt(4) != present.Author || got[1].Lead != 11 {
		t.Fatalf("the head: %+v", got[1])
	}
	if got[2].StyleAt(0) != present.Match {
		t.Fatalf("a line without a style: %+v", got[2])
	}
	current := markHits(got, []hit{{1, 5, 8}}, present.MatchCurrent)
	if current[1].StyleAt(5) != present.MatchCurrent || current[1].StyleAt(9) != present.Match || got[1].StyleAt(5) != present.Match {
		t.Fatalf("the current one over the hit: %+v", current[1])
	}
}

// A hit is found again on the screen lines its line wraps into, in parts
// where it breaks, at every width: each part is the hit's text on the
// screen, and the parts hold all of it but the spaces the wrap drops.
func TestHitsArePlacedOnTheWrappedLines(t *testing.T) {
	t.Parallel()

	lines := []string{
		"Kickoff",
		"Wir starten im M\u00e4rz mit allen Seiten-Vorlagen, dann die Texte, dann das Logo im M\u00e4rz.",
		"",
		"   eingeru\u0308ckt im M\u00e4rz",
	}
	styles := []present.LineStyle{{}, {Indent: 2}, {}, {Lead: 3, LeadStyle: present.Strong}}
	for _, query := range []string{"m\u00e4rz", "im m\u00e4rz", "vorlagen, dann", "texte, dann das logo"} {
		found := findHits(lines, query)
		if len(found) == 0 {
			t.Fatalf("%q: no hit", query)
		}
		for _, width := range []int{6, 9, 14, 23, 40, 80} {
			c := present.Column{Text: present.NewBody(strings.Join(lines, "\n")), Styles: markHits(styles, found, present.Match), Cursor: -1}
			wrapped, _, origins := view.Wrap(c, width, ansi.WcWidth)
			placed := placeHits(found, wrapped, origins)
			if len(placed) != len(found) {
				t.Fatalf("%q at %d: %d placed of %d", query, width, len(placed), len(found))
			}
			for i, parts := range placed {
				var shown []string
				for _, p := range parts {
					shown = append(shown, wrapped[p.line][p.from:p.to])
				}
				h := found[i]
				want := strings.Join(strings.Fields(lines[h.line][h.from:h.to]), "")
				if got := strings.Join(strings.Fields(strings.Join(shown, " ")), ""); len(parts) == 0 || got != want {
					t.Fatalf("%q at %d: hit %d shown as %q, want %q", query, width, i, shown, want)
				}
			}
		}
	}
}
