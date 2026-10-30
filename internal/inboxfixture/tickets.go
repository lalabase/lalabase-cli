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
// attachments (two pages); #6 (C1d) has a description and two comments the
// server shortened, 30 comments and 12 attachments with metadata of every
// kind; #8 (K1) a description and a newest comment longer than the tools'
// cut and the client's pane; the rest carry the recorded two comments and
// two attachments. The tools cut a long text as the core's get_ticket does;
// REST (rest.go) sends every text whole. The scenarios act on Website's
// page 2 as they do on A's inbox page 2, (401) on a read of ticket #2,
// (temporary) on page 2 of #6's comments and (vanished) on the newest
// comments of #6 and #8 read whole.

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

// projectOrder numbers the projects, for their tickets' ids.
var projectOrder = []string{ProjectWebsite, ProjectIntern, ProjectHandbuch, ProjectArchiv}

// TicketID is the id of ticket number n of a project. Ids are unique across
// projects, as the server's are: REST reads a ticket by its id alone.
func TicketID(project string, n int) string {
	if project == ProjectWebsite && n == 1 {
		return TicketLogin
	}
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", 500000+1000*slices.Index(projectOrder, project)+n)
}

// ticketByID is the project and number of a ticket by its id; 0 for none.
func ticketByID(id string) (string, int) {
	for _, project := range projectOrder {
		for n := 1; n <= ticketCounts[project]; n++ {
			if TicketID(project, n) == id {
				return project, n
			}
		}
	}
	return "", 0
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

// statusKeys is the catalogue of every project of the fixture, in the
// server's order.
var statusKeys = []string{"new", "in_progress", "resolved", "closed", "backlog"}

// statusMeanings are the status functions of each key, as the core sends
// them with a ticket (status_functions).
var statusMeanings = map[string][]any{
	"new": {"is_new"}, "in_progress": {"is_in_progress"}, "resolved": {"is_completed"}, "closed": {"is_closed"}, "backlog": {"is_backlog"},
}

// ticketRows are the rows of a project: #1 and #2 keep the recorded
// titles, the rest are "Ticket n"; every third is unassigned, every fourth
// has no status; a status means what its name says (status_functions);
// what the writes changed (C2a) lies over that.
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
		row["status"], row["status_functions"] = "new", statusMeanings["new"]
		if i == 1 {
			row["status"], row["status_functions"] = "in_progress", statusMeanings["in_progress"]
		}
		if i%4 == 0 {
			row["status"], row["status_functions"] = nil, []any{}
		}
		if e := f.editOf(row["id"].(string)); e != nil {
			for k, v := range e.row {
				row[k] = v
			}
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
	if _, ok := ticketCounts[project]; !ok {
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
	if project == ProjectWebsite && page == 2 && f.scenarioOn(w, "W2", req) {
		return
	}
	q, _ := args["q"].(string)
	reply(http.StatusOK, toolObject(f.ticketPage(project, q, page, perPage)), nil)
}

// ticketPage is a page of a project's tickets, those whose title holds the
// search when there is one, in the tool's form (REST's too).
func (f *Fixture) ticketPage(project, q string, page, perPage int) map[string]any {
	rows := f.ticketRows(project)
	if q != "" {
		rows = slices.DeleteFunc(rows, func(row map[string]any) bool {
			title, _ := row["title"].(string)
			return !strings.Contains(strings.ToLower(title), strings.ToLower(q))
		})
	}
	from := (page - 1) * perPage
	data := []map[string]any{}
	if from < len(rows) {
		data = rows[from:min(len(rows), from+perPage)]
	}
	return map[string]any{
		"data": data,
		"meta": map[string]any{"page": page, "per_page": perPage, "total": len(rows), "available_statuses": statusKeys},
	}
}

func (f *Fixture) ticket(w http.ResponseWriter, args map[string]any, reply func(int, any, any), req Request) {
	project, _ := args["project_id"].(string)
	id, _ := args["ticket_id"].(string)
	if _, ok := args["number"]; ok || id == "" {
		reply(http.StatusOK, toolError("invalid_input", "the inbox fixture reads tickets by ticket_id"), nil)
		return
	}
	if f.scenario == Older {
		// A server before S1 does not know the arguments of C1d.
		for _, name := range []string{"full_text", "post_id"} {
			if _, ok := args[name]; ok {
				reply(http.StatusOK, toolError("invalid_input", "unknown argument "+name), nil)
				return
			}
		}
	}
	fullText, _ := args["full_text"].(bool)
	postID, _ := args["post_id"].(string)
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
	if number == 6 && postsPage == 2 && postID == "" && f.scenario == Temporary && f.scenarioOn(w, "T6P2", req) {
		return
	}
	t := f.ticketRecord(project, number, postsPage, perPage, order == "newest", attachmentsPage, false)
	if fullText {
		if whole := wholeDescription(number); whole != "" {
			t["description_html"], t["description_text"] = whole, whole
		}
	}
	if postID != "" {
		one, ok := f.onePost(project, number, postID, t)
		if !ok {
			reply(http.StatusOK, toolEmpty("No comment found for "+postID+"."), nil)
			return
		}
		t = one
	}
	reply(http.StatusOK, toolObject(t), nil)
}

// The texts of ticket #6: the server's marker after a shortened text, and
// the whole text a read with full_text or post_id brings.
const (
	truncationMarker    = "\n\n\u2026[truncated]"
	SixDescription      = "Description of six, the first part" + truncationMarker
	SixDescriptionWhole = "Description of six, the first part and the whole rest."
)

// The long texts of ticket #8 (K1): longer than the tools' cut on the page
// (toolsCut, the core's get_ticket) and the client's pane. Their ends say
// whether a text arrived whole.
const (
	toolsCut   = 8000
	EightEnd   = "The end of eight's description."
	EightReply = "The end of eight's newest comment."
)

// EightPostID is the id of the newest comment of ticket #8, the long one.
const EightPostID = "00000000-0000-4000-8000-000000900002"

// eightText is a text of n characters of words with end at its end.
func eightText(start, end string, n int) string {
	words := strings.Repeat("Many words to read on the terminal, line after line. ", n/53+1)
	return start + words[:n-len(start)-len(end)] + end
}

var (
	eightDescription = eightText("Description of eight: ", EightEnd, 9000)
	eightComment     = eightText("Comment of eight: ", EightReply, 9000)
)

// toolsText is a text as the tools send it on a page: cut after toolsCut
// characters, with the marker.
func toolsText(text string) string {
	runes := []rune(text)
	if len(runes) <= toolsCut {
		return text
	}
	return string(runes[:toolsCut]) + truncationMarker
}

// wholeDescription is the description of ticket n whole where the tools
// cut it (#6, #8), "" for the rest.
func wholeDescription(n int) string {
	switch n {
	case 6:
		return SixDescriptionWhole
	case 8:
		return eightDescription
	}
	return ""
}

// SixPostID is the id of comment i (1 the oldest) of ticket #6.
func SixPostID(i int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", 700000+i) }

// sixPosts are the comments of #6, oldest first: the two newest are
// shortened, whole on post_id.
func (f *Fixture) sixPosts(whole bool) []any {
	recorded, _ := f.ticketTmpl["posts"].([]any)
	var out []any
	for i := 1; i <= 30; i++ {
		p := copyMap(recorded[0].(map[string]any))
		p["id"], p["created_at"] = SixPostID(i), fmt.Sprintf("2026-01-06T10:%02d:00Z", i)
		body := fmt.Sprintf("Six comment %d", i)
		if i >= 29 {
			body = fmt.Sprintf("Six comment %d begins", i) + truncationMarker
			if whole {
				body = fmt.Sprintf("Six comment %d begins and ends here.", i)
			}
		}
		p["body_html"], p["body_text"] = body, body
		out = append(out, p)
	}
	return out
}

// sixAttachments have metadata of every kind: odd ones all of it, even
// ones none (an older server), the last one zero bytes.
func sixAttachments() []any {
	var out []any
	for i := 1; i <= 12; i++ {
		a := map[string]any{"id": fmt.Sprintf("00000000-0000-4000-8000-%012d", 800000+i), "filename": fmt.Sprintf("six-%02d.txt", i)}
		if i%2 == 1 {
			a["content_type"], a["byte_size"], a["text_status"], a["text"] = "text/plain", i*1000, "available", "text of the file"
		}
		if i == 12 {
			a["byte_size"] = 0
		}
		out = append(out, a)
	}
	return out
}

// posts are the comments of ticket n, oldest first, with the comments the
// writes added (C2a): whole, or as the tools send them on a page (#6's
// and #8's long ones cut).
func (f *Fixture) posts(project string, n int, whole bool) []any {
	recorded, _ := f.ticketTmpl["posts"].([]any)
	posts := slices.Clone(recorded)
	switch n {
	case 2:
		posts = []any{}
	case 3:
		posts = nil
		for i := range 30 {
			p := copyMap(recorded[0].(map[string]any))
			p["id"] = fmt.Sprintf("00000000-0000-4000-8000-%012d", 600000+i)
			p["body_html"], p["body_text"] = fmt.Sprintf("Comment %d", i+1), fmt.Sprintf("Comment %d", i+1)
			p["created_at"] = fmt.Sprintf("2026-01-05T10:%02d:00Z", i)
			posts = append(posts, p)
		}
	case 6:
		posts = f.sixPosts(whole)
	case 8:
		long := copyMap(recorded[len(recorded)-1].(map[string]any))
		body := eightComment
		if !whole {
			body = toolsText(body)
		}
		long["id"], long["body_html"], long["body_text"] = EightPostID, body, body
		posts[len(posts)-1] = long
	}
	if e := f.editOf(TicketID(project, n)); e != nil {
		posts = append(posts, e.posts...)
	}
	return posts
}

// vanished: in the scenario of that name the newest comments of #6 and #8
// are hidden when read whole.
func (f *Fixture) vanished(postID string) bool {
	return f.scenario == Vanished && (postID == SixPostID(30) || postID == EightPostID)
}

// onePost is the answer to post_id, in the server's shape: the comment
// whole, the count of readable comments beside it, the page of
// attachments, no page of comments. false: the ticket has no such comment,
// or (vanished) its newest one is hidden.
func (f *Fixture) onePost(project string, n int, postID string, page map[string]any) (map[string]any, bool) {
	if f.vanished(postID) {
		return nil, false
	}
	posts := f.posts(project, n, true)
	for _, p := range posts {
		if p.(map[string]any)["id"] != postID {
			continue
		}
		t := copyMap(page)
		for _, k := range []string{"posts_page", "posts_per_page", "posts_pages", "posts_order"} {
			delete(t, k)
		}
		t["posts"], t["posts_total"], t["posts_truncated"], t["post_id"] = []any{p}, len(posts), len(posts) > 1, postID
		return t, true
	}
	return nil, false
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
// server's shape (the page fields always, as a 0.9.0 server sends them):
// its texts whole (REST) or as the tools cut them.
func (f *Fixture) ticketRecord(project string, n, postsPage, perPage int, newest bool, attachmentsPage int, whole bool) map[string]any {
	rows := f.ticketRows(project)
	row := rows[n-1]
	t := copyMap(f.ticketTmpl)
	for _, k := range []string{"id", "number", "title", "status", "status_functions", "assignees"} {
		t[k] = row[k]
	}
	t["project"] = map[string]any{"id": project, "name": f.projectName(project)}
	posts := f.posts(project, n, whole)
	attachments := []any{map[string]any{"filename": "design.pdf", "content_type": "application/pdf", "byte_size": 1024}, map[string]any{"filename": "screenshot.png", "content_type": "image/png", "byte_size": 2048}}
	switch n {
	case 2:
		t["description_html"], t["description_text"], t["assignees"], t["creator"], t["status"], t["status_functions"] = "", "", []any{}, nil, nil, []any{}
		attachments = []any{}
	case 4:
		attachments = nil
		for i := range 12 {
			attachments = append(attachments, map[string]any{"filename": fmt.Sprintf("file-%02d.txt", i+1), "content_type": "text/plain", "byte_size": 100})
		}
	case 6:
		t["description_html"], t["description_text"] = SixDescription, SixDescription
		attachments = sixAttachments()
	case 8:
		cut := toolsText(eightDescription)
		t["description_html"], t["description_text"] = cut, cut
	}
	if text := wholeDescription(n); whole && text != "" {
		t["description_html"], t["description_text"] = text, text
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

// bookedTime answers summarize_time_sessions as the server does for the
// own time on one ticket (S6): 15 minutes for each step of the ticket's
// number, so a person trying the client sees the line and tests can tell
// tickets apart, and what was booked since (K6); no such ticket is empty,
// as on the server.
func (f *Fixture) bookedTime(args map[string]any, reply func(int, any, any)) {
	project, _ := args["project_id"].(string)
	id, _ := args["ticket"].(string)
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
	f.mu.Lock()
	minutes := 15*number + f.booked[id]
	f.mu.Unlock()
	reply(http.StatusOK, toolObject(map[string]any{
		"scope":   "own",
		"filters": map[string]any{"ticket": map[string]any{"id": id, "number": number}},
		"total":   map[string]any{"minutes": minutes, "sessions": 1, "hours": float64(minutes) / 60},
	}), nil)
}
