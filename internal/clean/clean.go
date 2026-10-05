// Package clean makes text other people wrote safe to print in a terminal.
//
// Titles, messages, descriptions, comments, names and server error texts come
// from other members of an organisation. Unfiltered, an escape sequence in them
// sets the window title, writes the clipboard (OSC 52), plants a hyperlink
// (OSC 8) or overwrites lines with a forged message. Every foreign field passes
// through Text or Inline before it reaches the screen, in every mode.
//
// The output is valid UTF-8 for a UTF-8 terminal. An 8-bit terminal that reads
// C1 controls from single bytes is out of reach of any cleaning: ordinary
// letters carry such bytes once encoded ("\u00db" is c3 9b, and 9b is CSI
// there). The output layer refuses a locale that is not UTF-8 instead.
//
// A port of clean() in bin/lala of the core repository; the test cases there
// are the golden file of this package.
package clean

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// keepFormat reports the format characters (category Cf) that carry real
// content and forge nothing: the joiners that build family emoji and Persian
// words (ZWNJ, ZWJ) and the soft hyphen. Every other one goes: bidi overrides
// mirror the rest of a line ("Trojan Source"), zero-width spaces split a marker
// so it reads the same and matches nothing, tag characters hide ASCII text.
func keepFormat(r rune) bool {
	return r == '\u200c' || r == '\u200d' || r == '\u00ad'
}

// isControl reports the C0 controls except tab and newline (ESC among them),
// DEL and the C1 range.
func isControl(r rune) bool {
	return r <= 0x08 || (r >= 0x0b && r <= 0x1f) || (r >= 0x7f && r <= 0x9f)
}

// Text cleans a block of foreign text. Tab and newline stay; line and paragraph
// separators become newlines, because they end a line wherever they are
// rendered.
func Text(s string) string {
	return clean(s, false)
}

// Inline cleans foreign text for one line: additionally every run of tab,
// newline and carriage return becomes one space, so a value cannot open a new
// row or field in a list.
func Inline(s string) string {
	return clean(s, true)
}

func clean(s string, inline bool) string {
	var b strings.Builder
	b.Grow(len(s))
	// Inline mode collapses a run of tab, newline and CR into one space; the
	// space is written when the run ends.
	pendingSpace := false
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		// An invalid byte decodes as RuneError and is written as U+FFFD, so the
		// output is always valid UTF-8.
		if r == '\u2028' || r == '\u2029' {
			r = '\n'
		}
		if inline && (r == '\t' || r == '\n' || r == '\r') {
			pendingSpace = true
			continue
		}
		if pendingSpace {
			b.WriteByte(' ')
			pendingSpace = false
		}
		switch {
		case isControl(r):
			// A control between two words separated them: it becomes a space, so
			// "a\x0bb" stays two words instead of turning into "ab".
			b.WriteByte(' ')
		case unicode.Is(unicode.Cf, r) && !keepFormat(r):
			// dropped
		default:
			b.WriteRune(r)
		}
	}
	if pendingSpace {
		b.WriteByte(' ')
	}
	return b.String()
}
