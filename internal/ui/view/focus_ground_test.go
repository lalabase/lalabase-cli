package view

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// The focused pane stands on a ground a little lighter than black: each
// of its lines starts on it, takes it up again after every reset inside
// and gives it back at its end. The other panes have none.
func TestTheFocusedPaneHasItsGround(t *testing.T) {
	g := geometryOf(120, 24)
	s := areaScreen(agentRows(2, true), 0)
	for _, focus := range []Pane{Navigation, List, Agents} {
		out := Render(s, g, focus, ansi.WcWidth)
		grounds := strings.Count(out, focusGround)
		if grounds == 0 {
			t.Fatalf("focus %d: no ground", focus)
		}
		for i, line := range strings.Split(out, "\n") {
			if i < 2 || i >= 2+g.BodyHeight {
				if strings.Contains(line, focusGround) {
					t.Fatalf("focus %d, line %d outside the body is on the ground", focus, i)
				}
				continue
			}
			// Every reset inside a grounded run is followed by the ground.
			for _, run := range strings.Split(line, "\x1b[49m") {
				at := strings.Index(run, focusGround)
				if at < 0 {
					continue
				}
				rest := run[at:]
				if n := strings.Count(rest, "\x1b[m"); n != strings.Count(rest, "\x1b[m"+focusGround) {
					t.Fatalf("focus %d, line %d: a reset ends the ground: %q", focus, i, rest)
				}
			}
		}
	}
	// Three columns, the list focused: its column alone is grounded.
	out := Render(s, g, List, ansi.WcWidth)
	line := strings.Split(out, "\n")[3]
	at, end := strings.Index(line, focusGround), strings.LastIndex(line, "\x1b[49m")
	if at < 0 || end < at {
		t.Fatalf("no ground: %q", line)
	}
	if from, to := ansi.StringWidth(line[:at]), ansi.StringWidth(line[:end]); from != g.NavigationWidth || to != g.NavigationWidth+g.ListWidth {
		t.Fatalf("ground from cell %d to %d, want the list's %d to %d", from, to, g.NavigationWidth, g.NavigationWidth+g.ListWidth)
	}
}

func TestOnFocusGroundTakesTheGroundUpAfterResets(t *testing.T) {
	got := onFocusGround([]string{"a\x1b[1mb\x1b[mc\x1b[0md\x1b[100me\x1b[49mf"})[0]
	want := focusGround + "a\x1b[1mb\x1b[m" + focusGround + "c\x1b[m" + focusGround + "d\x1b[100me" + focusGround + "f\x1b[49m"
	if got != want {
		t.Fatalf("%q\nwant %q", got, want)
	}
}
