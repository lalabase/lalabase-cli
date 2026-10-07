package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/cache"
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
		if !slices.Contains(spec.Args, name) && !slices.Contains(spec.Optional, name) {
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
	comment = "00000000-0000-4000-8000-000000000051"
	reply   = "00000000-0000-4000-8000-000000000052"
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
	state, ans := api.MarkNotificationRead(context.Background(), unread, "key-0001")
	if !ans.OK() || state.State != "read" || state.ReadAt == nil {
		t.Fatalf("%v %+v", ans.Class, state)
	}
	if (*sent)["idempotency_key"] != "key-0001" || (*sent)["notification_id"] != unread {
		t.Fatalf("sent %v", *sent)
	}
	assertSentDeclared(t, MarkReadSpec, *sent)
}

func TestMarkingAMissingNotificationIsNotFound(t *testing.T) {
	api, _ := replayCase(t, "mark_notification_read", "empty")
	if _, ans := api.MarkNotificationRead(context.Background(), missing, "key-0002"); ans.Class != transport.NotFound {
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
	comment, ans := api.PostComment(context.Background(), website, login, "Behoben in 2.4.1.", "key-0003")
	if !ans.OK() || comment.BodyText != "Behoben in 2.4.1." || comment.TicketID != login {
		t.Fatalf("%v %+v", ans.Class, comment)
	}
	if (*sent)["idempotency_key"] != "key-0003" || (*sent)["body"] != "Behoben in 2.4.1." {
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
	if !ans.OK() || len(projects) != 4 || projects[0].Organisation.ID != org {
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
	if _, ans := replayOlder(t, "post_comment", "empty").PostComment(ctx, website, missing, "Hallo", "key-0004"); ans.Class != transport.NotFound {
		t.Errorf("PostComment: got %v %q", ans.Class, ans.Message)
	}
	if _, ans := replayOlder(t, "mark_notification_read", "empty").MarkNotificationRead(ctx, missing, "key-0005"); ans.Class != transport.NotFound {
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
		`"attachments":[],"attachments_total":0,"attachments_truncated":false}`
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

// A key the server would treat as none (blank after strip) or that no
// client of ours makes is refused before sending: the server would write
// without remembering the key, and a retry would write again.
func TestAnIdempotencyKeyOfTheWrongFormIsRefusedBeforeSending(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits++ }))
	defer srv.Close()
	c, _ := transport.New(srv.URL, "lalab_pat_test")
	api := New(c)
	for _, key := range []string{" \t\n", "short", "key with spaces", " key-0001", strings.Repeat("k", 129), "key-0001\x00"} {
		if _, ans := api.PostComment(context.Background(), website, login, "Hallo", key); ans.Class != transport.InvalidInput {
			t.Errorf("PostComment with %q: got %v", key, ans.Class)
		}
		if _, ans := api.MarkNotificationRead(context.Background(), unread, key); ans.Class != transport.InvalidInput {
			t.Errorf("MarkNotificationRead with %q: got %v", key, ans.Class)
		}
	}
	if hits != 0 {
		t.Fatalf("%d requests sent", hits)
	}
}

// replayText serves one tool answer with the given text and the machine field.
func replayText(t *testing.T, text string) *API {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}}, "_meta": map[string]any{"lalabase/kind": "ok"},
	}})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }))
	t.Cleanup(srv.Close)
	c, _ := transport.New(srv.URL, "lalab_pat_test")
	return New(c)
}

// recordedText is the text of a recorded tool answer, changed by edit.
func recordedText(t *testing.T, tool string, edit func(map[string]any)) string {
	t.Helper()
	file, err := contract.Load()
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	_ = json.Unmarshal(file.Contract.MCP.Calls[tool]["ok"].Response.Body, &body)
	var payload map[string]any
	_ = json.Unmarshal([]byte(body.Result.Content[0].Text), &payload)
	edit(payload)
	out, _ := json.Marshal(payload)
	return string(out)
}

// A deleted user is filtered out of the server's lookups: its id stays, its
// name becomes null (Tickets::Serializer, Notifications::Serializer). One
// such row must not make the whole ticket or inbox page unreadable.
func TestADeletedUserDoesNotBreakTheTicketOrTheInbox(t *testing.T) {
	text := recordedText(t, "get_ticket", func(p map[string]any) {
		p["creator"].(map[string]any)["name"] = nil
		p["posts"].([]any)[0].(map[string]any)["author"].(map[string]any)["name"] = nil
	})
	ticket, ans := replayText(t, text).Ticket(context.Background(), website, login)
	if !ans.OK() || ticket.Creator == nil || ticket.Creator.Name != "" || ticket.Posts[0].Author == nil || ticket.Posts[0].Author.ID == "" {
		t.Fatalf("ticket: %v %q %+v", ans.Class, ans.Message, ticket)
	}

	text = recordedText(t, "list_notifications", func(p map[string]any) {
		row := p["data"].([]any)[0].(map[string]any)
		row["actor"].(map[string]any)["name"] = nil
		row["project"] = map[string]any{"id": "p-gone", "name": nil}
	})
	if _, ans := replayText(t, text).Notifications(context.Background(), NotificationQuery{}); !ans.OK() {
		t.Fatalf("inbox: %v %q", ans.Class, ans.Message)
	}

	text = recordedText(t, "post_comment", func(p map[string]any) { p["author"].(map[string]any)["name"] = nil })
	if _, ans := replayText(t, text).PostComment(context.Background(), website, login, "x", "key-0009"); !ans.OK() {
		t.Fatalf("comment: %v %q", ans.Class, ans.Message)
	}
}

// Turning an answer into a protocol error keeps what the transport knew: a
// write that reached the server stays "sent", so it is reloaded, not resent.
func TestAProtocolErrorKeepsSentAndStatus(t *testing.T) {
	_, ans := replayText(t, `{}`).PostComment(context.Background(), website, login, "x", "key-0010")
	if ans.Class != transport.Protocol || !ans.Sent || ans.Status != 200 {
		t.Fatalf("got %v sent=%v status=%d", ans.Class, ans.Sent, ans.Status)
	}
}

// The name of a deleted user may be null, its id may not: an empty id is no
// address for anything the person does next.
func TestAPersonWithoutAnIDIsAProtocolError(t *testing.T) {
	ctx := context.Background()
	cases := map[string]func() transport.Answer{
		"ticket creator": func() transport.Answer {
			text := recordedText(t, "get_ticket", func(p map[string]any) { p["creator"].(map[string]any)["id"] = nil })
			_, ans := replayText(t, text).Ticket(ctx, website, login)
			return ans
		},
		"comment author in a ticket": func() transport.Answer {
			text := recordedText(t, "get_ticket", func(p map[string]any) {
				p["posts"].([]any)[0].(map[string]any)["author"].(map[string]any)["id"] = nil
			})
			_, ans := replayText(t, text).Ticket(ctx, website, login)
			return ans
		},
		"inbox actor": func() transport.Answer {
			text := recordedText(t, "list_notifications", func(p map[string]any) {
				p["data"].([]any)[0].(map[string]any)["actor"].(map[string]any)["id"] = nil
			})
			_, ans := replayText(t, text).Notifications(ctx, NotificationQuery{})
			return ans
		},
		"new comment author": func() transport.Answer {
			text := recordedText(t, "post_comment", func(p map[string]any) { p["author"].(map[string]any)["id"] = nil })
			_, ans := replayText(t, text).PostComment(ctx, website, login, "x", "key-0011")
			return ans
		},
	}
	for name, run := range cases {
		if ans := run(); ans.Class != transport.Protocol {
			t.Errorf("%s: got %v %q", name, ans.Class, ans.Message)
		}
	}
}

// --- server slice S1 (MCP 0.8.0, 0.9.0) ----------------------------------------

// sentExactly fails unless the adapter sent these arguments and no others: an
// optional argument the person did not use is not sent, so a request without
// the new options is the request of an older client.
func sentExactly(t *testing.T, sent map[string]any, want map[string]any) {
	t.Helper()
	if len(sent) != len(want) {
		t.Fatalf("sent %v, want %v", sent, want)
	}
	for k, v := range want {
		if sent[k] != v {
			t.Fatalf("sent %s = %#v, want %#v (all: %v)", k, sent[k], v, sent)
		}
	}
}

func TestTheInboxOfOneOrganisationSendsItsID(t *testing.T) {
	api, sent := replayCase(t, "list_notifications", "by_organisation")
	page, ans := api.Notifications(context.Background(), NotificationQuery{Status: "all", OrganisationID: org})
	if !ans.OK() || len(page.Data) != 2 || page.Data[0].Organisation.ID != org || len(page.Meta.ByOrganisation) != 1 {
		t.Fatalf("%v %+v", ans.Class, page)
	}
	sentExactly(t, *sent, map[string]any{"status": "all", "organisation_id": org})
	assertSentDeclared(t, NotificationsSpec, *sent)
}

func TestAnOrganisationOutOfReachIsNotFound(t *testing.T) {
	api, _ := replayCase(t, "list_notifications", "organisation_not_found")
	if _, ans := api.Notifications(context.Background(), NotificationQuery{OrganisationID: missing}); ans.Class != transport.NotFound {
		t.Fatalf("got %v %q", ans.Class, ans.Message)
	}
}

func TestTheInboxWithoutAnOrganisationSendsNone(t *testing.T) {
	api, sent := replayCase(t, "list_notifications", "ok")
	if _, ans := api.Notifications(context.Background(), NotificationQuery{Status: "unread"}); !ans.OK() {
		t.Fatalf("%v", ans.Class)
	}
	sentExactly(t, *sent, map[string]any{"status": "unread"})
}

func TestATicketWithoutOptionsSendsWhatItSentBefore(t *testing.T) {
	api, sent := replayCase(t, "get_ticket", "ok")
	if _, ans := api.TicketWith(context.Background(), website, login, TicketOptions{}); !ans.OK() {
		t.Fatalf("%v", ans.Class)
	}
	sentExactly(t, *sent, map[string]any{"project_id": website, "ticket_id": login})
}

func TestTheNewestCommentsComeAsAPage(t *testing.T) {
	api, sent := replayCase(t, "get_ticket", "page_newest")
	ticket, ans := api.TicketWith(context.Background(), website, login, TicketOptions{PostsPerPage: 1, Newest: true})
	if !ans.OK() || len(ticket.Posts) != 1 || ticket.Posts[0].ID != reply || ticket.PostsTotal != 2 ||
		ticket.PostsPage != 1 || ticket.PostsPerPage != 1 || ticket.PostsPages != 2 || ticket.PostsOrder != "newest" ||
		!ticket.PostsTruncated || ticket.AttachmentsPage != 1 {
		t.Fatalf("%v %q %+v", ans.Class, ans.Message, ticket)
	}
	// JSON numbers arrive as float64 on the recording side.
	sentExactly(t, *sent, map[string]any{"project_id": website, "ticket_id": login, "posts_per_page": float64(1), "posts_order": "newest"})
	assertSentDeclared(t, TicketSpec, *sent)
}

// Oldest first is the server's default: the client does not send it.
func TestPagingSendsOnlyWhatDiffersFromTheDefault(t *testing.T) {
	api, sent := replayCase(t, "get_ticket", "ok")
	if _, ans := api.TicketWith(context.Background(), website, login, TicketOptions{PostsPage: 2, AttachmentsPage: 3}); !ans.OK() {
		t.Fatalf("%v %q", ans.Class, ans.Message)
	}
	sentExactly(t, *sent, map[string]any{"project_id": website, "ticket_id": login, "posts_page": float64(2), "attachments_page": float64(3)})
	assertSentDeclared(t, TicketSpec, *sent)
}

// full_text goes out as a JSON boolean: the server refuses the text "true".
func TestOneCommentComesWholeAndFullTextIsABoolean(t *testing.T) {
	api, sent := replayCase(t, "get_ticket", "one_post")
	ticket, ans := api.TicketWith(context.Background(), website, login, TicketOptions{PostID: comment, FullText: true})
	if !ans.OK() || len(ticket.Posts) != 1 || ticket.Posts[0].ID != comment || ticket.PostID != comment || ticket.PostsTotal != 2 {
		t.Fatalf("%v %q %+v", ans.Class, ans.Message, ticket)
	}
	sentExactly(t, *sent, map[string]any{"project_id": website, "ticket_id": login, "post_id": comment, "full_text": true})
	assertSentDeclared(t, TicketSpec, *sent)
}

// A missing or hidden comment answers "empty" like a missing ticket, and the
// cache removes a record on "not found". The adapter turns it into a refusal
// with a reason, so the ticket the comment was asked of stays: if the ticket
// itself is gone, the next plain read says so.
func TestAMissingCommentKeepsTheTicket(t *testing.T) {
	api, _ := replayCase(t, "get_ticket", "post_not_found")
	ans := func() transport.Answer {
		_, ans := api.TicketWith(context.Background(), website, login, TicketOptions{PostID: missing})
		return ans
	}()
	if ans.Class != transport.Refused || ans.Reason != CommentNotFound || !strings.Contains(ans.Message, "No comment found") {
		t.Fatalf("got %v %q %q", ans.Class, ans.Reason, ans.Message)
	}
	if d := cache.Decide(ans.Class, cache.Read); d != cache.KeepShowMessage {
		t.Fatalf("cache decision %v", d)
	}
	// Without PostID a missing ticket stays "not found", and is removed.
	api, _ = replayCase(t, "get_ticket", "empty")
	if _, ans := api.TicketWith(context.Background(), website, missing, TicketOptions{}); ans.Class != transport.NotFound ||
		cache.Decide(ans.Class, cache.Read) != cache.Remove {
		t.Fatalf("plain: %v", ans.Class)
	}
}

// The reason is a value others rely on (the plan, C1's cache rule), not only
// a constant both sides share.
func TestTheMissingCommentReasonIsPinned(t *testing.T) {
	if CommentNotFound != "comment_not_found" {
		t.Fatalf("CommentNotFound is %q", CommentNotFound)
	}
}

// The adapter changes class and reason, nothing else: status, sent, code,
// message and payload are what the transport gave.
func TestTheMissingCommentKeepsWhatTheTransportKnew(t *testing.T) {
	file, err := contract.Load()
	if err != nil {
		t.Fatal(err)
	}
	ex := file.Contract.MCP.Calls["get_ticket"]["post_not_found"]
	// The recorded answer under the id of each request.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID json.RawMessage `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var body map[string]any
		_ = json.Unmarshal(ex.Response.Body, &body)
		body["id"] = req.ID
		out, _ := json.Marshal(body)
		w.WriteHeader(ex.Response.Status)
		_, _ = w.Write(out)
	}))
	defer srv.Close()
	c, _ := transport.New(srv.URL, "lalab_pat_test")
	args := map[string]any{"project_id": website, "ticket_id": login, "post_id": missing}
	raw := c.CallTool(context.Background(), transport.Call{Tool: "get_ticket", Args: args, OneRecord: true})
	_, got := New(c).TicketWith(context.Background(), website, login, TicketOptions{PostID: missing})
	if raw.Class != transport.NotFound || got.Class != transport.Refused {
		t.Fatalf("raw %v, adapted %v", raw.Class, got.Class)
	}
	if got.Status != raw.Status || got.Status != 200 || got.Sent != raw.Sent || !got.Sent || got.Code != raw.Code ||
		got.Message != raw.Message || string(got.Payload) != string(raw.Payload) {
		t.Fatalf("raw %+v\nadapted %+v", raw, got)
	}
}

// Only "not found" is about the comment. A ticket the reader may not see
// stays "no access", and the cache removes it.
func TestNoAccessWithAPostIDStaysNoAccess(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":1,"result":{"isError":true,"content":[{"type":"text","text":"Not authorized."}],"_meta":{"lalabase/code":"forbidden"}}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
	defer srv.Close()
	c, _ := transport.New(srv.URL, "lalab_pat_test")
	_, ans := New(c).TicketWith(context.Background(), website, login, TicketOptions{PostID: comment})
	if ans.Class != transport.NoAccess || ans.Reason != "" || cache.Decide(ans.Class, cache.Read) != cache.Remove {
		t.Fatalf("got %v %q", ans.Class, ans.Reason)
	}
}

// The whole description alone: only full_text goes out, and an answer
// without page fields is fine, because no page was asked for.
func TestTheWholeDescriptionAlone(t *testing.T) {
	var sent map[string]any
	text := recordedText(t, "get_ticket", func(p map[string]any) {
		p["description_text"] = "lang"
		for _, k := range []string{"posts_page", "posts_per_page", "posts_pages", "posts_order", "attachments_page", "attachments_pages"} {
			delete(p, k)
		}
	})
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}}, "_meta": map[string]any{"lalabase/kind": "ok"},
	}})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Params struct {
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		sent = req.Params.Arguments
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	c, _ := transport.New(srv.URL, "lalab_pat_test")
	ticket, ans := New(c).TicketWith(context.Background(), website, login, TicketOptions{FullText: true})
	if !ans.OK() || ticket.DescriptionText != "lang" {
		t.Fatalf("%v %q %q", ans.Class, ans.Message, ticket.DescriptionText)
	}
	sentExactly(t, sent, map[string]any{"project_id": website, "ticket_id": login, "full_text": true})
}

// attachments_truncated is required on every ticket answer, with options or
// without: a missing one would read as "complete".
func TestEveryTicketAnswerSaysWhetherItsAttachmentsAreComplete(t *testing.T) {
	for _, o := range []TicketOptions{{}, {FullText: true}} {
		for name, edit := range map[string]func(map[string]any){
			"missing": func(p map[string]any) { delete(p, "attachments_truncated") },
			"null":    func(p map[string]any) { p["attachments_truncated"] = nil },
		} {
			text := recordedText(t, "get_ticket", func(p map[string]any) { edit(p); p["attachments_total"] = 11 })
			if _, ans := replayText(t, text).TicketWith(context.Background(), website, login, o); ans.Class != transport.Protocol {
				t.Errorf("%+v, %s: %v", o, name, ans.Class)
			}
		}
	}
}

// A negative page is refused before anything is sent: the server refuses it
// too, and dropping it would answer page 1 as if asked for.
func TestANegativePageIsRefusedBeforeSending(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits++ }))
	defer srv.Close()
	c, _ := transport.New(srv.URL, "lalab_pat_test")
	for _, o := range []TicketOptions{{PostsPage: -1}, {PostsPerPage: -1}, {AttachmentsPage: -1}} {
		if _, ans := New(c).TicketWith(context.Background(), website, login, o); ans.Class != transport.InvalidInput {
			t.Errorf("%+v: got %v", o, ans.Class)
		}
	}
	if hits != 0 {
		t.Fatalf("%d requests sent", hits)
	}
}

// attachments_truncated says whether the page is a part; read wrong it says
// "complete" for a cut list.
func TestAttachmentsTruncatedIsReadAndRequired(t *testing.T) {
	ctx := context.Background()
	text := recordedText(t, "get_ticket", func(p map[string]any) {
		p["attachments_total"] = 11
		p["attachments_truncated"] = true
	})
	ticket, ans := replayText(t, text).TicketWith(ctx, website, login, TicketOptions{AttachmentsPage: 1})
	if !ans.OK() || !ticket.AttachmentsTruncated {
		t.Fatalf("%v %q truncated %v", ans.Class, ans.Message, ticket.AttachmentsTruncated)
	}
	cases := map[string]func(map[string]any){
		"missing": func(p map[string]any) { delete(p, "attachments_truncated") },
		"null":    func(p map[string]any) { p["attachments_truncated"] = nil },
	}
	for name, edit := range cases {
		text := recordedText(t, "get_ticket", edit)
		if _, ans := replayText(t, text).TicketWith(ctx, website, login, TicketOptions{AttachmentsPage: 1}); ans.Class != transport.Protocol {
			t.Errorf("page, %s: %v", name, ans.Class)
		}
		withPost := recordedText(t, "get_ticket", func(p map[string]any) { edit(p); p["post_id"] = comment })
		if _, ans := replayText(t, withPost).TicketWith(ctx, website, login, TicketOptions{PostID: comment}); ans.Class != transport.Protocol {
			t.Errorf("one comment, %s: %v", name, ans.Class)
		}
	}
}

// A page of attachments alone is a page: its fields are required as well.
func TestAPageOfAttachmentsAloneNeedsItsPageFields(t *testing.T) {
	for _, field := range []string{"attachments_page", "attachments_pages"} {
		for name, edit := range map[string]func(map[string]any){
			"missing": func(p map[string]any) { delete(p, field) },
			"null":    func(p map[string]any) { p[field] = nil },
		} {
			text := recordedText(t, "get_ticket", edit)
			if _, ans := replayText(t, text).TicketWith(context.Background(), website, login, TicketOptions{AttachmentsPage: 2}); ans.Class != transport.Protocol ||
				!strings.Contains(ans.Message, field) {
				t.Errorf("%s %s: %v %q", field, name, ans.Class, ans.Message)
			}
		}
	}
}

// One comment comes with a page of attachments; their page fields are
// required there as well.
func TestOneCommentNeedsTheAttachmentPageFields(t *testing.T) {
	for _, field := range []string{"attachments_page", "attachments_pages"} {
		for name, edit := range map[string]func(map[string]any){
			"missing": func(p map[string]any) { delete(p, field) },
			"null":    func(p map[string]any) { p[field] = nil },
		} {
			text := recordedText(t, "get_ticket", func(p map[string]any) { edit(p); p["post_id"] = comment })
			if _, ans := replayText(t, text).TicketWith(context.Background(), website, login, TicketOptions{PostID: comment}); ans.Class != transport.Protocol ||
				!strings.Contains(ans.Message, field) {
				t.Errorf("%s %s: %v %q", field, name, ans.Class, ans.Message)
			}
		}
	}
}

func TestPagingTheServerCannotReadIsInvalidInput(t *testing.T) {
	api, _ := replayCase(t, "get_ticket", "invalid_paging")
	if _, ans := api.TicketWith(context.Background(), website, login, TicketOptions{Newest: true}); ans.Class != transport.InvalidInput {
		t.Fatalf("got %v %q", ans.Class, ans.Message)
	}
}

// A paged request relies on the page fields; one that lacks them is a
// protocol error, never a page 0 of 0. A request without options does not
// need them, so an older server's ticket still reads.
func TestAPageWithoutItsPageFieldsIsAProtocolError(t *testing.T) {
	ctx := context.Background()
	text := recordedText(t, "get_ticket", func(p map[string]any) { delete(p, "posts_pages") })
	if _, ans := replayText(t, text).TicketWith(ctx, website, login, TicketOptions{PostsPage: 2}); ans.Class != transport.Protocol ||
		!strings.Contains(ans.Message, "posts_pages") {
		t.Fatalf("paged: %v %q", ans.Class, ans.Message)
	}
	if _, ans := replayText(t, text).TicketWith(ctx, website, login, TicketOptions{}); !ans.OK() {
		t.Fatalf("plain: %v %q", ans.Class, ans.Message)
	}
	// Newest first alone is a page as well: the order the server used must be there.
	text = recordedText(t, "get_ticket", func(p map[string]any) { delete(p, "posts_order") })
	if _, ans := replayText(t, text).TicketWith(ctx, website, login, TicketOptions{Newest: true}); ans.Class != transport.Protocol ||
		!strings.Contains(ans.Message, "posts_order") {
		t.Fatalf("newest: %v %q", ans.Class, ans.Message)
	}
	text = recordedText(t, "get_ticket", func(p map[string]any) { delete(p, "post_id") })
	if _, ans := replayText(t, text).TicketWith(ctx, website, login, TicketOptions{PostID: comment}); ans.Class != transport.Protocol ||
		!strings.Contains(ans.Message, "post_id") {
		t.Fatalf("one comment: %v %q", ans.Class, ans.Message)
	}
}
