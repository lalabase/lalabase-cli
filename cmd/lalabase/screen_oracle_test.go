package main

import (
	"reflect"
	"testing"
)

// The screen plays what the renderer writes: a full draw, then a changed
// cell, a deleted run, an inserted run, a scrolled region.
func TestTheScreenFollowsTheRenderersEdits(t *testing.T) {
	out := "\x1b[H\x1b[2Jfirst line\r\nsecond\r\nthird"
	if got := renderScreen(out, 4, 12); !reflect.DeepEqual(got, []string{"first line", "second", "third", ""}) {
		t.Fatalf("draw: %q", got)
	}
	// One changed cell: the marker of a cursor row.
	out += "\x1b[2;1H>"
	if got := renderScreen(out, 4, 12); got[1] != ">econd" {
		t.Fatalf("cell: %q", got)
	}
	// Delete two cells, insert one blank, erase to the end of the line.
	out += "\x1b[1;1H\x1b[2P"
	if got := renderScreen(out, 4, 12); got[0] != "rst line" {
		t.Fatalf("delete: %q", got)
	}
	out += "\x1b[1;4H\x1b[@"
	if got := renderScreen(out, 4, 12); got[0] != "rst  line" {
		t.Fatalf("insert: %q", got)
	}
	out += "\x1b[1;5H\x1b[K"
	if got := renderScreen(out, 4, 12); got[0] != "rst" {
		t.Fatalf("erase: %q", got)
	}
	// A scrolling region of the last three lines, scrolled up by one.
	out += "\x1b[2;4r\x1b[S\x1b[r"
	if got := renderScreen(out, 4, 12); !reflect.DeepEqual(got, []string{"rst", "third", "", ""}) {
		t.Fatalf("scroll: %q", got)
	}
	// Styles and modes change no cell; a wide rune takes two.
	out = "\x1b[H\x1b[2J\x1b[1mab\x1b[m\x1b[?25l界c"
	if got := renderScreen(out, 1, 8); got[0] != "ab界c" {
		t.Fatalf("styles and width: %q", got)
	}
	if !screenHas([]string{"ab界c"}, "b界") || screenHas([]string{"ab"}, "ac") {
		t.Fatal("screenHas")
	}
}
