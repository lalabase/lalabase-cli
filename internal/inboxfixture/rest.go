package inboxfixture

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The ticket view over REST (K1): the routes of the core's R1, built from
// the same tickets as the tools' answers (tickets.go), in the shapes the
// core sends them: the list as the tool's text, a ticket under data with
// its permissions, one comment under data. REST cuts no text.
//
// The fixture's /me says which REST API it has. The contract's /me lists
// R1's capabilities, and the client reads the ticket view here. A fixture
// before R1 (BeforeR1, and the scenarios of servers before R0, whose /me
// lists no capabilities at all) answers as such a server would: a list
// without the status catalogue, a ticket without pages, no route for a
// number or for one comment. The client takes the tools there; a client
// that asked here anyway would find answers it cannot use.

// Option changes a fixture at construction.
type Option func(*Fixture) error

// r1Capabilities are what the core's R1 and the slices after it added to
// /me: without them, /me is that of API 1.2.0.
var r1Capabilities = []string{"tickets.paging", "tickets.by_number", "posts.show", "time_sessions.book", "posts.plain", "knowledge.search", "tickets.changes"}

// BeforeR1 is a server whose REST API is older than the core's R1 (API
// 1.2.0): its /me lists the capabilities of then. The client reads the
// ticket view through the tools.
func BeforeR1() Option {
	return func(f *Fixture) error {
		me, err := editMe(f.me, func(data map[string]any) {
			caps, _ := data["capabilities"].([]any)
			data["capabilities"] = slices.DeleteFunc(slices.Clone(caps), func(c any) bool {
				name, _ := c.(string)
				return slices.Contains(r1Capabilities, name)
			})
			data["api_version"] = "1.2.0"
		})
		f.me, f.r1 = me, false
		return err
	}
}

// withoutCapabilities is /me as a server before R0 answers it: nothing of
// the REST API's version and capabilities.
func withoutCapabilities(me json.RawMessage) (json.RawMessage, error) {
	return editMe(me, func(data map[string]any) {
		delete(data, "api_version")
		delete(data, "capabilities")
	})
}

func editMe(me json.RawMessage, edit func(data map[string]any)) (json.RawMessage, error) {
	var body map[string]any
	if err := json.Unmarshal(me, &body); err != nil {
		return nil, err
	}
	data, _ := body["data"].(map[string]any)
	if data == nil {
		return nil, errors.New("inbox fixture: the recorded /me has no data")
	}
	edit(data)
	return json.Marshal(body)
}

// restError answers an error in the API's own form, with its code (R0).
func restError(w http.ResponseWriter, req Request, f *Fixture, status int, code, message string) {
	req.Status = status
	f.record(req)
	body := map[string]any{"error": code, "code": code}
	if message != "" {
		body["message"] = message
	}
	out, _ := json.Marshal(body)
	w.WriteHeader(status)
	_, _ = w.Write(out)
}

// restObject answers 200 with a body.
func restObject(w http.ResponseWriter, req Request, f *Fixture, body any) {
	req.Status = http.StatusOK
	f.record(req)
	out, _ := json.Marshal(body)
	_, _ = w.Write(out)
}

// restNumbers are the query parameters that are whole numbers: the log
// keeps them as numbers, as a tool's arguments are kept.
var restNumbers = []string{"page", "per_page", "posts_page", "posts_per_page", "attachments_page"}

// restArgs are a REST request's path ids and query, under the names the
// tools give them, for a test's assertions.
func restArgs(r *http.Request, ids map[string]string) map[string]any {
	args := map[string]any{}
	for name, id := range ids {
		args[name] = id
	}
	for name, values := range r.URL.Query() {
		args[name] = values[0]
		if n, err := strconv.Atoi(values[0]); err == nil && slices.Contains(restNumbers, name) {
			args[name] = float64(n)
		}
	}
	return args
}

// rest serves the REST routes of the ticket view; anything else under
// /api/v1/projects/ and /api/v1/tickets/ is not here.
func (f *Fixture) rest(w http.ResponseWriter, r *http.Request, req Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/"), "/")
	switch {
	case len(parts) == 3 && parts[0] == "projects" && parts[2] == "tickets":
		req.Route, req.Args = "tickets.list", restArgs(r, map[string]string{"project_id": parts[1]})
		f.restTickets(w, r, req, parts[1])
	case len(parts) == 2 && parts[0] == "tickets":
		req.Route, req.Args = "tickets.show", restArgs(r, map[string]string{"ticket_id": parts[1]})
		project, n := ticketByID(parts[1])
		f.restTicket(w, r, req, project, n)
	case len(parts) == 5 && parts[0] == "projects" && parts[2] == "tickets" && parts[3] == "number" && f.r1:
		req.Route, req.Args = "tickets.by_number", restArgs(r, map[string]string{"project_id": parts[1], "number": parts[4]})
		n, err := strconv.Atoi(parts[4])
		if _, ok := ticketCounts[parts[1]]; !ok || err != nil || n < 1 || n > ticketCounts[parts[1]] {
			restError(w, req, f, http.StatusNotFound, "not_found", "")
			return
		}
		f.restTicket(w, r, req, parts[1], n)
	case len(parts) == 4 && parts[0] == "tickets" && parts[2] == "posts" && f.r1:
		req.Route, req.Args = "posts.show", restArgs(r, map[string]string{"ticket_id": parts[1], "post_id": parts[3]})
		f.restPost(w, r, req, parts[1], parts[3])
	default:
		restError(w, req, f, http.StatusNotFound, "not_found", "")
	}
}

// restTickets is GET /api/v1/projects/:project_id/tickets: the page of the
// tools' list, the status catalogue from R1 on.
func (f *Fixture) restTickets(w http.ResponseWriter, r *http.Request, req Request, project string) {
	q := r.URL.Query()
	if _, ok := ticketCounts[project]; !ok {
		restError(w, req, f, http.StatusNotFound, "not_found", "")
		return
	}
	for _, name := range []string{"assignee"} {
		if q.Has(name) {
			restError(w, req, f, http.StatusUnprocessableEntity, "invalid_input", "the inbox fixture does not filter by "+name)
			return
		}
	}
	page, perPage := 1, 25
	for name, v := range map[string]*int{"page": &page, "per_page": &perPage} {
		if raw := q.Get(name); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 {
				restError(w, req, f, http.StatusUnprocessableEntity, "invalid_input", "invalid "+name)
				return
			}
			*v = n
		}
	}
	if project == ProjectWebsite && page == 2 && f.scenarioOn(w, "W2", req) {
		return
	}
	body := f.ticketPage(project, q.Get("q"), q.Get("status"), page, perPage)
	if !f.r1 {
		delete(body["meta"].(map[string]any), "available_statuses")
	}
	restObject(w, req, f, body)
}

// restTicket is GET /api/v1/tickets/:id (and by number): with any paging
// parameter a page of comments and of attachments with their counts, as
// the tool's; without, every comment and no counts. Every text whole.
func (f *Fixture) restTicket(w http.ResponseWriter, r *http.Request, req Request, project string, n int) {
	if n == 0 {
		restError(w, req, f, http.StatusNotFound, "not_found", "")
		return
	}
	q := r.URL.Query()
	paged := f.r1 && (q.Has("posts_page") || q.Has("posts_per_page") || q.Has("posts_order") || q.Has("attachments_page"))
	postsPage, perPage, attachmentsPage, newest := 1, 50, 1, false
	if paged {
		// Checked before the ticket is looked up, as the core does.
		var bad string
		for name, v := range map[string]*int{"posts_page": &postsPage, "posts_per_page": &perPage, "attachments_page": &attachmentsPage} {
			if raw := q.Get(name); raw != "" {
				parsed, err := strconv.Atoi(raw)
				if err != nil || parsed < 1 || name == "posts_per_page" && parsed > 50 {
					bad = name
				}
				*v = parsed
			}
		}
		switch order := q.Get("posts_order"); order {
		case "", "oldest":
		case "newest":
			newest = true
		default:
			bad = "posts_order"
		}
		if bad != "" {
			restError(w, req, f, http.StatusUnprocessableEntity, "invalid_input", "invalid "+bad)
			return
		}
	}
	if n == 2 && f.scenario == Unauthorized && f.scenarioOn(w, "T2", req) {
		return
	}
	if f.scenario == SlowReads && !f.slowREST(r, req) {
		return
	}
	if n == 6 && postsPage == 2 && f.scenario == Temporary && f.scenarioOn(w, "T6P2", req) {
		return
	}
	if !paged {
		// Every readable comment, no counts, no attachments.
		postsPage, perPage = 1, max(1, len(f.posts(project, n, true)))
	}
	t := f.ticketRecord(project, n, postsPage, perPage, newest, attachmentsPage, true)
	if !paged {
		for _, k := range []string{"posts_total", "posts_truncated", "posts_page", "posts_per_page", "posts_pages", "posts_order",
			"attachments", "attachments_total", "attachments_truncated", "attachments_page", "attachments_pages"} {
			delete(t, k)
		}
	}
	restObject(w, req, f, map[string]any{"data": t, "permissions": f.permissions})
}

// restPost is GET /api/v1/tickets/:ticket_id/posts/:id: the comment whole;
// a missing, hidden or vanished one answers like a missing ticket.
func (f *Fixture) restPost(w http.ResponseWriter, r *http.Request, req Request, ticketID, postID string) {
	project, n := ticketByID(ticketID)
	if f.scenario == SlowReads && !f.slowREST(r, req) {
		return
	}
	if n > 0 && !f.vanished(postID) {
		for _, p := range f.posts(project, n, true) {
			if p.(map[string]any)["id"] == postID {
				restObject(w, req, f, map[string]any{"data": p})
				return
			}
		}
	}
	restError(w, req, f, http.StatusNotFound, "not_found", "")
}

// slowREST holds a read of a ticket for SlowReads, as the tools' reads are
// held; false, logged without a status, when the client left meanwhile.
func (f *Fixture) slowREST(r *http.Request, req Request) bool {
	select {
	case <-time.After(slowAnswer):
		return true
	case <-r.Context().Done():
		req.Status = 0
		f.record(req)
		return false
	}
}
