package view

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/present"
)

// ReadingWidth bounds the lines of free text: in a wide pane a line longer
// than this is hard to follow back to its start. The rest of the pane
// stays empty.
const ReadingWidth = 80

// minIndented is the least width an indented line keeps; below it the
// indent is dropped, so a narrow pane does not lose its text to margins.
const minIndented = 12

// The frame of a pane: rounded corners, the title in the top border, a
// space inside either side, so the text does not touch it. It takes
// frameCells of a pane's width and frameLines of its height; the panes
// stand side by side, the frames are what parts them.
const (
	frameTopLeft     = "\u256d"
	frameTopRight    = "\u256e"
	frameBottomLeft  = "\u2570"
	frameBottomRight = "\u256f"
	frameSide        = "\u2502"
	frameCells       = 4
	frameLines       = 2
)

// rule is the character of a heading's rule.
const rule = "─"

// part is a piece of a screen line in one style; screenLine is one line of
// the pane, its parts in order.
type part struct {
	text  string
	style present.Style
}

type screenLine []part

func (l screenLine) plain() string {
	var b strings.Builder
	for _, p := range l {
		b.WriteString(p.text)
	}
	return b.String()
}

// render draws the line in its styles, fitted to width. A line that does
// not fit (an oversized grapheme) is fitted as plain text.
func (l screenLine) render(width int, method ansi.Method) string {
	plain := l.plain()
	if method.StringWidth(plain) > width {
		return fit(plain, width, method)
	}
	var b strings.Builder
	for _, p := range l {
		b.WriteString(styled(p.text, p.style))
	}
	return b.String()
}

// styled draws text in a style; spaces stay unstyled, so nothing but the
// text itself carries an attribute. A tone comes from the palette; a
// terminal without colour (NO_COLOR) draws the text alone.
func styled(text string, s present.Style) string {
	if strings.TrimSpace(text) == "" || s == present.Plain {
		return text
	}
	return styleOf(s).Render(text)
}

// quieted draws text in a style and faint: a status's tone stays seen in
// a row that holds nothing to act on.
func quieted(text string, s present.Style) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	return styleOf(s).Faint(true).Render(text)
}

func styleOf(s present.Style) lipgloss.Style {
	style := lipgloss.NewStyle()
	switch s {
	case present.Strong:
		return style.Bold(true).Foreground(brightColor)
	case present.Faint:
		return style.Faint(true)
	case present.StatusOpen:
		return style.Foreground(cyanColor)
	case present.StatusActive:
		return style.Foreground(yellowColor)
	case present.StatusDone:
		return style.Foreground(greenColor)
	case present.NoticeProblem:
		return style.Bold(true).Foreground(redColor)
	case present.NoticeWaiting:
		return style.Foreground(yellowColor)
	case present.Own:
		return style.Bold(true).Foreground(brandColor)
	case present.Author:
		return style.Bold(true).Foreground(authorColor)
	case present.AgentApproval:
		return style.Bold(true).Foreground(authorColor)
	case present.AgentWaiting:
		return style.Foreground(authorColor)
	case present.AgentWaitingSoft:
		return style.Foreground(authorSoftColor)
	case present.AgentWaitingDim:
		return style.Foreground(authorDimColor)
	case present.AgentLost:
		return style.Foreground(redColor)
	case present.AgentWorking:
		return style.Foreground(cyanColor)
	case present.AgentStarting:
		return style.Faint(true).Foreground(cyanColor)
	case present.AgentEnded:
		return style.Faint(true).Foreground(greenColor)
	}
	return style
}

// textLines wraps a column's free text into screen lines: line by line,
// each in its style, at the reading width.
func textLines(c present.Column, width int, method ansi.Method) []screenLine {
	text := strings.ReplaceAll(c.Text.String(), "\t", "    ")
	var out []screenLine
	for i, line := range strings.Split(text, "\n") {
		var st present.LineStyle
		if i < len(c.Styles) {
			st = c.Styles[i]
		}
		out = append(out, wrapStyled(line, st, width, method)...)
	}
	return out
}

// wrapStyled wraps one line of text (no newline in it) into screen lines
// and gives every grapheme the style of its place in the line: the
// wrapping keeps the text's graphemes in order and drops only spaces, so
// each one shown is found again in the line.
func wrapStyled(line string, st present.LineStyle, width int, method ansi.Method) []screenLine {
	out := wrapParts(line, st, width, method)
	reading := min(width, ReadingWidth)
	if st.Rule && len(out) > 0 {
		last := &out[len(out)-1]
		if room := reading - method.StringWidth(last.plain()); room >= 3 {
			*last = append(*last, part{text: " "}, part{text: strings.Repeat(rule, room-1), style: present.Faint})
		}
	}
	return out
}

// wrapParts is wrapStyled without a heading's rule.
func wrapParts(line string, st present.LineStyle, width int, method ansi.Method) []screenLine {
	reading := min(width, ReadingWidth)
	indent := indentOf(st, reading)
	wrapped := wrapBody(line, max(1, reading-indent), method)
	out := make([]screenLine, 0, len(wrapped))
	pos := 0
	for _, w := range wrapped {
		var l screenLine
		if indent > 0 {
			l = append(l, part{text: strings.Repeat(" ", indent)})
		}
		for w != "" {
			cluster, _ := ansi.FirstGraphemeCluster(w, method)
			w = w[len(cluster):]
			for pos < len(line) && !strings.HasPrefix(line[pos:], cluster) {
				skipped, _ := ansi.FirstGraphemeCluster(line[pos:], method)
				pos += len(skipped)
			}
			style := st.RestStyle
			if pos < st.Lead {
				style = st.LeadStyle
			}
			pos += len(cluster)
			if n := len(l); n > 0 && l[n-1].style == style && (indent == 0 || n > 1) {
				l[n-1].text += cluster
				continue
			}
			l = append(l, part{text: cluster, style: style})
		}
		out = append(out, l)
	}
	return out
}

// indentOf is the indent a line of this style takes at the reading width.
func indentOf(st present.LineStyle, reading int) int {
	if indent := max(0, st.Indent); reading-indent >= minIndented {
		return indent
	}
	return 0
}

// Wrap is c's free text wrapped at width as the pane draws it: one line
// per screen line, each with the style that draws it the same, so the view
// wraps each into itself again. A model that holds a long text wraps it
// once per width with it and hands the lines over as Column.Wrapped.
func Wrap(c present.Column, width int, method ansi.Method) ([]string, []present.LineStyle) {
	var lines []string
	var styles []present.LineStyle
	text := strings.ReplaceAll(c.Text.String(), "\t", "    ")
	for i, line := range strings.Split(text, "\n") {
		var st present.LineStyle
		if i < len(c.Styles) {
			st = c.Styles[i]
		}
		wrapped := wrapParts(line, st, width, method)
		indent := indentOf(st, min(width, ReadingWidth))
		for j, l := range wrapped {
			if indent > 0 && len(l) > 0 {
				l = l[1:] // the indent's part; the style brings it again
			}
			out := present.LineStyle{LeadStyle: st.LeadStyle, RestStyle: st.RestStyle, Indent: st.Indent, Rule: st.Rule && j == len(wrapped)-1}
			if len(l) > 0 && st.Lead > 0 && st.LeadStyle != st.RestStyle && l[0].style == st.LeadStyle {
				out.Lead = len(l[0].text)
			}
			lines = append(lines, l.plain())
			styles = append(styles, out)
		}
	}
	return lines, styles
}

// wrappedLines are the screen lines from..to of a text wrapped already
// (Column.Wrapped): each wraps into itself again at the width it was
// wrapped at.
func wrappedLines(c present.Column, from, to, width int, method ansi.Method) []screenLine {
	var out []screenLine
	for i := from; i < to; i++ {
		var st present.LineStyle
		if i < len(c.Styles) {
			st = c.Styles[i]
		}
		out = append(out, wrapStyled(c.Wrapped[i], st, width, method)...)
	}
	return out
}
