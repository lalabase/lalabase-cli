package ui

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/gboyke/lalabase-cli/internal/api"
)

// The pane's bound counts characters, never bytes: a text of the bound in
// letters of two bytes is whole, a server's cut of it stays the server's,
// and a cut never splits a character (review round 1 of #48, finding 2).
func TestShownCutsAtTheBoundInCharacters(t *testing.T) {
	const limit = 8
	umlauts := strings.Repeat("ä", limit)
	cases := []struct {
		name  string
		raw   string
		uncut bool
		text  string
		short shortening
		limit int // limit when zero
	}{
		{"one below", "abcdefg", false, "abcdefg", notShortened, 0},
		{"at the bound", "abcdefgh", false, "abcdefgh", notShortened, 0},
		{"one past", "abcdefghi", false, "abcdefgh", byView, 0},
		{"two bytes each, at the bound", umlauts, false, umlauts, notShortened, 0},
		{"two bytes each, one past", umlauts + "ö", false, umlauts, byView, 0},
		{"the server's cut at the bound", umlauts + api.TruncationMarker, false, umlauts, byServer, 0},
		{"the server's cut past the bound", umlauts + "ö" + api.TruncationMarker, false, umlauts, byView, 0},
		{"a typed marker over REST", "abc" + api.TruncationMarker, true, "abc" + api.TruncationMarker, notShortened, 32},
		{"a typed marker past the bound over REST", umlauts + api.TruncationMarker, true, umlauts, byView, 0},
	}
	for _, c := range cases {
		bound := limit
		if c.limit > 0 {
			bound = c.limit
		}
		text, short := shown(c.raw, bound, c.uncut)
		if text != c.text || short != c.short || !utf8.ValidString(text) {
			t.Errorf("%s: %q %v, want %q %v", c.name, text, short, c.text, c.short)
		}
	}
	// At the pane's own bound, in letters of three bytes.
	page := strings.Repeat("€", maxPageText)
	if text, short := shown(page, maxPageText, false); text != page || short != notShortened {
		t.Fatalf("%d characters cut to %d", maxPageText, utf8.RuneCountInString(text))
	}
	if text, short := shown(page+"€", maxPageText, false); text != page || short != byView {
		t.Fatalf("%d characters cut to %d (%v)", maxPageText+1, utf8.RuneCountInString(text), short)
	}
}
