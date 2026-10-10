package ui

import (
	"strings"

	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/present"
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
		if v.query != "" {
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

// detailColumn is the ticket in the object pane: its head, the
// description, the first page of comments and of attachments; or what
// stands in for it (nothing opened, loading, the server's refusal).
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
	c.Text = present.NewBody(ticketText(d.record, a.pagedOn()))
	c.Offset = d.offset
	return c
}

// ticketText is the body of the pane. Every field is foreign; the whole is
// cleaned once as a body, so a title cannot open a line of its own but a
// description keeps its lines.
func ticketText(t api.Ticket, paged bool) string {
	label := func(id uitext.ID, value string) string { return uitext.Inline(id).String() + ": " + value }
	lines := []string{
		uitext.Fill(uitext.TicketNumber, t.Number).String() + " " + textOr(t.Title, uitext.UntitledTicket),
		label(uitext.LabelStatus, statusOf(t.Status)),
		label(uitext.LabelAssignees, assigneesOf(t.Assignees)),
		label(uitext.LabelCreator, personOf(t.Creator)),
		label(uitext.LabelProject, textOr(t.Project.Name, uitext.UnnamedProject)),
		label(uitext.LabelUpdated, timeOf(t.UpdatedAt, false)),
		"",
	}
	if strings.TrimSpace(present.NewBody(t.DescriptionText).String()) == "" {
		lines = append(lines, uitext.Inline(uitext.NoDescription).String())
	} else {
		lines = append(lines, t.DescriptionText)
	}
	lines = append(lines, "", commentsHeading(t, paged))
	for _, p := range t.Posts {
		head := personOf(p.Author) + " · " + timeOf(p.CreatedAt, true)
		if p.IsPrivate {
			head += " · " + uitext.Inline(uitext.Private).String()
		}
		body := p.BodyText
		if strings.TrimSpace(present.NewBody(body).String()) == "" {
			body = uitext.Inline(uitext.NoText).String()
		}
		lines = append(lines, head, body, "")
	}
	if len(t.Posts) == 0 {
		lines = append(lines, "")
	}
	lines = append(lines, attachmentsHeading(t, paged))
	for _, f := range t.Attachments {
		lines = append(lines, "- "+textOr(f.Filename, uitext.UnnamedFile))
	}
	return strings.Join(lines, "\n")
}

// commentsHeading says which comments are shown: the page, newest first,
// and the number of readable comments when the server pages; the first
// ones, oldest first, and whether they are all, when it does not.
func commentsHeading(t api.Ticket, paged bool) string {
	if t.PostsTotal == 0 && len(t.Posts) == 0 {
		return uitext.Inline(uitext.NoComments).String()
	}
	if paged {
		return uitext.Fill(uitext.CommentsPaged, t.PostsPage, max(1, t.PostsPages), t.PostsTotal).String()
	}
	if t.PostsTruncated {
		return uitext.Fill(uitext.CommentsFirst, len(t.Posts), t.PostsTotal).String()
	}
	return uitext.Fill(uitext.CommentsAll, t.PostsTotal).String()
}

func attachmentsHeading(t api.Ticket, paged bool) string {
	if t.AttachmentsTotal == 0 && len(t.Attachments) == 0 {
		return uitext.Inline(uitext.NoAttachments).String()
	}
	if paged {
		return uitext.Fill(uitext.AttachmentsPaged, t.AttachmentsPage, max(1, t.AttachmentsPages), t.AttachmentsTotal).String()
	}
	if t.AttachmentsTruncated {
		return uitext.Fill(uitext.AttachmentsFirst, len(t.Attachments), t.AttachmentsTotal).String()
	}
	return uitext.Fill(uitext.AttachmentsAll, t.AttachmentsTotal).String()
}
