package view

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/present"
)

// numbered is free text of n short lines, "line 1" to "line n".
func numbered(n int) present.Column {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i+1)
	}
	return present.Column{Title: present.NewInline("Ticket"), Text: present.NewBody(strings.Join(lines, "\n"))}
}

// barOf reads the scrollbar from a pane's body lines: the last cell of
// each, "" when a line ends in neither the thumb nor the track.
func barOf(lines []string) string {
	var b strings.Builder
	for _, l := range lines {
		plain := []rune(ansi.Strip(l))
		switch plain[len(plain)-1] {
		case []rune(thumb)[0]:
			b.WriteByte('#')
		case []rune(track)[0]:
			b.WriteByte('.')
		default:
			b.WriteByte(' ')
		}
	}
	return b.String()
}

// Free text that overflows carries a bar in the pane's last column: the
// thumb as long as the visible share, at the top only at the top, at the
// bottom only at the end, in between otherwise.
func TestScrollbarShowsWhereTheTextIs(t *testing.T) {
	c := numbered(40) // 40 lines, a body of 10: a thumb of 3 (10*10/40, rounded)
	const width, height = 20, 11
	top := 30
	for _, tc := range []struct {
		offset int
		want   string
	}{
		{0, "###......."},
		{1, ".###......"},
		{15, "....###..."},
		{top - 1, "......###."},
		{top, ".......###"},
		{top + 50, ".......###"},
	} {
		c.Offset = tc.offset
		got := pane(c, width, height, true, ansi.WcWidth)
		if bar := barOf(got[1:]); bar != tc.want {
			t.Errorf("offset %d: bar %q, want %q", tc.offset, bar, tc.want)
		}
		for i, line := range got {
			if w := ansi.WcWidth.StringWidth(line); w != width {
				t.Errorf("offset %d, line %d is %d cells wide", tc.offset, i, w)
			}
		}
	}
	// The title line carries no bar.
	if strings.ContainsAny(ansi.Strip(pane(c, width, height, true, ansi.WcWidth)[0]), thumb+track) {
		t.Fatal("a bar on the title line")
	}
}

// The thumb is never shorter than one cell, however long the text.
func TestScrollbarThumbKeepsOneCell(t *testing.T) {
	c := numbered(5000)
	c.Offset = 2500
	got := pane(c, 20, 6, false, ansi.WcWidth)
	if bar := barOf(got[1:]); strings.Count(bar, "#") != 1 || strings.Count(bar, ".") != 4 {
		t.Fatalf("bar %q", bar)
	}
}

// Text that fits shows no bar, and a list never does.
func TestNoScrollbarWithoutOverflowOrInAList(t *testing.T) {
	got := pane(numbered(5), 20, 11, true, ansi.WcWidth)
	if strings.ContainsAny(ansi.Strip(strings.Join(got, "")), thumb+track) {
		t.Fatalf("a bar on text that fits: %q", ansiStripAll(got))
	}
	var list []present.Row
	for i := range 40 {
		list = append(list, entry(fmt.Sprintf("row %d", i), "", "", false))
	}
	got = pane(present.Column{Rows: list, Cursor: 0}, 20, 11, true, ansi.WcWidth)
	if strings.ContainsAny(ansi.Strip(strings.Join(got, "")), thumb+track) {
		t.Fatalf("a bar in a list: %q", ansiStripAll(got))
	}
}

// Counting and drawing share one width: Extent is the number of lines the
// pane wraps the text into, so scrolling to Extent less Capacity shows the
// text's last line and nothing past it, whether the text overflows or not
// and however a line wraps at the column the bar takes.
func TestExtentAndTheDrawnTextAgree(t *testing.T) {
	words := strings.Repeat("abcd efgh ", 30)
	for _, width := range []int{8, 9, 10, 11, 12, 20, 40, 79, 80, 81, 82, 120} {
		for _, text := range []string{words, words + "\nEND", "short", strings.Repeat("x", 81) + "\nEND"} {
			g := Geometry{Columns: 2, ObjectWidth: width, BodyHeight: 7}
			c := present.Column{Text: present.NewBody(text)}
			extent, capacity := Extent(c, g, Object, ansi.WcWidth), Capacity(c, g)
			c.Offset = max(0, extent-capacity)
			got := pane(c, width, g.BodyHeight, true, ansi.WcWidth)
			var shown []string
			for _, l := range got[1:] {
				shown = append(shown, strings.TrimRight(strings.TrimRight(strings.TrimRight(ansi.Strip(l), " "), thumb+track), " "))
			}
			lines := textLines(c, textWidth(width), ansi.WcWidth)
			if len(lines) != extent {
				t.Fatalf("width %d: extent %d, the pane wraps into %d", width, extent, len(lines))
			}
			last := strings.TrimRight(lines[len(lines)-1].plain(), " ")
			if extent > capacity && shown[len(shown)-1] != last {
				t.Fatalf("width %d, %q: scrolled to the end, the last line shown is %q, want %q", width, text[:min(12, len(text))], shown[len(shown)-1], last)
			}
		}
	}
}

// The bar's column stays free in every free-text pane, so a part of the
// text (the place of a comment, which the ticket pane measures) wraps as
// the whole does.
func TestThePartOfATextWrapsAsTheWhole(t *testing.T) {
	head := strings.Repeat("abcd ", 4) // 19 cells and a space: one line at 20, two at 18
	whole := head + "\n" + strings.Repeat("more\n", 30)
	g := Geometry{Columns: 2, ObjectWidth: 20, BodyHeight: 7}
	part := Extent(present.Column{Text: present.NewBody(head)}, g, Object, ansi.WcWidth)
	all := textLines(present.Column{Text: present.NewBody(whole)}, textWidth(20), ansi.WcWidth)
	if part < 1 || strings.TrimSpace(all[part].plain()) != "more" {
		t.Fatalf("the head takes %d lines alone, the whole puts %q after it", part, all[part].plain())
	}
}
