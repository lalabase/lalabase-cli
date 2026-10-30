package clean

import (
	"strings"
	"testing"
	"unicode"
)

func TestDraftKeepsWhatAPersonWrote(t *testing.T) {
	for _, tc := range []struct{ name, in, multi, single string }{
		{"plain", "Login bricht ab", "Login bricht ab", "Login bricht ab"},
		{"umlauts and wide", "Gr\u00fc\u00dfe \u4e16\u754c", "Gr\u00fc\u00dfe \u4e16\u754c", "Gr\u00fc\u00dfe \u4e16\u754c"},
		{"combining", "e\u0301", "e\u0301", "e\u0301"},
		{"family emoji", "\U0001F468\u200d\U0001F469\u200d\U0001F467", "\U0001F468\u200d\U0001F469\u200d\U0001F467", "\U0001F468\u200d\U0001F469\u200d\U0001F467"},
		{"a typed space", " ", " ", " "},
		{"empty", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Draft(tc.in, true); got != tc.multi {
				t.Errorf("Draft(%q, true) = %q, want %q", tc.in, got, tc.multi)
			}
			if got := Draft(tc.in, false); got != tc.single {
				t.Errorf("Draft(%q, false) = %q, want %q", tc.in, got, tc.single)
			}
		})
	}
}

func TestDraftTakesOutWholeSequences(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"colour", "ab\x1b[31mcd\x1b[0mef", "abcdef"},
		{"cursor move", "a\x1b[2J\x1b[1;1Hb", "ab"},
		{"title by BEL", "\x1b]0;GEKAPERT\x07x", "x"},
		{"clipboard by ST", "\x1b]52;c;ZWNobyBoaQ==\x1b\\x", "x"},
		{"hyperlink", "\x1b]8;;https://evil.invalid\x07LINK\x1b]8;;\x07", "LINK"},
		{"device control", "\x1bPpayload\x1b\\x", "x"},
		{"application command", "\x1b_apc\x1b\\y", "y"},
		{"privacy message", "\x1b^pm\x1b\\y", "y"},
		{"start of string", "\x1bXsos\x1b\\y", "y"},
		{"two characters", "\x1bcreset", "reset"},
		{"charset", "\x1b(Bq", "q"},
		{"cut off at the end", "ab\x1b[", "ab"},
		{"an escape at the end", "ab\x1b", "ab"},
		{"two in a row", "\x1b[31m\x1b[1mz", "z"},
		{"bracketed paste end", "a\x1b[201~b", "ab"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, multiline := range []bool{true, false} {
				if got := Draft(tc.in, multiline); got != tc.want {
					t.Errorf("Draft(%q, %v) = %q, want %q", tc.in, multiline, got, tc.want)
				}
			}
		})
	}
}

func TestDraftFollowsTheRulesOfClean(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"a control parts two words", "a\x07b", "a b"},
		{"DEL", "a\x7fb", "a b"},
		{"C1 control", "a\u0085b", "a b"},
		{"bidi override", "a\u202eb", "ab"},
		{"bidi isolate", "a\u2066b\u2069c", "abc"},
		{"zero width space", "<\u200b<<", "<<<"},
		{"tag characters", "a\U000E0041b", "ab"},
		{"byte order mark", "\ufeffab", "ab"},
		{"a byte no terminal prints", "a\xffb", "ab"},
		{"a character cut in two", "a\xe2bc", "a\ufffdbc"},
		{"the joiners stay", "a\u200cb\u200dc", "a\u200cb\u200dc"},
		{"the soft hyphen stays", "a\u00adb", "a\u00adb"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, multiline := range []bool{true, false} {
				if got := Draft(tc.in, multiline); got != tc.want {
					t.Errorf("Draft(%q, %v) = %q, want %q", tc.in, multiline, got, tc.want)
				}
			}
		})
	}
}

// A terminal pastes a line break as CR, a file from Windows brings CRLF, the
// Unicode separators end a line wherever they are rendered: all of them are
// one line break to a field of several lines, and one space to a field of
// one, as a run of them is in clean's Inline.
func TestDraftLineBreaks(t *testing.T) {
	for _, tc := range []struct{ name, in, multi, single string }{
		{"LF", "a\nb", "a\nb", "a b"},
		{"CR", "a\rb", "a\nb", "a b"},
		{"CRLF", "a\r\nb", "a\nb", "a b"},
		{"line separator", "a\u2028b", "a\nb", "a b"},
		{"paragraph separator", "a\u2029b", "a\nb", "a b"},
		{"two breaks", "a\n\nb", "a\n\nb", "a b"},
		{"a sequence between CR and LF", "a\r\x1b[0m\nb", "a\nb", "a b"},
		{"a tab", "a\tb", "a    b", "a b"},
		{"a tab and a break", "a\t\nb", "a    \nb", "a b"},
		{"at the end", "a\n", "a\n", "a "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Draft(tc.in, true); got != tc.multi {
				t.Errorf("Draft(%q, true) = %q, want %q", tc.in, got, tc.multi)
			}
			if got := Draft(tc.in, false); got != tc.single {
				t.Errorf("Draft(%q, false) = %q, want %q", tc.in, got, tc.single)
			}
		})
	}
}

// Every code point, one by one, as TestEveryCodePointFollowsTheRule does for
// Text and Inline: a character the table above did not think of follows the
// same rule. ESC alone is the start of a sequence and goes without a space.
func TestDraftOfEveryCodePoint(t *testing.T) {
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if r >= 0xd800 && r <= 0xdfff {
			continue
		}
		in := string(r)
		var multi, single string
		switch {
		case r == 0x1b:
			multi, single = "", ""
		case r == '\t':
			multi, single = "    ", " "
		case r == '\n' || r == '\r' || r == 0x2028 || r == 0x2029:
			multi, single = "\n", " "
		case r <= 0x1f || (r >= 0x7f && r <= 0x9f):
			multi, single = " ", " "
		case unicode.Is(unicode.Cf, r) && !allowedFormat[r]:
			multi, single = "", ""
		default:
			multi, single = in, in
		}
		if got := Draft(in, true); got != multi {
			t.Fatalf("Draft(%U, true) = %q, want %q", r, got, multi)
		}
		if got := Draft(in, false); got != single {
			t.Fatalf("Draft(%U, false) = %q, want %q", r, got, single)
		}
	}
}

// assertDraft is what the buffer may hold: terminal-safe text, without a tab
// or a CR whatever the kind of field, without a line break in one line, and
// the same text when it passes the filter again (a paste of a paste).
func assertDraft(t testing.TB, in string) {
	t.Helper()
	multi, single := Draft(in, true), Draft(in, false)
	assertTerminalSafe(t, in, multi, false)
	assertTerminalSafe(t, in, single, true)
	if strings.ContainsAny(multi, "\t\r") {
		t.Fatalf("Draft(%q, true) keeps a tab or a CR: %q", in, multi)
	}
	if again := Draft(multi, true); again != multi {
		t.Fatalf("Draft is not idempotent on %q: %q then %q", in, multi, again)
	}
	if again := Draft(single, false); again != single {
		t.Fatalf("Draft is not idempotent on %q: %q then %q", in, single, again)
	}
}

func TestDraftOfEveryGoldenInputIsSafe(t *testing.T) {
	for _, c := range loadGolden(t) {
		assertDraft(t, c.Input)
	}
}

func TestDraftOfALongPasteIsLinear(t *testing.T) {
	// One megabyte of everything Draft has to look at: an unfinished
	// sequence that cannot end, and text around it.
	long := strings.Repeat("\x1b[31ma\x1b]0;b\r\n\u202e", 1<<16)
	assertDraft(t, long)
}
