package view

import (
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
			reversed = append(reversed, on)
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
		for _, n := range seq {
			switch n {
			case 0, 27:
				on = false
			case 7:
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
	return reversed, params
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
	c := present.Column{Title: present.NewInline("Tickets"), Cursor: 1, Rows: []present.Row{
		{Primary: present.NewInline("#1 Login"), Trailing: present.NewInline("open"), TrailingStyle: present.StatusOpen, Secondary: present.NewInline("Ben")},
		{Primary: present.NewInline("#2 Catering"), Trailing: present.NewInline("in progress"), TrailingStyle: present.StatusActive, Secondary: present.NewInline("Kai"), Active: true},
		{Primary: present.NewInline("#3 Archiv"), Trailing: present.NewInline("done"), TrailingStyle: present.StatusDone, Secondary: present.NewInline("Anna"), Quiet: true},
	}}
	const width = 32
	got := pane(c, width, 12, true, ansi.WcWidth)
	for i, line := range got {
		want := 0
		if i == 3 || i == 4 {
			want = width
		}
		if n := reversedCells(line); n != want {
			t.Errorf("line %d (%q): %d cells reversed, want %d", i, ansi.Strip(line), n, want)
		}
		if w := ansi.WcWidth.StringWidth(line); w != width {
			t.Errorf("line %d is %d cells wide", i, w)
		}
	}
	if !strings.HasPrefix(ansi.Strip(got[3]), "> #2 Catering") {
		t.Fatalf("the marker is gone from the bar: %q", ansi.Strip(got[3]))
	}
	for _, i := range []int{3, 4} {
		_, params := cellAttrs(got[i])
		if params[2] || params[33] || params[36] || params[32] {
			t.Fatalf("line %d: faint or a tone inside the bar: %v in %q", i, params, got[i])
		}
	}
	if _, params := cellAttrs(got[3]); !params[1] {
		t.Fatalf("the active entry lost its bold in the bar: %q", got[3])
	}
	if _, params := cellAttrs(got[4]); params[1] {
		t.Fatalf("the second line is bold in the bar: %q", got[4])
	}
	// The rows outside the bar keep their styles.
	if !strings.Contains(got[1], "\x1b[36m") || !strings.Contains(got[2], faint+"Ben") {
		t.Fatalf("a row outside the bar lost its style: %q %q", got[1], got[2])
	}
}

// A pane without the focus keeps the marker alone, as before.
func TestAnUnfocusedPaneMarksItsCursorWithoutABar(t *testing.T) {
	c := present.Column{Title: present.NewInline("Organisations"), Cursor: 0, Rows: []present.Row{
		entry("Nordlicht GmbH", "30", "", true),
		entry("Website", "2", "", false),
	}}
	got := pane(c, 24, 4, false, ansi.WcWidth)
	for i, line := range got {
		if n := reversedCells(line); n != 0 {
			t.Fatalf("line %d reversed in an unfocused pane: %q", i, line)
		}
	}
	if !strings.HasPrefix(ansi.Strip(got[1]), "> Nordlicht") {
		t.Fatalf("the marker: %q", ansi.Strip(got[1]))
	}
}

// A bar is the pane's width also when its text does not fit (the marker
// dropped, the text cut), in compact mode, and on a row without a value.
func TestTheBarFillsThePaneInEveryLayout(t *testing.T) {
	cases := map[string]struct {
		c             present.Column
		width, height int
		lines         []int
	}{
		"overflow": {present.Column{Cursor: 0, Rows: []present.Row{entry("A very long organisation name", "12345", "", false)}}, 10, 3, []int{1}},
		"compact":  {present.Column{Cursor: 1, Rows: []present.Row{entry("a", "", "b", false), entry("c", "", "d", false)}}, 12, 5, []int{2}},
		"heading":  {present.Column{Cursor: 0, Rows: []present.Row{entry("Inbox", "", "", false)}}, 12, 3, []int{1}},
		"wide":     {present.Column{Cursor: 0, Rows: []present.Row{entry("x", "1", "", false)}}, 90, 3, []int{1}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := pane(tc.c, tc.width, tc.height, true, ansi.WcWidth)
			for _, i := range tc.lines {
				if n := reversedCells(got[i]); n != tc.width {
					t.Fatalf("line %d: %d of %d cells reversed: %q", i, n, tc.width, got[i])
				}
			}
		})
	}
}
