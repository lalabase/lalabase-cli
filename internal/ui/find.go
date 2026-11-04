package ui

import (
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
	"github.com/gboyke/lalabase-cli/internal/uitext"
)

// The find in a pane (F1, the meeting; F2 brings the ticket): / opens a
// line, Enter finds what it holds in the lines the pane shows, n and N
// move through the hits, Esc ends it. Local: no request, no cache, nothing
// written. The hits are found in the lines as laid out, cleaned already
// where they are shown (rule 1), so the person finds what they read: a
// speaker's name without its markers, a bullet as drawn. Nothing here
// cleans again.
//
// A hit is folded rune by rune (strings.EqualFold on a window of the
// query's length in runes), never by lowering the line: lowering can change
// a rune's length in bytes (the dotted capital I, the capital sharp s), and
// a hit is a range of the line's bytes. No pattern, no umlaut written out,
// never across a line. Hits do not overlap: the next one is looked for
// behind a hit.
//
// The pieces are shared, the pane decides what they mean to it: the state
// of a find (finder), the matcher (findHits), the marks in a copy of the
// styles (markHits) and where a hit stands once its line is wrapped
// (placeHits).

// --- the mode -----------------------------------------------------------------

// findTarget is the find of the pane in focus, nil where the pane has none:
// the meeting shown (F1).
func (m Model) findTarget() *finder {
	a := m.app
	if a == nil || a.expired || m.focus != view.Object || !a.meetingsSection() || !a.meetingInPane() {
		return nil
	}
	return &a.meeting.find
}

// settleFind closes the find line when its pane has no find any more: the
// meeting gone or another one in its place, the focus elsewhere, the
// session over. A key never goes to a line that is not drawn.
func (m *Model) settleFind() {
	if m.mode == FindEntry && m.findTarget() == nil {
		m.mode = Navigate
	}
}

// startFind opens the find line of the pane in focus, if it has a find.
func (m Model) startFind() Model {
	if f := m.findTarget(); f != nil {
		f.open()
		m.mode = FindEntry
	}
	return m
}

// findKey is a key while the find line is open: the line's own, and Enter
// finds what it holds from the top of the pane as it stands.
func (m Model) findKey(kind Action, text string) (Model, tea.Cmd) {
	f := m.findTarget()
	if f == nil {
		m.mode = Navigate
		return m, nil
	}
	top := 0
	if kind == Submit {
		top = m.meetingTop()
	}
	if f.edit(kind, text) {
		m.mode = Navigate
		if kind == Submit {
			m.findInMeeting(top)
		}
	}
	return m, nil
}

// --- the matcher and the marks ------------------------------------------------

// hit is a hit of the find: its line and the bytes from..to of it; on a
// screen line once placed (placeHits).
type hit struct{ line, from, to int }

// findHits finds query in lines, one line at a time, hit after hit; none
// for an empty query.
func findHits(lines []string, query string) []hit {
	if query == "" {
		return nil
	}
	_, size := utf8.DecodeRuneInString(query)
	head, runes := query[:size], utf8.RuneCountInString(query)
	var out []hit
	for i, line := range lines {
		for at := 0; at < len(line); {
			_, n := utf8.DecodeRuneInString(line[at:])
			// The first rune alone first: most windows end there.
			if strings.EqualFold(line[at:at+n], head) {
				if end := windowEnd(line, at, runes); end > 0 && strings.EqualFold(line[at:end], query) {
					out = append(out, hit{line: i, from: at, to: end})
					at = end
					continue
				}
			}
			at += n
		}
	}
	return out
}

// windowEnd is the byte after the runes of line from at, -1 when the line
// ends before them.
func windowEnd(line string, at, runes int) int {
	for ; runes > 0; runes-- {
		if at >= len(line) {
			return -1
		}
		_, n := utf8.DecodeRuneInString(line[at:])
		at += n
	}
	return at
}

// markHits is a copy of styles with a span in style over every hit, each
// before the spans its line has: the first span holding a byte draws it
// (present.LineStyle.StyleAt), so a hit inside a tab or a lead is drawn as
// a hit. A line beyond styles gets a style of its own. The styles given
// stay as they are.
func markHits(styles []present.LineStyle, hits []hit, style present.Style) []present.LineStyle {
	n := len(styles)
	for _, h := range hits {
		n = max(n, h.line+1)
	}
	out := make([]present.LineStyle, n)
	copy(out, styles)
	lead := make(map[int][]present.Span)
	for _, h := range hits {
		lead[h.line] = append(lead[h.line], present.Span{From: h.from, To: h.to, Style: style})
	}
	for line, spans := range lead {
		out[line].Spans = append(spans, out[line].Spans...)
	}
	return out
}

// placeHits finds hits on the screen lines their lines were wrapped into
// (view.Wrap, origins one per screen line): for each hit its parts, one per
// screen line it stands on, in order, as bytes of that screen line. The
// wrap drops nothing but spaces where it breaks, so a hit always stands
// somewhere; the hits come in order, the screen lines too.
func placeHits(hits []hit, lines []string, origins []view.Origin) [][]hit {
	out := make([][]hit, len(hits))
	end := func(s int) int { return origins[s].From + len(lines[s]) }
	at := 0
	for i, h := range hits {
		for at < len(origins) && (origins[at].Line < h.line || origins[at].Line == h.line && end(at) <= h.from) {
			at++
		}
		for s := at; s < len(origins) && origins[s].Line == h.line && origins[s].From < h.to; s++ {
			if from, to := max(h.from, origins[s].From), min(h.to, end(s)); from < to {
				out[i] = append(out[i], hit{line: s, from: from - origins[s].From, to: to - origins[s].From})
			}
		}
	}
	return out
}

// --- the state ----------------------------------------------------------------

// finder is a find in a pane: the line while it is typed, the query found
// ("" for none), the hit it stands on, and whether the last move went round
// the end (+1 from the last hit to the first, -1 back from the first).
type finder struct {
	entry, query string
	current      int
	round        int
}

// open opens the line with the query found in it, as the list's search
// line opens with its search.
func (f *finder) open() { f.entry = f.query }

// edit is a key of the line: text and Backspace change it, Esc discards
// it, Enter finds what it holds without the spaces around it (a blank line
// ends the find). It says whether the line closed.
func (f *finder) edit(kind Action, text string) bool {
	switch kind {
	case InsertText:
		f.entry += text
	case DeleteBack:
		if f.entry != "" {
			runes := []rune(f.entry)
			f.entry = string(runes[:len(runes)-1])
		}
	case Cancel:
		f.entry = ""
		return true
	case Submit:
		f.query, f.entry = strings.TrimSpace(f.entry), ""
		f.current, f.round = 0, 0
		return true
	}
	return false
}

// at is the hit the find stands on among hits, kept within them (a
// reload may leave fewer); -1 for none.
func (f finder) at(hits int) int {
	if hits == 0 {
		return -1
	}
	return max(0, min(f.current, hits-1))
}

// move is n (+1) and N (-1): the next or the previous hit, round the end.
func (f *finder) move(delta, hits int) {
	if hits == 0 {
		return
	}
	next := f.at(hits) + delta
	f.round = 0
	switch {
	case next >= hits:
		next, f.round = 0, 1
	case next < 0:
		next, f.round = hits-1, -1
	}
	f.current = next
}

// line is the find line as typed, with its caret.
func (f finder) line() present.Inline {
	return present.NewInline(uitext.Inline(uitext.FindLine).String() + f.entry + uitext.Inline(uitext.Caret).String())
}

// place says in words where the find stands among hits: "3 of 17", after a
// move round the end with a note, or that there is no match. The marks say
// the same in colour; this is what is left without it.
func (f finder) place(hits int) present.Inline {
	at := f.at(hits)
	switch {
	case at < 0:
		return uitext.Inline(uitext.FindNoMatch)
	case f.round > 0:
		return uitext.Fill(uitext.FindFromTop, at+1, hits)
	case f.round < 0:
		return uitext.Fill(uitext.FindFromBottom, at+1, hits)
	}
	return uitext.Fill(uitext.FindPlace, at+1, hits)
}
