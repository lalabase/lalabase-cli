package view

import (
	"reflect"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/present"
)

// boldIn is whether bold is set somewhere in a line.
func boldIn(line string) bool {
	_, params := cellsWith(line, 1, 22)
	return params[1]
}

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
	got := content(c, 24, 5, false, ansi.WcWidth)
	want := []string{
		"  All organisations   33",
		"\x1b[1;38;2;241;243;245m> Nordlicht GmbH      30\x1b[m",
		"  A very long organis… ?",
		"                        ",
		"                        ",
	}
	for i := range want {
		if ansi.Strip(got[i]) != ansi.Strip(want[i]) || (i == 1 && !boldIn(got[i])) {
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
	got = content(two, 30, 11, false, ansi.WcWidth)
	if ansi.Strip(got[0]) != "> #1 Login bricht ab          " || ansi.Strip(got[1]) != "  Ben Berger · Website · 2026…" {
		t.Fatalf("two-line rows: %q", got[0:2])
	}
	if ansi.Strip(got[2]) != "  #2 Zweites                  " {
		t.Fatalf("second row: %q", got[2])
	}
	// Below the compact threshold the second line is left out.
	got = content(two, 30, 5, false, ansi.WcWidth)
	if ansi.Strip(got[0]) != "> #1 Login bricht ab          " || ansi.Strip(got[1]) != "  #2 Zweites                  " {
		t.Fatalf("compact rows: %q", got[0:2])
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
	got := content(c, 10, 4, false, ansi.WcWidth) // 4 rows
	if ansi.Strip(got[3]) != "> h       " || ansi.Strip(got[0]) != "  e       " {
		t.Fatalf("cursor below: %q", got)
	}
	c.Cursor, c.Offset = 1, 6
	got = content(c, 10, 4, false, ansi.WcWidth)
	if ansi.Strip(got[0]) != "  a       " && ansi.Strip(got[0]) != "> b       " {
		t.Fatalf("cursor above: %q", got)
	}
	c.Cursor, c.Offset = 9, 100
	got = content(c, 10, 4, false, ansi.WcWidth)
	if ansi.Strip(got[3]) != "> j       " {
		t.Fatalf("offset beyond the end: %q", got)
	}
	c.Notice, c.Cursor, c.Offset = present.NewInline("Loading…"), 0, 0
	got = content(c, 10, 4, false, ansi.WcWidth)
	if ansi.Strip(got[0]) != "Loading…  " || ansi.Strip(got[1]) != "> a       " || ansi.Strip(got[3]) != "  c       " {
		t.Fatalf("notice: %q", got)
	}
	// The frame takes two lines of the body; the title stands in it.
	g := Geometry{BodyHeight: 6}
	if Capacity(c, g) != 3 || Capacity(present.Column{Rows: list}, g) != 4 {
		t.Fatalf("capacity %d / %d", Capacity(c, g), Capacity(present.Column{Rows: list}, g))
	}
	twoLine := present.Column{Rows: []present.Row{entry("a", "", "b", false)}}
	if Capacity(twoLine, Geometry{BodyHeight: 12}) != 5 || Capacity(twoLine, Geometry{BodyHeight: 7}) != 5 {
		t.Fatalf("two-line capacity %d / %d", Capacity(twoLine, Geometry{BodyHeight: 12}), Capacity(twoLine, Geometry{BodyHeight: 7}))
	}
	if Capacity(c, Geometry{BodyHeight: 3}) != 0 || Capacity(present.Column{Rows: list}, Geometry{BodyHeight: 2}) != 0 {
		t.Fatal("capacity without a body")
	}
}

// Free text scrolls by its offset; the last line is kept in sight and an
// overflow is marked. The text keeps the last column free for the bar.
func TestTextScrollsByOffset(t *testing.T) {
	c := present.Column{Title: present.NewInline("Details"), Text: present.NewBody("one\ntwo\nthree\nfour\nfive")}
	got := content(c, 8, 3, false, ansi.WcWidth)
	if !reflect.DeepEqual(ansiStripAll(got), []string{"one    \u2503", "two    \u2503", "three… \u254e"}) {
		t.Fatalf("top: %q", ansiStripAll(got))
	}
	c.Offset = 3
	got = content(c, 8, 3, false, ansi.WcWidth)
	if !reflect.DeepEqual(ansiStripAll(got), []string{"three  \u254e", "four   \u2503", "five   \u2503"}) {
		t.Fatalf("clamped offset: %q", ansiStripAll(got))
	}
	c.Offset = 1
	got = content(c, 8, 3, false, ansi.WcWidth)
	if !reflect.DeepEqual(ansiStripAll(got), []string{"two    \u2503", "three  \u2503", "four…  \u254e"}) {
		t.Fatalf("offset 1: %q", ansiStripAll(got))
	}
}

func ansiStripAll(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = ansi.Strip(l)
	}
	return out
}
