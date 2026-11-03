package ui

import (
	"strings"
	"unicode/utf8"

	"github.com/gboyke/lalabase-cli/internal/present"
)

// The text of a meeting's section laid out for reading (K7c). The core
// sends Markdown: a summary written by a model, notes and transcripts as
// people pasted them, a speaker's turn mostly as a line "**Name**" with
// the words below it. The pane shows a small part of Markdown and leaves
// the rest as written:
//
//   - in the transcript alone, a speaker's line becomes a head, the name
//     bold in the tone of an author (the person's own name in theirs), the
//     turn indented below it, an empty line before the next head. A
//     speaker is a bold name alone on a line ("**Name**"), or a bold name
//     with a colon before their words ("**Name:** words") when it is one
//     of the meeting's participants. With participants known, a bold line
//     alone names one of them too: "**Fazit**" is no person. In a summary
//     or notes such a line is bold, nothing more (review round 1 of #9);
//   - a heading ("# ...") is bold and ends a turn, a list item ("- ", "* ",
//     "+ ") starts with a bullet, a line that starts bold keeps that part
//     bold;
//   - the markers of bold text ("**") are dropped where they pair up, but
//     not in a line with an address or code ("://", "`").
//
// The text is cleaned before it is read (rule 1) and its tabs widened as
// the view widens them: the layout works on what is shown, so the lines
// and their styles stay in step.

// maxSpeaker bounds a speaker's name: a longer bold line is a sentence.
const maxSpeaker = 60

// indentTurn is the indent of a speaker's words below their name.
const indentTurn = 2

// meetingLayout is what the layout of a section knows of its meeting.
type meetingLayout struct {
	// me is the person's own name, "" for none.
	me string
	// speakers are the names of the meeting's participants, cleaned; none
	// known, any short bold name alone on a line is a speaker.
	speakers []string
	// transcript: the section is the transcript, the one with speakers.
	transcript bool
}

// speaker says whether a name is a speaker's: a bold name alone on a line,
// or one with their words after it (inline), which must name a
// participant. A name is matched as the core's Meetings::SpeakerMarkup
// matches a label (review round 2 of #9): normalised (nameKey), its
// diarisation suffix left out, a participant's first name enough.
func (l meetingLayout) speaker(name string, inline bool) bool {
	if !l.transcript || name == "" || utf8.RuneCountInString(name) > maxSpeaker {
		return false
	}
	if len(l.speakers) == 0 {
		return !inline
	}
	key := nameKey(withoutSuffix(name))
	if key == "" {
		return false
	}
	for _, s := range l.speakers {
		full := nameKey(s)
		if first, _, _ := strings.Cut(full, " "); key == full || key == first {
			return true
		}
	}
	return false
}

// own says whether a speaker's name is the person's: the same name, matched
// as a speaker is, the first name alone too.
func (l meetingLayout) own(name string) bool {
	me := nameKey(l.me)
	if me == "" {
		return false
	}
	key := nameKey(withoutSuffix(name))
	first, _, _ := strings.Cut(me, " ")
	return key == me || key == first
}

// nameKey is a name as the core normalises one (PersonName.normalize):
// spaces collapsed, lower case, umlauts written out. The core composes the
// name first (NFC); the client has no normaliser for that and compares as
// written.
func nameKey(name string) string {
	s := strings.ToLower(strings.Join(strings.Fields(name), " "))
	return strings.NewReplacer("\u00e4", "ae", "\u00f6", "oe", "\u00fc", "ue", "\u00df", "ss").Replace(s)
}

// withoutSuffix is a speaker's name without the note a transcript tool
// adds: "(Speaker 2)", "(Sprecher 3)" or "(?)", as the core strips it.
func withoutSuffix(name string) string {
	s := strings.TrimSpace(name)
	open := strings.LastIndex(s, "(")
	if open < 0 || !strings.HasSuffix(s, ")") {
		return s
	}
	note := strings.ToLower(strings.TrimSpace(s[open+1 : len(s)-1]))
	for _, word := range []string{"speaker", "sprecher"} {
		if digits, ok := strings.CutPrefix(note, word); ok {
			note = strings.TrimSpace(digits)
			if note == "" || strings.Trim(note, "0123456789") != "" {
				return s
			}
			return strings.TrimSpace(s[:open])
		}
	}
	if note == "?" {
		return strings.TrimSpace(s[:open])
	}
	return s
}

// meetingBody lays out one section's raw text.
func meetingBody(raw string, l meetingLayout) ([]string, []present.LineStyle) {
	var lines []string
	var styles []present.LineStyle
	add := func(line string, st present.LineStyle) {
		lines = append(lines, line)
		styles = append(styles, st)
	}
	turn := false
	head := func(name string) {
		if n := len(lines); n > 0 && strings.TrimSpace(lines[n-1]) != "" {
			add("", present.LineStyle{})
		}
		tone := present.Author
		if l.own(name) {
			tone = present.Own
		}
		add(name, present.LineStyle{Lead: len(name), LeadStyle: tone, RestStyle: tone})
		turn = true
	}
	text := strings.ReplaceAll(present.NewBody(raw).String(), "\t", "    ")
	for _, line := range strings.Split(text, "\n") {
		if name, ok := speakerOf(line); ok && l.speaker(name, false) {
			head(name)
			continue
		}
		indent := 0
		if turn {
			indent = indentTurn
		}
		if heading, ok := headingOf(line); ok {
			add(heading, present.LineStyle{Lead: len(heading), LeadStyle: present.Strong, RestStyle: present.Strong})
			turn = false
			continue
		}
		bullet := ""
		if item, depth, ok := listItemOf(line); ok {
			bullet, line, indent = "• ", item, indent+depth
		}
		lead, rest := leadingBold(line)
		if name, words, ok := inlineSpeaker(lead, rest); ok && bullet == "" && l.speaker(name, true) {
			head(name)
			if words != "" {
				add(words, present.LineStyle{Indent: indentTurn})
			}
			continue
		}
		text := bullet + lead + unbold(rest)
		st := present.LineStyle{Indent: indent}
		if lead != "" {
			st.Lead, st.LeadStyle = len(bullet)+len(lead), present.Strong
		}
		add(text, st)
	}
	return lines, styles
}

// speakerOf reads a line that is a bold name alone ("**Name**", a colon
// inside or after the markers allowed).
func speakerOf(line string) (string, bool) {
	s := strings.TrimSuffix(strings.TrimSpace(line), ":")
	if !strings.HasPrefix(s, "**") || !strings.HasSuffix(s, "**") || len(s) <= 4 {
		return "", false
	}
	name := strings.TrimSpace(strings.TrimSuffix(s[2:len(s)-2], ":"))
	if name == "" || strings.Contains(name, "**") || utf8.RuneCountInString(name) > maxSpeaker {
		return "", false
	}
	return name, true
}

// inlineSpeaker reads a bold start that names a speaker before their
// words: "**Name:** words" or "**Name**: words".
func inlineSpeaker(lead, rest string) (string, string, bool) {
	switch {
	case lead == "":
		return "", "", false
	case strings.HasSuffix(lead, ":"):
		return strings.TrimSpace(strings.TrimSuffix(lead, ":")), strings.TrimSpace(rest), true
	case strings.HasPrefix(rest, ":"):
		return strings.TrimSpace(lead), strings.TrimSpace(rest[1:]), true
	}
	return "", "", false
}

// headingOf reads a Markdown heading: one to six "#", a space, the text.
func headingOf(line string) (string, bool) {
	s := strings.TrimSpace(line)
	n := len(s) - len(strings.TrimLeft(s, "#"))
	if n < 1 || n > 6 || !strings.HasPrefix(s[n:], " ") {
		return "", false
	}
	text := unbold(strings.TrimSpace(s[n:]))
	return text, text != ""
}

// listItemOf reads a list item: its text and the indent of its nesting
// (the spaces before the marker, at most 8).
func listItemOf(line string) (string, int, bool) {
	s := strings.TrimLeft(line, " ")
	depth := min(len(line)-len(s), 8)
	for _, marker := range []string{"- ", "* ", "+ "} {
		if strings.HasPrefix(s, marker) && strings.TrimSpace(s[len(marker):]) != "" {
			return strings.TrimLeft(s[len(marker):], " "), depth, true
		}
	}
	return "", 0, false
}

// leadingBold splits a line that starts bold ("**Ergebnis:** Start im
// März.") into the bold part without its markers and the rest.
func leadingBold(line string) (string, string) {
	if !strings.HasPrefix(line, "**") {
		return "", line
	}
	end := strings.Index(line[2:], "**")
	if end <= 0 {
		return "", line
	}
	return line[2 : 2+end], line[2+end+2:]
}

// unbold drops the markers of bold text where they pair up; a lone marker
// stays as written, and so does a line with an address or code in it.
func unbold(s string) string {
	if n := strings.Count(s, "**"); n == 0 || n%2 != 0 || strings.Contains(s, "://") || strings.Contains(s, "`") {
		return s
	}
	return strings.ReplaceAll(s, "**", "")
}
