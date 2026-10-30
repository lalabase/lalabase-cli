package view

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/present"
)

// metaColumn is a ticket with a long text and its meta bar.
func metaColumn() present.Column {
	var text []string
	for i := 1; i <= 40; i++ {
		text = append(text, fmt.Sprintf("Line %d of the ticket", i))
	}
	return present.Column{
		Title:  present.NewInline("Ticket"),
		Cursor: -1,
		Text:   present.NewBody(strings.Join(text, "\n")),
		Meta: []present.MetaLine{
			{Text: present.NewInline("Status"), Style: present.Faint},
			{Text: present.NewInline("in progress"), Style: present.StatusActive},
			{},
			{Text: present.NewInline("People"), Style: present.Faint},
			{Text: present.NewInline("Anna Albers")},
			{Text: present.NewInline("A name far longer than the bar is wide")},
		},
	}
}

// L2: the meta bar stands on the right of a ticket pane exactly from an
// inner width of 76: 24 cells, a faint rule with a space either side, so
// the text keeps 52. Every line is the pane's width; the text's scrollbar
// stands in the text's part, left of the bar.
func TestTheMetaBarStandsFromAnInnerWidthOf76(t *testing.T) {
	if MetaFits(75) || !MetaFits(76) || !MetaFits(77) {
		t.Fatalf("MetaFits 75 %v, 76 %v, 77 %v", MetaFits(75), MetaFits(76), MetaFits(77))
	}
	for _, inner := range []int{75, 76, 77} {
		c := metaColumn()
		width, height := inner+frameCells, 10
		lines := pane(c, width, height, true, ansi.WcWidth)
		if len(lines) != height {
			t.Fatalf("inner %d: %d lines", inner, len(lines))
		}
		for i, line := range lines {
			if w := ansi.WcWidth.StringWidth(line); w != width {
				t.Fatalf("inner %d, line %d is %d cells: %q", inner, i, w, ansi.Strip(line))
			}
		}
		body := ansiStripAll(lines[1 : height-1])
		shown := strings.Contains(strings.Join(body, "\n"), "Status")
		if shown != (inner >= 76) {
			t.Fatalf("inner %d: bar shown %v\n%s", inner, shown, strings.Join(body, "\n"))
		}
		if !shown {
			continue
		}
		text := inner - 24
		for i, line := range body {
			cells := []rune(strings.TrimSuffix(strings.TrimPrefix(line, frameSide+" "), " "+frameSide))
			if len(cells) != inner {
				t.Fatalf("inner %d, line %d: %d cells inside", inner, i, len(cells))
			}
			if string(cells[text:text+3]) != " "+metaRule+" " {
				t.Fatalf("inner %d, line %d: no rule at %d: %q", inner, i, text, string(cells))
			}
			if bar := string(cells[text-1]); bar != thumb && bar != track {
				t.Fatalf("inner %d, line %d: no scrollbar at %d: %q", inner, i, text-1, string(cells))
			}
		}
		want := []string{"Status", "in progress", "", "People", "Anna Albers", "A name far longer th…"}
		for i, w := range want {
			got := strings.TrimRight(string([]rune(strings.TrimSuffix(strings.TrimPrefix(body[i], frameSide+" "), " "+frameSide))[text+3:]), " ")
			if got != w {
				t.Fatalf("inner %d, bar line %d: %q, want %q", inner, i, got, w)
			}
		}
	}
}

// The bar does not scroll with the text; its labels are faint, a status
// in its tone, the other values plain; a bar longer than the pane is cut
// and says so.
func TestTheMetaBarStaysAndKeepsItsStyles(t *testing.T) {
	c := metaColumn()
	top := pane(c, 84, 10, true, ansi.WcWidth)
	c.Offset = 7
	scrolled := pane(c, 84, 10, true, ansi.WcWidth)
	text := 80 - 24
	for i := 1; i < 9; i++ {
		a, b := []rune(ansi.Strip(top[i])), []rune(ansi.Strip(scrolled[i]))
		if string(a[2+text:]) != string(b[2+text:]) {
			t.Fatalf("line %d: the bar moved with the text: %q / %q", i, string(a), string(b))
		}
		if string(a[2:2+text-2]) == string(b[2:2+text-2]) {
			t.Fatalf("line %d: the text did not scroll", i)
		}
	}
	if !strings.Contains(top[1], faint+"Status") {
		t.Fatalf("the label is not faint: %q", top[1])
	}
	if !strings.Contains(top[2], "\x1b[38;2;223;193;132min progress") {
		t.Fatalf("the status is not in its tone: %q", top[2])
	}
	if !strings.Contains(top[5], "Anna Albers") || strings.Contains(top[5], "\x1b[1;38;2;241;243;245mAnna") || strings.Contains(top[5], "\x1b[2mAnna") {
		t.Fatalf("the value is not plain: %q", top[5])
	}
	short := pane(c, 84, 6, true, ansi.WcWidth)
	if got := strings.TrimRight(ansi.Strip(short[4]), " "+frameSide); !strings.HasSuffix(got, "People…") {
		t.Fatalf("a cut bar does not say so: %q", got)
	}
}

// The text beside the bar wraps narrower, and Extent counts what the pane
// draws: the lines of the text at the narrower width.
func TestExtentMeasuresTheTextBesideTheBar(t *testing.T) {
	long := strings.Repeat("word ", 40)
	c := present.Column{Cursor: -1, Text: present.NewBody(long)}
	g := Geometry{Width: 160, Height: 24, BodyHeight: 20, Columns: 2, ListWidth: 80, ObjectWidth: 80}
	without := Extent(c, g, Object, ansi.WcWidth)
	c.Meta = []present.MetaLine{{Text: present.NewInline("Status")}}
	with := Extent(c, g, Object, ansi.WcWidth)
	if want := len(wrapBody(long, InnerWidth(g, Object)-24-2, ansi.WcWidth)); with != want || with <= without {
		t.Fatalf("extent with the bar %d (without %d), want %d", with, without, want)
	}
	// Below the threshold the bar is ignored, the text keeps its width.
	g.ObjectWidth = 79
	c.Meta = nil
	narrow := Extent(c, g, Object, ansi.WcWidth)
	c.Meta = []present.MetaLine{{Text: present.NewInline("Status")}}
	if got := Extent(c, g, Object, ansi.WcWidth); got != narrow {
		t.Fatalf("below the threshold: %d, want %d", got, narrow)
	}
}

// Review round 2 of #32: the rule is faint, and a bar exactly as long as
// the body is whole: its last line keeps its style and no mark.
func TestTheMetaBarsRuleIsFaintAndAFullBarIsWhole(t *testing.T) {
	c := metaColumn()
	c.Meta = c.Meta[:5] // Status, in progress, "", People, Anna Albers
	lines := content(c, 80, len(c.Meta), true, ansi.WcWidth)
	for i, line := range lines {
		if !strings.Contains(line, "\x1b[2;38;2;106;110;120m"+metaRule) {
			t.Fatalf("line %d: the rule is not faint: %q", i, line)
		}
	}
	// The bar's part of the line: the text before it marks its own overflow.
	last := lines[len(lines)-1]
	last = last[strings.LastIndex(last, metaRule):]
	if strings.Contains(last, "…") || !strings.HasSuffix(strings.TrimRight(ansi.Strip(last), " "), "Anna Albers") || strings.Contains(last, faint+"Anna") {
		t.Fatalf("a full bar is marked as cut: %q", last)
	}
}

// C9b: a line of the bar may lead with a glyph in a style of its own (an
// agent's state); the line is cut with an ellipsis like the others, the
// glyph kept.
func TestAMetaLineLeadsWithItsGlyph(t *testing.T) {
	c := metaColumn()
	c.Meta = append(c.Meta,
		present.MetaLine{Text: present.NewInline("Agent"), Style: present.Faint},
		present.MetaLine{Lead: present.NewInline("⣿"), LeadStyle: present.AgentWaiting, Text: present.NewInline("waiting for you · 59 min")},
	)
	width := 76 + frameCells
	lines := pane(c, width, 12, true, ansi.WcWidth)
	line := lines[1+len(c.Meta)-1]
	plain := ansi.Strip(line)
	if !strings.Contains(plain, "⣿ waiting for you · …") {
		t.Fatalf("%q", plain)
	}
	if got := inTone(line, 35); got != "⣿" {
		t.Fatalf("in magenta: %q", got)
	}
	if w := ansi.WcWidth.StringWidth(line); w != width {
		t.Fatalf("%d cells", w)
	}
}
