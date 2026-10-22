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

// Separator stands between two panes, drawn faint; SeparatorWidth is its
// width in cells, which the layout leaves for it.
const (
	Separator      = " │ "
	SeparatorWidth = 3
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
// text itself carries an attribute. A status's tone is one of the
// terminal's own sixteen colours, so the user's theme picks the shade, and
// a terminal without colour (NO_COLOR) draws the text alone.
func styled(text string, s present.Style) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	switch s {
	case present.Strong:
		return lipgloss.NewStyle().Bold(true).Render(text)
	case present.Faint:
		return lipgloss.NewStyle().Faint(true).Render(text)
	case present.StatusOpen:
		return lipgloss.NewStyle().Foreground(lipgloss.Cyan).Render(text)
	case present.StatusActive:
		return lipgloss.NewStyle().Foreground(lipgloss.Yellow).Render(text)
	case present.StatusDone:
		return lipgloss.NewStyle().Foreground(lipgloss.Green).Render(text)
	case present.NoticeProblem:
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Red).Render(text)
	case present.NoticeWaiting:
		return lipgloss.NewStyle().Foreground(lipgloss.Yellow).Render(text)
	}
	return text
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
	reading := min(width, ReadingWidth)
	indent := max(0, st.Indent)
	if reading-indent < minIndented {
		indent = 0
	}
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
	if st.Rule && len(out) > 0 {
		last := &out[len(out)-1]
		if room := reading - method.StringWidth(last.plain()); room >= 3 {
			*last = append(*last, part{text: " "}, part{text: strings.Repeat(rule, room-1), style: present.Faint})
		}
	}
	return out
}
