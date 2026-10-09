package view

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/uitext"
)

// Render draws the screen into the geometry: a header, a context line, the
// visible panes side by side, and the help at the bottom. The help lines
// were counted into the geometry by the caller (HelpLines), so nothing of
// it is cut.
func Render(s present.Screen, g Geometry, focus Pane, method ansi.Method) string {
	if g.Width <= 0 || g.Height <= 0 {
		return ""
	}
	helpLines := HelpLines(s.Help, g.Width, method)
	if g.Small {
		lines := make([]string, g.Height)
		lines[0] = inline(uitext.Inline(uitext.TooSmall), g.Width, method)
		for i, line := range helpLines {
			if at := g.Height - len(helpLines) + i; at > 0 {
				lines[at] = line
			}
		}
		return strings.Join(lines, "\n")
	}
	header := uitext.Inline(uitext.App).String() + " | " + uitext.Inline(uitext.DefaultProfile).String()
	if s.Identity.String() != "" {
		header += " | " + s.Identity.String()
	}
	context := s.Context.String()
	if g.Columns == 1 {
		context = s.Crumbs[0].String()
		if focus >= List {
			context += " > " + s.Crumbs[1].String()
		}
		if focus == Object {
			context += " > " + s.Crumbs[2].String()
		}
	} else if g.Columns == 2 && focus == Navigation {
		context = s.Chooser.Title.String()
	}
	if s.Status.String() != "" {
		context += " | " + s.Status.String()
	}
	lines := []string{fit(header, g.Width, method), fit(context, g.Width, method)}
	panes := Visible(g, focus)
	columns := make([][]string, len(panes))
	for i, p := range panes {
		columns[i] = pane(column(s, p), ColumnWidth(g, p), g.BodyHeight, p == focus, method)
	}
	for row := 0; row < g.BodyHeight; row++ {
		parts := make([]string, len(columns))
		for i := range columns {
			parts[i] = columns[i][row]
		}
		lines = append(lines, strings.Join(parts, "|"))
	}
	return strings.Join(append(lines, helpLines...), "\n")
}

func column(s present.Screen, which Pane) present.Column {
	switch which {
	case List:
		return s.List
	case Object:
		return s.Object
	}
	return s.Chooser
}

// Capacity is the number of rows (or lines of text) a column of this
// geometry shows at once: the body less the title and the notice, divided
// by the row height.
func Capacity(c present.Column, g Geometry) int {
	body := g.BodyHeight - 1
	if c.Notice.String() != "" {
		body--
	}
	if body <= 0 {
		return 0
	}
	if c.Rows == nil {
		return body
	}
	return max(1, body/rowHeight(c, body))
}

// Extent is what a column holds: its rows, or the lines its text wraps
// into at the pane's width. More than Capacity means there is something
// to scroll to.
func Extent(c present.Column, g Geometry, which Pane, method ansi.Method) int {
	if c.Rows != nil {
		return len(c.Rows)
	}
	width := ColumnWidth(g, which)
	if width <= 0 {
		return 0
	}
	return len(wrapBody(strings.ReplaceAll(c.Text.String(), "\t", "    "), width, method))
}

// compactBelow is the body height below which rows show their first line
// only: on a short terminal two lines per row leave too few rows.
const compactBelow = 8

// rowHeight is 2 for rows with a second line, 1 in compact mode or for rows
// without one.
func rowHeight(c present.Column, body int) int {
	if body < compactBelow {
		return 1
	}
	for _, r := range c.Rows {
		if r.Secondary.String() != "" {
			return 2
		}
	}
	return 1
}

func pane(c present.Column, width, height int, focused bool, method ansi.Method) []string {
	if height <= 0 {
		return nil
	}
	lines := make([]string, height)
	lines[0] = inline(c.Title, width, method)
	if focused {
		lines[0] = lipgloss.NewStyle().Bold(true).Render(lines[0])
	}
	at := 1
	if c.Notice.String() != "" && height > 1 {
		lines[1] = inline(c.Notice, width, method)
		at = 2
	}
	body := height - at
	var content []string
	if c.Rows != nil {
		content = rows(c, width, body, method)
	} else {
		content = scrolled(c, width, body, method)
	}
	copy(lines[at:], content)
	for i := range lines {
		lines[i] += strings.Repeat(" ", max(0, width-method.StringWidth(lines[i])))
	}
	return lines
}

// rows renders the visible rows, the cursor kept in sight: the offset moves
// only when the cursor would be outside.
func rows(c present.Column, width, body int, method ansi.Method) []string {
	if body <= 0 || width <= 0 {
		return nil
	}
	h := rowHeight(c, body)
	capacity := max(1, body/h)
	offset := max(0, min(c.Offset, max(0, len(c.Rows)-capacity)))
	if c.Cursor >= 0 && c.Cursor < len(c.Rows) {
		if c.Cursor < offset {
			offset = c.Cursor
		}
		if c.Cursor >= offset+capacity {
			offset = c.Cursor - capacity + 1
		}
	}
	var out []string
	for i := offset; i < len(c.Rows) && len(out)+h <= body; i++ {
		out = append(out, row(c.Rows[i], width, i == c.Cursor, h == 2, method)...)
	}
	return out
}

// row is one entry: a marker, the first line with its trailing value at
// the end, and the second line indented. The entry in use is bold.
func row(r present.Row, width int, cursor, twoLines bool, method ansi.Method) []string {
	marker := "  "
	if cursor {
		marker = "> "
	}
	trailing := r.Trailing.String()
	room := width - method.StringWidth(marker)
	if trailing != "" {
		room -= method.StringWidth(trailing) + 1
	}
	first := fit(r.Primary.String(), max(0, room), method)
	if trailing != "" {
		first += strings.Repeat(" ", max(0, room-method.StringWidth(first))) + " " + trailing
	}
	first = fit(marker+first, width, method)
	if r.Active {
		first = lipgloss.NewStyle().Bold(true).Render(first)
	}
	if !twoLines {
		return []string{first}
	}
	return []string{first, fit("  "+r.Secondary.String(), width, method)}
}

// scrolled renders free text from its offset, clamped so the last line is
// still in sight; an overflow is marked on the last line.
func scrolled(c present.Column, width, body int, method ansi.Method) []string {
	if width <= 0 || body <= 0 {
		return nil
	}
	text := strings.ReplaceAll(c.Text.String(), "\t", "    ")
	lines := wrapBody(text, width, method)
	offset := max(0, min(c.Offset, max(0, len(lines)-body)))
	lines = lines[offset:]
	overflow := len(lines) > body
	lines = lines[:min(len(lines), body)]
	for i := range lines {
		lines[i] = fit(lines[i], width, method)
	}
	if overflow && len(lines) > 0 {
		last := len(lines) - 1
		lines[last] = fit(lines[last]+uitext.Inline(uitext.Ellipsis).String(), width, method)
	}
	return lines
}
