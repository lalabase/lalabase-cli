package textentry

import (
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

const (
	family  = "\U0001F468\u200d\U0001F469\u200d\U0001F467"
	flagDE  = "\U0001F1E9\U0001F1EA"
	flagFR  = "\U0001F1EB\U0001F1F7"
	eAcute  = "e\u0301"
	world   = "\u4e16\u754c"
	caret   = "|"
	ellipse = "~"
)

func filled(t *testing.T, text string, multiline bool) *Entry {
	t.Helper()
	e := New(1000, multiline)
	if e.Insert(text) {
		t.Fatalf("Insert(%q) was cut", text)
	}
	return e
}

func assertEntry(t *testing.T, e *Entry, value string, at int) {
	t.Helper()
	if got := e.Value(); got != value {
		t.Fatalf("value %q, want %q", got, value)
	}
	if e.caret != at {
		t.Fatalf("caret at %d, want %d in %q", e.caret, at, value)
	}
}

func TestTypingAndTheCaretKeys(t *testing.T) {
	e := New(100, false)
	assertEntry(t, e, "", 0)
	e.Insert("a")
	e.Insert("c")
	assertEntry(t, e, "ac", 2)
	e.Left()
	e.Insert("b")
	assertEntry(t, e, "abc", 2)
	e.Home()
	assertEntry(t, e, "abc", 0)
	e.Left()
	assertEntry(t, e, "abc", 0)
	e.Backspace()
	assertEntry(t, e, "abc", 0)
	e.Delete()
	assertEntry(t, e, "bc", 0)
	e.End()
	assertEntry(t, e, "bc", 2)
	e.Right()
	assertEntry(t, e, "bc", 2)
	e.Delete()
	assertEntry(t, e, "bc", 2)
	e.Backspace()
	assertEntry(t, e, "b", 1)
}

// The caret steps over what a person sees as one character, however many
// runes it is, and a deletion takes it whole.
func TestTheCaretAndTheDeletionsTakeWholeGraphemes(t *testing.T) {
	for _, tc := range []struct{ name, grapheme string }{
		{"combining", eAcute},
		{"family", family},
		{"flag", flagDE},
		{"wide", "\u4e16"},
		{"skin tone", "\U0001F44D\U0001F3FD"},
		{"keycap", "1\ufe0f\u20e3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runes := utf8.RuneCountInString(tc.grapheme)
			e := filled(t, "a"+tc.grapheme+"b", false)
			e.Left()
			assertEntry(t, e, "a"+tc.grapheme+"b", 1+runes)
			e.Left()
			assertEntry(t, e, "a"+tc.grapheme+"b", 1)
			e.Right()
			assertEntry(t, e, "a"+tc.grapheme+"b", 1+runes)
			e.Backspace()
			assertEntry(t, e, "ab", 1)
			e = filled(t, "a"+tc.grapheme+"b", false)
			e.Home()
			e.Right()
			e.Delete()
			assertEntry(t, e, "ab", 1)
		})
	}
}

func TestTwoFlagsAreTwoGraphemes(t *testing.T) {
	e := filled(t, flagDE+flagFR, false)
	e.Left()
	assertEntry(t, e, flagDE+flagFR, 2)
	e.Backspace()
	assertEntry(t, e, flagFR, 0)
}

// A joiner typed between two emoji makes them one grapheme, and a deletion
// can do the same: the caret never stays inside.
func TestTheCaretLeavesAGraphemeThatFormsAroundIt(t *testing.T) {
	woman, man := "\U0001F469", "\U0001F468"
	e := filled(t, woman+man, false)
	e.Left()
	e.Insert("\u200d")
	assertEntry(t, e, woman+"\u200d"+man, 3)
	e = filled(t, woman+"\u200dx"+man, false)
	e.Left()
	e.Backspace()
	assertEntry(t, e, woman+"\u200d"+man, 3)
	// A mark after the caret joins the character before it.
	e = filled(t, "ab", false)
	e.Left()
	e.Insert("\u0301")
	assertEntry(t, e, "a\u0301b", 2)
	// One half of a flag, typed before a whole one, takes its first half.
	e = filled(t, flagDE, false)
	e.Home()
	e.Insert("\U0001F1EB")
	assertEntry(t, e, "\U0001F1EB\U0001F1E9\U0001F1EA", 2)
}

func TestWhatIsInsertedPassesTheFilter(t *testing.T) {
	for _, multiline := range []bool{false, true} {
		e := New(100, multiline)
		e.Insert("a\x1b[31mb\x07c\u202ed")
		if got := e.Value(); got != "ab cd" {
			t.Fatalf("multiline %v: %q", multiline, got)
		}
	}
}

func TestLineBreaksByTheKindOfEntry(t *testing.T) {
	one := New(100, false)
	one.Insert("a\r\nb\nc")
	if got := one.Value(); got != "a b c" {
		t.Fatalf("one line: %q", got)
	}
	several := New(100, true)
	several.Insert("a\r\nb\rc")
	if got := several.Value(); got != "a\nb\nc" {
		t.Fatalf("several lines: %q", got)
	}
}

func TestTheLimitIsHard(t *testing.T) {
	e := New(3, false)
	for _, r := range "abc" {
		if e.Insert(string(r)) {
			t.Fatalf("%c was cut", r)
		}
	}
	if !e.Insert("d") {
		t.Fatal("a key past the limit was not reported")
	}
	assertEntry(t, e, "abc", 3)
	e.Home()
	if !e.Insert("x") {
		t.Fatal("a key past the limit was not reported")
	}
	assertEntry(t, e, "abc", 0)
	e.Delete()
	if e.Insert("x") {
		t.Fatal("the room a deletion left was refused")
	}
	assertEntry(t, e, "xbc", 1)
}

// A paste over the limit takes what fits and stops before the grapheme that
// does not: half an emoji is no text.
func TestAPasteOverTheLimitIsCutBetweenGraphemes(t *testing.T) {
	familyRunes := utf8.RuneCountInString(family)
	for _, tc := range []struct {
		name  string
		limit int
		paste string
		want  string
		cut   bool
	}{
		{"fits", 4, "abcd", "abcd", false},
		{"cut", 3, "abcd", "abc", true},
		{"before a family", familyRunes, "a" + family, "a", true},
		{"a family that fits", familyRunes + 1, "a" + family, "a" + family, false},
		{"a mark stays with its letter", 2, "a" + eAcute, "a", true},
		{"nothing fits", 1, family, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := New(tc.limit, false)
			if cut := e.Insert(tc.paste); cut != tc.cut {
				t.Errorf("cut %v, want %v", cut, tc.cut)
			}
			assertEntry(t, e, tc.want, utf8.RuneCountInString(tc.want))
		})
	}
}

func TestAPasteIntoTheMiddleKeepsWhatFollows(t *testing.T) {
	e := New(5, false)
	e.Insert("ab")
	e.Left()
	if !e.Insert("wxyz") {
		t.Fatal("not reported as cut")
	}
	assertEntry(t, e, "awxyb", 4)
}

func TestAPrefilledValueIsAnEntryLikeAnyOther(t *testing.T) {
	e := New(100, false)
	e.Insert("Login\x1b[2J bricht ab")
	assertEntry(t, e, "Login bricht ab", 15)
}

// plain is a drawn line without its caret.
func plain(line string) string { return strings.ReplaceAll(line, caret, "") }

func sized(t *testing.T, text string, width, height int) *Entry {
	t.Helper()
	e := filled(t, text, true)
	e.Size(width, height, ansi.WcWidth)
	return e
}

func TestWrappingAtSpacesAndInLongWords(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		width      int
		want       []string
	}{
		{"fits", "ab cd", 8, []string{"ab cd|"}},
		{"at a space", "aaa bbb ccc", 9, []string{"aaa bbb ", "ccc|"}},
		{"a word that needs its space too", "aaa bbb ccc", 8, []string{"aaa ", "bbb ccc|"}},
		{"a word too long", "abcdefghij", 5, []string{"abcd", "efgh", "ij|"}},
		{"a word after a space", "ab cdefghij", 5, []string{"ab ", "cdef", "ghij|"}},
		{"a line break", "ab\ncd", 8, []string{"ab", "cd|"}},
		{"empty lines", "a\n\nb", 8, []string{"a", "", "b|"}},
		{"a break at the end", "a\n", 8, []string{"a", "|"}},
		{"nothing", "", 8, []string{"|"}},
		{"wide characters", "\u4e16\u754c\u4e16\u754c", 5, []string{"\u4e16\u754c", "\u4e16\u754c|"}},
		{"wide characters and an odd room", "\u4e16\u754c\u4e16", 4, []string{"\u4e16", "\u754c", "\u4e16|"}},
		{"a word that fills the line", "abcd efg", 5, []string{"abcd", " efg|"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := sized(t, tc.text, tc.width, 10)
			if got := e.Lines(caret, ellipse); !slices.Equal(got, tc.want) {
				t.Fatalf("lines %q, want %q", got, tc.want)
			}
		})
	}
}

// The lines are the text: nothing is lost at a wrap, and only line breaks
// are not drawn.
func TestWrappingLosesNothing(t *testing.T) {
	text := "Lorem ipsum  dolor\n\nsit " + family + world + " amet, " + strings.Repeat("x", 40) + " consectetur " + eAcute + eAcute + "\n"
	// From the width of the widest grapheme on, draw cuts nothing.
	for width := 8; width < 40; width++ {
		e := sized(t, text, width, 100)
		got := strings.Join(e.Lines(caret, ellipse), "")
		if want := strings.ReplaceAll(text, "\n", ""); plain(got) != want {
			t.Fatalf("width %d: %q, want %q", width, plain(got), want)
		}
	}
}

func TestTheCaretStandsWhereItIsInAWrappedText(t *testing.T) {
	e := sized(t, "aaa bbb ccc", 9, 10)
	e.Left()
	e.Left()
	e.Left()
	if got := e.Lines(caret, ellipse); !slices.Equal(got, []string{"aaa bbb ", "|ccc"}) {
		t.Fatalf("at the start of the second line: %q", got)
	}
	e.Left()
	if got := e.Lines(caret, ellipse); !slices.Equal(got, []string{"aaa bbb| ", "ccc"}) {
		t.Fatalf("before the space: %q", got)
	}
}

func TestUpAndDownKeepTheColumn(t *testing.T) {
	e := sized(t, "abcdef\nab\nabcdef", 20, 10)
	e.Move(-1)
	assertEntry(t, e, e.Value(), 9) // the end of the short line
	e.Move(-1)
	assertEntry(t, e, e.Value(), 6) // the column of the start again
	e.Move(1)
	e.Move(1)
	assertEntry(t, e, e.Value(), 16)
	e.Move(1)
	assertEntry(t, e, e.Value(), 16) // the last line stays
	e.Left()
	e.Move(-1)
	assertEntry(t, e, e.Value(), 9) // a move of its own forgets the column
	e.Home()
	assertEntry(t, e, e.Value(), 7)
	e.End()
	assertEntry(t, e, e.Value(), 9)
	e.Move(-5)
	assertEntry(t, e, e.Value(), 2)
	e.Move(-1)
	assertEntry(t, e, e.Value(), 2)
}

func TestUpAndDownWalkWrappedLinesAndWideCharacters(t *testing.T) {
	e := sized(t, "\u4e16\u754c\u4e16\u754cabcd", 5, 10)
	// Lines: two wide characters, two wide characters, abcd.
	e.Home()
	assertEntry(t, e, e.Value(), 4)
	e.Move(-1)
	assertEntry(t, e, e.Value(), 2)
	e.Right()
	e.Move(-1)
	assertEntry(t, e, e.Value(), 1) // the same column of cells
	e.End()
	assertEntry(t, e, e.Value(), 1) // the end of a wrapped line stays on it
	e.Home()
	assertEntry(t, e, e.Value(), 0)
}

func TestHomeAndEndStayOnTheScreenLine(t *testing.T) {
	e := sized(t, "aaa bbb ccc", 9, 10)
	e.Home()
	assertEntry(t, e, e.Value(), 8)
	e.Left()
	assertEntry(t, e, e.Value(), 7)
	e.End()
	assertEntry(t, e, e.Value(), 7) // before the space that wraps
	e.Home()
	assertEntry(t, e, e.Value(), 0)
}

func TestTheFieldScrollsWithTheCaret(t *testing.T) {
	e := sized(t, "1\n2\n3\n4\n5\n6", 8, 3)
	if got := e.Lines(caret, ellipse); !slices.Equal(got, []string{"4", "5", "6|"}) {
		t.Fatalf("at the end: %q", got)
	}
	e.Move(-1)
	if got := e.Lines(caret, ellipse); !slices.Equal(got, []string{"4", "5|", "6"}) {
		t.Fatalf("inside the view it does not scroll: %q", got)
	}
	e.Move(-2)
	if got := e.Lines(caret, ellipse); !slices.Equal(got, []string{"3|", "4", "5"}) {
		t.Fatalf("over the top: %q", got)
	}
	e.Move(-5)
	if got := e.Lines(caret, ellipse); !slices.Equal(got, []string{"1|", "2", "3"}) {
		t.Fatalf("at the top: %q", got)
	}
	e.Move(4)
	if got := e.Lines(caret, ellipse); !slices.Equal(got, []string{"3", "4", "5|"}) {
		t.Fatalf("over the bottom: %q", got)
	}
}

func TestTheFieldLeavesNoEmptyRoomBelowItsText(t *testing.T) {
	e := sized(t, "1\n2\n3\n4\n5\n6", 8, 3)
	for range 4 {
		e.Backspace()
	}
	assertEntry(t, e, "1\n2\n3\n4", 7)
	if got := e.Lines(caret, ellipse); !slices.Equal(got, []string{"2", "3", "4|"}) {
		t.Fatalf("after deleting: %q", got)
	}
	e.Size(8, 10, ansi.WcWidth)
	if got := e.Lines(caret, ellipse); !slices.Equal(got, []string{"1", "2", "3", "4|"}) {
		t.Fatalf("after growing: %q", got)
	}
}

func TestAResizeKeepsTheCaretInSight(t *testing.T) {
	e := sized(t, strings.Repeat("word ", 40), 40, 5)
	e.Home()
	e.Move(-3)
	before := e.Value()
	for _, size := range [][2]int{{20, 3}, {9, 2}, {60, 1}, {12, 8}, {3, 1}} {
		e.Size(size[0], size[1], ansi.WcWidth)
		if e.Value() != before {
			t.Fatal("a resize changed the text")
		}
		if got := e.Lines(caret, ellipse); strings.Count(strings.Join(got, ""), caret) != 1 {
			t.Fatalf("size %v: no caret in %q", size, got)
		}
	}
}

func TestOneLineScrollsSideways(t *testing.T) {
	e := New(100, false)
	e.Size(8, 1, ansi.WcWidth)
	for _, tc := range []struct{ text, want string }{
		{"abcdef", "abcdef|"},
		{"g", "abcdefg|"},
		{"h", "~cdefgh|"},
		{"i", "~defghi|"},
	} {
		e.Insert(tc.text)
		if got := e.Lines(caret, ellipse); !slices.Equal(got, []string{tc.want}) {
			t.Fatalf("after %q: %q, want %q", tc.text, got, tc.want)
		}
	}
	// The caret moves inside the window before the window moves.
	e.Left()
	if got := e.Lines(caret, ellipse); !slices.Equal(got, []string{"~defgh|i"}) {
		t.Fatalf("one left: %q", got)
	}
	for range 4 {
		e.Left()
	}
	if got := e.Lines(caret, ellipse); !slices.Equal(got, []string{"~d|efghi"}) {
		t.Fatalf("to the left end: %q", got)
	}
	e.Left()
	if got := e.Lines(caret, ellipse); !slices.Equal(got, []string{"~|defghi"}) {
		t.Fatalf("at the left end: %q", got)
	}
	e.Left()
	if got := e.Lines(caret, ellipse); !slices.Equal(got, []string{"~|cdefg~"}) {
		t.Fatalf("past the left end: %q", got)
	}
	e.Home()
	if got := e.Lines(caret, ellipse); !slices.Equal(got, []string{"|abcdef~"}) {
		t.Fatalf("home: %q", got)
	}
	e.End()
	if got := e.Lines(caret, ellipse); !slices.Equal(got, []string{"~defghi|"}) {
		t.Fatalf("end: %q", got)
	}
}

func TestOneLineShowsAsMuchAsFitsWhenItShrinks(t *testing.T) {
	e := New(100, false)
	e.Size(8, 1, ansi.WcWidth)
	e.Insert("abcdefghijkl")
	for range 6 {
		e.Backspace()
	}
	if got := e.Lines(caret, ellipse); !slices.Equal(got, []string{"abcdef|"}) {
		t.Fatalf("after deleting: %q", got)
	}
}

// A wide character that the window's edge would cut shows as blanks, so the
// line keeps its width and no half character reaches the terminal.
func TestOneLineNeverCutsAWideCharacter(t *testing.T) {
	for _, method := range []ansi.Method{ansi.WcWidth, ansi.GraphemeWidth} {
		e := New(100, false)
		e.Size(9, 1, method)
		e.Insert(strings.Repeat("\u4e16", 12))
		for range 12 {
			got := e.Lines(caret, ellipse)
			if len(got) != 1 || method.StringWidth(got[0]) > 9 {
				t.Fatalf("line %q wider than its field", got)
			}
			if strings.Count(got[0], caret) != 1 {
				t.Fatalf("line %q has no caret", got)
			}
			e.Left()
		}
	}
}

func TestAGraphemeWiderThanTheFieldStillShowsTheCaret(t *testing.T) {
	joined := strings.Repeat("\U0001F468\u200d", 6) + "\U0001F468"
	for _, multiline := range []bool{false, true} {
		e := New(100, multiline)
		e.Insert(joined)
		e.Size(6, 3, ansi.WcWidth)
		for range 2 {
			got := strings.Join(e.Lines(caret, ellipse), "")
			if strings.Count(got, caret) != 1 {
				t.Fatalf("multiline %v: no caret in %q", multiline, got)
			}
			if ansi.WcWidth.StringWidth(got) > 18 {
				t.Fatalf("multiline %v: %q wider than the field", multiline, got)
			}
			e.Left()
		}
	}
}

// Whatever a person does, the buffer stays within its limit, the caret on a
// grapheme boundary, and the drawn lines within the field, with one caret.
func TestRandomEditingKeepsTheInvariants(t *testing.T) {
	pieces := []string{"a", " ", "\n", "word", eAcute, "\u0301", "\u200d", family, flagDE, "\U0001F1E9", "\u4e16", world, "\U0001F468", "\ufe0f", strings.Repeat("x", 30), "\u00ad"}
	for _, method := range []ansi.Method{ansi.WcWidth, ansi.GraphemeWidth} {
		for seed := range uint64(20) {
			rng := rand.New(rand.NewPCG(seed, 1))
			e := New(120, seed%2 == 0)
			width, height := 12+rng.IntN(20), 1+rng.IntN(6)
			e.Size(width, height, method)
			for step := range 2000 {
				switch rng.IntN(10) {
				case 0, 1, 2, 3:
					e.Insert(pieces[rng.IntN(len(pieces))])
				case 4:
					e.Backspace()
				case 5:
					e.Delete()
				case 6:
					e.Left()
				case 7:
					e.Right()
				case 8:
					e.Move(rng.IntN(5) - 2)
				case 9:
					width, height = 12+rng.IntN(20), 1+rng.IntN(6)
					e.Size(width, height, method)
				}
				assertInvariants(t, e, width, height, "seed "+strconv.FormatUint(seed, 10)+" step "+strconv.Itoa(step), method)
			}
		}
	}
}

func assertInvariants(t *testing.T, e *Entry, width, height int, where string, method ansi.Method) {
	t.Helper()
	if len(e.text) > e.limit {
		t.Fatalf("%s: %d runes over the limit %d", where, len(e.text), e.limit)
	}
	if !strings.ContainsRune(boundaries(e), rune(e.caret)) {
		t.Fatalf("%s: caret %d inside a grapheme of %q", where, e.caret, e.Value())
	}
	lines := e.Lines(caret, ellipse)
	if len(lines) > max(1, height) || len(lines) == 0 {
		t.Fatalf("%s: %d lines in a field of %d", where, len(lines), height)
	}
	carets := 0
	for _, line := range lines {
		carets += strings.Count(line, caret)
		if w := method.StringWidth(line); w > width {
			t.Fatalf("%s: line %q is %d wide in a field of %d", where, line, w, width)
		}
	}
	if carets != 1 {
		t.Fatalf("%s: %d carets in %q", where, carets, lines)
	}
}

// boundaries lists every position between two graphemes, found on its own.
func boundaries(e *Entry) string {
	var out strings.Builder
	at := 0
	for s := e.Value(); s != ""; {
		out.WriteRune(rune(at))
		c, _ := ansi.FirstGraphemeCluster(s, ansi.WcWidth)
		at += utf8.RuneCountInString(c)
		s = s[len(c):]
	}
	out.WriteRune(rune(at))
	return out.String()
}

func BenchmarkTypingInAFullField(b *testing.B) {
	e := New(1<<16, true)
	e.Size(100, 40, ansi.WcWidth)
	e.Insert(strings.Repeat("Lorem ipsum dolor sit amet, \u4e16\u754c\n", 1<<16/30))
	e.Home()
	for b.Loop() {
		e.Insert("x")
		_ = e.Lines(caret, ellipse)
		e.Backspace()
	}
}

func BenchmarkPastingOverTheLimit(b *testing.B) {
	paste := strings.Repeat("Lorem ipsum dolor sit amet, \x1b[31mconsectetur\r\n", 1<<20/44)
	for b.Loop() {
		e := New(1<<16, true)
		e.Size(100, 40, ansi.WcWidth)
		e.Insert(paste)
	}
}
