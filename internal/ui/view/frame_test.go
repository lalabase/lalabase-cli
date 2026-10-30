package view

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/present"
)

// A pane stands in a rounded frame, its title in the top border, its text
// inside the sides; every line is the pane's width, the frame's height.
func TestAPaneStandsInAFrameWithItsTitleInTheBorder(t *testing.T) {
	t.Parallel()

	c := present.Column{Title: present.NewInline("Tickets"), Cursor: -1, Rows: []present.Row{entry("#1 Login", "", "", false)}}
	got := pane(c, 20, 4, false, ansi.WcWidth)
	want := []string{
		"╭─ Tickets ────────╮",
		"│   #1 Login       │",
		"│                  │",
		"╰──────────────────╯",
	}
	if got := ansiStripAll(got); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("frame:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// A title wider than the border is cut, never pushes the corner out;
	// a border without room for one has none.
	for _, width := range []int{5, 6, 7, 8, 12} {
		c.Title = present.NewInline("A very long title of the pane")
		lines := pane(c, width, 4, true, ansi.WcWidth)
		for i, line := range lines {
			if w := ansi.WcWidth.StringWidth(line); w != width {
				t.Errorf("width %d, line %d is %d cells: %q", width, i, w, ansi.Strip(line))
			}
		}
		top := []rune(ansi.Strip(lines[0]))
		if string(top[0]) != frameTopLeft || string(top[len(top)-1]) != frameTopRight {
			t.Errorf("width %d: the top border's corners: %q", width, string(top))
		}
	}
	// A pane too small for a frame is its text alone.
	if got := pane(c, 4, 4, false, ansi.WcWidth); strings.ContainsAny(ansi.Strip(strings.Join(got, "")), frameSide+frameTopLeft) {
		t.Fatalf("a frame in two cells: %q", ansiStripAll(got))
	}
	if got := pane(c, 20, 2, false, ansi.WcWidth); len(got) != 2 || strings.ContainsAny(ansi.Strip(strings.Join(got, "")), frameSide+frameTopLeft) {
		t.Fatalf("a frame in two lines: %q", ansiStripAll(got))
	}
}

// The focused pane's frame is blue and bold, its title bold; the others'
// frames are faint, their titles plain. The frame never takes the text's
// styles.
func TestTheFocusedPanesFrameStandsOut(t *testing.T) {
	t.Parallel()

	c := present.Column{Title: present.NewInline("Tickets"), Cursor: -1, Text: present.NewBody("text")}
	focused := pane(c, 20, 4, true, ansi.WcWidth)
	rest := pane(c, 20, 4, false, ansi.WcWidth)
	for _, i := range []int{0, 1, 3} {
		if _, params := cellsWith(focused[i], 34, 39); !params[1] {
			t.Errorf("focused, line %d: the frame is not blue and bold: %q", i, focused[i])
		}
		if _, params := cellsWith(rest[i], 2, 22); len(params) == 0 || params[34] {
			t.Errorf("unfocused, line %d: the frame is not faint: %q", i, rest[i])
		}
	}
	if !strings.Contains(focused[0], bold+"Tickets") || strings.Contains(rest[0], bold+"Tickets") {
		t.Fatalf("the titles: %q / %q", focused[0], rest[0])
	}
	if b, _ := cellsWith(focused[1], 34, 39); b[1] || b[2] {
		t.Fatalf("the text took the frame's colour: %q", focused[1])
	}
}

// A quiet row keeps its value's tone, faint: a done ticket is drawn faint
// and its status still in the colour of its meaning. A row that is not
// quiet draws its tone at full strength.
func TestAQuietRowKeepsItsToneFaint(t *testing.T) {
	t.Parallel()

	c := present.Column{Cursor: -1, Rows: []present.Row{
		{Primary: present.NewInline("#3 Archiv"), Trailing: present.NewInline("done"), TrailingStyle: present.StatusDone, Quiet: true},
		{Primary: present.NewInline("#4 Login"), Trailing: present.NewInline("open"), TrailingStyle: present.StatusOpen},
	}}
	got := content(c, 30, 2, false, ansi.WcWidth)
	if !strings.HasPrefix(got[0], faint) {
		t.Fatalf("the quiet row is not faint: %q", got[0])
	}
	if _, params := cellsWith(got[0], 32, 39); !params[2] {
		t.Fatalf("the quiet status is not green and faint: %q", got[0])
	}
	if _, params := cellsWith(got[1], 36, 39); params[2] || strings.HasPrefix(got[1], faint) {
		t.Fatalf("the open row is faint: %q", got[1])
	}
}

// The panes of a screen stand side by side in their frames and fill its
// width exactly, in every layout; only the focused one is blue.
func TestTheFramedPanesFillTheWidth(t *testing.T) {
	t.Parallel()

	s := present.Screen{
		Chooser: present.Column{Title: present.NewInline("Organisations"), Cursor: -1, Text: present.NewBody("a")},
		List:    present.Column{Title: present.NewInline("Tickets"), Cursor: -1, Text: present.NewBody("b")},
		Object:  present.Column{Title: present.NewInline("Ticket"), Cursor: -1, Text: present.NewBody("c")},
		Help:    []present.KeyHelp{{Key: present.NewInline("q"), Help: present.NewInline("quit")}},
	}
	for _, g := range []Geometry{
		{Width: 130, Height: 12, BodyHeight: 9, Columns: 3, NavigationWidth: 26, ListWidth: 41, ObjectWidth: 63},
		{Width: 90, Height: 12, BodyHeight: 9, Columns: 2, ListWidth: 36, ObjectWidth: 54},
		{Width: 60, Height: 12, BodyHeight: 9, Columns: 1, ListWidth: 60},
	} {
		lines := strings.Split(Render(s, g, List, ansi.WcWidth), "\n")
		body := lines[2 : 2+g.BodyHeight]
		for i, line := range body {
			if w := ansi.WcWidth.StringWidth(line); w != g.Width {
				t.Errorf("%d columns, body line %d is %d cells", g.Columns, i, w)
			}
		}
		top := ansi.Strip(body[0])
		if n := strings.Count(top, frameTopLeft); n != g.Columns {
			t.Errorf("%d columns: %d frames in %q", g.Columns, n, top)
		}
		if n := strings.Count(body[0], "\x1b[1;34m"); n != 2 {
			// the focused pane's border before and after its title
			t.Errorf("%d columns: the blue border drawn %d times in %q", g.Columns, n, body[0])
		}
	}
}
