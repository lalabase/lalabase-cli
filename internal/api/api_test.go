package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/contract"
	"github.com/gboyke/lalabase-cli/internal/transport"
)

func TestMissingFindsAbsentFieldsAtEveryDepth(t *testing.T) {
	payload := []byte(`{"data":[{"id":"a","org":{"id":"o"}},{"org":null},{"id":"c","org":{}}],"meta":{"total":3,"note":null}}`)
	got, err := Missing(payload, []string{"data[].id", "data[].org.id", "meta.total", "meta.page", "data"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"data[1].id", "data[1].org", "data[2].org.id", "meta.page"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// Required means present and not null: a null id is an empty address, a null
// "read" a false "unread". A field that may be null is marked with "?".
func TestARequiredFieldMayNotBeNull(t *testing.T) {
	payload := []byte(`{"id":null,"status":null,"ticket":null,"project":{"id":null},"items":null,"tags":[null]}`)
	got, err := Missing(payload, []string{"id", "status?", "ticket?.id", "project?.id", "items[]", "tags[]"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"id", "project.id", "items[]", "tags[0]"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	// "?" allows null, not absence.
	got, _ = Missing([]byte(`{}`), []string{"status?", "ticket?.id"})
	if !slices.Equal(got, []string{"status?", "ticket?"}) {
		t.Fatalf("absent nullable fields: got %v", got)
	}
}

func TestMissingRefusesTheWrongShape(t *testing.T) {
	cases := map[string]string{
		"data[].id": `{"data":{"id":"a"}}`, // an object where a list belongs
		"meta.page": `{"meta":[1]}`,        // a list where an object belongs
		"id":        `[1,2]`,               // no object at the top
	}
	for path, payload := range cases {
		got, err := Missing([]byte(payload), []string{path})
		if err != nil || len(got) != 1 {
			t.Errorf("%s in %s: got %v %v, want it reported missing", path, payload, got, err)
		}
	}
	if _, err := Missing([]byte(`not json`), []string{"id"}); err == nil {
		t.Error("no JSON was accepted")
	}
}

// replayCase serves one recorded answer of the contract and records the
// arguments the adapter sent.
func replayCase(t *testing.T, tool, name string) (*API, *map[string]any) {
	t.Helper()
	return replayRecorded(t, tool, name, false)
}

// replayOlder serves the recorded answer without the machine field, as a
// server before MCP 0.7.0 sends it.
func replayOlder(t *testing.T, tool, name string) *API {
	t.Helper()
	api, _ := replayRecorded(t, tool, name, true)
	return api
}

func replayRecorded(t *testing.T, tool, name string, older bool) (*API, *map[string]any) {
	t.Helper()
	file, err := contract.Load()
	if err != nil {
		t.Fatal(err)
	}
	ex, ok := file.Contract.MCP.Calls[tool][name]
	if !ok {
		t.Fatalf("the contract has no %s/%s", tool, name)
	}
	sent := map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Params.Name != tool {
			t.Errorf("called %s, want %s", req.Params.Name, tool)
		}
		sent = req.Params.Arguments
		// The recorded answer under the id of this request.
		var body map[string]any
		_ = json.Unmarshal(ex.Response.Body, &body)
		body["id"] = req.ID
		if result, ok := body["result"].(map[string]any); ok && older {
			delete(result, "_meta")
		}
		out, _ := json.Marshal(body)
		w.WriteHeader(ex.Response.Status)
		_, _ = w.Write(out)
	}))
	t.Cleanup(srv.Close)
	c, err := transport.New(srv.URL, "lalab_pat_test")
	if err != nil {
		t.Fatal(err)
	}
	return New(c), &sent
}

// Every argument an adapter sends is one its spec declares, so feature
// detection checks exactly what will be sent.
func assertSentDeclared(t *testing.T, spec Spec, sent map[string]any) {
	t.Helper()
	for name := range sent {
		if !slices.Contains(spec.Args, name) {
			t.Errorf("%s sent %q, which its spec does not declare", spec.Name, name)
		}
	}
}

const (
	org     = "00000000-0000-4000-8000-000000000001"
	website = "00000000-0000-4000-8000-000000000031"
	login   = "00000000-0000-4000-8000-000000000041"
	unread  = "00000000-0000-4000-8000-000000000071"
	missing = "00000000-0000-4000-8000-000000000999"
)

func TestNotificationsReadsTheRecordedInbox(t *testing.T) {
	api, sent := replayCase(t, "list_notifications", "ok")
	page, ans := api.Notifications(context.Background(), NotificationQuery{Status: "unread", Page: 1, PerPage: 25})
	if !ans.OK() {
		t.Fatalf("%v %s", ans.Class, ans.Message)
	}
	if page.Meta.UnreadTotal != 1 || len(page.Data) != 1 || page.Data[0].ID != unread || page.Data[0].Organisation.ID != org ||
		page.Data[0].Ticket == nil || page.Data[0].Ticket.Number != 1 || len(page.Meta.ByOrganisation) != 1 {
		t.Fatalf("decoded %+v", page)
	}
	assertSentDeclared(t, NotificationsSpec, *sent)
}

func TestMarkNotificationReadSendsItsKey(t *testing.T) {
	api, sent := replayCase(t, "mark_notification_read", "ok")
	state, ans := api.MarkNotificationRead(context.Background(), unread, "key-1")
	if !ans.OK() || state.State != "read" || state.ReadAt == nil {
		t.Fatalf("%v %+v", ans.Class, state)
	}
	if (*sent)["idempotency_key"] != "key-1" || (*sent)["notification_id"] != unread {
		t.Fatalf("sent %v", *sent)
	}
	assertSentDeclared(t, MarkReadSpec, *sent)
}

func TestMarkingAMissingNotificationIsNotFound(t *testing.T) {
	api, _ := replayCase(t, "mark_notification_read", "empty")
	if _, ans := api.MarkNotificationRead(context.Background(), missing, "key-2"); ans.Class != transport.NotFound {
		t.Fatalf("got %v", ans.Class)
	}
}

func TestTicketsReadsTheRecordedList(t *testing.T) {
	api, sent := replayCase(t, "list_tickets", "ok")
	page, ans := api.Tickets(context.Background(), TicketQuery{ProjectID: website, Page: 1})
	if !ans.OK() || len(page.Data) != 2 || page.Data[0].Title != "Login bricht ab" || len(page.Meta.AvailableStatuses) == 0 {
		t.Fatalf("%v %+v", ans.Class, page)
	}
	assertSentDeclared(t, TicketsSpec, *sent)
}

func TestTicketReadsTheRecordedTicketWithItsComments(t *testing.T) {
	api, sent := replayCase(t, "get_ticket", "ok")
	ticket, ans := api.Ticket(context.Background(), website, login)
	if !ans.OK() || ticket.Number != 1 || ticket.Status == nil || *ticket.Status != "in_progress" ||
		len(ticket.Posts) != 2 || ticket.PostsTotal != 2 || ticket.Posts[0].Author == nil || ticket.Posts[0].Author.Name != "Ben Berger" {
		t.Fatalf("%v %+v", ans.Class, ticket)
	}
	assertSentDeclared(t, TicketSpec, *sent)
}

func TestTicketByNumberSendsTheNumberAsText(t *testing.T) {
	api, sent := replayCase(t, "get_ticket", "ok_by_number")
	if _, ans := api.TicketByNumber(context.Background(), website, 1); !ans.OK() {
		t.Fatalf("%v", ans.Class)
	}
	if (*sent)["number"] != "1" {
		t.Fatalf("sent %v", *sent)
	}
	assertSentDeclared(t, TicketSpec, *sent)
}

func TestAMissingTicketIsNotFound(t *testing.T) {
	api, _ := replayCase(t, "get_ticket", "empty")
	if _, ans := api.Ticket(context.Background(), website, missing); ans.Class != transport.NotFound {
		t.Fatalf("got %v", ans.Class)
	}
}

func TestPostCommentSendsBodyAndKey(t *testing.T) {
	api, sent := replayCase(t, "post_comment", "ok")
	comment, ans := api.PostComment(context.Background(), website, login, "Behoben in 2.4.1.", "key-3")
	if !ans.OK() || comment.BodyText != "Behoben in 2.4.1." || comment.TicketID != login {
		t.Fatalf("%v %+v", ans.Class, comment)
	}
	if (*sent)["idempotency_key"] != "key-3" || (*sent)["body"] != "Behoben in 2.4.1." {
		t.Fatalf("sent %v", *sent)
	}
	assertSentDeclared(t, CommentSpec, *sent)
}

func TestSearchKnowledgeReadsTheRecordedHits(t *testing.T) {
	api, sent := replayCase(t, "search_knowledge", "ok")
	page, ans := api.SearchKnowledge(context.Background(), KnowledgeQuery{ProjectID: website, Q: "Deploy"})
	if !ans.OK() || len(page.Data) != 1 || page.Data[0].Title != "Deploy-Anleitung" || page.Data[0].Type != "note" {
		t.Fatalf("%v %+v", ans.Class, page)
	}
	assertSentDeclared(t, KnowledgeSpec, *sent)
}

// An answer that lacks a field the client relies on is a protocol error, not
// an object with empty values.
func TestAnAnswerWithoutARequiredFieldIsAProtocolError(t *testing.T) {
	text := `{"id":"x","number":1,"title":"T"}` // no status, posts, ...
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}}, "_meta": map[string]any{"lalabase/kind": "ok"},
	}})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }))
	defer srv.Close()
	c, _ := transport.New(srv.URL, "lalab_pat_test")
	_, ans := New(c).Ticket(context.Background(), website, login)
	if ans.Class != transport.Protocol || !strings.Contains(ans.Message, "posts") {
		t.Fatalf("got %v %q", ans.Class, ans.Message)
	}
}

func restServer(t *testing.T, path, name string) *API {
	t.Helper()
	file, err := contract.Load()
	if err != nil {
		t.Fatal(err)
	}
	ex := file.Contract.REST[path][name]
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(ex.Response.Status)
		_, _ = w.Write(ex.Response.Body)
	}))
	t.Cleanup(srv.Close)
	c, _ := transport.New(srv.URL, "lalab_pat_test")
	return New(c)
}

func TestMeSaysWhetherTheTokenMayWrite(t *testing.T) {
	me, ans := restServer(t, "GET /api/v1/me", "ok_read_only").Me(context.Background())
	if !ans.OK() || me.Token.ReadOnly == nil || !*me.Token.ReadOnly || me.User.DisplayName != "Anna Albers" {
		t.Fatalf("%v %+v", ans.Class, me)
	}
	me, _ = restServer(t, "GET /api/v1/me", "ok").Me(context.Background())
	if me.Token.ReadOnly == nil || *me.Token.ReadOnly {
		t.Fatalf("write token read as %+v", me.Token)
	}
}

func TestOrganisationsAndProjects(t *testing.T) {
	orgs, ans := restServer(t, "GET /api/v1/organisations", "ok").Organisations(context.Background())
	if !ans.OK() || len(orgs) != 1 || orgs[0].Name != "Nordlicht GmbH" {
		t.Fatalf("%v %+v", ans.Class, orgs)
	}
	projects, ans := restServer(t, "GET /api/v1/projects", "ok").Projects(context.Background(), "")
	if !ans.OK() || len(projects) != 3 || projects[0].Organisation.ID != org {
		t.Fatalf("%v %+v", ans.Class, projects)
	}
	if _, ans := restServer(t, "GET /api/v1/organisations", "unauthorized").Organisations(context.Background()); ans.Class != transport.Unauthenticated {
		t.Fatalf("401: got %v", ans.Class)
	}
}

// The organisation filter is a query parameter, escaped; it cannot add a
// second parameter or leave the path.
func TestProjectsEscapesTheOrganisationFilter(t *testing.T) {
	var raw string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()
	c, _ := transport.New(srv.URL, "lalab_pat_test")
	if _, ans := New(c).Projects(context.Background(), "x&admin=1"); !ans.OK() {
		t.Fatalf("%v %s", ans.Class, ans.Message)
	}
	if raw != "organisation_id=x%26admin%3D1" {
		t.Fatalf("query %q", raw)
	}
}

// An older server answers a missing record with a sentence naming the id.
// The adapters must recognise it, or a deleted record stays in the cache.
func TestAnOlderServersNotFoundIsRecognisedByEveryOneRecordAdapter(t *testing.T) {
	ctx := context.Background()
	if _, ans := replayOlder(t, "get_ticket", "empty").Ticket(ctx, website, missing); ans.Class != transport.NotFound {
		t.Errorf("Ticket: got %v %q", ans.Class, ans.Message)
	}
	if _, ans := replayOlder(t, "post_comment", "empty").PostComment(ctx, website, missing, "Hallo", "key-4"); ans.Class != transport.NotFound {
		t.Errorf("PostComment: got %v %q", ans.Class, ans.Message)
	}
	if _, ans := replayOlder(t, "mark_notification_read", "empty").MarkNotificationRead(ctx, missing, "key-5"); ans.Class != transport.NotFound {
		t.Errorf("MarkNotificationRead: got %v %q", ans.Class, ans.Message)
	}
	// The recorded "by number" case found the ticket; an older server's
	// "No ticket found for number N." is checked with a server of its own.
	body := `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"No ticket found for number 7."}]}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
	defer srv.Close()
	c, _ := transport.New(srv.URL, "lalab_pat_test")
	if _, ans := New(c).TicketByNumber(ctx, website, 7); ans.Class != transport.NotFound {
		t.Errorf("TicketByNumber: got %v %q", ans.Class, ans.Message)
	}
}

// Every write carries the key of the person's action; without one a retry
// after a lost answer would write twice. No request is sent.
func TestAWriteWithoutAnIdempotencyKeyIsRefusedBeforeSending(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits++ }))
	defer srv.Close()
	c, _ := transport.New(srv.URL, "lalab_pat_test")
	api := New(c)
	if _, ans := api.PostComment(context.Background(), website, login, "Hallo", ""); ans.Class != transport.InvalidInput {
		t.Errorf("PostComment: got %v", ans.Class)
	}
	if _, ans := api.MarkNotificationRead(context.Background(), unread, ""); ans.Class != transport.InvalidInput {
		t.Errorf("MarkNotificationRead: got %v", ans.Class)
	}
	if hits != 0 {
		t.Fatalf("%d requests sent", hits)
	}
}

// A field of the wrong type (a number sent as text) is a protocol error,
// not a zero.
func TestAFieldOfTheWrongTypeIsAProtocolError(t *testing.T) {
	text := `{"id":"x","number":"1","title":"T","status":null,"description_text":"","assignees":[],"creator":null,` +
		`"updated_at":"u","project":{"id":"p","name":"P"},"posts":[],"posts_total":0,"posts_truncated":false,` +
		`"attachments":[],"attachments_total":0}`
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}}, "_meta": map[string]any{"lalabase/kind": "ok"},
	}})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }))
	defer srv.Close()
	c, _ := transport.New(srv.URL, "lalab_pat_test")
	if _, ans := New(c).Ticket(context.Background(), website, login); ans.Class != transport.Protocol || !strings.Contains(ans.Message, "wrong type") {
		t.Fatalf("got %v %q", ans.Class, ans.Message)
	}
}

// The scenario of the review: every field present, every value null. The
// adapter must not hand out an inbox row with an empty id.
func TestAnInboxOfNullsIsAProtocolError(t *testing.T) {
	text := `{"data":[{"id":null,"read":null,"message":null,"event_type":null,"last_event_at":null,` +
		`"organisation":{"id":null,"name":null},"ticket":{},"project":null,"actor":null}],` +
		`"meta":{"page":null,"per_page":null,"total":null,"unread_total":null,"by_organisation":null}}`
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}}, "_meta": map[string]any{"lalabase/kind": "ok"},
	}})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }))
	defer srv.Close()
	c, _ := transport.New(srv.URL, "lalab_pat_test")
	_, ans := New(c).Notifications(context.Background(), NotificationQuery{})
	if ans.Class != transport.Protocol || !strings.Contains(ans.Message, "data[0].id") || !strings.Contains(ans.Message, "data[0].ticket.id") {
		t.Fatalf("got %v %q", ans.Class, ans.Message)
	}
}
