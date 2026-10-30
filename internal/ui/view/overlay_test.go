package view

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/present"
)

var bothMethods = []ansi.Method{ansi.WcWidth, ansi.GraphemeWidth}

func inlineOf(s string) present.Inline { return present.NewInline(s) }

func smallDialog() present.Dialog {
	return present.Dialog{
		Title: inlineOf("Book time · #42 Login bricht ab"),
		Fields: []present.DialogField{
			{Label: inlineOf("Duration"), Entry: []present.Inline{inlineOf("30\u258f")}, Hint: inlineOf("30, 1:30 or 1.5"), Active: true},
			{Label: inlineOf("Note"), Entry: []present.Inline{inlineOf(" ")}},
		},
		Help: []present.KeyHelp{
			{Key: inlineOf("enter"), Help: inlineOf("book")},
			{Key: inlineOf("tab"), Help: inlineOf("next field")},
			{Key: inlineOf("esc"), Help: inlineOf("cancel")},
		},
	}
}

func largeDialog() present.Dialog {
	return present.Dialog{
		Title:  inlineOf("Comment · #42 Login bricht ab"),
		Large:  true,
		Fields: []present.DialogField{{Label: inlineOf("Comment"), Entry: []present.Inline{inlineOf("first line"), inlineOf("second\u258f")}, Hint: inlineOf("Plain text"), Active: true}},
		Help: []present.KeyHelp{
			{Key: inlineOf("ctrl+s"), Help: inlineOf("send")},
			{Key: inlineOf("enter"), Help: inlineOf("new line")},
			{Key: inlineOf("esc"), Help: inlineOf("cancel")},
		},
	}
}

func sizeOf(width, height int) Geometry { return Geometry{Width: width, Height: height} }

// dots is a screen of dots, one character per cell.
func dots(g Geometry) string {
	lines := make([]string, g.Height)
	for i := range lines {
		lines[i] = strings.Repeat(".", g.Width)
	}
	return strings.Join(lines, "\n")
}

func TestTheSmallBoxLooksAsPlanned(t *testing.T) {
	t.Parallel()

	g := sizeOf(80, 24)
	got := strings.Split(ansi.Strip(Overlay(dots(g), smallDialog(), g, ansi.WcWidth)), "\n")
	want := []string{
		".............╭─ Book time · #42 Login bricht ab ──────────────────╮.............",
		".............│                                                    │.............",
		".............│  Duration  30\u258f                                     │.............",
		".............│            30, 1:30 or 1.5                         │.............",
		".............│  Note                                              │.............",
		".............│                                                    │.............",
		".............│   enter book   tab next field   esc cancel         │.............",
		".............╰────────────────────────────────────────────────────╯.............",
	}
	top := (g.Height - len(want)) / 2
	for i, line := range want {
		if got[top+i] != line {
			t.Errorf("line %d:\n got %q\nwant %q", top+i, got[top+i], line)
		}
	}
	for _, i := range []int{0, top - 1, top + len(want), g.Height - 1} {
		if got[i] != strings.Repeat(".", 80) {
			t.Errorf("line %d is not the screen: %q", i, got[i])
		}
	}
}

func TestTheBoxSitsInTheMiddleInItsWidth(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name          string
		dialog        present.Dialog
		width, height int
		boxW, boxH    int
	}{
		{"small at 80", smallDialog(), 80, 24, 54, 8},
		{"small at 120", smallDialog(), 120, 40, 54, 8},
		{"small at 60", smallDialog(), 60, 24, 54, 8},
		{"small below 60", smallDialog(), 59, 24, 59, 8},
		{"small at 40, its help on two lines", smallDialog(), 40, 20, 40, 9},
		{"large at 120", largeDialog(), 120, 40, 96, 32},
		{"large at 80", largeDialog(), 80, 24, 64, 19},
		{"large at 60", largeDialog(), 60, 24, 54, 19},
		{"large below 60", largeDialog(), 50, 24, 50, 19},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := sizeOf(tc.width, tc.height)
			box := dialogBox(tc.dialog, g, ansi.WcWidth)
			if len(box) != tc.boxH {
				t.Fatalf("%d lines, want %d", len(box), tc.boxH)
			}
			for i, row := range box {
				if w := ansi.WcWidth.StringWidth(row); w != tc.boxW {
					t.Fatalf("line %d is %d wide, want %d: %q", i, w, tc.boxW, ansi.Strip(row))
				}
			}
			lines := strings.Split(ansi.Strip(Overlay(dots(g), tc.dialog, g, ansi.WcWidth)), "\n")
			x, y := (tc.width-tc.boxW)/2, (tc.height-tc.boxH)/2
			if got := []rune(lines[y])[x]; string(got) != frameTopLeft {
				t.Errorf("the corner at %d,%d is %q in %q", x, y, got, lines[y])
			}
			if y > 0 && lines[y-1] != strings.Repeat(".", tc.width) {
				t.Errorf("the line above the box: %q", lines[y-1])
			}
		})
	}
}

// The room of a large field is what its box leaves: the entry's lines stand
// between its label and its hint, and the entry fills them.
func TestTheLargeBoxGivesItsFieldTheRest(t *testing.T) {
	t.Parallel()

	d := largeDialog()
	g := sizeOf(80, 24)
	width, height := DialogEntry(d, g, ansi.WcWidth)
	if width != 58 || height != 12 {
		t.Fatalf("entry %dx%d", width, height)
	}
	box := dialogBox(d, g, ansi.WcWidth)
	if !strings.Contains(ansi.Strip(box[2]), "Comment") || !strings.Contains(ansi.Strip(box[3]), "first line") || !strings.Contains(ansi.Strip(box[4]), "second\u258f") {
		t.Fatalf("the top of the box:\n%s", strings.Join(ansiStripAll(box[:6]), "\n"))
	}
	if !strings.Contains(ansi.Strip(box[15]), "Plain text") {
		t.Fatalf("the hint is not under the entry:\n%s", strings.Join(ansiStripAll(box), "\n"))
	}
	if !strings.Contains(ansi.Strip(box[17]), "ctrl+s") {
		t.Fatalf("the help is not at the bottom:\n%s", strings.Join(ansiStripAll(box), "\n"))
	}
}

func TestTheEntriesOfASmallBoxStandInOneColumn(t *testing.T) {
	t.Parallel()

	d := smallDialog()
	g := sizeOf(80, 24)
	width, height := DialogEntry(d, g, ansi.WcWidth)
	if width != 38 || height != 1 {
		t.Fatalf("entry %dx%d", width, height)
	}
	// The narrowest box still leaves the entry its room.
	d.Fields[0].Label = inlineOf(strings.Repeat("Label", 20))
	if width, _ := DialogEntry(d, sizeOf(24, 8), ansi.WcWidth); width < 8 {
		t.Fatalf("a long label left the entry %d cells", width)
	}
}

// A label is measured as the terminal counts: in the width method of the
// run, a heart with its emoji selector takes two cells, and stays whole.
// A low screen takes from a large box in this order: the empty line, the
// hint, the label; its field keeps three lines for as long as it can.
func TestALowLargeBoxGivesWayInOrder(t *testing.T) {
	t.Parallel()

	d := largeDialog()
	d.Fields[0].Label = inlineOf("LABEL")
	for _, tc := range []struct {
		height                int
		blank, hint, label    bool
		entryLines, boxHeight int
	}{
		{13, true, true, true, 3, 10},
		{12, false, true, true, 3, 9},
		{10, false, false, true, 3, 8},
		{9, false, false, false, 3, 7},
		{8, false, false, false, 3, 7},
	} {
		g := sizeOf(80, tc.height)
		box := ansiStripAll(dialogBox(d, g, ansi.WcWidth))
		plain := strings.Join(box, "\n")
		blank := strings.TrimSpace(strings.Trim(box[1], "│")) == ""
		if blank != tc.blank || strings.Contains(plain, "Plain text") != tc.hint || strings.Contains(plain, "LABEL") != tc.label || len(box) != tc.boxHeight {
			t.Errorf("height %d: %d lines:\n%s", tc.height, len(box), plain)
		}
		if _, lines := DialogEntry(d, g, ansi.WcWidth); lines != tc.entryLines {
			t.Errorf("height %d: %d lines of entry, want %d", tc.height, lines, tc.entryLines)
		}
	}
}

// The last line of the screen stays the screen's: it names ctrl+c, the one
// key the box does not.
func TestTheBoxLeavesTheLastLineFree(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("The server refused this. ", 12)
	for _, d := range []present.Dialog{smallDialog(), largeDialog()} {
		for _, text := range []string{"", "Refused.", long} {
			d.Error, d.ErrorStyle = inlineOf(text), present.NoticeProblem
			for height := 8; height <= 42; height++ {
				for width := 24; width <= 130; width += 7 {
					box := dialogBox(d, sizeOf(width, height), ansi.WcWidth)
					y := (height - len(box)) / 2
					if y+len(box) > height-1 {
						t.Fatalf("%dx%d with %q: %d lines from line %d", width, height, text, len(box), y)
					}
				}
			}
		}
	}
}

func TestALabelIsMeasuredInTheWidthOfTheRun(t *testing.T) {
	t.Parallel()

	d := smallDialog()
	d.Fields[0].Label = inlineOf("\u2764\ufe0f")
	d.Fields[1].Label = inlineOf("x")
	box := ansiStripAll(dialogBox(d, sizeOf(80, 24), ansi.GraphemeWidth))
	if !strings.Contains(strings.Join(box, "\n"), "\u2764\ufe0f  30") {
		t.Fatalf("the label was cut:\n%s", strings.Join(box, "\n"))
	}
}

func TestTheFocusedFieldShowsItself(t *testing.T) {
	t.Parallel()

	box := dialogBox(smallDialog(), sizeOf(80, 24), ansi.WcWidth)
	// The active label is bold, the other one is not; the frame is blue
	// and bold, as the focused pane's.
	if _, params := cellsWith(box[2], 1, 22); !params[1] {
		t.Errorf("the active label is not bold: %q", box[2])
	}
	if strings.Contains(box[4], "\x1b[1;38;2;241;243;245mNote") {
		t.Errorf("the other label is bold: %q", box[4])
	}
	if _, params := cellsWith(box[0], 34, 39); !params[1] {
		t.Errorf("the frame is not the focused pane's: %q", box[0])
	}
	if !strings.Contains(box[3], "\x1b[2m30, 1:30 or 1.5") {
		t.Errorf("the hint is not faint: %q", box[3])
	}
}

func TestTheErrorLineWrapsAndTakesItsTone(t *testing.T) {
	t.Parallel()

	d := smallDialog()
	long := strings.Repeat("The server refused this. ", 12)
	d.Error, d.ErrorStyle = inlineOf(long), present.NoticeProblem
	g := sizeOf(80, 24)
	box := dialogBox(d, g, ansi.WcWidth)
	plain := strings.Join(ansiStripAll(box), "\n")
	if !strings.Contains(plain, "The server refused this.") || !strings.Contains(plain, "…") {
		t.Fatalf("no cut error line in:\n%s", plain)
	}
	if len(box) != 10 {
		t.Fatalf("three lines of error make a box of ten, got %d:\n%s", len(box), plain)
	}
	found := false
	for _, row := range box {
		if strings.Contains(ansi.Strip(row), "The server") {
			_, params := cellsWith(row, 31, 39)
			found = found || params[1]
		}
	}
	if !found {
		t.Fatalf("the error line has not the tone of a problem: %q", box)
	}
}

func TestALongTitleIsCutInTheBorder(t *testing.T) {
	t.Parallel()

	d := smallDialog()
	d.Title = inlineOf(strings.Repeat("\u4e16", 80))
	for _, row := range dialogBox(d, sizeOf(80, 24), ansi.WcWidth) {
		if w := ansi.WcWidth.StringWidth(row); w != 54 {
			t.Fatalf("a line of %d cells: %q", w, ansi.Strip(row))
		}
	}
}

// Every size of a terminal the interface shows at all: the box stays inside
// the screen, its lines as wide as it says, its help and the caret of the
// active field in sight.
func TestEveryTerminalShowsTheBoxWhole(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("no ", 60)
	large := largeDialog()
	large.Fields[0].Entry = []present.Inline{inlineOf("first\u258f"), inlineOf("second")}
	three := smallDialog()
	three.Fields = append(three.Fields, present.DialogField{Label: inlineOf("Third"), Entry: []present.Inline{inlineOf("text")}, Hint: inlineOf("hint"), Active: false})
	three.Fields[0].Active, three.Fields[2].Active = false, true
	three.Fields[0].Entry = []present.Inline{inlineOf("30")}
	three.Fields[2].Entry = []present.Inline{inlineOf("text\u258f")}
	for name, d := range map[string]present.Dialog{"small": smallDialog(), "large": large, "three": three} {
		for _, withError := range []bool{false, true} {
			if withError {
				d.Error, d.ErrorStyle = inlineOf(long), present.NoticeProblem
			}
			for _, method := range bothMethods {
				for width := 24; width <= 130; width += 3 {
					for height := 8; height <= 42; height++ {
						g := sizeOf(width, height)
						box := dialogBox(d, g, method)
						where := fmt.Sprintf("%s %dx%d error %v", name, width, height, withError)
						if len(box) > height {
							t.Fatalf("%s: %d lines", where, len(box))
						}
						plain := strings.Join(ansiStripAll(box), "\n")
						if !strings.Contains(plain, "esc") {
							t.Fatalf("%s: no help:\n%s", where, plain)
						}
						if !strings.Contains(plain, "\u258f") {
							t.Fatalf("%s: no caret:\n%s", where, plain)
						}
						out := Overlay(dots(g), d, g, method)
						lines := strings.Split(out, "\n")
						if len(lines) != height {
							t.Fatalf("%s: a screen of %d lines", where, len(lines))
						}
						for i, line := range lines {
							if w := method.StringWidth(line); w != width {
								t.Fatalf("%s: line %d is %d wide: %q", where, i, w, ansi.Strip(line))
							}
						}
					}
				}
			}
		}
	}
}

func TestASmallGeometryShowsNoBox(t *testing.T) {
	t.Parallel()

	g := Geometry{Width: 20, Height: 6, Small: true}
	screen := dots(g)
	if got := Overlay(screen, smallDialog(), g, ansi.WcWidth); got != screen {
		t.Fatalf("a box on a small screen: %q", got)
	}
	for _, g := range []Geometry{{}, {Width: 0, Height: 10}, {Width: 10, Height: 0}} {
		if got := Overlay("", smallDialog(), g, ansi.WcWidth); got != "" {
			t.Fatalf("a box without a screen: %q", got)
		}
	}
}

// cell is what the terminal holds in one cell after a line: what is written
// there and the attributes in force.
type cell struct{ text, style string }

// cellsOf plays a line into cells, by its own reading of the sequences: a
// sequence that is no complete SGR fails the test, so a cut through one
// shows here.
func cellsOf(t *testing.T, line string, method ansi.Method) []cell {
	t.Helper()
	sgr := regexp.MustCompile(`^\x1b\[[0-9;:]*m`)
	var out []cell
	style := ""
	for line != "" {
		if line[0] == 0x1b {
			seq := sgr.FindString(line)
			if seq == "" {
				t.Fatalf("a broken sequence at %q", line)
			}
			line = line[len(seq):]
			if seq == "\x1b[m" || seq == "\x1b[0m" {
				style = ""
			} else {
				style += seq
			}
			continue
		}
		if line[0] < 0x20 {
			t.Fatalf("a control byte at %q", line)
		}
		c, width := ansi.FirstGraphemeCluster(line, method)
		line = line[len(c):]
		if width == 0 {
			if len(out) > 0 {
				out[len(out)-1].text += c
			}
			continue
		}
		out = append(out, cell{c, style})
		for range width - 1 {
			out = append(out, cell{"", style})
		}
	}
	return out
}

func TestOverlayCutsBetweenCellsAndKeepsTheirStyles(t *testing.T) {
	t.Parallel()

	bold := lipgloss.NewStyle().Bold(true)
	blue := lipgloss.NewStyle().Foreground(lipgloss.Blue)
	bar := lipgloss.NewStyle().Reverse(true)
	wide := strings.Repeat("\u4e16", 40)
	family := "\U0001F468\u200d\U0001F469\u200d\U0001F467"
	for name, line := range map[string]string{
		"plain":            strings.Repeat("abcdefghij", 8),
		"one style across": bar.Render(strings.Repeat("abcdefghij", 8)),
		"styles at every cut": func() string {
			var b strings.Builder
			for i := range 80 {
				b.WriteString(bold.Render(string(rune('a' + i%26))))
			}
			return b.String()
		}(),
		"two tones":        blue.Render(strings.Repeat("x", 30)) + " " + bold.Render(strings.Repeat("y", 49)),
		"wide":             wide,
		"wide, one off":    "a" + wide[:len(wide)-3],
		"wide in a style":  bar.Render("a" + wide[:len(wide)-3]),
		"joined emoji":     "ab" + strings.Repeat(family, 13),
		"combining":        strings.Repeat("e\u0301", 80),
		"flags":            strings.Repeat("\U0001F1E9\U0001F1EA", 40),
		"a long sequence":  "\x1b[38;2;255;255;255;48;2;0;0;0m" + strings.Repeat("z", 80) + "\x1b[m",
		"short":            "abc",
		"ends in the box":  strings.Repeat("a", 40),
		"ends in the wide": strings.Repeat("a", 39) + "\u4e16",
	} {
		t.Run(name, func(t *testing.T) {
			g := sizeOf(80, 24)
			screen := make([]string, 24)
			for i := range screen {
				screen[i] = line
			}
			for _, d := range []present.Dialog{smallDialog(), largeDialog()} {
				for _, method := range bothMethods {
					box := dialogBox(d, g, method)
					boxW := method.StringWidth(box[0])
					x, y := (80-boxW)/2, (24-len(box))/2
					lines := strings.Split(Overlay(strings.Join(screen, "\n"), d, g, method), "\n")
					before := cellsOf(t, line, method)
					for row, got := range lines {
						if !utf8.ValidString(got) {
							t.Fatalf("invalid UTF-8 in %q", got)
						}
						cells := cellsOf(t, got, method)
						if row < y || row >= y+len(box) {
							if !slices.Equal(cells, before) {
								t.Fatalf("line %d outside the box changed: %q", row, got)
							}
							continue
						}
						for at := range max(len(before), len(cells)) {
							if at >= x && at < x+boxW {
								continue
							}
							var want, have cell
							if at < len(before) {
								want = before[at]
							}
							if at < len(cells) {
								have = cells[at]
							}
							if want == have {
								continue
							}
							// A short line is padded up to the box, and a
							// wide character an edge goes through leaves
							// plain blanks in the cells that remain.
							blank := at >= len(before) && at < x
							if at < len(before) {
								first, end := at, at+1
								for first > 0 && before[first].text == "" {
									first--
								}
								for end < len(before) && before[end].text == "" {
									end++
								}
								blank = first < x+boxW && end > x
							}
							if blank && have == (cell{" ", ""}) {
								continue
							}
							t.Fatalf("line %d, cell %d: %+v, want %+v\n%q", row, at, have, want, got)
						}
					}
				}
			}
		})
	}
}

// What stands behind the box comes from Render, foreign text included: every
// line keeps the screen's width and the box's edges, only complete SGR
// sequences stay, and the hostile text stays the harmless text it became.
func TestOverlayOverAHostileScreen(t *testing.T) {
	t.Parallel()

	hostile := "\x1b[2J\x1b[95mCSI\x1b[0m\x1b]8;;https://evil.invalid\x07LINK\x1b]8;;\x1b\\\x1b]52;c;YQ==\x1b\\\x1bPpayload\x1b\\\x07\u202e\u2066\u2069\xff\u009b\u4e16\u754c\U0001F468\u200d\U0001F469\u200d\U0001F467e\u0301"
	rows := make([]present.Row, 30)
	for i := range rows {
		rows[i] = present.Row{Primary: inlineOf(hostile), Trailing: inlineOf("open"), TrailingStyle: present.StatusOpen, Secondary: inlineOf(hostile)}
	}
	text := strings.Repeat(hostile+"\n", 30)
	screen := present.Screen{
		Identity: inlineOf(hostile), Context: inlineOf(hostile), Crumbs: [3]present.Inline{inlineOf("a"), inlineOf("b"), inlineOf("c")},
		Chooser: present.Column{Title: inlineOf(hostile), Rows: rows, Cursor: 1},
		List:    present.Column{Title: inlineOf(hostile), Rows: rows, Cursor: 2},
		Object:  present.Column{Title: inlineOf(hostile), Text: present.NewBody(text), Cursor: -1},
		Help:    []present.KeyHelp{{Key: inlineOf("q"), Help: inlineOf("quit")}},
	}
	for _, method := range bothMethods {
		for _, tc := range []struct {
			columns, width, height int
		}{{3, 120, 40}, {2, 100, 30}, {2, 80, 24}, {1, 50, 20}} {
			g := Geometry{Width: tc.width, Height: tc.height, BodyHeight: tc.height - 3, Columns: tc.columns, HelpHeight: 1}
			switch tc.columns {
			case 3:
				g.NavigationWidth, g.ListWidth, g.ObjectWidth = 26, 38, 56
			case 2:
				g.ListWidth, g.ObjectWidth = tc.width*2/5, tc.width-tc.width*2/5
			default:
				g.ListWidth = tc.width
			}
			for _, d := range []present.Dialog{smallDialog(), largeDialog()} {
				d.Title = inlineOf(hostile)
				d.Error, d.ErrorStyle = inlineOf(hostile), present.NoticeProblem
				under := Render(screen, g, List, method)
				out := Overlay(under, d, g, method)
				for i, line := range strings.Split(out, "\n") {
					if !utf8.ValidString(line) {
						t.Fatalf("line %d: invalid UTF-8", i)
					}
					cells := cellsOf(t, line, method)
					if len(cells) > tc.width {
						t.Fatalf("%dx%d line %d is %d cells: %q", tc.width, tc.height, i, len(cells), line)
					}
				}
				if strings.Contains(out, "\x1b]") || strings.Contains(out, "\x1b[2J") || strings.Contains(out, "\u202e") {
					t.Fatalf("a sequence of the hostile text reached the screen: %q", out)
				}
			}
		}
	}
}

// Render's lines hold nothing but text and SGR, but what around does with any
// line ends: it reads the sequences of a line, broken or not, one by one.
func FuzzAroundEnds(f *testing.F) {
	f.Add("\x1b[1mabc\x1b[m\u4e16", 1, 3)
	f.Add("\x1b", 0, 2)
	f.Add("\x1b]0;open", 2, 4)
	f.Add("a\xffb\x9b", 1, 2)
	f.Fuzz(func(t *testing.T, line string, left, right int) {
		if left < 0 || right < left || right > 1000 {
			return
		}
		for _, method := range bothMethods {
			before, after := around(line, left, right, method)
			if !strings.Contains(line, "\x1b") && utf8.ValidString(line) {
				if w := method.StringWidth(before); w != left {
					t.Fatalf("before %q is %d wide, want %d", before, w, left)
				}
			}
			_ = after
		}
	})
}

// What stands on either side of the box, cell by cell, in the two width
// methods: the left part as wide as it must be, no style left open, and the
// right part with the styles in force there, or nothing at all.
func TestAround(t *testing.T) {
	t.Parallel()

	wide := "\u4e16"
	for _, tc := range []struct {
		name, line, head, after string
		blanks                  int
	}{
		{"both sides", "abcdefgh", "ab", "gh", 0},
		{"a line that ends before the box", "a", "a", "", 1},
		{"a line that ends in the box", "abcde", "ab", "", 0},
		{"a wide character on the left edge", "a" + wide + "cdefgh", "a", "fgh", 1},
		{"a wide character on the right edge", "abcde" + wide + "gh", "ab", " gh", 0},
		{"a wide character on the right edge at the end", "abcde" + wide, "ab", " ", 0},
		{"a mark on the last cell before the box", "ae\u0301cdefgh", "ae\u0301", "gh", 0},
		{"a mark on the first cell in the box", "abc\u0301defgh", "ab", "gh", 0},
		{"a mark on the last cell in the box", "abcdef\u0301gh", "ab", "gh", 0},
		{"a mark on the first cell after the box", "abcdefg\u0301h", "ab", "g\u0301h", 0},
		{"a style ending under the box", "\x1b[1mabcd\x1b[mefgh", "\x1b[1mab", "\x1b[1m\x1b[mgh", 0},
		{"a style open past the end of the line", "\x1b[7mabcd", "\x1b[7mab", "", 0},
		{"a style starting under the box", "ab\x1b[7mcdefgh", "ab", "\x1b[7mgh", 0},
		{"a style starting behind the box", "abcdefg\x1b[7mh", "ab", "g\x1b[7mh", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, method := range bothMethods {
				before, after := around(tc.line, 2, 6, method)
				if want := tc.head + ansi.ResetStyle + strings.Repeat(" ", tc.blanks); before != want {
					t.Errorf("method %d: before %q, want %q", method, before, want)
				}
				if after != tc.after {
					t.Errorf("method %d: after %q, want %q", method, after, tc.after)
				}
			}
		})
	}
	// A joined emoji of six cells over the box of two: blanks on both sides.
	family := "\U0001F468\u200d\U0001F469\u200d\U0001F467"
	if before, after := around("a"+family+"b", 3, 5, ansi.WcWidth); before != "a"+ansi.ResetStyle+"  " || after != "  b" {
		t.Errorf("around a grapheme wider than the box: %q, %q", before, after)
	}
}
