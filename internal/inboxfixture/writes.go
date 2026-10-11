package inboxfixture

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The writes of C2 (C2a): update_ticket, one change per call, and
// post_comment with a plain or an HTML body. The fixture validates as the
// server does, in the server's order, with the server's texts, keeps the
// changes as an overlay over its synthetic rows (edits), appends comments
// to its tickets, and answers a key seen before with the original result
// without a second change. A comment marks the author's unread rows that
// name the ticket as read (core #989), so the counts and the pages change
// as on the server. The scenarios act on every write through writeGate
// and answerWrite, which mark_notification_read uses too (marks.go).

// ticketEdit is what the writes changed on one ticket: the row fields
// (title, status, assignees, updated_at) and the comments added, oldest
// first.
type ticketEdit struct {
	row   map[string]any
	posts []any
}

// The kinds a Mutation names.
const (
	MutationMarkRead = "mark_read"
	MutationComment  = "comment"
	MutationStatus   = "status"
	MutationAssign   = "assign"
	MutationTitle    = "title"
)

// writeStamp is the time every write stamps on what it changed.
const writeStamp = "2026-01-05T10:30:00Z"

// The texts of the server, as recorded in the contract copy.
const (
	textOneChange        = "exactly one change per call: title, status, or add_assignee_ids/remove_assignee_ids"
	textStatusNotAllowed = "status is not a status of this project; use a key from meta.available_statuses"
	textUserNotAllowed   = "a user in this change cannot be assigned in this project"
	textNoStatusField    = "this project has no status field"
	textNotAssignable    = "nothing was changed: a person in this change no longer has access to this project"
	textInvalidFormat    = "Invalid arguments: value at `/format` is not one of: [\"html\", \"plain\"]"
	textFormatNotString  = "Invalid arguments: value at `/format` is not a string, value at `/format` is not one of: [\"html\", \"plain\"]"
	textProjectNotFound  = "Project not found or not accessible."
	textNotAuthorized    = "Not authorized."
	textReadOnly         = "This token is read-only; writes are not permitted."
	// maxTitle and maxAssignees are the server's bounds.
	maxTitle     = 255
	maxAssignees = 50
)

// The tickets of Website the Refusals scenario refuses a write on: #2
// answers a status and an assignment with value_not_allowed, a title with
// validation_failed and a comment with forbidden; #3 answers a status with
// no_status_field; #4 answers every write with forbidden; #5 answers every
// write as vanished (empty); #7 answers an assignment with
// user_not_assignable. Every other write succeeds.
const (
	refusedValues     = 2
	refusedNoStatus   = 3
	refusedWrite      = 4
	refusedVanished   = 5
	refusedAssignable = 7
)

var uuidForm = regexp.MustCompile(`^(?:[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}|[0-9a-fA-F]{32})$`)

// edgeBlankLines is a run of blank lines (ASCII spaces and tabs) at either
// edge of a plain body (Posts::PlainTextBody in the core).
var edgeBlankLines = regexp.MustCompile(`^(?:[ \t]*\n)+|(?:\n[ \t]*)+$`)

// htmlTag is what the fixture strips from an HTML body for body_text.
var htmlTag = regexp.MustCompile(`<[^>]*>`)

// editOf is a copy of what the writes changed on a ticket, or nil.
func (f *Fixture) editOf(id string) *ticketEdit {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.edits[id]
	if !ok {
		return nil
	}
	return &ticketEdit{row: copyMap(e.row), posts: slices.Clone(e.posts)}
}

// edit is the edit of a ticket, made on first use. Called with f.mu held.
func (f *Fixture) edit(id string) *ticketEdit {
	e, ok := f.edits[id]
	if !ok {
		e = &ticketEdit{row: map[string]any{}}
		f.edits[id] = e
	}
	return e
}

// ticketNumber is the number of the ticket with the id in a project, 0
// for none.
func ticketNumber(project, id string) int {
	for n := 1; n <= ticketCounts[project]; n++ {
		if TicketID(project, n) == id {
			return n
		}
	}
	return 0
}

// writeGate applies the scenarios that act on a write before anything is
// looked up: a read-only token, a revoked token (401), the rate limit on
// the first attempt at a place. It says whether it answered.
func (f *Fixture) writeGate(w http.ResponseWriter, place string, reply func(int, any, any), req Request) bool {
	switch f.scenario {
	case ReadOnly:
		reply(http.StatusOK, toolError("read_only", textReadOnly), nil)
		return true
	case Unauthorized:
		req.Status = http.StatusUnauthorized
		f.record(req)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
		return true
	case RateLimited:
		return f.scenarioOn(w, place, req)
	}
	return false
}

// answerWrite answers a write that was carried out, as the scenario says:
// lost (the connection closes after the request was read, as a server
// that acted and then failed; status 0: no answer went out), slow (after
// a pause), else at once.
func (f *Fixture) answerWrite(w http.ResponseWriter, r *http.Request, reply func(int, any, any), req Request, result map[string]any) {
	switch f.scenario {
	case Lost:
		req.Status = 0
		f.record(req)
		if h, ok := w.(http.Hijacker); ok {
			if conn, _, err := h.Hijack(); err == nil {
				_ = conn.Close()
			}
		}
		return
	case Slow:
		if !f.pause(w, r, req) {
			return
		}
	}
	reply(http.StatusOK, toolObject(result), nil)
}

// summary is the ticket summary update_ticket answers with: the row as it
// is now plus the fields the server's summary carries beside it.
func (f *Fixture) summary(project string, n int) map[string]any {
	t := copyMap(f.ticketRows(project)[n-1])
	t["ticket_type"], t["parent_id"] = "support", nil
	t["creator"], t["created_at"] = f.ticketTmpl["creator"], f.ticketTmpl["created_at"]
	return t
}

// updateTicket answers update_ticket.
func (f *Fixture) updateTicket(w http.ResponseWriter, r *http.Request, args map[string]any, reply func(int, any, any), req Request) {
	key, _ := args["idempotency_key"].(string)
	f.mu.Lock()
	f.keys = append(f.keys, key)
	f.mu.Unlock()
	id, _ := args["ticket_id"].(string)
	if f.writeGate(w, "update:"+id, reply, req) {
		return
	}
	// The request alone: unknown keys, the ticket id, one kind, the value.
	known := []string{"ticket_id", "title", "status", "add_assignee_ids", "remove_assignee_ids", "project_id", "idempotency_key"}
	var unknown []string
	for k := range args {
		if !slices.Contains(known, k) {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		slices.Sort(unknown)
		reply(http.StatusOK, toolError("invalid_input", "unknown arguments: "+strings.Join(unknown, ", ")), nil)
		return
	}
	id = strings.TrimSpace(id)
	switch {
	case id == "":
		reply(http.StatusOK, toolError("invalid_input", "ticket_id is required"), nil)
		return
	case !uuidForm.MatchString(id):
		reply(http.StatusOK, toolError("invalid_input", "invalid ticket_id format"), nil)
		return
	}
	_, hasTitle := args["title"]
	_, hasStatus := args["status"]
	_, hasAdd := args["add_assignee_ids"]
	_, hasRemove := args["remove_assignee_ids"]
	kinds := 0
	for _, has := range []bool{hasTitle, hasStatus, hasAdd || hasRemove} {
		if has {
			kinds++
		}
	}
	if kinds != 1 {
		reply(http.StatusOK, toolError("invalid_input", textOneChange), nil)
		return
	}
	var (
		kind        string
		title       string
		status      string
		add, remove []string
		refusal     map[string]any
	)
	switch {
	case hasTitle:
		kind = MutationTitle
		title, refusal = checkTitle(args["title"])
	case hasStatus:
		kind = MutationStatus
		status, refusal = checkStatus(args["status"])
	default:
		kind = MutationAssign
		add, remove, refusal = f.checkAssignees(args, hasAdd, hasRemove)
	}
	if refusal != nil {
		reply(http.StatusOK, refusal, nil)
		return
	}
	// The ticket, in its project.
	project, _ := args["project_id"].(string)
	if _, ok := ticketCounts[project]; !ok {
		reply(http.StatusOK, toolError("not_found", textProjectNotFound), nil)
		return
	}
	n := ticketNumber(project, id)
	if n == 0 {
		reply(http.StatusOK, toolEmpty("No ticket found for "+id+"."), nil)
		return
	}
	if refusal := f.refuseWrite(project, n, kind); refusal != nil {
		reply(http.StatusOK, refusal, nil)
		return
	}
	// What the project's state decides: the status key, the people.
	if kind == MutationStatus && !slices.Contains(statusKeys, status) {
		reply(http.StatusOK, toolRefused("value_not_allowed", textStatusNotAllowed), nil)
		return
	}
	if kind == MutationAssign {
		for _, person := range add {
			if _, ok := f.people[person]; !ok {
				reply(http.StatusOK, toolRefused("value_not_allowed", textUserNotAllowed), nil)
				return
			}
		}
	}
	f.mu.Lock()
	if key != "" {
		if result, ok := f.results[key]; ok {
			f.mu.Unlock()
			reply(http.StatusOK, toolObject(result), nil)
			return
		}
	}
	e := f.edit(id)
	switch kind {
	case MutationTitle:
		e.row["title"] = title
	case MutationStatus:
		e.row["status"] = status
	case MutationAssign:
		current, _ := f.currentAssignees(project, n)
		var next []any
		for _, a := range current {
			if person, _ := a.(map[string]any)["id"].(string); !slices.Contains(remove, person) {
				next = append(next, a)
			}
		}
		for _, person := range add {
			if !slices.ContainsFunc(next, func(a any) bool { return a.(map[string]any)["id"] == person }) {
				next = append(next, map[string]any{"id": person, "name": f.people[person]})
			}
		}
		if next == nil {
			next = []any{}
		}
		e.row["assignees"] = next
	}
	e.row["updated_at"] = writeStamp
	f.mutations = append(f.mutations, Mutation{Kind: kind, Ticket: id, Key: key})
	f.mu.Unlock()
	result := f.summary(project, n)
	f.mu.Lock()
	if key != "" {
		f.results[key] = result
	}
	f.mu.Unlock()
	f.answerWrite(w, r, reply, req, result)
}

// currentAssignees are the assignees of a ticket as they are now. Called
// with f.mu held.
func (f *Fixture) currentAssignees(project string, n int) ([]any, bool) {
	if e, ok := f.edits[TicketID(project, n)]; ok {
		if list, ok := e.row["assignees"].([]any); ok {
			return list, true
		}
	}
	// The rows without the overlay (f.mu is held): the synthetic rule.
	if n%3 == 0 {
		return []any{}, false
	}
	list, _ := f.ticketRow["assignees"].([]any)
	return list, false
}

// checkTitle is the server's title rule (Lalabase::Tools::TicketTitle):
// a string, one line without control or invisible characters, with a
// character that shows, at most 255 characters after the edges are
// trimmed. Refused, never cleaned.
func checkTitle(value any) (string, map[string]any) {
	title, ok := value.(string)
	if !ok {
		return "", toolError("invalid_input", "title must be a string")
	}
	if !utf8.ValidString(title) {
		return "", toolError("invalid_input", "title must be valid UTF-8")
	}
	for _, r := range title {
		if forbiddenInTitle(r) {
			return "", toolError("invalid_input", "title must be one line without control characters")
		}
	}
	title = strings.TrimFunc(title, unicode.IsSpace)
	if !strings.ContainsFunc(title, func(r rune) bool {
		return unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsPunct(r) || unicode.IsSymbol(r)
	}) {
		return "", toolError("invalid_input", "title is required")
	}
	if utf8.RuneCountInString(title) > maxTitle {
		return "", toolError("invalid_input", fmt.Sprintf("title is longer than %d characters", maxTitle))
	}
	return title, nil
}

// forbiddenInTitle: a control character that is no space, a line break
// or tab, an invisible or direction character, a tag character.
func forbiddenInTitle(r rune) bool {
	switch {
	case unicode.IsControl(r) && !unicode.IsSpace(r):
		return true
	case r == '\t', r == '\n', r == '\v', r == '\f', r == '\r', r == 0x85, r == 0x2028, r == 0x2029, r == 0x61C, r == 0x180E:
		return true
	case r == 0x200B, r == 0x200E, r == 0x200F, r == 0xFEFF:
		return true
	case r >= 0x202A && r <= 0x202E, r >= 0x2060 && r <= 0x2064, r >= 0x2066 && r <= 0x2069, r >= 0xFFF9 && r <= 0xFFFB:
		return true
	case r >= 0xE0000 && r <= 0xE007F:
		return true
	}
	return false
}

func checkStatus(value any) (string, map[string]any) {
	status, ok := value.(string)
	if !ok {
		return "", toolError("invalid_input", "status must be a string")
	}
	status = strings.TrimSpace(status)
	if status == "" {
		return "", toolError("invalid_input", "status is required")
	}
	return status, nil
}

// checkAssignees reads both lists: strings, "me" or a UUID, at most 50,
// at least one person over both, no person in both. "me" is the person the
// fixture's /me names.
func (f *Fixture) checkAssignees(args map[string]any, hasAdd, hasRemove bool) (add, remove []string, refusal map[string]any) {
	read := func(name string, present bool) ([]string, map[string]any) {
		if !present {
			return nil, nil
		}
		list, ok := args[name].([]any)
		if !ok {
			return nil, toolError("invalid_input", name+` must be a list of user UUIDs or "me"`)
		}
		var ids []string
		for _, v := range list {
			s, ok := v.(string)
			if !ok {
				return nil, toolError("invalid_input", name+` must be a list of user UUIDs or "me"`)
			}
			ids = append(ids, strings.TrimSpace(s))
		}
		if len(ids) > maxAssignees {
			return nil, toolError("invalid_input", fmt.Sprintf("at most %d user ids per list", maxAssignees))
		}
		for i, id := range ids {
			if id == "me" {
				ids[i] = f.meID
				continue
			}
			if !uuidForm.MatchString(id) {
				return nil, toolError("invalid_input", "invalid user id in "+name)
			}
			ids[i] = strings.ToLower(id)
		}
		return ids, nil
	}
	if add, refusal = read("add_assignee_ids", hasAdd); refusal != nil {
		return nil, nil, refusal
	}
	if remove, refusal = read("remove_assignee_ids", hasRemove); refusal != nil {
		return nil, nil, refusal
	}
	if len(add) == 0 && len(remove) == 0 {
		return nil, nil, toolError("invalid_input", "add_assignee_ids or remove_assignee_ids must name at least one user")
	}
	for _, id := range add {
		if slices.Contains(remove, id) {
			return nil, nil, toolError("invalid_input", "add_assignee_ids and remove_assignee_ids name the same user")
		}
	}
	return add, remove, nil
}

// refuseWrite is the refusal the Refusals scenario gives a write of a kind
// on a ticket of Website, or nil.
func (f *Fixture) refuseWrite(project string, n int, kind string) map[string]any {
	if f.scenario != Refusals || project != ProjectWebsite {
		return nil
	}
	id := TicketID(project, n)
	switch {
	case n == refusedValues && kind == MutationStatus:
		return toolRefused("value_not_allowed", textStatusNotAllowed)
	case n == refusedValues && kind == MutationAssign:
		return toolRefused("value_not_allowed", textUserNotAllowed)
	case n == refusedValues && kind == MutationTitle:
		return toolRefused("validation_failed", "title could not be saved: Title is too long")
	case n == refusedValues && kind == MutationComment:
		return toolError("forbidden", textNotAuthorized)
	case n == refusedNoStatus && kind == MutationStatus:
		return toolRefused("no_status_field", textNoStatusField)
	case n == refusedWrite:
		return toolError("forbidden", textNotAuthorized)
	case n == refusedVanished:
		return toolEmpty("No ticket found for " + id + ".")
	case n == refusedAssignable && kind == MutationAssign:
		return toolRefused("user_not_assignable", textNotAssignable)
	}
	return nil
}

// postComment answers post_comment.
func (f *Fixture) postComment(w http.ResponseWriter, r *http.Request, args map[string]any, reply func(int, any, any), req Request) {
	key, _ := args["idempotency_key"].(string)
	f.mu.Lock()
	f.keys = append(f.keys, key)
	f.mu.Unlock()
	id, _ := args["ticket_id"].(string)
	if f.writeGate(w, "comment:"+id, reply, req) {
		return
	}
	// The SDK checks the schema before the tool runs: a format that is not
	// one of the two is refused with its text. A server without the
	// argument (older, s1-only, s2-only) ignores it and takes HTML.
	format := "html"
	if raw, ok := args["format"]; ok && !f.dropped["post_comment:format"] {
		s, isString := raw.(string)
		switch {
		case !isString:
			reply(http.StatusOK, toolError("invalid_input", textFormatNotString), nil)
			return
		case s != "html" && s != "plain":
			reply(http.StatusOK, toolError("invalid_input", textInvalidFormat), nil)
			return
		}
		format = s
	}
	body, isString := args["body"].(string)
	if !isString {
		reply(http.StatusOK, toolError("invalid_input", "Invalid arguments: value at `/body` is not a string"), nil)
		return
	}
	id = strings.TrimSpace(id)
	switch {
	case id == "":
		reply(http.StatusOK, toolError("invalid_input", "ticket_id is required"), nil)
		return
	case !uuidForm.MatchString(id):
		reply(http.StatusOK, toolError("invalid_input", "invalid ticket_id format"), nil)
		return
	case !utf8.ValidString(body):
		reply(http.StatusOK, toolError("invalid_input", "body must be valid UTF-8"), nil)
		return
	}
	plain := format == "plain"
	if plain && strings.ContainsRune(body, 0) {
		reply(http.StatusOK, toolError("invalid_input", "body must not contain NUL bytes"), nil)
		return
	}
	var text string
	if plain {
		text = edgeBlankLines.ReplaceAllString(strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\r", "\n"), "")
	} else {
		text = htmlTag.ReplaceAllString(body, "")
	}
	if strings.TrimSpace(text) == "" {
		reply(http.StatusOK, toolError("invalid_input", "body is required"), nil)
		return
	}
	project, _ := args["project_id"].(string)
	if _, ok := ticketCounts[project]; !ok {
		reply(http.StatusOK, toolError("not_found", textProjectNotFound), nil)
		return
	}
	n := ticketNumber(project, id)
	if n == 0 {
		reply(http.StatusOK, toolEmpty("No ticket found for "+id+"."), nil)
		return
	}
	if refusal := f.refuseWrite(project, n, MutationComment); refusal != nil {
		reply(http.StatusOK, refusal, nil)
		return
	}
	private := args["is_private"] == true || args["is_private"] == "true"
	f.mu.Lock()
	if key != "" {
		if result, ok := f.results[key]; ok {
			f.mu.Unlock()
			reply(http.StatusOK, toolObject(result), nil)
			return
		}
	}
	f.nextPost++
	post := map[string]any{
		"id":         fmt.Sprintf("00000000-0000-4000-8000-%012d", 900000+f.nextPost),
		"ticket_id":  id,
		"author":     map[string]any{"id": f.meID, "name": f.people[f.meID]},
		"body_text":  text,
		"body_html":  body,
		"is_private": private,
		"created_at": writeStamp,
	}
	e := f.edit(id)
	e.posts = append(e.posts, post)
	e.row["updated_at"] = writeStamp
	// Core #989: the author's unread rows that name the ticket are read.
	for _, org := range []string{OrgA, OrgB} {
		for _, row := range f.rows[org] {
			ticket, _ := row["ticket"].(map[string]any)
			if read, _ := row["read"].(bool); !read && ticket != nil && ticket["id"] == id {
				row["read"], row["read_at"], row["state"] = true, writeStamp, "read"
			}
		}
	}
	f.mutations = append(f.mutations, Mutation{Kind: MutationComment, Ticket: id, Key: key})
	result := copyMap(post)
	delete(result, "body_html")
	if key != "" {
		f.results[key] = result
	}
	f.mu.Unlock()
	f.answerWrite(w, r, reply, req, result)
}

// recordedPeople reads the people the fixture knows (the person of /me,
// the assignees of the recorded row, the creator of the recorded ticket)
// and the id of /me.
func recordedPeople(me json.RawMessage, row, ticket map[string]any) (map[string]string, string, error) {
	var body struct {
		Data struct {
			User struct {
				ID          string `json:"id"`
				DisplayName string `json:"display_name"`
			} `json:"user"`
		} `json:"data"`
	}
	if err := json.Unmarshal(me, &body); err != nil || body.Data.User.ID == "" {
		return nil, "", fmt.Errorf("inbox fixture: the recorded /me names no user")
	}
	people := map[string]string{body.Data.User.ID: body.Data.User.DisplayName}
	add := func(v any) {
		if m, ok := v.(map[string]any); ok {
			if id, ok := m["id"].(string); ok && id != "" {
				name, _ := m["name"].(string)
				people[id] = name
			}
		}
	}
	if list, ok := row["assignees"].([]any); ok {
		for _, a := range list {
			add(a)
		}
	}
	add(ticket["creator"])
	return people, body.Data.User.ID, nil
}
