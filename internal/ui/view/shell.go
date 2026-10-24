package view

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/uitext"
)

// Render draws the screen into the geometry: a header, a context line, the
// visible panes side by side, each in its frame, and the help at the
// bottom. The help lines
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
	context := contextLine(s, g, focus)
	lines := []string{fit(header, g.Width, method)}
	switch {
	case s.Status.String() == "":
		lines = append(lines, fit(context, g.Width, method))
	case isHint(s):
		// A hint takes the path's line, and the lines it wraps into under it.
		lines = append(lines, noticeLines(s.Status.String(), s.StatusStyle, g.Width, g.NoticeHeight+1, method)...)
	case g.NoticeHeight > 0:
		lines = append(append(lines, fit(context, g.Width, method)), noticeLines(s.Status.String(), s.StatusStyle, g.Width, g.NoticeHeight, method)...)
	default:
		line := screenLine{{text: context + statusSeparator}, {text: s.Status.String(), style: s.StatusStyle}}
		lines = append(lines, line.render(g.Width, method))
	}
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
		lines = append(lines, strings.Join(parts, ""))
	}
	return strings.Join(append(lines, helpLines...), "\n")
}

// statusSeparator stands between the context and a notice beside it.
const statusSeparator = " | "

// contextLine is where the person is: the path of two and three columns,
// the path of the panes in one, the chooser's title while two columns show
// it.
func contextLine(s present.Screen, g Geometry, focus Pane) string {
	switch {
	case g.Columns == 1:
		step := uitext.Inline(uitext.PathStep).String()
		context := s.Crumbs[0].String()
		if focus >= List {
			context += step + s.Crumbs[1].String()
		}
		if focus == Object {
			context += step + s.Crumbs[2].String()
		}
		return context
	case g.Columns == 2 && focus == Navigation:
		return s.Chooser.Title.String()
	}
	return s.Context.String()
}

// NoticeLines is the number of lines the screen's status notice needs
// under the context line of this geometry: 0 when there is none or it
// stands whole beside the context, else the lines it wraps into at the
// width; for a hint, which takes the context line itself, the lines it
// wraps into after that one. The caller gives it as many as the body can spare
// (Geometry.NoticeHeight), so it is not cut to a few words: for some keys
// (x) it is the only answer, and the longest notices are instructions.
func NoticeLines(s present.Screen, g Geometry, focus Pane, method ansi.Method) int {
	status := s.Status.String()
	switch {
	case status == "":
		return 0
	case isHint(s):
		// The first line is the path's.
		return len(wrapBody(status, g.Width, method)) - 1
	case method.StringWidth(contextLine(s, g, focus)+statusSeparator+status) <= g.Width:
		return 0
	}
	return len(wrapBody(status, g.Width, method))
}

// isHint: the screen's notice is a short hint (plain), which stands in the
// path's place (L2); a problem or a wait stands beside it.
func isHint(s present.Screen) bool {
	return s.Status.String() != "" && s.StatusStyle == present.Plain
}

// noticeLines is the notice wrapped at the width into at most height
// lines, each in the notice's tone; what does not fit is marked on the
// last one.
func noticeLines(status string, style present.Style, width, height int, method ansi.Method) []string {
	wrapped := wrapBody(status, width, method)
	if len(wrapped) > height {
		wrapped = append(wrapped[:height-1:height-1], strings.Join(wrapped[height-1:], " ")+uitext.Inline(uitext.Ellipsis).String())
	}
	for i, line := range wrapped {
		wrapped[i] = screenLine{{text: line, style: style}}.render(width, method)
	}
	return wrapped
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
// geometry shows at once: the body less the frame (the title stands in its
// top border) and the notice, divided by the row height.
func Capacity(c present.Column, g Geometry) int {
	body := g.BodyHeight - frameLines
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
// into inside the pane's frame. More than Capacity means there is
// something to scroll to.
func Extent(c present.Column, g Geometry, which Pane, method ansi.Method) int {
	if c.Rows != nil {
		return len(c.Rows)
	}
	width := InnerWidth(g, which)
	if width <= 0 {
		return 0
	}
	return len(textLines(c, textWidth(width), method))
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

// pane is a column in its frame, width by height: the title in the top
// border, the text inside the sides. The focused pane's frame is blue and
// bold, the others' faint, so the eye finds where the keys act. A pane too
// small for a frame is its text alone.
func pane(c present.Column, width, height int, focused bool, method ansi.Method) []string {
	if height <= 0 {
		return nil
	}
	if width <= frameCells || height <= frameLines {
		return content(c, width, height, focused, method)
	}
	border := lipgloss.NewStyle().Faint(true)
	title := lipgloss.NewStyle()
	if focused {
		border = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Blue)
		title = title.Bold(true)
	}
	run := width - 2 // the border between the corners
	lines := make([]string, 0, height)
	lines = append(lines, frameTop(c.Title.String(), run, border, title, method))
	left, right := border.Render(frameSide)+" ", " "+border.Render(frameSide)
	for _, line := range content(c, width-frameCells, height-frameLines, focused, method) {
		lines = append(lines, left+line+right)
	}
	return append(lines, border.Render(frameBottomLeft+strings.Repeat(rule, run)+frameBottomRight))
}

// frameTop is the top border, run cells between its corners, with the
// title in it, "╭─ Title ───╮"; a title wider than the border is cut, a
// border without room for one has none.
func frameTop(text string, run int, border, title lipgloss.Style, method ansi.Method) string {
	room := run - 3 // the rule before the title and a space either side
	text = fit(text, max(0, room), method)
	if room < 1 || text == "" {
		return border.Render(frameTopLeft + strings.Repeat(rule, run) + frameTopRight)
	}
	rest := run - 3 - method.StringWidth(text)
	return border.Render(frameTopLeft+rule+" ") + title.Render(text) + border.Render(" "+strings.Repeat(rule, rest)+frameTopRight)
}

// content is what stands inside a pane's frame, width by height, every
// line padded to the width: the notice, then the rows or the text.
func content(c present.Column, width, height int, focused bool, method ansi.Method) []string {
	if height <= 0 {
		return nil
	}
	lines := make([]string, height)
	at := 0
	if c.Notice.String() != "" {
		lines[0] = inline(c.Notice, width, method)
		at = 1
	}
	body := height - at
	var drawn []string
	if c.Rows != nil {
		drawn = rows(c, width, body, focused, method)
	} else {
		drawn = scrollable(c, width, body, method)
	}
	copy(lines[at:], drawn)
	for i := range lines {
		lines[i] += strings.Repeat(" ", max(0, width-method.StringWidth(lines[i])))
	}
	return lines
}

// rows renders the visible rows, the cursor kept in sight: the offset moves
// only when the cursor would be outside. The cursor row is a bar: reversed
// in the focused pane, on a grey ground elsewhere.
func rows(c present.Column, width, body int, focused bool, method ansi.Method) []string {
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
	// The values at the end stand in one column, right-aligned, as wide as
	// the widest of the list: a longer one does not move the titles' ends.
	trailing := 0
	for _, r := range c.Rows {
		trailing = max(trailing, method.StringWidth(r.Trailing.String()))
	}
	var out []string
	for i := offset; i < len(c.Rows) && len(out)+h <= body; i++ {
		cursor := i == c.Cursor
		bar := noBar
		switch {
		case cursor && focused:
			bar = focusBar
		case cursor:
			bar = restBar
		}
		out = append(out, row(c.Rows[i], width, trailing, cursor, bar, h == 2, method)...)
	}
	return out
}

// minAligned is the least room a column of values leaves for the text; in
// a narrower list the value follows each entry's own text, which keeps
// more of a title than the alignment is worth.
const minAligned = 20

// barKind is how a cursor row is drawn: not at all (not the cursor), as
// the bar of the focused pane, or as the quieter bar of a pane without the
// focus.
type barKind uint8

const (
	noBar barKind = iota
	focusBar
	restBar
)

// row is one entry: a marker, the first line with its trailing value at
// the end, in a column trailingWidth wide, and the second line indented
// and faint. The entry in use is bold, one with nothing to act on faint
// (its value's tone faint with it), a value that asks for attention bold.
// A bar (the cursor row) is drawn over the pane's whole width, both lines
// of it: reversed in the focused pane, so the eye finds where the keys
// act, on the terminal's grey (colour 8) elsewhere, so the place stays
// seen beside it. Faint and a status's tone give way in it (faint on a
// reversed ground is hard to read, and a tone would turn into a coloured
// block), bold stays. The marker stays too: a terminal that draws no
// attributes still shows the cursor.
func row(r present.Row, width, trailingWidth int, cursor bool, bar barKind, twoLines bool, method ansi.Method) []string {
	marker := "  "
	if cursor {
		marker = "> "
	}
	trailing := r.Trailing.String()
	if trailing == "" {
		// An entry without a value (a heading) takes the whole width.
		trailingWidth = 0
	}
	room := width - method.StringWidth(marker)
	if trailingWidth > 0 {
		room -= trailingWidth + 1
	}
	if room < minAligned {
		// No room for a column of values: the value follows the text.
		room = width - method.StringWidth(marker)
		if trailing != "" {
			room -= method.StringWidth(trailing) + 1
		}
		trailingWidth = method.StringWidth(trailing)
	}
	primary := fit(r.Primary.String(), max(0, room), method)
	value := ""
	if trailingWidth > 0 {
		value = strings.Repeat(" ", max(0, trailingWidth-method.StringWidth(trailing))) + trailing
		primary += strings.Repeat(" ", max(0, room-method.StringWidth(primary))) + " "
	}
	if method.StringWidth(marker+primary+value) > width {
		primary, value = fit(marker+primary+value, width, method), ""
		marker = ""
	}
	weight := present.Plain
	switch {
	case r.Active:
		weight = present.Strong
	case r.Quiet:
		weight = present.Faint
	}
	valueWeight := weight
	if r.TrailingStyle != present.Plain {
		valueWeight = r.TrailingStyle
	}
	if bar != noBar {
		lines := []string{barLine(marker+primary+value, width, r.Active, bar, method)}
		if twoLines {
			lines = append(lines, barLine("  "+fit(r.Secondary.String(), max(0, width-2), method), width, false, bar, method))
		}
		return lines
	}
	first := styled(marker+primary, weight) + styled(value, valueWeight)
	if r.Quiet && r.TrailingStyle != present.Plain {
		first = styled(marker+primary, weight) + quieted(value, r.TrailingStyle)
	}
	if !twoLines {
		return []string{first}
	}
	return []string{first, "  " + styled(fit(r.Secondary.String(), max(0, width-2), method), present.Faint)}
}

// barLine draws a line of a bar: padded to the width, every cell of it
// reversed or on the grey ground, bold when strong.
func barLine(text string, width int, strong bool, bar barKind, method ansi.Method) string {
	text += strings.Repeat(" ", max(0, width-method.StringWidth(text)))
	style := lipgloss.NewStyle().Reverse(true)
	if bar == restBar {
		style = lipgloss.NewStyle().Background(lipgloss.BrightBlack)
	}
	return style.Bold(strong).Render(text)
}

// The scrollbar of a free-text pane: a track down the pane's last column
// and on it a thumb, as long as the share of the text in sight and where
// that share is. The two differ in shape, not only in weight, so a
// terminal without attributes shows them; neither is the separator
// between panes.
const (
	thumb = "\u2503" // heavy vertical
	track = "\u254e" // light double dash vertical
)

// textWidth is the width a free-text pane wraps its text at: the pane less
// the scrollbar's column and a space before it, always, overflow or not. A
// part of a text (the place of a comment, which the ticket pane measures)
// then wraps as the whole does, and Extent counts what the pane draws. A
// pane too narrow for both has no bar.
func textWidth(width int) int {
	if width < 3 {
		return width
	}
	return width - 2
}

// scrollable is free text with its scrollbar when it overflows: the text
// at textWidth, each line padded, the bar in the pane's last column.
func scrollable(c present.Column, width, body int, method ansi.Method) []string {
	inner := textWidth(width)
	lines, offset, total := scrolledAt(c, inner, body, method)
	if inner == width || total <= body {
		return lines
	}
	bar := scrollbar(body, total, offset)
	for i := range lines {
		lines[i] += strings.Repeat(" ", max(0, width-1-method.StringWidth(lines[i]))) + bar[i]
	}
	return lines
}

// scrollbar is the bar's cells for a body of the given height over total
// lines from offset (clamped, so total-body at the end). The thumb is the
// visible share, at least one cell; it touches the bottom only at the end
// and, where there is room for a place between, the top only at the top,
// so a thumb at either end means it.
func scrollbar(body, total, offset int) []string {
	length := max(1, min(body, (body*body+total/2)/total))
	room, last := body-length, total-body
	start := (offset*room + last/2) / last
	if offset < last && room >= 1 {
		start = min(start, room-1)
	}
	if offset > 0 && room >= 2 {
		start = max(start, 1)
	}
	cells := make([]string, body)
	for i := range cells {
		if i >= start && i < start+length {
			cells[i] = thumb
		} else {
			cells[i] = lipgloss.NewStyle().Faint(true).Render(track)
		}
	}
	return cells
}

// scrolled renders free text from its offset, clamped so the last line is
// still in sight; an overflow is marked on the last line.
func scrolled(c present.Column, width, body int, method ansi.Method) []string {
	lines, _, _ := scrolledAt(c, width, body, method)
	return lines
}

// scrolledAt is scrolled with the clamped offset and the number of lines
// the text wraps into.
func scrolledAt(c present.Column, width, body int, method ansi.Method) (lines []string, offset, total int) {
	if width <= 0 || body <= 0 {
		return nil, 0, 0
	}
	all := textLines(c, width, method)
	total = len(all)
	offset = max(0, min(c.Offset, max(0, len(all)-body)))
	all = all[offset:]
	overflow := len(all) > body
	all = all[:min(len(all), body)]
	lines = make([]string, len(all))
	for i, l := range all {
		lines[i] = l.render(width, method)
	}
	if overflow && len(all) > 0 {
		last := len(all) - 1
		lines[last] = fit(all[last].plain()+uitext.Inline(uitext.Ellipsis).String(), width, method)
	}
	return lines, offset, total
}
