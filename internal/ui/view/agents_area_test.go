package view

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/present"
)

// C9b: the Agents area stands under the organisations while an agent
// exists, at most half the navigation high, and scrolls beyond; A puts
// the focus into it (three columns), the agents in the list's place (two)
// or on a screen of their own (one).

func agentRows(n int, second bool) []present.Row {
	rows := make([]present.Row, n)
	for i := range rows {
		rows[i] = present.Row{Primary: present.NewInline(fmt.Sprintf("#%d Task %d", i+1, i+1)), Marked: true, Mark: present.NewInline("⣿"), MarkStyle: present.AgentWaiting}
		if second {
			rows[i].Secondary = present.NewInline("Other Org")
		}
	}
	return rows
}

func areaScreen(rows []present.Row, cursor int) present.Screen {
	return present.Screen{
		Context: present.NewInline("Organisations"),
		Crumbs:  [3]present.Inline{present.NewInline("Organisations"), present.NewInline("Inbox"), present.NewInline("Details")},
		Chooser: present.Column{Title: present.NewInline("Organisations"), Cursor: 1, Rows: []present.Row{
			{Primary: present.NewInline("All organisations")}, {Primary: present.NewInline("  Inbox"), Trailing: present.NewInline("3")},
		}},
		List:   present.Column{Title: present.NewInline("Inbox"), Cursor: -1, Text: present.NewBody("list")},
		Object: present.Column{Title: present.NewInline("Details"), Cursor: -1, Text: present.NewBody("details")},
		Agents: present.Column{Title: present.NewInline("Agents"), Cursor: cursor, Rows: rows},
	}
}

func geometryOf(width, height int) Geometry {
	g := Geometry{Width: width, Height: height, BodyHeight: height - 3, HelpHeight: 1}
	switch {
	case width >= 120:
		g.Columns, g.NavigationWidth = 3, 26
		g.ListWidth = (width - 26) * 2 / 5
		g.ObjectWidth = width - 26 - g.ListWidth
	case width >= 80:
		g.Columns, g.ListWidth = 2, width*2/5
		g.ObjectWidth = width - g.ListWidth
	default:
		g.Columns, g.ListWidth = 1, width
	}
	return g
}

// column cuts a pane's cells out of the rendered body lines.
func bodyColumn(out string, from, width, top, height int) []string {
	lines := strings.Split(out, "\n")
	var got []string
	for i := top; i < top+height && i < len(lines); i++ {
		cells := []rune(ansi.Strip(lines[i]))
		got = append(got, string(cells[from:min(len(cells), from+width)]))
	}
	return got
}

func TestTheAreaStandsUnderTheOrganisations(t *testing.T) {
	t.Parallel()

	g := geometryOf(120, 24) // body 21
	out := Render(areaScreen(agentRows(2, false), -1), g, Navigation, ansi.WcWidth)
	nav := bodyColumn(out, 0, 26, 2, g.BodyHeight)
	// Two rows of one line: a frame of four lines at the bottom.
	area := AgentsHeight(areaScreen(nil, -1).Chooser, areaScreen(agentRows(2, false), -1).Agents, g.BodyHeight, false)
	if area != 4 {
		t.Fatalf("area of %d lines", area)
	}
	if !strings.HasPrefix(nav[0], "╭─ Organisations") || !strings.HasPrefix(nav[len(nav)-area], "╭─ Agents") {
		t.Fatalf("%s", strings.Join(nav, "\n"))
	}
	if !strings.Contains(nav[len(nav)-3], "⣿ #1 Task 1") || !strings.Contains(nav[len(nav)-2], "⣿ #2 Task 2") {
		t.Fatalf("%s", strings.Join(nav, "\n"))
	}
	if !strings.HasPrefix(nav[len(nav)-area-1], "╰") {
		t.Fatalf("the organisations' frame does not end above the area:\n%s", strings.Join(nav, "\n"))
	}
	// Without agents the organisations take the whole column.
	none := Render(areaScreen(nil, -1), g, Navigation, ansi.WcWidth)
	if strings.Contains(none, "Agents") {
		t.Fatal("an area without agents")
	}
}

// At most half the column high; beyond, it scrolls with its cursor.
func TestTheAreaIsAtMostHalfTheColumnAndScrolls(t *testing.T) {
	t.Parallel()

	g := geometryOf(120, 24)
	rows := agentRows(20, true)
	c := areaScreen(rows, 17).Agents
	if got := AgentsHeight(areaScreen(nil, -1).Chooser, c, g.BodyHeight, false); got != g.BodyHeight/2 {
		t.Fatalf("area of %d lines in a body of %d", got, g.BodyHeight)
	}
	out := Render(areaScreen(rows, 17), g, Agents, ansi.WcWidth)
	nav := bodyColumn(out, 0, 26, 2, g.BodyHeight)
	joined := strings.Join(nav, "\n")
	if !strings.Contains(joined, "> ⣿ #18 Task 18") {
		t.Fatalf("the cursor is out of sight:\n%s", joined)
	}
	if strings.Contains(joined, "#1 Task 1 ") {
		t.Fatalf("the first row is still in sight:\n%s", joined)
	}
	// The focus is the area's: its frame stands out, the organisations'
	// does not.
	lines := strings.Split(out, "\n")
	top := 2 + g.BodyHeight - g.BodyHeight/2
	if _, params := cellsWith(lines[top], 34, 39); !params[1] {
		t.Errorf("the area's frame is not focused: %q", lines[top])
	}
	if _, params := cellsWith(lines[2], 34, 39); params[34] {
		t.Errorf("the organisations' frame is focused: %q", lines[2])
	}
}

// In two columns the agents stand under the organisations, as in three,
// beside the object pane; in one a screen of their own; no line is wider than the terminal in any layout, and every
// line of the body is its width.
func TestTheAgentsInTwoColumnsAndOne(t *testing.T) {
	t.Parallel()

	for _, width := range []int{60, 80, 100, 120, 160} {
		g := geometryOf(width, 24)
		for _, focus := range []Pane{Navigation, List, Object, Agents} {
			if focus == Object && g.Columns == 3 {
				continue
			}
			out := Render(areaScreen(agentRows(5, true), 2), g, focus, ansi.WcWidth)
			for i, line := range strings.Split(out, "\n") {
				body := i >= 2 && i < 2+g.BodyHeight
				if w := ansi.WcWidth.StringWidth(line); w > width || (body && w != width) {
					t.Fatalf("%d, focus %d, line %d: %d cells: %q", width, focus, i, w, ansi.Strip(line))
				}
			}
			if focus != Agents {
				continue
			}
			column := bodyColumn(out, 0, ColumnWidth(g, Agents), 2, g.BodyHeight)
			switch {
			case g.Columns == 1 && !strings.HasPrefix(column[0], "╭─ Agents"):
				t.Errorf("%d: the agents are not alone: %q", width, column[0])
			case g.Columns == 2 && (!strings.HasPrefix(column[0], "╭─ Organisations") || !slices.ContainsFunc(column, func(l string) bool { return strings.HasPrefix(l, "╭─ Agents") })):
				t.Errorf("%d: the agents are not under the organisations:\n%s", width, strings.Join(column, "\n"))
			}
			// One column names the agents; in two and three the path
			// stays where the person is, the organisations in sight.
			if got := contextLine(areaScreen(nil, 0), g, Agents); (g.Columns == 1) != (got == "Agents") {
				t.Errorf("%d: path %q", width, got)
			}
			if g.Columns == 1 && strings.Contains(out, "Details") {
				t.Errorf("%d: one column shows more than the agents", width)
			}
			if g.Columns == 2 && !strings.Contains(out, "Details") {
				t.Errorf("%d: two columns lost the object pane", width)
			}
		}
	}
}

// Rows with a second line keep it in a short area: whether rows take two
// lines is decided by the half of the column the area may take, not by
// the few lines two agents need (a list's compact mode would drop the
// organisation and leave the lines it was measured for empty).
func TestTheAreasRowsKeepTheirSecondLine(t *testing.T) {
	t.Parallel()

	g := geometryOf(100, 24) // body 21, half 10
	s := areaScreen(agentRows(2, true), -1)
	if got := AgentsHeight(s.Chooser, s.Agents, g.BodyHeight, false); got != 6 {
		t.Fatalf("area of %d lines", got)
	}
	out := Render(s, g, Navigation, ansi.WcWidth)
	nav := bodyColumn(out, 0, ColumnWidth(g, Navigation), 2, g.BodyHeight)
	area := nav[len(nav)-6:]
	want := []string{"╭─ Agents", "│   ⣿ #1 Task 1", "│     Other Org", "│   ⣿ #2 Task 2", "│     Other Org", "╰"}
	for i, w := range want {
		if !strings.HasPrefix(area[i], w) {
			t.Fatalf("line %d: %q, want %q\n%s", i, area[i], w, strings.Join(area, "\n"))
		}
	}
	if got := AgentsCapacity(s.Chooser, s.Agents, g.BodyHeight); got != 2 {
		t.Fatalf("capacity %d", got)
	}
	// Ten agents: half the column while the chooser has the focus; all
	// but a quarter while the agents have it, which is when their
	// capacity counts: seven rows of two lines in sight.
	ten := areaScreen(agentRows(10, true), 9).Agents
	if got, focused, capacity := AgentsHeight(s.Chooser, ten, g.BodyHeight, false), AgentsHeight(s.Chooser, ten, g.BodyHeight, true), AgentsCapacity(s.Chooser, ten, g.BodyHeight); got != 10 || focused != 16 || capacity != 7 {
		t.Fatalf("height %d, focused %d, capacity %d", got, focused, capacity)
	}
	// A short terminal: compact rows, as in any list.
	short := geometryOf(100, 14) // body 11, half 5
	if got, focused, capacity := AgentsHeight(s.Chooser, ten, short.BodyHeight, false), AgentsHeight(s.Chooser, ten, short.BodyHeight, true), AgentsCapacity(s.Chooser, ten, short.BodyHeight); got != 5 || focused != 8 || capacity != 6 {
		t.Fatalf("short: height %d, focused %d, capacity %d", got, focused, capacity)
	}
}

// Review of #36 on bdded98: in a low pane with a notice in the chooser,
// the area leaves the chooser its frame, its notice and one entry.
func TestTheAreaLeavesTheChooserAnEntry(t *testing.T) {
	t.Parallel()

	s := areaScreen(agentRows(2, false), -1)
	s.Chooser.Notice = present.NewInline("Inbox is not supported.")
	for height := 4; height <= 12; height++ {
		area := AgentsHeight(s.Chooser, s.Agents, height, false)
		if area > 0 && height-area < frameLines+2 {
			t.Fatalf("height %d: the area takes %d, the chooser keeps %d", height, area, height-area)
		}
		if area > 0 && ChooserCapacity(s.Chooser, s.Agents, height) < 1 {
			t.Fatalf("height %d: no entry of the chooser in sight", height)
		}
	}
	g := geometryOf(120, 9) // body 6
	out := Render(s, g, Navigation, ansi.WcWidth)
	if !strings.Contains(out, "All organisations") {
		t.Fatalf("no entry of the chooser:\n%s", ansi.Strip(out))
	}
}

// FocusedCapacity promises as many agents as the screen draws, in every
// layout the agents' list shows in: beside the chooser in three columns,
// in the list's place in two, alone in one. Its split was once worked out
// in the interface apart from the drawing, and the two drifted.
func TestFocusedCapacityIsWhatTheAgentsPaneDraws(t *testing.T) {
	t.Parallel()

	for _, size := range [][2]int{{60, 30}, {100, 30}, {130, 30}, {130, 14}, {100, 14}} {
		g := geometryOf(size[0], size[1])
		s := areaScreen(agentRows(20, true), 0)
		want := FocusedCapacity(s.Agents, s.Chooser, s.Agents, g, Agents)
		out := Render(s, g, Agents, ansi.WcWidth)
		if got := strings.Count(ansi.Strip(out), " Task "); got != want {
			t.Errorf("%dx%d (%d columns): %d agents drawn, capacity %d\n%s", size[0], size[1], g.Columns, got, want, out)
		}
	}
}
