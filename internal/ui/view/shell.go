package view

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/uitext"
)

// Render draws the screen into the geometry: a header, the path line, the
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
	context := contextLine(s, g, focus)
	lines := []string{headerLine(s, g.Width, method)}
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
		if p == Navigation {
			columns[i] = navigation(s, ColumnWidth(g, p), g.BodyHeight, focus, method)
			continue
		}
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

// headerLine is the first line: the app, the profile and the identity on
// the left, the agents' summary at the right end (C9b) when there is one.
// Where the line is too narrow for both, the identity is cut first; then
// the word "Agents" goes, then the counts one by one from the least
// urgent; the app and the profile are never cut for the summary.
func headerLine(s present.Screen, width int, method ansi.Method) string {
	base := uitext.Inline(uitext.App).String() + " | " + uitext.Inline(uitext.DefaultProfile).String()
	header, least := base, base
	if s.Identity.String() != "" {
		header += " | " + s.Identity.String()
		least += " | " + uitext.Inline(uitext.Ellipsis).String()
	}
	counts := s.AgentCounts
	word := width >= summaryWordFrom
	for len(counts) > 0 {
		summary := agentSummary(counts, word)
		room := width - method.StringWidth(summary.plain()) - 1
		if room >= method.StringWidth(least) {
			left := fit(header, room, method)
			return left + strings.Repeat(" ", width-method.StringWidth(left)-method.StringWidth(summary.plain())) + summary.render(width, method)
		}
		if word {
			word = false
			continue
		}
		counts = counts[:len(counts)-1]
	}
	return fit(header, width, method)
}

// summaryWordFrom is the width from which the summary starts with its
// word: below it the glyphs say it alone.
const summaryWordFrom = 120

// agentSummary is the summary's line: the word, if any, then per state
// its glyph in its tone and its count, parted by " · ".
func agentSummary(counts []present.AgentCount, word bool) screenLine {
	var l screenLine
	if word {
		l = append(l, part{text: uitext.Inline(uitext.Agents).String() + " "})
	}
	for i, c := range counts {
		if i > 0 {
			l = append(l, part{text: summarySeparator})
		}
		l = append(l, part{text: c.Glyph.String(), style: c.Style}, part{text: " " + strconv.Itoa(c.Count)})
	}
	return l
}

// summarySeparator parts the summary's counts.
const summarySeparator = " · "

// statusSeparator stands between the path and a notice beside it.
const statusSeparator = " | "

// contextLine is where the person is: the path of two and three columns,
// the path of the panes in one, the chooser's title while two columns show
// it.
func contextLine(s present.Screen, g Geometry, focus Pane) string {
	switch {
	case focus == Agents && g.Columns < 3:
		return s.Agents.Title.String()
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
// under the path line of this geometry: 0 when there is none or it
// stands whole beside the path, else the lines it wraps into at the
// width; for a hint, which takes the path line itself, the lines it
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
	case Agents:
		return s.Agents
	}
	return s.Chooser
}

// navigation is the navigation pane: the organisations, and under them,
// while an agent exists, the Agents area in a frame of its own
// (AgentsHeight), focused when the focus is in it.
func navigation(s present.Screen, width, height int, focus Pane, method ansi.Method) []string {
	area := AgentsHeight(s.Agents, height)
	lines := pane(s.Chooser, width, height-area, focus == Navigation, method)
	if area == 0 {
		return lines
	}
	return append(lines, paneOf(s.Agents, width, area, focus == Agents, agentsRowLines(s.Agents, height), method)...)
}

// agentsRowLines is the height of the area's rows in a navigation pane of
// height lines: two with a second line, one without, as in any list; the
// half of the pane the area may take decides whether it is too short for
// two (compactBelow), never the few lines a short list of agents needs.
func agentsRowLines(c present.Column, height int) int {
	return rowHeight(c, height/2-frameLines)
}

// AgentsCapacity is the number of agents the area shows at once in a
// navigation pane of height lines; 0 without an area.
func AgentsCapacity(c present.Column, height int) int {
	body := AgentsHeight(c, height) - frameLines
	if c.Notice.String() != "" {
		body--
	}
	if body <= 0 {
		return 0
	}
	return max(1, body/agentsRowLines(c, height))
}

// AgentsHeight is the height of the Agents area under the organisations
// in a navigation pane of height lines: its frame and its rows, at most
// half the pane; 0 without rows, or where half the pane holds no frame
// with a row in it. Beyond that its rows scroll with the cursor.
func AgentsHeight(c present.Column, height int) int {
	limit := height / 2
	if len(c.Rows) == 0 || limit < frameLines+1 {
		return 0
	}
	need := frameLines + len(c.Rows)*agentsRowLines(c, height)
	if c.Notice.String() != "" {
		need++
	}
	return min(need, limit)
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
	width := textArea(c, InnerWidth(g, which))
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
	return paneOf(c, width, height, focused, 0, method)
}

// paneOf is pane with rows of lines lines each (0: by the body, rowHeight).
func paneOf(c present.Column, width, height int, focused bool, lines int, method ansi.Method) []string {
	if height <= 0 {
		return nil
	}
	if width <= frameCells || height <= frameLines {
		return contentOf(c, width, height, focused, lines, method)
	}
	border := lipgloss.NewStyle().Faint(true)
	title := lipgloss.NewStyle()
	if focused {
		border = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Blue)
		title = title.Bold(true)
	}
	run := width - 2 // the border between the corners
	out := make([]string, 0, height)
	out = append(out, frameTop(c.Title.String(), run, border, title, method))
	left, right := border.Render(frameSide)+" ", " "+border.Render(frameSide)
	for _, line := range contentOf(c, width-frameCells, height-frameLines, focused, lines, method) {
		out = append(out, left+line+right)
	}
	return append(out, border.Render(frameBottomLeft+strings.Repeat(rule, run)+frameBottomRight))
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
	return contentOf(c, width, height, focused, 0, method)
}

// contentOf is content with rows of rowLines lines each (0: by the body).
func contentOf(c present.Column, width, height int, focused bool, rowLines int, method ansi.Method) []string {
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
	switch text := textArea(c, width); {
	case c.Rows != nil:
		if rowLines == 0 {
			rowLines = rowHeight(c, body)
		}
		drawn = rows(c, width, body, rowLines, focused, method)
	case text < width:
		drawn = besideMeta(scrollable(c, text, body, method), c.Meta, text, body, method)
	default:
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
func rows(c present.Column, width, body, h int, focused bool, method ansi.Method) []string {
	if body <= 0 || width <= 0 {
		return nil
	}
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
	// The agent's column: its glyph and a space, or two spaces (C9a).
	glyph, mark, indent := "", "", "  "
	if r.Marked {
		glyph = r.Mark.String()
		if method.StringWidth(glyph) > 1 {
			glyph = ""
		}
		mark = glyph + strings.Repeat(" ", markCells-method.StringWidth(glyph))
		indent += strings.Repeat(" ", markCells)
	}
	trailing := r.Trailing.String()
	if trailing == "" {
		// An entry without a value (a heading) takes the whole width.
		trailingWidth = 0
	}
	lead := method.StringWidth(marker + mark)
	room := width - lead
	if trailingWidth > 0 {
		room -= trailingWidth + 1
	}
	if room < minAligned {
		// No room for a column of values: the value follows the text.
		room = width - lead
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
	if method.StringWidth(marker+mark+primary+value) > width {
		primary, value = fit(marker+mark+primary+value, width, method), ""
		marker, glyph, mark = "", "", ""
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
		// The glyph takes the bar's colour: its shape says the state.
		lines := []string{barLine(marker+mark+primary+value, width, r.Active, bar, method)}
		if twoLines {
			lines = append(lines, barLine(indent+fit(r.Secondary.String(), max(0, width-len(indent)), method), width, false, bar, method))
		}
		return lines
	}
	text := styled(marker+primary, weight)
	if mark != "" {
		// Not faint with a quiet row: an agent's state stays seen.
		text = styled(marker, weight) + styled(glyph, r.MarkStyle) + mark[len(glyph):] + styled(primary, weight)
	}
	first := text + styled(value, valueWeight)
	if r.Quiet && r.TrailingStyle != present.Plain {
		first = text + quieted(value, r.TrailingStyle)
	}
	if !twoLines {
		return []string{first}
	}
	return []string{first, indent + styled(fit(r.Secondary.String(), max(0, width-len(indent)), method), present.Faint)}
}

// markCells is the width of an agent's column: the glyph and a space.
const markCells = 2

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

// The meta bar of a wide ticket pane (L2): metaCells on the pane's right,
// a faint rule with a space either side and the lines after it; the text
// keeps at least minMetaText cells, its scrollbar included.
const (
	metaRule    = "\u2502"
	metaCells   = 24
	minMetaText = 52
)

// MetaFits: a pane of this inner width has room for the meta bar beside
// its text. The model asks it before it builds the bar, so the facts stand
// either in the bar or in the ticket's head, never in both or neither.
func MetaFits(width int) bool { return width >= minMetaText+metaCells }

// textArea is the width of a free-text pane's text: the pane less the meta
// bar where it stands.
func textArea(c present.Column, width int) int {
	if c.Rows == nil && len(c.Meta) > 0 && MetaFits(width) {
		return width - metaCells
	}
	return width
}

// besideMeta puts the meta bar after the text's lines, each padded to the
// text's width; the bar starts at the top whatever the text's offset, and
// a bar longer than the body is cut, its last line marked.
func besideMeta(text []string, meta []present.MetaLine, width, body int, method ansi.Method) []string {
	room := metaCells - 3
	rule := " " + lipgloss.NewStyle().Faint(true).Render(metaRule) + " "
	lines := make([]string, body)
	for i := range lines {
		left := ""
		if i < len(text) {
			left = text[i]
		}
		lines[i] = left + strings.Repeat(" ", max(0, width-method.StringWidth(left))) + rule
		if i >= len(meta) {
			continue
		}
		value, style := fit(meta[i].Text.String(), room, method), meta[i].Style
		if i == body-1 && len(meta) > body {
			value, style = fit(meta[i].Text.String()+uitext.Inline(uitext.Ellipsis).String(), room, method), present.Faint
		}
		lines[i] += styled(value, style)
	}
	return lines
}
