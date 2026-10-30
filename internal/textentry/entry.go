// Package textentry is the buffer behind an input field of the interface
// (M1): runes with a caret, one line or several, up to a hard limit. It is
// the client's own code and not a widget, since Bubbles' textarea imports
// atotto/clipboard, which starts a child process (the C2 design).
//
// Threat model: what a person pastes is text from anywhere. Everything that
// enters the buffer passes clean.Draft, typed or pasted, so the buffer never
// holds what the screen would not show, and what is shown is what is sent.
// A paste of any size costs its length once and takes no more than the
// limit. The caret stands between two graphemes, never inside one: it steps
// and deletes what a person sees as one character, and an edit that joins two
// graphemes around it moves it to the end of the new one. A mark with nothing
// to stand on does not stay: the screen would not show it.
//
// The interface owns an Entry behind a pointer and changes it in its Update
// alone: Size tells it the room it has, and Lines reads what to draw. What
// the entry keeps in sight follows the caret, in lines (several lines) and
// in cells (one line, or a grapheme wider than the field).
package textentry

import (
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/clean"
)

// cluster is one grapheme of the text: where it starts, in runes, and how
// many cells it takes. The list ends in a cluster without text, at the
// text's end, so that the one after any grapheme can always be asked for.
type cluster struct {
	start, cells int
}

// line is one line on the screen: the clusters from first up to last.
// wrapped: it ends because the field does, not in a line break or the text's
// end, so the position after it is the start of the next line.
type line struct {
	first, last int
	wrapped     bool
}

// Entry is one input field's text and caret. Its zero value is no entry; use
// New.
type Entry struct {
	text      []rune
	caret     int // in runes, between two graphemes
	limit     int
	multiline bool

	width, height int
	method        ansi.Method
	clusters      []cluster
	lines         []line
	// top is the first line in sight; left the first cell in sight of the
	// caret's line, which only moves when that line is wider than the field.
	top, left int
	// column is the cell Move keeps when it passes shorter lines; -1 when
	// the last thing done was no such move.
	column int
}

// New is an empty entry of at most limit runes, of one line or of several.
func New(limit int, multiline bool) *Entry {
	e := &Entry{limit: limit, multiline: multiline, column: -1}
	e.arrange()
	return e
}

// Value is the text, as it would be sent.
func (e *Entry) Value() string { return string(e.text) }

// Insert puts text at the caret, through clean.Draft, and reports whether it
// was cut. What does not fit the limit is left out from the first grapheme
// that does not fit: half a character is no text.
func (e *Entry) Insert(text string) (cut bool) {
	text = clean.Draft(text, e.multiline)
	room := e.limit - len(e.text)
	if utf8.RuneCountInString(text) > room {
		text, cut = fitted(text, room), true
	}
	add := []rune(text)
	e.text = slices.Insert(e.text, e.caret, add...)
	e.caret += len(add)
	e.edited()
	return cut
}

// fitted is the graphemes of text that fit room runes, from its start. The
// width method plays no part in where a grapheme ends.
func fitted(text string, room int) string {
	end := 0
	for rest := text; rest != ""; {
		c, _ := ansi.FirstGraphemeCluster(rest, ansi.WcWidth)
		room -= utf8.RuneCountInString(c)
		if room < 0 {
			break
		}
		end += len(c)
		rest = rest[len(c):]
	}
	return text[:end]
}

// Backspace deletes the grapheme before the caret.
func (e *Entry) Backspace() {
	if i := e.at(e.caret); i > 0 {
		from := e.clusters[i-1].start
		e.text = slices.Delete(e.text, from, e.caret)
		e.caret = from
		e.edited()
	}
}

// Delete deletes the grapheme after the caret.
func (e *Entry) Delete() {
	if i := e.at(e.caret); i < len(e.clusters)-1 {
		e.text = slices.Delete(e.text, e.caret, e.clusters[i+1].start)
		e.edited()
	}
}

// edited brings everything that follows from the text up to date.
func (e *Entry) edited() {
	e.column = -1
	e.arrange()
}

// Left moves the caret one grapheme back.
func (e *Entry) Left() {
	if i := e.at(e.caret); i > 0 {
		e.moveTo(e.clusters[i-1].start)
	}
}

// Right moves the caret one grapheme on.
func (e *Entry) Right() {
	if i := e.at(e.caret); i < len(e.clusters)-1 {
		e.moveTo(e.clusters[i+1].start)
	}
}

// Home moves the caret to the start of its line on the screen.
func (e *Entry) Home() { e.moveTo(e.clusters[e.lines[e.caretLine()].first].start) }

// End moves the caret to the end of its line on the screen; on a wrapped
// line that is before its last grapheme, since the place after it is on the
// next line.
func (e *Entry) End() { e.moveTo(e.clusters[e.lineEnd(e.lines[e.caretLine()])].start) }

func (e *Entry) moveTo(rune int) {
	e.caret, e.column = rune, -1
	e.follow()
}

// lineEnd is the last place of a line the caret can stand on.
func (e *Entry) lineEnd(l line) int {
	if l.wrapped {
		return l.last - 1
	}
	return l.last
}

// Move moves the caret by lines on the screen, in the same column of cells,
// or as near as the line allows; a field of one line stays. The column stays
// the one it started in for as long as the moves go on.
func (e *Entry) Move(lines int) {
	if !e.multiline {
		return
	}
	from := e.caretLine()
	if e.column < 0 {
		e.column = e.span(e.lines[from].first, e.at(e.caret))
	}
	l := e.lines[max(0, min(len(e.lines)-1, from+lines))]
	i, cells := l.first, 0
	for end := e.lineEnd(l); i < end && cells+e.clusters[i].cells <= e.column; i++ {
		cells += e.clusters[i].cells
	}
	e.caret = e.clusters[i].start
	e.follow()
}

// Page moves the caret by the lines in sight, up with a negative pages.
func (e *Entry) Page(pages int) { e.Move(pages * max(1, e.height)) }

// Size tells the entry the room it is shown in, width cells by height lines,
// and how the terminal counts cells.
func (e *Entry) Size(width, height int, method ansi.Method) {
	if width == e.width && height == e.height && method == e.method {
		return
	}
	e.width, e.height, e.method = width, height, method
	e.arrange()
}

// Lines are the lines in sight, with caret at the caret's place and more at
// an end of a line the field cuts; both take one cell. An entry not in focus
// gives a space for its caret, so that its text stays where it was.
func (e *Entry) Lines(caret, more string) []string {
	shown := e.caretLine()
	height := max(1, e.height)
	out := make([]string, 0, height)
	for i := e.top; i < len(e.lines) && len(out) < height; i++ {
		if i == shown {
			out = append(out, e.draw(e.lines[i], caret, e.left, more))
			continue
		}
		out = append(out, e.draw(e.lines[i], "", 0, more))
	}
	return out
}

// draw is a line with its caret, if any, from cell left on. A line the field
// holds is drawn whole. Of a wider one the field shows width cells; a cut
// end takes one of them for more, and a wide grapheme an edge goes through
// shows as blanks, so that no half character reaches the terminal.
func (e *Entry) draw(l line, caret string, left int, more string) string {
	caretAt := -1
	total := e.span(l.first, l.last)
	if caret != "" {
		caretAt = e.at(e.caret)
		total++
	}
	from, to := 0, total
	var head, tail string
	if e.width > 0 && total > e.width {
		from, to = left, left+e.width
		if from > 0 {
			from, head = from+1, more
		}
		if to < total {
			to, tail = to-1, more
		}
	}
	var b strings.Builder
	b.WriteString(head)
	cell := 0
	// put writes what stands at the current cell and says whether it did,
	// whole.
	put := func(text string, cells int) bool {
		cell += cells
		if at := cell - cells; at >= from && cell <= to {
			b.WriteString(text)
			return true
		} else if visible := min(cell, to) - max(at, from); visible > 0 {
			b.WriteString(strings.Repeat(" ", visible))
		}
		return false
	}
	// A grapheme of no cells that follows no grapheme, a lone mark, is left
	// out: drawn after the caret, a blank or the frame's space it would join
	// them and change their width.
	base := false
	for i := l.first; i <= l.last; i++ {
		if i == caretAt {
			put(caret, 1)
			base = false
		}
		if i == l.last {
			break
		}
		c := e.clusters[i]
		if c.cells > 0 || base {
			base = put(string(e.text[c.start:e.clusters[i+1].start]), c.cells)
		}
	}
	b.WriteString(tail)
	return b.String()
}

// arrange brings the graphemes, the caret, the lines and what is in sight up
// to date after a change of the text or the room.
func (e *Entry) arrange() {
	e.segment()
	if e.dropLone() {
		e.segment()
	}
	// An edit may have joined two graphemes around the caret.
	e.caret = e.clusters[e.at(e.caret)].start
	e.wrap()
	e.follow()
}

// segment finds the graphemes of the text.
func (e *Entry) segment() {
	e.clusters = e.clusters[:0]
	start := 0
	for s := string(e.text); s != ""; {
		c, cells := ansi.FirstGraphemeCluster(s, e.method)
		e.clusters = append(e.clusters, cluster{start: start, cells: cells})
		start += utf8.RuneCountInString(c)
		s = s[len(c):]
	}
	e.clusters = append(e.clusters, cluster{start: start})
}

// dropLone takes the graphemes of no cells out of the text that stand on
// nothing, at its start or a line's: a mark, a joiner or a selector with no
// letter before it. The screen would show nothing of them, so they would
// leave a field that looks empty and is not. Once through the text, however
// many there are.
func (e *Entry) dropLone() (dropped bool) {
	kept := e.text[:0]
	caret := e.caret
	lineStart := true
	for i, c := range e.clusters[:len(e.clusters)-1] {
		runes := e.text[c.start:e.clusters[i+1].start]
		newline := runes[0] == '\n'
		if lineStart && c.cells == 0 && !newline {
			dropped = true
			caret -= max(0, min(e.caret, c.start+len(runes))-c.start)
			continue
		}
		lineStart = newline
		kept = append(kept, runes...)
	}
	e.text, e.caret = kept, caret
	return dropped
}

// wrap breaks the text into lines: at its line breaks and, in a field of
// several lines, where the next grapheme would not leave the caret its cell,
// after the line's last space if it has one. A grapheme stands on a line of
// its own when it is wider than that (draw deals with it); every grapheme
// but the line breaks is on exactly one line.
func (e *Entry) wrap() {
	e.lines = e.lines[:0]
	room := e.width - 1
	first, cells := 0, 0
	space, spaceCells := -1, 0 // after the line's last space
	graphemes := len(e.clusters) - 1
	for i, c := range e.clusters[:graphemes] {
		if e.text[c.start] == '\n' {
			e.lines = append(e.lines, line{first: first, last: i})
			first, cells, space = i+1, 0, -1
			continue
		}
		if e.multiline && room > 0 && i > first && cells+c.cells > room {
			end := i
			if space > first {
				end = space
				cells -= spaceCells
			} else {
				cells = 0
			}
			e.lines = append(e.lines, line{first: first, last: end, wrapped: true})
			first, space = end, -1
		}
		cells += c.cells
		if e.text[c.start] == ' ' {
			space, spaceCells = i+1, cells
		}
	}
	e.lines = append(e.lines, line{first: first, last: graphemes})
}

// follow keeps the caret in sight: the lines move only when it left them,
// and the field shows no empty room below its text, or beside it.
func (e *Entry) follow() {
	shown := e.caretLine()
	height := max(1, e.height)
	e.top = max(min(e.top, shown, len(e.lines)-height), shown-height+1, 0)
	l := e.lines[shown]
	total := e.span(l.first, l.last) + 1
	if e.width <= 0 || total <= e.width {
		e.left = 0
		return
	}
	// The caret's cell, inside the cells a cut end takes for its mark.
	cell := e.span(l.first, e.at(e.caret))
	e.left = max(min(max(min(e.left, cell-1), cell+2-e.width), total-e.width), 0)
}

// at is the index of the first cluster that starts at this rune or after it.
func (e *Entry) at(rune int) int {
	i, _ := slices.BinarySearchFunc(e.clusters, rune, func(c cluster, r int) int { return c.start - r })
	return i
}

// caretLine is the index of the line the caret stands on.
func (e *Entry) caretLine() int {
	i, found := slices.BinarySearchFunc(e.lines, e.at(e.caret), func(l line, c int) int { return l.first - c })
	if found {
		return i
	}
	return i - 1
}

// span is the cells of the clusters from first up to last.
func (e *Entry) span(first, last int) int {
	cells := 0
	for _, c := range e.clusters[first:last] {
		cells += c.cells
	}
	return cells
}
