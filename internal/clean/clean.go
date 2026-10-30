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
// there). The interface (C1) and the agent commands (C4) will refuse a
// locale that is not UTF-8 instead; C0 does not check the locale yet.
//
// A port of clean() in bin/lala of the core repository; the test cases there
// are the golden file of this package. Draft is this client's own: the filter
// of what a person types or pastes into an input field.
package clean

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
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

// lineBreaks are the two ways a line break arrives that is none to clean: a
// terminal pastes it as CR, a file from Windows brings CRLF.
var lineBreaks = strings.NewReplacer("\r\n", "\n", "\r", "\n")

// draftTab is what a tab becomes in a field of several lines: the width of
// no cell can be known for one, and the panes show it as four spaces.
const draftTab = "    "

// Draft cleans what a person types or pastes into an input field (M1): the
// one filter in front of every buffer, so that a buffer holds nothing the
// screen would not show, and what is shown is what is sent.
//
// Escape sequences go whole, not only their ESC, since nobody meant to write
// "[31m". An unfinished one takes the rest of the text with it, as it would
// on a terminal. What is left follows the rules of Text and Inline: controls,
// bidi and invisible characters go, and a field of several lines (multiline)
// keeps its line breaks, whichever way they arrived, with its tabs as spaces;
// a field of one line takes a run of them as one space.
func Draft(s string, multiline bool) string {
	s = lineBreaks.Replace(ansi.Strip(s))
	if !multiline {
		return clean(s, true)
	}
	return strings.ReplaceAll(clean(s, false), "\t", draftTab)
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
