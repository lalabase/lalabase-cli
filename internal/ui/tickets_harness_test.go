package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/contract"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

// The harness of the tickets section (C1c): the fakes answer the projects
// of A from the contract, B has none; the tickets of a project are
// synthesised from the recorded row, the ticket from the recorded one.

// The recorded projects of A, by id.
const (
	website  = "00000000-0000-4000-8000-000000000031" // 30 tickets, two pages
	intern   = "00000000-0000-4000-8000-000000000032" // no tickets
	handbuch = "00000000-0000-4000-8000-000000000033" // 3 tickets
	archiv   = "00000000-0000-4000-8000-000000000035" // 5 tickets
	login    = "00000000-0000-4000-8000-000000000041" // the recorded ticket, #1 of Website
)

type ticketRead struct {
	project, id string
	o           api.TicketOptions
}

func recordedTickets(t *testing.T, file *contract.File) ([]api.Project, api.TicketRow, api.Ticket) {
	t.Helper()
	var projects struct {
		Data []api.Project `json:"data"`
	}
	if err := json.Unmarshal(file.Contract.REST["GET /api/v1/projects"]["ok_one_organisation"].Response.Body, &projects); err != nil {
		t.Fatal(err)
	}
	text := func(tool, name string) []byte {
		var body struct {
			Result struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"result"`
		}
		_ = json.Unmarshal(file.Contract.MCP.Calls[tool][name].Response.Body, &body)
		return []byte(body.Result.Content[0].Text)
	}
	page, ans := api.DecodeTickets(transport.Answer{Class: transport.Object, Payload: text("list_tickets", "ok"), Status: 200, Sent: true})
	if !ans.OK() || len(page.Data) == 0 {
		t.Fatalf("recorded tickets: %v", ans.Message)
	}
	record, ans := api.DecodeTicket(transport.Answer{Class: transport.Object, Payload: text("get_ticket", "ok"), Status: 200, Sent: true}, api.TicketOptions{})
	if !ans.OK() {
		t.Fatalf("recorded ticket: %v", ans.Message)
	}
	return projects.Data, page.Data[0], record
}

func (f *fakes) hold(ctx context.Context, held bool) bool {
	f.mu.Lock()
	gate, ignoreCancel := f.gate, f.ignoreCancel
	f.mu.Unlock()
	if gate == nil || !held {
		return true
	}
	if ignoreCancel {
		<-gate
		return true
	}
	select {
	case <-gate:
		return true
	case <-ctx.Done():
		return false
	}
}

func (f *fakes) ProjectsAnswer(ctx context.Context, org string) transport.Answer {
	f.mu.Lock()
	f.projectCalls = append(f.projectCalls, org)
	answer, held := f.projects, f.gateProjects
	f.mu.Unlock()
	if !f.hold(ctx, held) {
		return transport.Answer{Class: transport.Canceled, Message: "canceled"}
	}
	return answer(org)
}

func (f *fakes) TicketsAnswer(ctx context.Context, q api.TicketQuery) transport.Answer {
	f.mu.Lock()
	f.ticketQueries = append(f.ticketQueries, q)
	answer, held := f.tickets, f.gateTickets
	f.mu.Unlock()
	if !f.hold(ctx, held) {
		return transport.Answer{Class: transport.Canceled, Message: "canceled"}
	}
	return answer(q)
}

func (f *fakes) TicketAnswer(ctx context.Context, project, id string, o api.TicketOptions) transport.Answer {
	f.mu.Lock()
	f.ticketReads = append(f.ticketReads, ticketRead{project: project, id: id, o: o})
	answer, held := f.ticket, f.gateTicket != "" && f.gateTicket == id
	f.mu.Unlock()
	if !f.hold(ctx, held) {
		return transport.Answer{Class: transport.Canceled, Message: "canceled"}
	}
	return answer(project, id, o)
}

func (f *fakes) projectReads() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.projectCalls)
}

func (f *fakes) ticketListReads() []api.TicketQuery {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.ticketQueries)
}

func (f *fakes) detailReads() []ticketRead {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.ticketReads)
}

func (f *fakes) setTickets(fn func(q api.TicketQuery) transport.Answer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tickets = fn
}

func (f *fakes) setTicket(fn func(project, id string, o api.TicketOptions) transport.Answer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ticket = fn
}

// setGate holds the reads of one ticket on gate from now on.
func (f *fakes) setGate(gate chan struct{}, ticket string, ignoreCancel bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gate, f.gateTicket, f.ignoreCancel = gate, ticket, ignoreCancel
}

func (f *fakes) setProjects(fn func(org string) transport.Answer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.projects = fn
}

func objectAnswer(v any) transport.Answer {
	payload, _ := json.Marshal(v)
	return transport.Answer{Class: transport.Object, Payload: payload, Status: 200, Sent: true}
}

// defaultProjects: A has the recorded four, B none, anything else is not
// reachable.
func (d recordedData) defaultProjects(org string) transport.Answer {
	switch org {
	case orgA:
		return objectAnswer(map[string]any{"data": d.projects})
	case orgB:
		return objectAnswer(map[string]any{"data": []api.Project{}})
	}
	return transport.Answer{Class: transport.NotFound, Message: "not found", Status: 404, Sent: true}
}

// ticketID is the id of ticket number n of a project: the recorded ticket
// keeps its id.
func ticketID(project string, n int) string {
	if project == website && n == 1 {
		return login
	}
	return idOf(500000 + n)
}

// ticketRows makes the rows of a project from the recorded row: #1 and #2
// keep the recorded titles, the rest are "Ticket n"; every third is
// unassigned, every fourth has no status.
func (d recordedData) ticketRows(project string, n int) []api.TicketRow {
	titles := map[int]string{1: "Login bricht ab", 2: "Export als CSV"}
	var out []api.TicketRow
	for i := 1; i <= n; i++ {
		row := d.ticketTmpl
		row.ID, row.Number = ticketID(project, i), i
		row.Title = titles[i]
		if row.Title == "" {
			row.Title = fmt.Sprintf("Ticket %d", i)
		}
		row.Assignees = slices.Clone(d.ticketTmpl.Assignees)
		if i%3 == 0 {
			row.Assignees = []api.Ref{}
		}
		status := "new"
		if i == 1 {
			status = "in_progress" // as recorded
		}
		row.Status = &status
		if i%4 == 0 {
			row.Status = nil
		}
		out = append(out, row)
	}
	return out
}

func projectRows(project string) int {
	return map[string]int{website: 30, intern: 0, handbuch: 3, archiv: 5}[project]
}

// ticketPageAnswer is one page of tickets as the server sends it.
func ticketPageAnswer(page, perPage, total int, rows []api.TicketRow) transport.Answer {
	var p api.TicketPage
	p.Data = rows
	if p.Data == nil {
		p.Data = []api.TicketRow{}
	}
	p.Meta.Page, p.Meta.PerPage, p.Meta.Total = page, perPage, total
	p.Meta.AvailableStatuses = []string{"new", "in_progress", "resolved"}
	return objectAnswer(p)
}

// defaultTickets: the project's rows, those whose title holds the search
// when there is one, paged.
func (d recordedData) defaultTickets(q api.TicketQuery) transport.Answer {
	n, ok := map[string]int{website: 30, intern: 0, handbuch: 3, archiv: 5}[q.ProjectID]
	if !ok {
		return transport.Answer{Class: transport.NotFound, Message: "Project not found or not accessible.", Status: 200, Sent: true}
	}
	rows := d.ticketRows(q.ProjectID, n)
	if q.Q != "" {
		rows = slices.DeleteFunc(rows, func(r api.TicketRow) bool { return !strings.Contains(strings.ToLower(r.Title), strings.ToLower(q.Q)) })
	}
	per := q.PerPage
	if per == 0 {
		per = 25
	}
	page := max(1, q.Page)
	from := (page - 1) * per
	var data []api.TicketRow
	if from < len(rows) {
		data = rows[from:min(len(rows), from+per)]
	}
	return ticketPageAnswer(page, per, len(rows), data)
}

// ticketRecord is ticket n of a project as the server would send it: the
// recorded ticket with the row's id, number and title. #2 has nothing (no
// description, assignees, creator, status, comments); #3 has 30 comments
// (two pages); #4 has 12 attachments (two pages); the rest the recorded
// two comments and two attachments.
func (d recordedData) ticketRecord(project string, n int, o api.TicketOptions) api.Ticket {
	rows := d.ticketRows(project, max(n, 1))
	row := rows[len(rows)-1]
	t := d.recordedTicket
	t.ID, t.Number, t.Title, t.Status, t.Assignees = row.ID, row.Number, row.Title, row.Status, row.Assignees
	t.Project = api.Ref{ID: project, Name: map[string]string{website: "Website-Relaunch", intern: "Intern", handbuch: "Handbuch", archiv: "Archiv"}[project]}
	t.Posts = slices.Clone(d.recordedTicket.Posts)
	t.Attachments = []api.Attachment{{Filename: "design.pdf"}, {Filename: "screenshot.png"}}
	switch n {
	case 2:
		t.DescriptionText, t.Assignees, t.Creator, t.Status, t.Posts, t.Attachments = "", nil, nil, nil, nil, nil
	case 3:
		t.Posts = nil
		for i := range 30 { // oldest first, as the server lists them
			p := d.recordedTicket.Posts[0]
			p.ID = idOf(600000 + i)
			p.BodyText = fmt.Sprintf("Comment %d", i+1)
			t.Posts = append(t.Posts, p)
		}
	case 4:
		t.Attachments = nil
		for i := range 12 {
			t.Attachments = append(t.Attachments, api.Attachment{Filename: fmt.Sprintf("file-%02d.txt", i+1)})
		}
	case 6:
		t.DescriptionText = sixDescription
		t.Posts = d.sixPosts(false)
		t.Attachments = sixAttachments(d.sixFiles)
	}
	if o.FullText && n == 6 {
		t.DescriptionText = sixDescriptionWhole
	}
	total, attachments := len(t.Posts), len(t.Attachments)
	t.PostsTotal, t.AttachmentsTotal = total, attachments
	if o.PostsPage > 0 || o.PostsPerPage > 0 || o.Newest || o.AttachmentsPage > 0 {
		per := o.PostsPerPage
		if per == 0 {
			per = 50
		}
		per = min(per, 50)
		page := max(1, o.PostsPage)
		if o.Newest {
			slices.Reverse(t.Posts)
		}
		from := (page - 1) * per
		if from < len(t.Posts) {
			t.Posts = t.Posts[from:min(len(t.Posts), from+per)]
		} else {
			t.Posts = nil
		}
		t.PostsPage, t.PostsPerPage, t.PostsPages = page, per, (total+per-1)/per
		t.PostsOrder = "oldest"
		if o.Newest {
			t.PostsOrder = "newest"
		}
		t.PostsTruncated = total > len(t.Posts)
		apage := max(1, o.AttachmentsPage)
		afrom := (apage - 1) * 10
		if afrom < len(t.Attachments) {
			t.Attachments = t.Attachments[afrom:min(len(t.Attachments), afrom+10)]
		} else {
			t.Attachments = nil
		}
		t.AttachmentsPage, t.AttachmentsPages = apage, (attachments+9)/10
		t.AttachmentsTruncated = attachments > len(t.Attachments)
	} else {
		if len(t.Posts) > 50 {
			t.Posts = t.Posts[:50]
		}
		t.PostsTruncated = total > len(t.Posts)
		if len(t.Attachments) > 10 {
			t.Attachments = t.Attachments[:10]
		}
		t.AttachmentsTruncated = attachments > len(t.Attachments)
		t.PostsPage, t.PostsPerPage, t.PostsPages, t.PostsOrder, t.AttachmentsPage, t.AttachmentsPages = 0, 0, 0, "", 0, 0
	}
	if t.Posts == nil {
		t.Posts = []api.Post{}
	}
	if t.Attachments == nil {
		t.Attachments = []api.Attachment{}
	}
	if t.Assignees == nil {
		t.Assignees = []api.Ref{}
	}
	return t
}

// ticketAnswer marshals a record as the server would, the page fields
// only when the record carries a page.
func ticketAnswer(t api.Ticket) transport.Answer {
	var m map[string]any
	payload, _ := json.Marshal(t)
	_ = json.Unmarshal(payload, &m)
	if t.PostsPage == 0 {
		drop := []string{"posts_page", "posts_per_page", "posts_pages", "posts_order"}
		if t.PostID == "" {
			// One comment carries the page of attachments (MCP 0.9.0).
			drop = append(drop, "attachments_page", "attachments_pages")
		}
		for _, k := range drop {
			delete(m, k)
		}
	}
	if t.PostID == "" {
		delete(m, "post_id")
	}
	return objectAnswer(m)
}

func (d recordedData) defaultTicket(project, id string, o api.TicketOptions) transport.Answer {
	for n := 1; n <= projectRows(project); n++ {
		if ticketID(project, n) != id {
			continue
		}
		if o.PostID != "" {
			return d.onePost(project, n, o.PostID)
		}
		return ticketAnswer(d.ticketRecord(project, n, o))
	}
	return transport.Answer{Class: transport.NotFound, Message: "No ticket found for " + id + ".", Status: 200, Sent: true}
}

// chooseTickets highlights the tickets entry of an organisation and opens
// it: the projects list.
func (h *harness) chooseTickets(org string) tea.Cmd {
	h.t.Helper()
	h.m.focus = view.Navigation
	h.m.app.highlight, h.m.app.highlightSection = org, sectionTickets
	return h.press("enter")
}

// openProject selects a project of the list and opens its tickets.
func (h *harness) openProject(id string) tea.Cmd {
	h.t.Helper()
	h.m.focus = view.List
	h.m.app.projects.selected = id
	return h.press("enter")
}

// tickets runs the start and opens the tickets of a project of A.
func (h *harness) openTickets(project string) {
	h.t.Helper()
	h.start()
	h.drive(h.chooseTickets(orgA))
	h.drive(h.openProject(project))
}

// typeText feeds every character as a key press.
func (h *harness) typeText(text string) {
	h.t.Helper()
	for _, r := range text {
		h.press(string(r))
	}
}

// chooserRow is the row of one entry of the chooser (an organisation's
// section), wherever the headings put it; headingRow the organisation's
// heading row. The C1b tests read counts and names through them.
func chooserRow(m Model, org string, s section) present.Row {
	rows := m.chooserColumn().Rows
	at, heading, first := -1, "", true
	for _, e := range m.app.entries() {
		if first || e.org != heading {
			at++
			heading, first = e.org, false
		}
		at++
		if e.org == org && e.section == s && at < len(rows) {
			return rows[at]
		}
	}
	return present.Row{}
}

func headingRow(m Model, org string) present.Row {
	rows := m.chooserColumn().Rows
	at, heading, first := -1, "", true
	for _, e := range m.app.entries() {
		if first || e.org != heading {
			at++
			heading, first = e.org, false
			if e.org == org && at < len(rows) {
				return rows[at]
			}
		}
		at++
	}
	return present.Row{}
}

// countShown is the count at the inbox entry of an organisation ("" for
// all).
func countShown(m Model, org string) string {
	return chooserRow(m, org, sectionInbox).Trailing.String()
}

// Ticket #6 of a project (C1d): a description and two comments the
// server shortened, 30 comments, 12 attachments with metadata of every
// kind (known, missing, zero bytes).
const (
	sixDescription      = "Description of six, the first part" + api.TruncationMarker
	sixDescriptionWhole = "Description of six, the first part and the whole rest."
)

// sixPostID is the id of comment i (1 the oldest) of ticket #6.
func sixPostID(i int) string { return idOf(700000 + i) }

// sixPosts are the comments of #6, oldest first; the two newest are
// shortened, whole with post_id.
func (d recordedData) sixPosts(whole bool) []api.Post {
	n := d.sixComments
	if n == 0 {
		n = 30
	}
	var out []api.Post
	for i := 1; i <= n; i++ {
		p := d.recordedTicket.Posts[0]
		p.ID, p.CreatedAt = sixPostID(i), fmt.Sprintf("2026-01-06T%02d:%02d:00Z", 10+i/60, i%60)
		p.BodyText = fmt.Sprintf("Six comment %d", i)
		if i >= 29 {
			p.BodyText = fmt.Sprintf("Six comment %d begins", i) + api.TruncationMarker
			if whole {
				p.BodyText = fmt.Sprintf("Six comment %d begins and ends here.", i)
			}
		}
		out = append(out, p)
	}
	return out
}

func sixAttachments(n int) []api.Attachment {
	if n == 0 {
		n = 12
	}
	var out []api.Attachment
	for i := 1; i <= n; i++ {
		a := api.Attachment{Filename: fmt.Sprintf("six-%02d.txt", i)}
		if i%2 == 1 {
			kind, status, size := "text/plain", "available", int64(i*1000)
			a.ContentType, a.TextStatus, a.ByteSize = &kind, &status, &size
		}
		if i == 12 {
			zero := int64(0)
			a.ByteSize = &zero
		}
		out = append(out, a)
	}
	return out
}

// onePost is the answer to post_id: the one comment whole, the count of
// readable comments beside it, the first page of attachments; a comment
// the ticket does not have is "not found", as the server says it.
func (d recordedData) onePost(project string, n int, postID string) transport.Answer {
	t := d.ticketRecord(project, n, api.TicketOptions{})
	posts := t.Posts
	if n == 6 {
		posts = d.sixPosts(true)
	}
	for _, p := range posts {
		if p.ID != postID {
			continue
		}
		t.Posts, t.PostsTotal, t.PostsTruncated, t.PostID = []api.Post{p}, len(posts), len(posts) > 1, postID
		t.AttachmentsPage, t.AttachmentsPages = 1, (t.AttachmentsTotal+9)/10
		return ticketAnswer(t)
	}
	return transport.Answer{Class: transport.NotFound, Message: "No comment found for " + postID + ".", Status: 200, Sent: true}
}
