package ui

import (
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
// the end, the assignees and the time of the last change below. While the
// search line is open it takes the notice.
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
	c.Rows = make([]present.Row, 0, len(v.rows))
	for i, r := range v.rows {
		if r.ID == v.selected {
			c.Cursor = i
		}
		c.Rows = append(c.Rows, present.Row{
			Primary:   present.NewInline(uitext.Fill(uitext.TicketNumber, r.Number).String() + " " + textOr(r.Title, uitext.UntitledTicket)),
			Trailing:  present.NewInline(bounded(statusOf(r.Status), maxStatus)),
			Secondary: present.NewInline(assigneesOf(r.Assignees) + " | " + timeOf(r.UpdatedAt, false)),
		})
	}
	c.Offset = v.offset
	return c
}

// maxStatus bounds a status at the end of a row: a status is a word, and
// a long one must not take the title's room.
const maxStatus = 20

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
	if !d.known {
		c.Text = uitext.Body(uitext.NoItem)
		return c
	}
	t := ticketText(d, a.pagedOn())
	c.Text = present.NewBody(strings.Join(t.lines, "\n"))
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
		c.Offset = m.wrappedLines(t.lines[:line])
	}
	return c
}

// wrappedLines is the number of screen lines the text's lines take in
// the object pane at the current width: the place of the line after them.
// The width alone decides it, never the help, so the help's bindings can
// ask for the pane without asking for themselves.
func (m Model) wrappedLines(lines []string) int {
	g := layout(m.width, m.height)
	n := 0
	for _, line := range lines {
		n += max(1, view.Extent(present.Column{Text: present.NewBody(line)}, g, view.Object, m.widthMethod))
	}
	return n
}

// ticketLines is the text of the pane, one cleaned line each, with the
// line of every section's heading and of every comment's head.
type ticketLines struct {
	lines []string
	areas [ticketAreas]int
	posts map[string]int
}

// add appends foreign or own text: a body is cleaned once and split into
// its lines, so the places counted are the lines shown.
func (t *ticketLines) add(texts ...string) {
	for _, text := range texts {
		t.lines = append(t.lines, strings.Split(present.NewBody(text).String(), "\n")...)
	}
}

// ticketText is the body of the pane. Every field is foreign; a one-line
// field is cleaned as one line, so a title cannot open a line of its own,
// but a description or a comment keeps its lines.
func ticketText(d *detailView, paged bool) ticketLines {
	r := d.record
	t := ticketLines{posts: map[string]int{}}
	label := func(id uitext.ID, value string) string { return uitext.Inline(id).String() + ": " + value }
	t.add(
		uitext.Fill(uitext.TicketNumber, r.Number).String()+" "+textOr(r.Title, uitext.UntitledTicket),
		label(uitext.LabelStatus, statusOf(r.Status)),
		label(uitext.LabelAssignees, assigneesOf(r.Assignees)),
		label(uitext.LabelCreator, personOf(r.Creator)),
		label(uitext.LabelProject, textOr(r.Project.Name, uitext.UnnamedProject)),
		label(uitext.LabelUpdated, timeOf(r.UpdatedAt, false)),
		"",
	)
	heading := func(area ticketArea, text string) {
		t.areas[area] = len(t.lines)
		if d.area == area {
			text = uitext.Inline(uitext.ActiveArea).String() + text
		}
		t.add(present.NewInline(text).String())
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
	t.add(bodyOr(text, uitext.NoDescription)...)
	if e != nil && e.post == "" {
		t.add(expansionState(e)...)
	}
	t.add("")

	heading(areaComments, commentsHeading(d, paged))
	switch {
	case d.commentsLoading():
		t.add(pageState(d), "")
	case len(r.Posts) == 0:
		t.add("")
	}
	for _, p := range r.Posts {
		if d.commentsLoading() {
			break
		}
		t.posts[p.ID] = len(t.lines)
		head := personOf(p.Author) + " · " + timeOf(p.CreatedAt, true)
		if p.IsPrivate {
			head += " · " + uitext.Inline(uitext.Private).String()
		}
		if d.area == areaComments && p.ID == d.post {
			head = uitext.Inline(uitext.SelectedComment).String() + head
		}
		t.add(present.NewInline(head).String())
		switch {
		case e != nil && e.post == p.ID && e.unavailable:
			// The server said the comment is missing: its text is hidden
			// at once, the page's copy too, until Esc or the next page.
			t.add(uitext.Inline(uitext.CommentUnavailable).String())
		case e != nil && e.post == p.ID && e.known:
			t.add(bodyOr(e.text, uitext.NoText)...)
			t.add(expansionState(e)...)
		default:
			t.add(bodyOr(p.BodyText, uitext.NoText)...)
			if e != nil && e.post == p.ID {
				t.add(expansionState(e)...)
			}
		}
		t.add("")
	}

	heading(areaAttachments, attachmentsHeading(d, paged))
	if d.attachmentsLoading() {
		t.add(pageState(d))
		return t
	}
	for _, f := range r.Attachments {
		t.add("- " + attachmentLine(f))
	}
	return t
}

// pageState stands in for a section whose page has not come: it loads,
// or it failed and R reads it again.
func pageState(d *detailView) string {
	if d.loading {
		return uitext.Inline(uitext.LoadingPage).String()
	}
	return uitext.Inline(uitext.PageNotLoaded).String()
}

// bodyOr is a foreign body as lines, without the server's marker of a
// shortened text and with a line that says so, or the fallback when
// nothing readable is left of it.
func bodyOr(raw string, fallback uitext.ID) []string {
	text, short := api.Shortened(raw)
	if strings.TrimSpace(present.NewBody(text).String()) == "" {
		text = uitext.Inline(fallback).String()
	}
	if short {
		return []string{text, uitext.Inline(uitext.TextShortened).String()}
	}
	return []string{text}
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
// zero bytes). One line: every value is cleaned as one.
func attachmentLine(f api.Attachment) string {
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
	return textOr(f.Filename, uitext.UnnamedFile) + " · " + kind + " · " + size + " · " + uitext.Inline(text).String()
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
