package inboxfixture

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
)

// The projects and tickets of the fixture (C1c): A has the four recorded
// projects, B none. The tickets of a project are made from the recorded
// ticket row, the ticket from the recorded ticket with its comments; the
// shapes are the server's, the numbers are fixed:
//
//	Website-Relaunch   30 tickets, two pages; #1 is the recorded ticket
//	Intern              none
//	Handbuch            3
//	Archiv              5
//
// Ticket #2 has nothing (no description, assignees, creator, status,
// comments or attachments), #3 has 30 comments (two pages), #4 has 12
// attachments (two pages); the rest carry the recorded two comments and
// two attachments. The scenarios act on Website's page 2 as they do on
// A's inbox page 2, and (401) on a read of ticket #2.

const (
	ProjectWebsite  = "00000000-0000-4000-8000-000000000031"
	ProjectIntern   = "00000000-0000-4000-8000-000000000032"
	ProjectHandbuch = "00000000-0000-4000-8000-000000000033"
	ProjectArchiv   = "00000000-0000-4000-8000-000000000035"
	// TicketLogin is the recorded ticket, #1 of Website.
	TicketLogin = "00000000-0000-4000-8000-000000000041"
)

// ticketCounts is the number of tickets per project.
var ticketCounts = map[string]int{ProjectWebsite: 30, ProjectIntern: 0, ProjectHandbuch: 3, ProjectArchiv: 5}

// TicketID is the id of ticket number n of a project.
func TicketID(project string, n int) string {
	if project == ProjectWebsite && n == 1 {
		return TicketLogin
	}
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", 500000+n)
}

// recordedTickets reads the recorded projects, the first recorded ticket
// row and the recorded ticket.
func recordedTickets(file contractFile) (projects []map[string]any, row, ticket map[string]any, err error) {
	var list struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(file.Contract.REST["GET /api/v1/projects"]["ok_one_organisation"].Response.Body, &list); err != nil || len(list.Data) == 0 {
		return nil, nil, nil, errors.New("inbox fixture: the contract copy records no projects")
	}
	text := func(tool, name string) ([]byte, error) {
		ex, ok := file.Contract.MCP.Calls[tool][name]
		if !ok {
			return nil, fmt.Errorf("inbox fixture: the contract copy records no %s", tool)
		}
		var body struct {
			Result struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"result"`
		}
		if err := json.Unmarshal(ex.Response.Body, &body); err != nil || len(body.Result.Content) == 0 {
			return nil, fmt.Errorf("inbox fixture: the recorded %s has no text", tool)
		}
		return []byte(body.Result.Content[0].Text), nil
	}
	rows, err := text("list_tickets", "ok")
	if err != nil {
		return nil, nil, nil, err
	}
	var page struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rows, &page); err != nil || len(page.Data) == 0 {
		return nil, nil, nil, errors.New("inbox fixture: the recorded list_tickets has no row")
	}
	record, err := text("get_ticket", "ok")
	if err != nil {
		return nil, nil, nil, err
	}
	if err := json.Unmarshal(record, &ticket); err != nil || ticket["id"] != TicketLogin {
		return nil, nil, nil, errors.New("inbox fixture: the recorded get_ticket is not the login ticket")
	}
	return list.Data, page.Data[0], ticket, nil
}

func copyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// ticketRows are the rows of a project: #1 and #2 keep the recorded
// titles, the rest are "Ticket n"; every third is unassigned, every fourth
// has no status.
func (f *Fixture) ticketRows(project string) []map[string]any {
	titles := map[int]string{1: "Login bricht ab", 2: "Export als CSV"}
	var out []map[string]any
	for i := 1; i <= ticketCounts[project]; i++ {
		row := copyMap(f.ticketRow)
		row["id"], row["number"] = TicketID(project, i), i
		row["title"] = titles[i]
		if row["title"] == "" {
			row["title"] = fmt.Sprintf("Ticket %d", i)
		}
		if i%3 == 0 {
			row["assignees"] = []any{}
		}
		row["status"] = "new"
		if i == 1 {
			row["status"] = "in_progress"
		}
		if i%4 == 0 {
			row["status"] = nil
		}
		out = append(out, row)
	}
	return out
}

func (f *Fixture) projects(w http.ResponseWriter, r *http.Request, req Request) {
	org := r.URL.Query().Get("organisation_id")
	var data []map[string]any
	switch org {
	case OrgA:
		data = f.projectList
	case OrgB:
		data = []map[string]any{}
	default:
		req.Status = http.StatusNotFound
		f.record(req)
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not_found"}`))
		return
	}
	req.Status = http.StatusOK
	f.record(req)
	out, _ := json.Marshal(map[string]any{"data": data})
	_, _ = w.Write(out)
}

// scenarioOn applies the scenario to one place (A's inbox page 2, Website's
// ticket page 2, ticket #2), attempt by attempt, and says whether it
// answered.
func (f *Fixture) scenarioOn(w http.ResponseWriter, place string, req Request) bool {
	f.mu.Lock()
	f.attempts[place]++
	attempt := f.attempts[place]
	f.mu.Unlock()
	switch {
	case f.scenario == Temporary && attempt <= 2:
		req.Status = http.StatusServiceUnavailable
		f.record(req)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"unavailable"}`))
	case f.scenario == RateLimited && attempt == 1:
		req.Status = http.StatusTooManyRequests
		f.record(req)
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate_limited"}`))
	case f.scenario == Unauthorized:
		req.Status = http.StatusUnauthorized
		f.record(req)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	default:
		return false
	}
	return true
}

func (f *Fixture) tickets(w http.ResponseWriter, args map[string]any, reply func(int, any, any), req Request) {
	project, _ := args["project_id"].(string)
	n, ok := ticketCounts[project]
	if !ok {
		reply(http.StatusOK, toolError("not_found", "Project not found or not accessible."), nil)
		return
	}
	for _, name := range []string{"status", "assignee", "tag", "project"} {
		if _, ok := args[name]; ok {
			reply(http.StatusOK, toolError("refused", "the inbox fixture does not filter by "+name), nil)
			return
		}
	}
	page, perPage := intArg(args, "page", 1), intArg(args, "per_page", 25)
	if page < 1 || perPage < 1 || perPage > 100 {
		reply(http.StatusOK, toolError("invalid_input", "page or per_page out of range"), nil)
		return
	}
	rows := f.ticketRows(project)
	if q, _ := args["q"].(string); q != "" {
		rows = slices.DeleteFunc(rows, func(row map[string]any) bool {
			title, _ := row["title"].(string)
			return !strings.Contains(strings.ToLower(title), strings.ToLower(q))
		})
	}
	if project == ProjectWebsite && page == 2 && f.scenarioOn(w, "W2", req) {
		return
	}
	_ = n
	from := (page - 1) * perPage
	data := []map[string]any{}
	if from < len(rows) {
		data = rows[from:min(len(rows), from+perPage)]
	}
	reply(http.StatusOK, toolObject(map[string]any{
		"data": data,
		"meta": map[string]any{"page": page, "per_page": perPage, "total": len(rows), "available_statuses": []string{"new", "in_progress", "resolved", "closed", "backlog"}},
	}), nil)
}

func (f *Fixture) ticket(w http.ResponseWriter, args map[string]any, reply func(int, any, any), req Request) {
	project, _ := args["project_id"].(string)
	id, _ := args["ticket_id"].(string)
	if _, ok := args["number"]; ok || id == "" {
		reply(http.StatusOK, toolError("invalid_input", "the inbox fixture reads tickets by ticket_id"), nil)
		return
	}
	for _, name := range []string{"full_text", "post_id"} {
		if _, ok := args[name]; ok {
			reply(http.StatusOK, toolError("refused", "the inbox fixture has no "+name), nil)
			return
		}
	}
	number := 0
	for n := 1; n <= ticketCounts[project]; n++ {
		if TicketID(project, n) == id {
			number = n
		}
	}
	if number == 0 {
		reply(http.StatusOK, toolEmpty("No ticket found for "+id+"."), nil)
		return
	}
	if number == 2 && f.scenario == Unauthorized && f.scenarioOn(w, "T2", req) {
		return
	}
	order, _ := args["posts_order"].(string)
	if order != "" && order != "oldest" && order != "newest" {
		reply(http.StatusOK, toolError("invalid_input", `Invalid arguments: value at "/posts_order" is not one of: ["oldest", "newest"]`), nil)
		return
	}
	postsPage, perPage, attachmentsPage := intArg(args, "posts_page", 1), intArg(args, "posts_per_page", 50), intArg(args, "attachments_page", 1)
	if postsPage < 1 || perPage < 1 || attachmentsPage < 1 {
		reply(http.StatusOK, toolError("invalid_input", "a page below 1"), nil)
		return
	}
	perPage = min(perPage, 50)
	reply(http.StatusOK, toolObject(f.ticketRecord(project, number, postsPage, perPage, order == "newest", attachmentsPage)), nil)
}

// toolEmpty is the answer of a tool that found nothing, as the server
// sends it: not an error, kind empty.
func toolEmpty(text string) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": false,
		"_meta":   map[string]any{"lalabase/kind": "empty"},
	}
}

// ticketRecord is ticket n of a project with the page asked for, in the
// server's shape (the page fields always, as a 0.9.0 server sends them).
func (f *Fixture) ticketRecord(project string, n, postsPage, perPage int, newest bool, attachmentsPage int) map[string]any {
	rows := f.ticketRows(project)
	row := rows[n-1]
	t := copyMap(f.ticketTmpl)
	for _, k := range []string{"id", "number", "title", "status", "assignees"} {
		t[k] = row[k]
	}
	t["project"] = map[string]any{"id": project, "name": f.projectName(project)}
	recordedPosts, _ := f.ticketTmpl["posts"].([]any)
	posts := slices.Clone(recordedPosts)
	attachments := []any{map[string]any{"filename": "design.pdf", "content_type": "application/pdf", "byte_size": 1024}, map[string]any{"filename": "screenshot.png", "content_type": "image/png", "byte_size": 2048}}
	switch n {
	case 2:
		t["description_html"], t["description_text"], t["assignees"], t["creator"], t["status"] = "", "", []any{}, nil, nil
		posts, attachments = []any{}, []any{}
	case 3:
		posts = nil
		for i := range 30 {
			p := copyMap(recordedPosts[0].(map[string]any))
			p["id"] = fmt.Sprintf("00000000-0000-4000-8000-%012d", 600000+i)
			p["body_html"], p["body_text"] = fmt.Sprintf("Comment %d", i+1), fmt.Sprintf("Comment %d", i+1)
			p["created_at"] = fmt.Sprintf("2026-01-05T10:%02d:00Z", i)
			posts = append(posts, p)
		}
	case 4:
		attachments = nil
		for i := range 12 {
			attachments = append(attachments, map[string]any{"filename": fmt.Sprintf("file-%02d.txt", i+1), "content_type": "text/plain", "byte_size": 100})
		}
	}
	total, attachmentsTotal := len(posts), len(attachments)
	if newest {
		slices.Reverse(posts)
	}
	from := (postsPage - 1) * perPage
	page := []any{}
	if from < len(posts) {
		page = posts[from:min(len(posts), from+perPage)]
	}
	afrom := (attachmentsPage - 1) * 10
	apage := []any{}
	if afrom < len(attachments) {
		apage = attachments[afrom:min(len(attachments), afrom+10)]
	}
	order := "oldest"
	if newest {
		order = "newest"
	}
	t["posts"], t["posts_total"], t["posts_truncated"] = page, total, total > len(page)
	t["posts_page"], t["posts_per_page"], t["posts_pages"], t["posts_order"] = postsPage, perPage, (total+perPage-1)/perPage, order
	t["attachments"], t["attachments_total"], t["attachments_truncated"] = apage, attachmentsTotal, attachmentsTotal > len(apage)
	t["attachments_page"], t["attachments_pages"] = attachmentsPage, (attachmentsTotal+9)/10
	return t
}

func (f *Fixture) projectName(id string) string {
	for _, p := range f.projectList {
		if p["id"] == id {
			name, _ := p["name"].(string)
			return name
		}
	}
	return ""
}
