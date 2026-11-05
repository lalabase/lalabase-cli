package view

import (
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/present"
)

const (
	bold  = "\x1b[1;38;2;241;243;245m"
	faint = "\x1b[2m"

	focusBold = "\x1b[1;38;2;107;154;238m"
)

// A line's lead and rest keep their styles across a wrap, its indent
// holds on every screen line, and a heading ends in a rule to the reading
// width.
func TestStyledLinesWrapWithTheirStyles(t *testing.T) {
	t.Parallel()

	c := present.Column{
		Text: present.NewBody("Kai Neahnung  28 Aug 15:45\nfirst words of a body that wraps\nComments"),
		Styles: []present.LineStyle{
			{Lead: len("Kai Neahnung"), LeadStyle: present.Strong, RestStyle: present.Faint},
			{Indent: 2},
			{Lead: len("Comments"), LeadStyle: present.Strong, Rule: true},
		},
	}
	lines := textLines(c, 20, ansi.WcWidth)
	var plain []string
	for _, l := range lines {
		plain = append(plain, l.plain())
	}
	want := []string{
		"Kai Neahnung  28 Aug",
		"15:45",
		"  first words of a",
		"  body that wraps",
		"Comments ───────────",
	}
	if strings.Join(plain, "|") != strings.Join(want, "|") {
		t.Fatalf("lines %q, want %q", plain, want)
	}
	head := lines[0].render(20, ansi.WcWidth)
	if !strings.HasPrefix(head, bold) || !strings.Contains(head, faint+"  28") || strings.Contains(head, bold+"28") {
		t.Fatalf("the head's styles: %q", head)
	}
	if got := lines[1].render(20, ansi.WcWidth); !strings.HasPrefix(got, faint) {
		t.Fatalf("the wrapped rest of the head is not faint: %q", got)
	}
	if got := lines[2].render(20, ansi.WcWidth); strings.Contains(got, "\x1b") {
		t.Fatalf("a plain line carries a style: %q", got)
	}
	if got := lines[4].render(20, ansi.WcWidth); !strings.HasPrefix(got, bold+"Comments") || !strings.Contains(got, faint+"─") {
		t.Fatalf("the heading: %q", got)
	}
}

// Text wraps at the reading width in a wide pane; a narrow pane keeps its
// text and drops the indent.
func TestStyledLinesKeepTheReadingWidthAndDropAnIndentWhenNarrow(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("word ", 40)
	lines := textLines(present.Column{Text: present.NewBody(long)}, 200, ansi.WcWidth)
	for _, l := range lines {
		if w := ansi.WcWidth.StringWidth(l.plain()); w > ReadingWidth {
			t.Fatalf("a line of %d cells in a pane of 200", w)
		}
	}
	rule := textLines(present.Column{Text: present.NewBody("H"), Styles: []present.LineStyle{{Rule: true}}}, 200, ansi.WcWidth)
	if w := ansi.WcWidth.StringWidth(rule[0].plain()); w != ReadingWidth {
		t.Fatalf("the rule ends at %d, not at the reading width", w)
	}
	narrow := textLines(present.Column{Text: present.NewBody("abc def"), Styles: []present.LineStyle{{Indent: 2}}}, minIndented+1, ansi.WcWidth)
	if narrow[0].plain() != "abc def" {
		t.Fatalf("the indent stayed in a narrow pane: %q", narrow[0].plain())
	}
	if n := Extent(present.Column{Text: present.NewBody(long)}, Geometry{Columns: 2, ListWidth: 10, ObjectWidth: 200}, Object, ansi.WcWidth); n != len(lines) {
		t.Fatalf("the extent %d is not the lines shown, %d", n, len(lines))
	}
}

// A line whose text a style map would get wrong (a lead past its end)
// draws what it has, never more or less text.
func TestAStyleNeverChangesTheText(t *testing.T) {
	t.Parallel()

	c := present.Column{Text: present.NewBody("a  b\t c"), Styles: []present.LineStyle{{Lead: 99, LeadStyle: present.Strong, Indent: 3, Rule: true}}}
	for _, width := range []int{1, 2, 5, 20, 90} {
		for _, l := range textLines(c, width, ansi.WcWidth) {
			if got := ansi.Strip(l.render(width, ansi.WcWidth)); got != l.plain() && ansi.WcWidth.StringWidth(l.plain()) <= width {
				t.Fatalf("width %d: rendered %q, text %q", width, got, l.plain())
			}
		}
	}
}

// The values of a list stand right-aligned in one column; the second line
// is faint; a quiet entry is faint, a highlighted value bold.
func TestRowValuesAlignAndWeigh(t *testing.T) {
	t.Parallel()

	c := present.Column{Title: present.NewInline("Tickets"), Cursor: -1, Rows: []present.Row{
		{Primary: present.NewInline("#2 Landingpage"), Trailing: present.NewInline("in_progress"), Secondary: present.NewInline("Anna")},
		{Primary: present.NewInline("#10 Catering"), Trailing: present.NewInline("new"), Secondary: present.NewInline("Kai")},
	}}
	got := content(c, 32, 11, false, ansi.WcWidth)
	if ansi.Strip(got[0]) != "  #2 Landingpage     in_progress" || ansi.Strip(got[2]) != "  #10 Catering               new" {
		t.Fatalf("rows %q", ansiStripAll(got[0:4]))
	}
	if !strings.Contains(got[1], faint+"Anna") {
		t.Fatalf("the second line is not faint: %q", got[1])
	}
	nav := present.Column{Title: present.NewInline("Organisations"), Cursor: -1, Rows: []present.Row{
		{Primary: present.NewInline("Inbox"), Trailing: present.NewInline("0"), Quiet: true},
		{Primary: present.NewInline("Inbox"), Trailing: present.NewInline("12"), TrailingStyle: present.Strong},
	}}
	got = content(nav, 20, 3, false, ansi.WcWidth)
	if !strings.HasPrefix(got[0], faint) || strings.Contains(got[0], bold) {
		t.Fatalf("the quiet entry: %q", got[0])
	}
	if strings.Contains(got[1], faint) || !strings.Contains(got[1], bold+"12") || strings.Contains(got[1], bold+"  Inbox") {
		t.Fatalf("the highlighted value: %q", got[1])
	}
}

// A list too narrow for a column of values keeps its titles: each value
// follows its own entry's text.
func TestANarrowListKeepsItsTitles(t *testing.T) {
	t.Parallel()

	c := present.Column{Title: present.NewInline("Tickets"), Cursor: -1, Rows: []present.Row{
		{Primary: present.NewInline("#2 Export als CSV"), Trailing: present.NewInline("new")},
		{Primary: present.NewInline("#1 Login"), Trailing: present.NewInline("in_progress")},
	}}
	got := content(c, 30, 3, false, ansi.WcWidth)
	if ansi.Strip(got[0]) != "  #2 Export als CSV        new" || ansi.Strip(got[1]) != "  #1 Login         in_progress" {
		t.Fatalf("rows %q", ansiStripAll(got[0:2]))
	}
}

// A status's tone is a colour of the client's palette (cyan, yellow, green),
// and only the status takes it.
func TestAStatusIsDrawnInItsTone(t *testing.T) {
	t.Parallel()

	for style, sgr := range map[present.Style]string{present.StatusOpen: "38;2;117;197;213", present.StatusActive: "38;2;223;193;132", present.StatusDone: "38;2;157;207;146"} {
		c := present.Column{Title: present.NewInline("Tickets"), Cursor: -1, Rows: []present.Row{
			{Primary: present.NewInline("#2 Export"), Trailing: present.NewInline("fertig"), TrailingStyle: style},
		}}
		got := content(c, 30, 1, false, ansi.WcWidth)[0]
		if !strings.Contains(got, "\x1b["+sgr+"mfertig") || strings.Contains(got, sgr+"m  #2") {
			t.Errorf("%v: %q", style, got)
		}
	}
	lines := wrapStyled("fertig · Website", present.LineStyle{Lead: len("fertig"), LeadStyle: present.StatusDone, RestStyle: present.Faint}, 40, ansi.WcWidth)
	if got := lines[0].render(40, ansi.WcWidth); !strings.Contains(got, "\x1b[38;2;157;207;146mfertig") || !strings.Contains(got, faint+" · Website") {
		t.Fatalf("the facts line: %q", got)
	}
}

// Violet is the authors' colour and, among the states, exactly the two
// waits': for an approval and for the person.
func TestOnlyAuthorsAndTheTwoWaitsDrawViolet(t *testing.T) {
	t.Parallel()

	for s := present.Plain; s <= present.AgentEnded; s++ {
		violet := s == present.Author || s == present.AgentApproval || s == present.AgentWaiting
		for _, out := range []string{styled("x", s), quieted("x", s)} {
			if got := strings.Contains(out, "191;148;228"); got != violet {
				t.Errorf("style %d draws violet: %v, want %v (%q)", s, got, violet, out)
			}
		}
	}
	if got := styled("x", present.Own); got != "\x1b[1;38;2;121;194;250mx\x1b[m" {
		t.Fatalf("own: %q", got)
	}
}

// The app's name opens the header in its colour; the rest of the line,
// and a header without the name, stay as they are.
func TestTheHeaderOpensWithTheBrand(t *testing.T) {
	if got := branded("lalabase | default"); got != "\x1b[1;38;2;121;194;250mlalabase\x1b[m | default" {
		t.Fatalf("%q", got)
	}
	if got := branded("lala…"); got != "lala…" {
		t.Fatalf("a cut name: %q", got)
	}
}

// A bar and a key cap stay reversed without colour, so NO_COLOR keeps
// them, and their fill is the palette's.
func TestAFilledRunIsReversed(t *testing.T) {
	if got := capStyle.Render("q"); got != "\x1b[7;38;2;216;218;223mq\x1b[m" {
		t.Fatalf("%q", got)
	}
}

// K7b: a text the model wrapped once (Wrap, Column.Wrapped) draws as the
// view draws the text itself, at every width and every offset: leads,
// indents, a heading's rule, long words, hyphens, wide graphemes, tabs.
func TestAWrappedTextDrawsAsTheTextItself(t *testing.T) {
	t.Parallel()

	text := strings.Join([]string{
		"Kickoff Relaunch",
		"Anna Albers 4 Jan 09:00 · 45 min",
		"",
		"» Transcript",
		"Anna: Wir starten im März, mit allen Seiten-Vorlagen und dem neuen Logo.\tDanach die Texte.",
		"Supercalifragilisticexpialidocious-und-so-weiter ein langes Wort",
		"日本語のテキストも折り返す 😀😀😀 und weiter",
		"   eingerückt mit Leerzeichen   ",
		"  Summary » Notes   Transcript ",
		"",
	}, "\n")
	styles := []present.LineStyle{
		{Lead: len("Kickoff Relaunch"), LeadStyle: present.Strong, RestStyle: present.Strong},
		{Lead: len("Anna Albers"), LeadStyle: present.Author, RestStyle: present.Faint},
		{},
		{Lead: len("» Transcript"), LeadStyle: present.Strong, Rule: true},
		{Indent: 2},
		{Lead: 10, LeadStyle: present.Strong, RestStyle: present.Faint},
		{Indent: 4, RestStyle: present.Faint},
		tabsStyle(),
	}
	c := present.Column{Text: present.NewBody(text), Styles: styles, Cursor: -1}
	for _, width := range []int{6, 14, 23, 40, 81, 120} {
		lines, st, origins := Wrap(c, textWidth(width), ansi.WcWidth)
		w := present.Column{Wrapped: lines, Styles: st, Cursor: -1}
		total := len(textLines(c, textWidth(width), ansi.WcWidth))
		if len(lines) != total || len(origins) != total {
			t.Fatalf("width %d: %d lines wrapped, %d origins, %d drawn", width, len(lines), len(origins), total)
		}
		// F1: each screen line says where its text starts in the line of
		// the text it comes from, in order.
		source := strings.Split(strings.ReplaceAll(c.Text.String(), "\t", "    "), "\n")
		for j, l := range lines {
			o := origins[j]
			after := j > 0 && (o.Line > origins[j-1].Line || o.Line == origins[j-1].Line && o.From >= origins[j-1].From+len(lines[j-1]))
			if !strings.HasPrefix(source[o.Line][o.From:], l) || j > 0 && !after {
				t.Fatalf("width %d: screen line %d %q from %+v", width, j, l, o)
			}
		}
		for offset := 0; offset <= total; offset++ {
			c.Offset, w.Offset = offset, offset
			for _, height := range []int{1, 3, 7} {
				want, got := content(c, width, height, true, ansi.WcWidth), content(w, width, height, true, ansi.WcWidth)
				if !slices.Equal(want, got) {
					t.Fatalf("width %d, offset %d, height %d:\nwant %q\ngot  %q", width, offset, height, want, got)
				}
			}
		}
	}
	// TextWidth is the width the pane wraps at, with the meta bar or
	// without: a text wrapped at it measures as the text itself.
	for _, meta := range [][]present.MetaLine{nil, {{Text: present.NewInline("Status")}}} {
		c.Meta = meta
		g := Geometry{Width: 160, Height: 24, BodyHeight: 20, Columns: 2, ListWidth: 60, ObjectWidth: 100}
		lines, st, _ := Wrap(c, TextWidth(c, g, Object), ansi.WcWidth)
		w := present.Column{Wrapped: lines, Styles: st, Meta: meta, Cursor: -1}
		if got, want := Extent(w, g, Object, ansi.WcWidth), Extent(c, g, Object, ansi.WcWidth); got != want || got != len(lines) {
			t.Fatalf("meta %v: extent %d, want %d", meta != nil, got, want)
		}
	}
}

// tabsStyle is the style of the line "  Summary » Notes   Transcript ": the
// first tab faint, the active one a tab (K7d).
func tabsStyle() present.LineStyle {
	first, active := len("  Summary "), len("» Notes ")
	return present.LineStyle{Spans: []present.Span{
		{From: 0, To: first, Style: present.Faint},
		{From: first, To: first + active, Style: present.Tab},
	}, Rule: true}
}

// K7d: a span of a line is drawn in its own style, the rest in the line's,
// and it keeps its style where the line wraps; the line still ends in its
// rule.
func TestASpanKeepsItsStyleAcrossAWrap(t *testing.T) {
	t.Parallel()

	c := present.Column{Text: present.NewBody("  Summary » Notes   Transcript "), Styles: []present.LineStyle{tabsStyle()}, Cursor: -1}
	lines := textLines(c, 60, ansi.WcWidth)
	if len(lines) != 1 {
		t.Fatalf("lines %q", lines)
	}
	got := lines[0].render(60, ansi.WcWidth)
	if !strings.Contains(got, styled("» Notes ", present.Tab)) || !strings.HasPrefix(got, styled("  Summary ", present.Faint)) {
		t.Fatalf("the spans: %q", got)
	}
	if !strings.Contains(got, "\x1b[m  Transcript ") && !strings.Contains(got, "\x1b[0m  Transcript ") {
		t.Fatalf("the rest is not plain: %q", got)
	}
	if !strings.Contains(got, faint+"─") {
		t.Fatalf("no rule: %q", got)
	}
	if styled("x", present.Tab) == "x" || styled("x", present.Tab) == styled("x", present.Strong) {
		t.Fatal("a tab has no style of its own")
	}

	narrow := textLines(c, 12, ansi.WcWidth)
	var tab []string
	for _, l := range narrow {
		for _, p := range l {
			if p.style == present.Tab {
				tab = append(tab, p.text)
			}
		}
	}
	// A narrow pane may break the tab at its space: both parts stay tabs.
	if got := strings.TrimSpace(strings.Join(tab, " ")); got != "» Notes" {
		t.Fatalf("the tab across the wrap: %q in %q", tab, narrow)
	}
}

// F2: a line with many spans (the hits of a find in a long comment) is
// drawn as one with few: every byte in the first span that holds it, the
// lead and the rest around them, across the wrap.
func TestALineWithManySpansDrawsAsItsFirstSpans(t *testing.T) {
	t.Parallel()

	line := strings.Repeat("ab cd ", 20)
	st := present.LineStyle{Lead: 4, LeadStyle: present.Strong, RestStyle: present.Faint}
	st.Spans = append(st.Spans, present.Span{From: 6, To: 14, Style: present.MatchCurrent})
	for i := 0; i < len(line); i += 6 {
		st.Spans = append(st.Spans, present.Span{From: i + 1, To: i + 4, Style: present.Match})
	}
	lines, styles, origins := Wrap(present.Column{Text: present.NewBody(line), Styles: []present.LineStyle{st}}, 40, ansi.WcWidth)
	if len(lines) < 2 {
		t.Fatalf("no wrap: %q", lines)
	}
	for s := range lines {
		for i := range len(lines[s]) {
			if got, want := styles[s].StyleAt(i), st.StyleAt(origins[s].From+i); got != want {
				t.Fatalf("line %d byte %d: %v, want %v", s, i, got, want)
			}
		}
	}
}

// F1: a hit of the find is bold in a tone of its own, the current one
// reversed in it, so both keep the weight of a bold line; a hit in a
// speaker's head is marked and bold, the rest of the name keeps its tone.
func TestAHitIsMarkedAndKeepsItsWeight(t *testing.T) {
	t.Parallel()

	if got := styled("Logo", present.Match); got != "\x1b[1;38;2;240;163;94mLogo\x1b[m" {
		t.Fatalf("a hit: %q", got)
	}
	if got := styled("Logo", present.MatchCurrent); got != "\x1b[1;7;38;2;240;163;94mLogo\x1b[m" {
		t.Fatalf("the current hit: %q", got)
	}
	head := present.LineStyle{Lead: len("Anna Albers"), LeadStyle: present.Author, RestStyle: present.Author}
	for _, style := range []present.Style{present.Match, present.MatchCurrent} {
		st := head
		st.Spans = []present.Span{{From: 5, To: 11, Style: style}}
		c := present.Column{Text: present.NewBody("Anna Albers"), Styles: []present.LineStyle{st}, Cursor: -1}
		lines, styles, _ := Wrap(c, 40, ansi.WcWidth)
		got := wrappedLines(present.Column{Wrapped: lines, Styles: styles, Cursor: -1}, 0, 1, 40, ansi.WcWidth)[0].render(40, ansi.WcWidth)
		if want := styled("Anna ", present.Author) + styled("Albers", style); got != want {
			t.Fatalf("%v in the head: %q, want %q", style, got, want)
		}
	}
}
