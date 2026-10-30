package view

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/present"
)

// A dialog (M1) is a box laid over the lines Render built. It takes the
// frame of the focused pane and a margin of two cells inside either side.
// Small it is dialogWidth wide, large about 80 % of the screen; on a terminal
// narrower than dialogFullBelow the box takes the whole width.
const (
	dialogFullBelow = 60
	dialogWidth     = 54
	dialogMargin    = "  "
	dialogSides     = 2 + 2*len(dialogMargin)
	// dialogLabelGap parts a label from its entry.
	dialogLabelGap = 2
	// dialogErrorLines is the most an error may take: a server's text says
	// what to change, and one line would cut it.
	dialogErrorLines = 3
	// dialogEntryLines is what a large field keeps before its box gives up
	// its spacing, its hint and its label.
	dialogEntryLines = 3
)

// dialogShape is how a dialog fills a terminal: the box's width, and of what
// may give way on a low screen what stays. Render's rule holds here too:
// the keys are never cut away, and the screen's last line stays free, for it
// names the one key the box does not. An error keeps its first line and the
// active field stays in sight, however low the screen.
type dialogShape struct {
	width, inner                  int
	label, entryWidth, entryLines int
	help                          []string
	errorLines                    int
	spaced, labels, hints         bool
	// from and to are the fields in sight.
	from, to int
}

func dialogShapeOf(d present.Dialog, g Geometry, method ansi.Method) dialogShape {
	s := dialogShape{width: g.Width}
	if g.Width >= dialogFullBelow {
		s.width = dialogWidth
		if d.Large {
			s.width = max(dialogWidth, g.Width*4/5)
		}
	}
	s.inner = s.width - dialogSides
	s.help = HelpLines(d.Help, s.inner, method)
	height := g.Height - 1
	if d.Large {
		fixed := frameLines + len(s.help) + 1
		height = min(height, max(g.Height*4/5, fixed+dialogEntryLines))
	}
	room := height - frameLines - len(s.help)
	s.errorLines = 1
	if text := d.Error.String(); text != "" {
		s.errorLines = max(1, min(len(wrapBody(text, s.inner, method)), dialogErrorLines, room-1))
	}
	room -= s.errorLines
	s.to = len(d.Fields)
	if d.Large {
		s.largeRows(d, room)
	} else {
		s.smallRows(d, room, method)
	}
	return s
}

// largeRows gives the one field what room is left, once its label, its hint
// and the line above them have theirs; they go in the reverse order when the
// field would be left too little.
func (s *dialogShape) largeRows(d present.Dialog, room int) {
	s.entryWidth = s.inner
	s.spaced = true
	if len(d.Fields) > 0 {
		s.labels = d.Fields[0].Label.String() != ""
		s.hints = d.Fields[0].Hint.String() != ""
	}
	rows := func() int { return room - count(s.spaced) - count(s.labels) - count(s.hints) }
	for _, gives := range []*bool{&s.spaced, &s.hints, &s.labels} {
		if rows() < dialogEntryLines {
			*gives = false
		}
	}
	s.entryLines = max(1, rows())
}

// smallRows lays out one line per field and its hint, in the columns of the
// widest label. A low screen takes the line above them first, then the
// hints, then the fields furthest from the active one.
func (s *dialogShape) smallRows(d present.Dialog, room int, method ansi.Method) {
	s.entryLines = 1
	for _, f := range d.Fields {
		s.label = max(s.label, method.StringWidth(f.Label.String()))
	}
	s.label = min(s.label, s.inner/2-1)
	s.entryWidth = s.inner - s.label - dialogLabelGap
	s.hints = true
	lines := func() int {
		n := len(d.Fields)
		if s.hints {
			for _, f := range d.Fields {
				n += count(f.Hint.String() != "")
			}
		}
		return n
	}
	s.spaced = lines()+1 <= room
	if lines() > room {
		s.hints = false
	}
	if room = max(1, room); len(d.Fields) > room {
		active := 0
		for i, f := range d.Fields {
			if f.Active {
				active = i
			}
		}
		s.from = min(active, len(d.Fields)-room)
		s.to = s.from + room
	}
}

func count(b bool) int {
	if b {
		return 1
	}
	return 0
}

// DialogEntry is the room an entry of the dialog has in this terminal: the
// cells of a line and the lines. The caller tells its entries, which wrap
// and scroll in it.
func DialogEntry(d present.Dialog, g Geometry, method ansi.Method) (width, height int) {
	s := dialogShapeOf(d, g, method)
	return s.entryWidth, s.entryLines
}

// dialogBox is the box, line by line, each as wide as the box.
func dialogBox(d present.Dialog, g Geometry, method ansi.Method) []string {
	s := dialogShapeOf(d, g, method)
	var rows []string
	if s.spaced {
		rows = append(rows, "")
	}
	if d.Large {
		rows = append(rows, s.largeField(d, method)...)
	} else {
		rows = append(rows, s.smallFields(d, method)...)
	}
	if text := d.Error.String(); text != "" {
		rows = append(rows, noticeLines(text, d.ErrorStyle, s.inner, s.errorLines, method)...)
	} else {
		rows = append(rows, make([]string, s.errorLines)...)
	}
	rows = append(rows, s.help...)
	border := lipgloss.NewStyle().Bold(true).Foreground(focusColor)
	run := s.width - 2
	out := make([]string, 0, len(rows)+frameLines)
	out = append(out, frameTop(d.Title.String(), run, border, border, method))
	left, right := border.Render(frameSide)+dialogMargin, dialogMargin+border.Render(frameSide)
	for _, row := range rows {
		out = append(out, left+row+strings.Repeat(" ", max(0, s.inner-method.StringWidth(row)))+right)
	}
	return append(out, border.Render(frameBottomLeft+strings.Repeat(rule, run)+frameBottomRight))
}

// largeField is the label, the entry's lines, filled up to its room, and the
// hint, one under the other.
func (s dialogShape) largeField(d present.Dialog, method ansi.Method) []string {
	if len(d.Fields) == 0 {
		return nil
	}
	f := d.Fields[0]
	var rows []string
	if s.labels {
		rows = append(rows, styled(fit(f.Label.String(), s.inner, method), weightOf(f)))
	}
	for i := range s.entryLines {
		if i < len(f.Entry) {
			rows = append(rows, fit(f.Entry[i].String(), s.inner, method))
		} else {
			rows = append(rows, "")
		}
	}
	if s.hints {
		rows = append(rows, styled(fit(f.Hint.String(), s.inner, method), present.Faint))
	}
	return rows
}

// smallFields is one line per field, the label in its column and the entry
// after it, and under the entry the field's hint, if any.
func (s dialogShape) smallFields(d present.Dialog, method ansi.Method) []string {
	var rows []string
	indent := strings.Repeat(" ", s.label+dialogLabelGap)
	for _, f := range d.Fields[s.from:s.to] {
		label := fit(f.Label.String(), s.label, method)
		row := styled(label, weightOf(f)) + strings.Repeat(" ", s.label-method.StringWidth(label)+dialogLabelGap)
		if len(f.Entry) > 0 {
			row += fit(f.Entry[0].String(), s.entryWidth, method)
		}
		rows = append(rows, row)
		if s.hints && f.Hint.String() != "" {
			rows = append(rows, indent+styled(fit(f.Hint.String(), s.entryWidth, method), present.Faint))
		}
	}
	return rows
}

func weightOf(f present.DialogField) present.Style {
	if f.Active {
		return present.Strong
	}
	return present.Plain
}

// Overlay lays the dialog's box over the lines Render built, in the middle
// of the screen. A terminal too small for anything keeps its one notice and
// its keys.
func Overlay(screen string, d present.Dialog, g Geometry, method ansi.Method) string {
	if g.Small || g.Width <= 0 || g.Height <= 0 {
		return screen
	}
	box := dialogBox(d, g, method)
	width := dialogShapeOf(d, g, method).width
	x, y := (g.Width-width)/2, (g.Height-len(box))/2
	lines := strings.Split(screen, "\n")
	for len(lines) < y+len(box) {
		lines = append(lines, "")
	}
	for i, row := range box {
		before, after := around(lines[y+i], x, x+width, method)
		lines[y+i] = before + row + after
	}
	return strings.Join(lines, "\n")
}

// around is a line of the screen without its cells from left up to right:
// what stands before them, exactly left cells wide and with no style left
// open, and what stands from right on, with the styles in force there.
// Sequences stay whole, since the line is read sequence by sequence and
// grapheme by grapheme. A wide character an edge goes through leaves a blank
// in the cell on the outside, so that everything else keeps its column.
//
// This is not ansi.Cut, which the plan named: in the interface's default
// width method its left edge does not cut (it returns the start of the line,
// x/ansi 0.11.8), it stops early on a joined emoji, and it keeps a wide
// character across an edge, which would shift the rest by a cell.
func around(line string, left, right int, method ansi.Method) (before, after string) {
	var head, styles, tail strings.Builder
	blanksBefore, blanksAfter := 0, 0
	cell := 0
	var state byte
	for rest := line; rest != ""; {
		seq, width, n, next := method.DecodeSequenceInString(rest, state, nil)
		rest, state = rest[n:], next
		end := cell + width
		switch {
		case seq[0] == ansi.ESC:
			if cell < left {
				head.WriteString(seq)
			}
			if cell < right {
				styles.WriteString(seq)
			} else {
				tail.WriteString(seq)
			}
		case width == 0:
			// A mark belongs to the cell before it, wherever that went.
			if cell <= left {
				head.WriteString(seq)
			} else if cell > right {
				tail.WriteString(seq)
			}
		default:
			if end <= left {
				head.WriteString(seq)
			} else if cell < left {
				blanksBefore = left - cell
			}
			if cell >= right {
				tail.WriteString(seq)
			} else if end > right {
				blanksAfter = end - right
			}
		}
		cell = end
	}
	head.WriteString(ansi.ResetStyle)
	head.WriteString(strings.Repeat(" ", blanksBefore+max(0, left-cell)))
	if tail.Len() == 0 {
		return head.String(), strings.Repeat(" ", blanksAfter)
	}
	return head.String(), strings.Repeat(" ", blanksAfter) + styles.String() + tail.String()
}
