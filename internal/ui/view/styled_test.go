package view

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/present"
)

const (
	bold  = "\x1b[1m"
	faint = "\x1b[2m"
)

// A line's lead and rest keep their styles across a wrap, its indent
// holds on every screen line, and a heading ends in a rule to the reading
// width.
func TestStyledLinesWrapWithTheirStyles(t *testing.T) {
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
	c := present.Column{Title: present.NewInline("Tickets"), Cursor: -1, Rows: []present.Row{
		{Primary: present.NewInline("#2 Landingpage"), Trailing: present.NewInline("in_progress"), Secondary: present.NewInline("Anna")},
		{Primary: present.NewInline("#10 Catering"), Trailing: present.NewInline("new"), Secondary: present.NewInline("Kai")},
	}}
	got := pane(c, 32, 12, false, ansi.WcWidth)
	if ansi.Strip(got[1]) != "  #2 Landingpage     in_progress" || ansi.Strip(got[3]) != "  #10 Catering               new" {
		t.Fatalf("rows %q", ansiStripAll(got[1:5]))
	}
	if !strings.Contains(got[2], faint+"Anna") {
		t.Fatalf("the second line is not faint: %q", got[2])
	}
	nav := present.Column{Title: present.NewInline("Organisations"), Cursor: -1, Rows: []present.Row{
		{Primary: present.NewInline("Inbox"), Trailing: present.NewInline("0"), Quiet: true},
		{Primary: present.NewInline("Inbox"), Trailing: present.NewInline("12"), TrailingStyle: present.Strong},
	}}
	got = pane(nav, 20, 4, false, ansi.WcWidth)
	if !strings.HasPrefix(got[1], faint) || strings.Contains(got[1], bold) {
		t.Fatalf("the quiet entry: %q", got[1])
	}
	if strings.Contains(got[2], faint) || !strings.Contains(got[2], bold+"12") || strings.Contains(got[2], bold+"  Inbox") {
		t.Fatalf("the highlighted value: %q", got[2])
	}
}

// A list too narrow for a column of values keeps its titles: each value
// follows its own entry's text.
func TestANarrowListKeepsItsTitles(t *testing.T) {
	c := present.Column{Title: present.NewInline("Tickets"), Cursor: -1, Rows: []present.Row{
		{Primary: present.NewInline("#2 Export als CSV"), Trailing: present.NewInline("new")},
		{Primary: present.NewInline("#1 Login"), Trailing: present.NewInline("in_progress")},
	}}
	got := pane(c, 30, 4, false, ansi.WcWidth)
	if ansi.Strip(got[1]) != "  #2 Export als CSV        new" || ansi.Strip(got[2]) != "  #1 Login         in_progress" {
		t.Fatalf("rows %q", ansiStripAll(got[1:3]))
	}
}

// A status's tone is a colour of the terminal's own palette (cyan, yellow,
// green: the user's theme picks the shade), and only the status takes it.
func TestAStatusIsDrawnInTheTerminalsOwnColours(t *testing.T) {
	for style, sgr := range map[present.Style]string{present.StatusOpen: "36", present.StatusActive: "33", present.StatusDone: "32"} {
		c := present.Column{Title: present.NewInline("Tickets"), Cursor: -1, Rows: []present.Row{
			{Primary: present.NewInline("#2 Export"), Trailing: present.NewInline("fertig"), TrailingStyle: style},
		}}
		got := pane(c, 30, 2, false, ansi.WcWidth)[1]
		if !strings.Contains(got, "\x1b["+sgr+"mfertig") || strings.Contains(got, sgr+"m  #2") {
			t.Errorf("%v: %q", style, got)
		}
	}
	lines := wrapStyled("fertig · Website", present.LineStyle{Lead: len("fertig"), LeadStyle: present.StatusDone, RestStyle: present.Faint}, 40, ansi.WcWidth)
	if got := lines[0].render(40, ansi.WcWidth); !strings.Contains(got, "\x1b[32mfertig") || !strings.Contains(got, faint+" · Website") {
		t.Fatalf("the facts line: %q", got)
	}
}
