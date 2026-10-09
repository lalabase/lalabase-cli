package view

import (
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/present"
)

func entry(primary, trailing, secondary string, active bool) present.Row {
	return present.Row{Primary: present.NewInline(primary), Trailing: present.NewInline(trailing), Secondary: present.NewInline(secondary), Active: active}
}

// A row is a marker, the first line with its value at the end, and the
// second line indented; the cursor row is marked, the active one bold.
func TestRowsRenderMarkerValueAndSecondLine(t *testing.T) {
	c := present.Column{
		Title: present.NewInline("Organisations"),
		Rows: []present.Row{
			entry("All organisations", "33", "", false),
			entry("Nordlicht GmbH", "30", "", true),
			entry("A very long organisation name that does not fit", "?", "", false),
		},
		Cursor: 1,
	}
	got := pane(c, 24, 6, false, ansi.WcWidth)
	want := []string{
		"Organisations           ",
		"  All organisations   33",
		"\x1b[1m> Nordlicht GmbH      30\x1b[0m",
		"  A very long organis… ?",
		"                        ",
		"                        ",
	}
	for i := range want {
		if ansi.Strip(got[i]) != ansi.Strip(want[i]) || (i == 2 && !strings.Contains(got[i], "\x1b[1m")) {
			t.Errorf("line %d: %q, want %q", i, got[i], want[i])
		}
		if w := ansi.WcWidth.StringWidth(got[i]); w != 24 {
			t.Errorf("line %d is %d cells wide", i, w)
		}
	}
	two := present.Column{Title: present.NewInline("Inbox"), Rows: []present.Row{
		entry("#1 Login bricht ab", "", "Ben Berger · Website · 2026-01-05 09:30", false),
		entry("#2 Zweites", "", "Anna Albers · No project · 2026-01-05 09:31", false),
	}, Cursor: 0}
	got = pane(two, 30, 12, false, ansi.WcWidth)
	if ansi.Strip(got[1]) != "> #1 Login bricht ab          " || ansi.Strip(got[2]) != "  Ben Berger · Website · 2026…" {
		t.Fatalf("two-line rows: %q", got[1:3])
	}
	if ansi.Strip(got[3]) != "  #2 Zweites                  " {
		t.Fatalf("second row: %q", got[3])
	}
	// Below the compact threshold the second line is left out.
	got = pane(two, 30, 6, false, ansi.WcWidth)
	if ansi.Strip(got[1]) != "> #1 Login bricht ab          " || ansi.Strip(got[2]) != "  #2 Zweites                  " {
		t.Fatalf("compact rows: %q", got[1:3])
	}
}

// The cursor stays in sight: the offset follows it down and up, and an
// offset beyond the end is clamped. A notice takes one line of the body.
func TestRowsKeepTheCursorInSightAndCountTheNotice(t *testing.T) {
	var list []present.Row
	for i := range 10 {
		list = append(list, entry(string(rune('a'+i)), "", "", false))
	}
	c := present.Column{Title: present.NewInline("T"), Rows: list, Cursor: 7, Offset: 0}
	got := pane(c, 10, 5, false, ansi.WcWidth) // title + 4 rows
	if ansi.Strip(got[4]) != "> h       " || ansi.Strip(got[1]) != "  e       " {
		t.Fatalf("cursor below: %q", got)
	}
	c.Cursor, c.Offset = 1, 6
	got = pane(c, 10, 5, false, ansi.WcWidth)
	if ansi.Strip(got[1]) != "  a       " && ansi.Strip(got[1]) != "> b       " {
		t.Fatalf("cursor above: %q", got)
	}
	c.Cursor, c.Offset = 9, 100
	got = pane(c, 10, 5, false, ansi.WcWidth)
	if ansi.Strip(got[4]) != "> j       " {
		t.Fatalf("offset beyond the end: %q", got)
	}
	c.Notice, c.Cursor, c.Offset = present.NewInline("Loading…"), 0, 0
	got = pane(c, 10, 5, false, ansi.WcWidth)
	if ansi.Strip(got[1]) != "Loading…  " || ansi.Strip(got[2]) != "> a       " || ansi.Strip(got[4]) != "  c       " {
		t.Fatalf("notice: %q", got)
	}
	g := Geometry{BodyHeight: 5}
	if Capacity(c, g) != 3 || Capacity(present.Column{Rows: list}, g) != 4 {
		t.Fatalf("capacity %d / %d", Capacity(c, g), Capacity(present.Column{Rows: list}, g))
	}
	twoLine := present.Column{Rows: []present.Row{entry("a", "", "b", false)}}
	if Capacity(twoLine, Geometry{BodyHeight: 11}) != 5 || Capacity(twoLine, Geometry{BodyHeight: 6}) != 5 {
		t.Fatalf("two-line capacity %d / %d", Capacity(twoLine, Geometry{BodyHeight: 11}), Capacity(twoLine, Geometry{BodyHeight: 6}))
	}
	if Capacity(c, Geometry{BodyHeight: 1}) != 0 {
		t.Fatal("capacity without a body")
	}
}

// Free text scrolls by its offset; the last line is kept in sight and an
// overflow is marked.
func TestTextScrollsByOffset(t *testing.T) {
	c := present.Column{Title: present.NewInline("Details"), Text: present.NewBody("one\ntwo\nthree\nfour\nfive")}
	got := pane(c, 8, 4, false, ansi.WcWidth)
	if !reflect.DeepEqual(ansiStripAll(got), []string{"Details ", "one     ", "two     ", "three…  "}) {
		t.Fatalf("top: %q", got)
	}
	c.Offset = 3
	got = pane(c, 8, 4, false, ansi.WcWidth)
	if !reflect.DeepEqual(ansiStripAll(got), []string{"Details ", "three   ", "four    ", "five    "}) {
		t.Fatalf("clamped offset: %q", got)
	}
	c.Offset = 1
	got = pane(c, 8, 4, false, ansi.WcWidth)
	if !reflect.DeepEqual(ansiStripAll(got), []string{"Details ", "two     ", "three   ", "four…   "}) {
		t.Fatalf("offset 1: %q", got)
	}
}

func ansiStripAll(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = ansi.Strip(l)
	}
	return out
}
