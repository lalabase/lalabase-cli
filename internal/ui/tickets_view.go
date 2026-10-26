package ui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
	"github.com/gboyke/lalabase-cli/internal/uitext"
)

// The presentation of the tickets section: API and cache values become
// present values here and nowhere else.

// projectsColumn lists the projects of the organisation, archived ones
// marked.
func (m Model) projectsColumn() present.Column {
	v := m.app.projects
	c := present.Column{Title: uitext.Inline(uitext.Projects), Cursor: -1}
	c.Notice = v.notice(uitext.NoProjects, len(v.rows) == 0)
	c.Rows = make([]present.Row, 0, len(v.rows))
	for i, p := range v.rows {
		if p.ID == v.selected {
			c.Cursor = i
		}
		row := present.Row{Primary: present.NewInline(textOr(p.Name, uitext.UnnamedProject))}
		if p.Archived {
			row.Trailing = uitext.Inline(uitext.Archived)
		}
		c.Rows = append(c.Rows, row)
	}
	c.Offset = v.offset
	return c
}

// ticketsColumn is the page of tickets: number and title, the status at
// the end, the assignees and the time of the last change below. The
// numbers stand right-aligned in a column as wide as the page's widest, so
// the titles and the lines below them start in one column; a done ticket
// is quiet, so the open ones stand out. Before the number, the glyph of
// the most urgent agent at the ticket (C9a). While the search line is open
// it takes the notice.
func (m Model) ticketsColumn() present.Column {
	v := m.app.tickets
	c := present.Column{Title: uitext.Inline(uitext.Tickets), Cursor: -1}
	if m.mode == TextEntry {
		c.Notice = present.NewInline(uitext.Inline(uitext.SearchLine).String() + v.entry + uitext.Inline(uitext.Caret).String())
	} else {
		empty := uitext.NoTickets
		switch {
		case v.total > 0:
			empty = uitext.NoneOnTicketPage
		case v.query != "":
			empty = uitext.NoMatches
		}
		c.Notice = v.notice(empty, len(v.rows) == 0)
	}
	numbers := make([]string, len(v.rows))
	width := 0
	for i, r := range v.rows {
		numbers[i] = uitext.Fill(uitext.TicketNumber, r.Number).String()
		width = max(width, len(numbers[i]))
	}
	c.Rows = make([]present.Row, 0, len(v.rows))
	for i, r := range v.rows {
		if r.ID == v.selected {
			c.Cursor = i
		}
		tone := statusStyle(r.StatusFunctions, present.Plain)
		row := present.Row{
			Primary:       present.NewInline(fmt.Sprintf("%*s %s", width, numbers[i], textOr(r.Title, uitext.UntitledTicket))),
			Trailing:      present.NewInline(bounded(statusOf(r.Status), maxStatus)),
			TrailingStyle: tone,
			Secondary:     present.NewInline(strings.Repeat(" ", width+1) + assigneesOf(r.Assignees) + " · " + m.app.when(r.UpdatedAt)),
			Quiet:         tone == present.StatusDone,
		}
		m.app.markAgent(&row, r.ID)
		c.Rows = append(c.Rows, row)
	}
	c.Offset = v.offset
	return c
}

// maxStatus bounds a status at the end of a row: a status is a word, and
// a long one must not take the title's room.
const maxStatus = 20

// statusTones are the tones of the status functions the core knows
// (SystemFunctions::Registry::STATUS_FUNCTIONS), in its lifecycle order.
// A status may carry more than one; the last one in this order wins, so a
// status that is new and in progress is drawn in progress.
var statusTones = []struct {
	function string
	style    present.Style
}{
	{"is_backlog", present.Faint},
	{"is_new", present.StatusOpen},
	{"is_open", present.StatusOpen},
	{"is_in_progress", present.StatusActive},
	{"is_completed", present.StatusDone},
	{"is_closed", present.StatusDone},
}

// statusStyle is the tone of a status by its functions; otherwise when it
// has none the client knows, as from a server before the field.
func statusStyle(functions []string, otherwise present.Style) present.Style {
	style := otherwise
	for _, tone := range statusTones {
		if slices.Contains(functions, tone.function) {
			style = tone.style
		}
	}
	return style
}

func statusOf(status *string) string {
	if status == nil {
		return uitext.Inline(uitext.NoStatus).String()
	}
	return textOr(*status, uitext.NoStatus)
}

func assigneesOf(refs []api.Ref) string {
	if len(refs) == 0 {
		return uitext.Inline(uitext.Unassigned).String()
	}
	names := make([]string, 0, len(refs))
	for _, r := range refs {
		names = append(names, textOr(r.Name, uitext.UnknownUser))
	}
	return strings.Join(names, ", ")
}

func personOf(ref *api.Ref) string {
	if ref == nil {
		return uitext.Inline(uitext.UnknownUser).String()
	}
	return textOr(ref.Name, uitext.UnknownUser)
}

// detailColumn is the ticket in the object pane: its head and its three
// sections (description, a page of comments, a page of attachments); or
// what stands in for it (nothing opened, loading, the server's refusal).
// The pane keeps the active section's heading or the selected comment in
// sight until the person scrolls (C1d).
func (m Model) detailColumn() present.Column {
	a := m.app
	c := present.Column{Title: uitext.Inline(uitext.Ticket), Cursor: -1}
	d := a.detail
	if d == nil {
		c.Text = uitext.Body(uitext.NoItem)
		if a.tickets != nil && !a.ticketOn() {
			c.Notice = uitext.Inline(uitext.TicketUnsupported)
		}
		return c
	}
	c.Notice = d.notice(uitext.NoItem, false)
	if o := a.origin; o != nil && c.Notice.String() == "" && a.marks[o.notification] != nil {
		// The outcome of marking the notification (C1f) in the line that is
		// always in sight: the head may be scrolled away.
		c.Notice = present.NewInline(a.markText(o.notification, o.unread))
	}
	if !d.known {
		c.Text = uitext.Body(uitext.NoItem)
		return c
	}
	// The facts stand in the bar where the pane has room for it, else in
	// the head; the width alone decides, as for wrappedLines.
	meta := view.MetaFits(view.InnerWidth(layoutWith(m.width, m.height, 1, m.focus), view.Object))
	tc := ticketContext{paged: a.pagedOn(), origin: m.originLine(), when: a.when, me: a.ownID(), meta: meta}
	if !meta {
		// The agent's line stands in the head, or in the bar (C9b).
		tc.agentGlyph, tc.agentStyle, tc.agent, _ = a.agentHead(d.id)
	}
	t := ticketText(d, tc)
	c.Text = present.NewBody(strings.Join(t.lines, "\n"))
	c.Styles = t.styles
	if meta {
		c.Meta = append(metaBar(d.record, a.when), a.agentMeta(d.id)...)
	}
	c.Offset = d.offset
	line := -1
	switch d.follow {
	case anchorArea:
		line = t.areas[d.area]
	case anchorPost:
		if at, ok := t.posts[d.post]; ok {
			line = at
		}
	}
	if line >= 0 {
		c.Offset = m.wrappedLines(t, line, c.Meta)
	}
	return c
}

// metaBar is the ticket's facts for the bar on the pane's right (L2): its
// status in the tone of its meaning, its project, its people (each
// assignee on a line, then the creator) and the time of its last change,
// each under a faint label. The server sends no time of creation.
func metaBar(r api.Ticket, when func(string) string) []present.MetaLine {
	label := func(id uitext.ID) present.MetaLine {
		return present.MetaLine{Text: uitext.Inline(id), Style: present.Faint}
	}
	value := func(text string) present.MetaLine { return present.MetaLine{Text: present.NewInline(text)} }
	bar := []present.MetaLine{
		label(uitext.BarStatus),
		{Text: present.NewInline(statusOf(r.Status)), Style: statusStyle(r.StatusFunctions, present.Plain)},
		{},
		label(uitext.BarProject),
		value(textOr(r.Project.Name, uitext.UnnamedProject)),
		{},
		label(uitext.BarPeople),
	}
	if len(r.Assignees) == 0 {
		bar = append(bar, value(uitext.Inline(uitext.Unassigned).String()))
	}
	for _, p := range r.Assignees {
		bar = append(bar, value(textOr(p.Name, uitext.UnknownUser)))
	}
	return append(bar,
		value(uitext.Fill(uitext.MetaCreator, personOf(r.Creator)).String()),
		present.MetaLine{},
		label(uitext.BarUpdated),
		value(when(r.UpdatedAt)),
	)
}

// wrappedLines is the number of screen lines the text's first n lines take
// in the object pane at the current width, in their styles, beside the
// meta bar if it stands: the place of the line after them. The width alone
// decides it, never the help, so the help's bindings can ask for the pane
// without asking for themselves.
func (m Model) wrappedLines(t ticketLines, n int, meta []present.MetaLine) int {
	g := layoutWith(m.width, m.height, 1, m.focus)
	return view.Extent(present.Column{Text: present.NewBody(strings.Join(t.lines[:n], "\n")), Styles: t.styles[:n], Meta: meta}, g, view.Object, m.widthMethod) - boolInt(n == 0)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ticketLines is the text of the pane, one cleaned line each with its
// style, and the line of every section's heading and of every comment's
// head.
type ticketLines struct {
	lines  []string
	styles []present.LineStyle
	areas  [ticketAreas]int
	posts  map[string]int
}

// add appends foreign or own text in one style: a body is cleaned once and
// split into its lines, so the places counted are the lines shown.
func (t *ticketLines) add(style present.LineStyle, texts ...string) {
	for _, text := range texts {
		for _, line := range strings.Split(present.NewBody(text).String(), "\n") {
			t.lines = append(t.lines, line)
			t.styles = append(t.styles, style)
		}
	}
}

// addLead appends one line whose lead (already cleaned, one line) is drawn
// in its own style before the rest: an author before the time.
func (t *ticketLines) addLead(lead string, leadStyle present.Style, rest string, restStyle present.Style) {
	line := present.NewInline(lead + rest).String()
	n := len(lead)
	if !strings.HasPrefix(line, lead) {
		// Cleaning changed the lead's bytes: the line is drawn in one style.
		n = 0
	}
	t.lines = append(t.lines, line)
	t.styles = append(t.styles, present.LineStyle{Lead: n, LeadStyle: leadStyle, RestStyle: restStyle})
}

// The styles of the ticket's lines.
var (
	plainLine = present.LineStyle{}
	bodyLine  = present.LineStyle{Indent: 2}
	noteLine  = present.LineStyle{Indent: 2, RestStyle: present.Faint}
)

// ticketContext is what the pane's text takes beside the record: whether
// the server pages, the line of the notification it was opened from, the
// clock, the token's own person (empty while it is not known), and whether
// the facts stand in the meta bar instead of the head.
type ticketContext struct {
	paged  bool
	origin string
	when   func(string) string
	me     string
	meta   bool
	// agent is the line of the ticket's most urgent agent (C9a), after
	// its glyph in its tone; "" for none.
	agent      string
	agentGlyph string
	agentStyle present.Style
}

// ownID is the user id of the token's own person: empty until /me
// succeeded, so nobody is taken for the own person before.
func (a *app) ownID() string {
	if a.steps[stepMe].state != ready {
		return ""
	}
	return a.me.User.ID
}

// ticketText is the body of the pane. Every field is foreign; a one-line
// field is cleaned as one line, so a title cannot open a line of its own,
// but a description or a comment keeps its lines.
func ticketText(d *detailView, c ticketContext) ticketLines {
	r, paged, origin, when := d.record, c.paged, c.origin, c.when
	t := ticketLines{posts: map[string]int{}}
	// The head: the title, then one faint line of its facts, unless they
	// stand in the meta bar, then the line of its agent, if it has one.
	t.addLead(uitext.Fill(uitext.TicketNumber, r.Number).String()+" "+textOr(r.Title, uitext.UntitledTicket), present.Strong, "", present.Strong)
	if !c.meta {
		assigned := uitext.Inline(uitext.MetaUnassigned).String()
		if len(r.Assignees) > 0 {
			assigned = uitext.Fill(uitext.MetaAssigned, assigneesOf(r.Assignees)).String()
		}
		// The status leads the facts, in the tone of its meaning.
		t.addLead(present.NewInline(statusOf(r.Status)).String(), statusStyle(r.StatusFunctions, present.Faint), " · "+strings.Join([]string{
			textOr(r.Project.Name, uitext.UnnamedProject),
			assigned,
			uitext.Fill(uitext.MetaCreator, personOf(r.Creator)).String(),
			uitext.Fill(uitext.MetaUpdated, when(r.UpdatedAt)).String(),
		}, " · "), present.Faint)
	}
	if c.agent != "" {
		// What the ticket's agent does (C9a), under the facts.
		t.addLead(c.agentGlyph, c.agentStyle, " "+c.agent, present.Plain)
	}
	if origin != "" {
		t.add(plainLine, uitext.Inline(uitext.LabelNotification).String()+": "+origin)
	}
	t.add(plainLine, "")
	heading := func(area ticketArea, text string) {
		t.areas[area] = len(t.lines)
		if d.area == area {
			text = uitext.Inline(uitext.ActiveArea).String() + text
		}
		text = present.NewInline(text).String()
		t.lines = append(t.lines, text)
		t.styles = append(t.styles, present.LineStyle{Lead: len(text), LeadStyle: present.Strong, Rule: true})
	}

	// The description, whole when it was expanded.
	e := d.expansion
	whole := e != nil && e.post == "" && e.known
	title := uitext.Inline(uitext.Description).String()
	if whole {
		title += " · " + uitext.Inline(uitext.WholeText).String()
	}
	heading(areaDescription, title)
	text := r.DescriptionText
	if whole {
		text = e.text
	}
	t.body(bodyOr(text, uitext.NoDescription))
	if e != nil && e.post == "" {
		t.add(noteLine, expansionState(e)...)
	}
	t.add(plainLine, "")

	heading(areaComments, commentsHeading(d, paged))
	switch {
	case d.commentsLoading():
		t.add(noteLine, pageState(d))
		t.add(plainLine, "")
	case len(r.Posts) == 0:
		t.add(plainLine, "")
	default:
		t.add(plainLine, "")
	}
	for _, p := range r.Posts {
		if d.commentsLoading() {
			break
		}
		// The head: the author bold, the time faint; the text indented
		// below it. The token's own person (by the id alone, never the
		// name) is blue and marked, so the mark stays without colour.
		t.posts[p.ID] = len(t.lines)
		author, tone := present.NewInline(personOf(p.Author)).String(), present.Strong
		if c.me != "" && p.Author != nil && p.Author.ID == c.me {
			author, tone = author+" "+uitext.Inline(uitext.You).String(), present.Own
		}
		if d.area == areaComments && p.ID == d.post {
			author = uitext.Inline(uitext.SelectedComment).String() + author
		}
		rest := "  " + when(p.CreatedAt)
		if p.IsPrivate {
			rest += " · " + uitext.Inline(uitext.Private).String()
		}
		t.addLead(author, tone, rest, present.Faint)
		switch {
		case e != nil && e.post == p.ID && e.unavailable:
			// The server said the comment is missing: its text is hidden
			// at once, the page's copy too, until Esc or the next page.
			t.add(noteLine, uitext.Inline(uitext.CommentUnavailable).String())
		case e != nil && e.post == p.ID && e.known:
			t.body(bodyOr(e.text, uitext.NoText))
			t.add(noteLine, expansionState(e)...)
		default:
			t.body(bodyOr(p.BodyText, uitext.NoText))
			if e != nil && e.post == p.ID {
				t.add(noteLine, expansionState(e)...)
			}
		}
		t.add(plainLine, "")
	}

	heading(areaAttachments, attachmentsHeading(d, paged))
	if d.attachmentsLoading() {
		t.add(noteLine, pageState(d))
		return t
	}
	for _, f := range r.Attachments {
		name, rest := attachmentLine(f)
		t.addLead(name, present.Plain, rest, present.Faint)
		t.styles[len(t.styles)-1].Indent = 2
	}
	return t
}

// body appends a foreign text indented, and the line that says the server
// shortened it faint.
func (t *ticketLines) body(text string, shortened bool) {
	t.add(bodyLine, text)
	if shortened {
		t.add(noteLine, uitext.Inline(uitext.TextShortened).String())
	}
}

// originLine is the line of the head that names the notification a ticket
// was opened from (C1f): unread or read, and what became of marking it.
// Empty for a ticket of the tickets section.
func (m Model) originLine() string {
	a := m.app
	o := a.origin
	if o == nil {
		return ""
	}
	state := uitext.NotificationRead
	if o.unread {
		state = uitext.NotificationUnread
	}
	text := uitext.Inline(state).String()
	if mark := a.markText(o.notification, o.unread); mark != "" {
		text += " · " + mark
	}
	return text
}

// pageState stands in for a section whose page has not come: it loads,
// or it failed and R reads it again.
func pageState(d *detailView) string {
	if d.loading {
		return uitext.Inline(uitext.LoadingPage).String()
	}
	return uitext.Inline(uitext.PageNotLoaded).String()
}

// bodyOr is a foreign body without the server's marker of a shortened
// text, and whether it was shortened; the fallback when nothing readable is
// left of it.
func bodyOr(raw string, fallback uitext.ID) (string, bool) {
	text, short := api.Shortened(raw)
	if strings.TrimSpace(present.NewBody(text).String()) == "" {
		text = uitext.Inline(fallback).String()
	}
	return text, short
}

// expansionState is the line under an expansion that is not settled: it
// loads, or it failed.
func expansionState(e *expansion) []string {
	switch {
	case e.err.String() != "":
		return []string{e.err.String()}
	case e.loading && !e.known:
		return []string{uitext.Inline(uitext.LoadingWholeText).String()}
	case e.stale:
		return []string{uitext.Inline(uitext.PossiblyStale).String()}
	}
	return nil
}

// commentsHeading says which comments are shown: the order, the page and
// the number of readable comments the server counts when it pages; the
// first ones, oldest first, and whether they are all, when it does not.
// While another page loads, the page asked for.
func commentsHeading(d *detailView, paged bool) string {
	t := d.record
	if t.PostsTotal == 0 && len(t.Posts) == 0 {
		return uitext.Inline(uitext.NoComments).String()
	}
	if paged && pagedKey(d.key) {
		order := uitext.OldestFirst
		if d.key.Newest {
			order = uitext.NewestFirst
		}
		pages := max(1, t.PostsPages, d.key.PostsPage)
		return uitext.Fill(uitext.CommentsPaged, uitext.Inline(order).String(), d.key.PostsPage, pages, t.PostsTotal).String()
	}
	if t.PostsTruncated {
		return uitext.Fill(uitext.CommentsFirst, len(t.Posts), t.PostsTotal).String()
	}
	return uitext.Fill(uitext.CommentsAll, t.PostsTotal).String()
}

func attachmentsHeading(d *detailView, paged bool) string {
	t := d.record
	if t.AttachmentsTotal == 0 && len(t.Attachments) == 0 {
		return uitext.Inline(uitext.NoAttachments).String()
	}
	if paged && pagedKey(d.key) {
		pages := max(1, t.AttachmentsPages, d.key.AttachmentsPage)
		return uitext.Fill(uitext.AttachmentsPaged, d.key.AttachmentsPage, pages, t.AttachmentsTotal).String()
	}
	if t.AttachmentsTruncated {
		return uitext.Fill(uitext.AttachmentsFirst, len(t.Attachments), t.AttachmentsTotal).String()
	}
	return uitext.Fill(uitext.AttachmentsAll, t.AttachmentsTotal).String()
}

// attachmentLine is one attachment: its name, and of its metadata what
// the server sent; what it did not send says so (a missing size is not
// zero bytes). One line: every value is cleaned as one. The name and the
// rest come apart, so the rest can be drawn faint.
func attachmentLine(f api.Attachment) (string, string) {
	kind := uitext.Inline(uitext.TypeUnknown).String()
	if f.ContentType != nil && strings.TrimSpace(present.NewInline(*f.ContentType).String()) != "" {
		kind = bounded(present.NewInline(*f.ContentType).String(), maxContentType)
	}
	size := uitext.Inline(uitext.SizeUnknown).String()
	if f.ByteSize != nil {
		size = sizeOf(*f.ByteSize)
	}
	text := uitext.TextStatusUnknown
	if f.TextStatus != nil {
		if id, ok := textStatuses[*f.TextStatus]; ok {
			text = id
		}
	}
	return textOr(f.Filename, uitext.UnnamedFile), " · " + kind + " · " + size + " · " + uitext.Inline(text).String()
}

// maxContentType bounds a MIME type: it is a short word, and a long one
// must not push the rest of the line out.
const maxContentType = 60

// textStatuses are the server's four words for an attachment's text
// (core ExtractPlainTextProcessingService.text_status); any other word is
// unknown.
var textStatuses = map[string]uitext.ID{
	"available": uitext.TextAvailable, "unsupported": uitext.TextUnsupported,
	"unavailable": uitext.TextUnavailable, "empty": uitext.TextEmpty,
}

// sizeOf is a size in bytes, binary units from 1 KiB on.
func sizeOf(n int64) string {
	const unit = 1024
	switch {
	case n < unit:
		return uitext.Fill(uitext.SizeBytes, n).String()
	case n < unit*unit:
		return uitext.Fill(uitext.SizeKiB, float64(n)/unit).String()
	case n < unit*unit*unit:
		return uitext.Fill(uitext.SizeMiB, float64(n)/(unit*unit)).String()
	}
	return uitext.Fill(uitext.SizeGiB, float64(n)/(unit*unit*unit)).String()
}
