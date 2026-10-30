package view

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/present"
)

var sgr = regexp.MustCompile(`\x1b\[([0-9;]*)m`)

// cellAttrs follows the SGR state through a line and returns, per cell,
// whether it is drawn reversed, and the parameters set while it was.
func cellAttrs(line string) (reversed []bool, params map[int]bool) {
	return cellsWith(line, 7, 27)
}

// grounded counts the cells of a line drawn on the rest colour.
func grounded(line string) int {
	cells, _ := cellsWith(line, restSGR, 49)
	n := 0
	for _, on := range cells {
		if on {
			n++
		}
	}
	return n
}

// paletteTones names the palette's colours by the plain SGR number of their
// hue, so a test reads as "cyan" whatever the shade; a colour without a hue
// of its own (bright text, a frame, the fill of a bar) has none. restSGR
// stands for the ground of a bar out of focus.
const restSGR = 100

var paletteTones = map[string]int{
	"38;2;117;197;213": 36, "38;2;223;193;132": 33, "38;2;157;207;146": 32, "38;2;223;115;118": 31,
	"38;2;191;148;228": 35, "38;2;107;154;238": 34, "38;2;121;194;250": 94, "48;2;59;63;70": restSGR,
}

// namedTones folds each 24-bit colour of an SGR sequence into its number.
func namedTones(seq []int) []int {
	var out []int
	for i := 0; i < len(seq); i++ {
		if (seq[i] != 38 && seq[i] != 48) || i+4 >= len(seq) || seq[i+1] != 2 {
			out = append(out, seq[i])
			continue
		}
		key := fmt.Sprintf("%d;2;%d;%d;%d", seq[i], seq[i+2], seq[i+3], seq[i+4])
		if n, ok := paletteTones[key]; ok {
			out = append(out, n)
		}
		i += 4
	}
	return out
}

// cellsWith follows the SGR state through a line and returns, per cell,
// whether the parameter set is on (set, until off or a reset), and the
// parameters set while it was.
func cellsWith(line string, set, off int) (cells []bool, params map[int]bool) {
	params = map[int]bool{}
	on := false
	rest := line
	for rest != "" {
		loc := sgr.FindStringSubmatchIndex(rest)
		text := rest
		if loc != nil {
			text = rest[:loc[0]]
		}
		for range ansi.WcWidth.StringWidth(text) {
			cells = append(cells, on)
		}
		if loc == nil {
			break
		}
		var seq []int
		for _, p := range strings.Split(rest[loc[2]:loc[3]], ";") {
			if n, err := strconv.Atoi(p); err == nil {
				seq = append(seq, n)
			} else if p == "" {
				seq = append(seq, 0)
			}
		}
		seq = namedTones(seq)
		for _, n := range seq {
			switch n {
			case 0, off:
				on = false
			case set:
				on = true
			}
		}
		if on {
			for _, n := range seq {
				params[n] = true
			}
		}
		rest = rest[loc[1]:]
	}
	return cells, params
}

// reversedCells counts the cells of a line drawn reversed.
func reversedCells(line string) int {
	r, _ := cellAttrs(line)
	n := 0
	for _, on := range r {
		if on {
			n++
		}
	}
	return n
}

// In the focused pane the cursor row is one bar: every cell of its line
// (both lines of a two-line entry) reversed, the marker kept, no other row
// touched. Faint and a status's tone give way inside the bar; bold stays.
func TestTheCursorRowOfTheFocusedPaneIsABar(t *testing.T) {
	t.Parallel()

	c := present.Column{Title: present.NewInline("Tickets"), Cursor: 1, Rows: []present.Row{
		{Primary: present.NewInline("#1 Login"), Trailing: present.NewInline("open"), TrailingStyle: present.StatusOpen, Secondary: present.NewInline("Ben")},
		{Primary: present.NewInline("#2 Catering"), Trailing: present.NewInline("in progress"), TrailingStyle: present.StatusActive, Secondary: present.NewInline("Kai"), Active: true},
		{Primary: present.NewInline("#3 Archiv"), Trailing: present.NewInline("done"), TrailingStyle: present.StatusDone, Secondary: present.NewInline("Anna"), Quiet: true},
	}}
	const width = 32
	got := content(c, width, 11, true, ansi.WcWidth)
	for i, line := range got {
		want := 0
		if i == 2 || i == 3 {
			want = width
		}
		if n := reversedCells(line); n != want {
			t.Errorf("line %d (%q): %d cells reversed, want %d", i, ansi.Strip(line), n, want)
		}
		if w := ansi.WcWidth.StringWidth(line); w != width {
			t.Errorf("line %d is %d cells wide", i, w)
		}
	}
	if !strings.HasPrefix(ansi.Strip(got[2]), "> #2 Catering") {
		t.Fatalf("the marker is gone from the bar: %q", ansi.Strip(got[2]))
	}
	for _, i := range []int{2, 3} {
		_, params := cellAttrs(got[i])
		if params[2] || params[33] || params[36] || params[32] {
			t.Fatalf("line %d: faint or a tone inside the bar: %v in %q", i, params, got[i])
		}
	}
	if _, params := cellAttrs(got[2]); !params[1] {
		t.Fatalf("the active entry lost its bold in the bar: %q", got[2])
	}
	if _, params := cellAttrs(got[3]); params[1] {
		t.Fatalf("the second line is bold in the bar: %q", got[3])
	}
	// The rows outside the bar keep their styles.
	if !strings.Contains(got[0], "\x1b[38;2;117;197;213m") || !strings.Contains(got[1], faint+"Ben") {
		t.Fatalf("a row outside the bar lost its style: %q %q", got[0], got[1])
	}
}

// A pane without the focus draws its cursor row on the grey ground, over
// the pane's whole width and both lines, never reversed: the place stays
// seen while the keys act elsewhere. The marker stays, bold stays, faint
// and a tone give way as in the focused bar.
func TestAnUnfocusedPaneDrawsItsCursorOnTheRestColour(t *testing.T) {
	t.Parallel()

	c := present.Column{Title: present.NewInline("Tickets"), Cursor: 0, Rows: []present.Row{
		{Primary: present.NewInline("#1 Login"), Trailing: present.NewInline("done"), TrailingStyle: present.StatusDone, Secondary: present.NewInline("Ben"), Active: true, Quiet: true},
		{Primary: present.NewInline("#2 Catering"), Trailing: present.NewInline("open"), Secondary: present.NewInline("Kai")},
	}}
	const width = 24
	got := content(c, width, 9, false, ansi.WcWidth)
	for i, line := range got {
		want := 0
		if i < 2 {
			want = width
		}
		if n := grounded(line); n != want {
			t.Errorf("line %d (%q): %d cells on the ground, want %d", i, ansi.Strip(line), n, want)
		}
		if n := reversedCells(line); n != 0 {
			t.Errorf("line %d reversed in an unfocused pane: %q", i, line)
		}
	}
	if !strings.HasPrefix(ansi.Strip(got[0]), "> #1 Login") {
		t.Fatalf("the marker: %q", ansi.Strip(got[0]))
	}
	for _, i := range []int{0, 1} {
		_, params := cellsWith(got[i], 100, 49)
		if params[2] || params[32] {
			t.Fatalf("line %d: faint or a tone on the ground: %v in %q", i, params, got[i])
		}
	}
	if _, params := cellsWith(got[0], 100, 49); !params[1] {
		t.Fatalf("the active entry lost its bold on the ground: %q", got[0])
	}
}

// A bar is the pane's width also when its text does not fit (the marker
// dropped, the text cut), in compact mode, and on a row without a value.
func TestTheBarFillsThePaneInEveryLayout(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		c             present.Column
		width, height int
		lines         []int
	}{
		"overflow": {present.Column{Cursor: 0, Rows: []present.Row{entry("A very long organisation name", "12345", "", false)}}, 10, 2, []int{0}},
		"compact":  {present.Column{Cursor: 1, Rows: []present.Row{entry("a", "", "b", false), entry("c", "", "d", false)}}, 12, 4, []int{1}},
		"heading":  {present.Column{Cursor: 0, Rows: []present.Row{entry("Inbox", "", "", false)}}, 12, 2, []int{0}},
		"wide":     {present.Column{Cursor: 0, Rows: []present.Row{entry("x", "1", "", false)}}, 90, 2, []int{0}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			for _, focused := range []bool{true, false} {
				count := reversedCells
				if !focused {
					count = grounded
				}
				got := content(tc.c, tc.width, tc.height, focused, ansi.WcWidth)
				for _, i := range tc.lines {
					if n := count(got[i]); n != tc.width {
						t.Fatalf("focused %v, line %d: %d of %d cells in the bar: %q", focused, i, n, tc.width, got[i])
					}
				}
			}
		})
	}
}
