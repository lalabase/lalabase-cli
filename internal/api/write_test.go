package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/transport"
)

// The writes of C2 (server slice S2, MCP 0.10.0 and 0.11.0): update_ticket,
// one change per call, and post_comment with a plain text body. Every
// answer is replayed from the contract copy.

// export is the recorded ticket #2 of Website, the one the recorded
// update_ticket cases change.
const export = "00000000-0000-4000-8000-000000000042"

// UpdateTicket sends the one argument of its change beside the project, the
// ticket and the key, nothing else: the status key as given, the title as
// given, "me" for an assignment (the server resolves it; the id from /me is
// never sent). The summary decodes into a list row.
func TestUpdateTicketSendsExactlyTheOneChange(t *testing.T) {
	ctx := context.Background()
	api, sent := replayCase(t, "update_ticket", "ok_status")
	row, ans := api.UpdateTicket(ctx, website, export, TicketChange{Status: "in_progress"}, "key-0001")
	if !ans.OK() || row.ID != export || row.Number != 2 || row.Status == nil || *row.Status != "in_progress" || row.Title != "Export als CSV" || row.UpdatedAt == "" {
		t.Fatalf("%v %q %+v", ans.Class, ans.Message, row)
	}
	sentExactly(t, *sent, map[string]any{"project_id": website, "ticket_id": export, "status": "in_progress", "idempotency_key": "key-0001"})
	assertSentDeclared(t, UpdateTicketSpec, *sent)

	api, sent = replayCase(t, "update_ticket", "ok_title")
	row, ans = api.UpdateTicket(ctx, website, export, TicketChange{Title: "Export als CSV und XLSX"}, "key-0002")
	if !ans.OK() || row.Title != "Export als CSV und XLSX" || len(row.Assignees) != 1 || row.Assignees[0].Name != "Anna Albers" {
		t.Fatalf("%v %q %+v", ans.Class, ans.Message, row)
	}
	sentExactly(t, *sent, map[string]any{"project_id": website, "ticket_id": export, "title": "Export als CSV und XLSX", "idempotency_key": "key-0002"})
	assertSentDeclared(t, UpdateTicketSpec, *sent)

	api, sent = replayCase(t, "update_ticket", "ok_assign")
	row, ans = api.UpdateTicket(ctx, website, export, TicketChange{AddMe: true}, "key-0003")
	if !ans.OK() || len(row.Assignees) != 1 || row.Assignees[0].ID != "00000000-0000-4000-8000-000000000011" {
		t.Fatalf("%v %q %+v", ans.Class, ans.Message, row)
	}
	ids, _ := (*sent)["add_assignee_ids"].([]any)
	if len(*sent) != 4 || len(ids) != 1 || ids[0] != "me" || (*sent)["idempotency_key"] != "key-0003" {
		t.Fatalf("sent %v", *sent)
	}
	if _, ok := (*sent)["remove_assignee_ids"]; ok {
		t.Fatal("an assignment to me removed somebody")
	}
	assertSentDeclared(t, UpdateTicketSpec, *sent)
}

// A change that names nothing, or two things, is refused before sending:
// the server would refuse it after a round trip (update_ticket/mixed).
func TestAChangeOfNoneOrTwoKindsIsRefusedBeforeSending(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits++ }))
	defer srv.Close()
	c, _ := transport.New(srv.URL, "lalab_pat_test")
	api := New(c)
	for _, change := range []TicketChange{{}, {Status: "new", Title: "x"}, {Status: "new", AddMe: true}, {Title: "x", AddMe: true}, {Status: "new", Title: "x", AddMe: true}} {
		if _, ans := api.UpdateTicket(context.Background(), website, export, change, "key-0004"); ans.Class != transport.InvalidInput || ans.Sent {
			t.Errorf("%+v: got %v sent %v", change, ans.Class, ans.Sent)
		}
	}
	if hits != 0 {
		t.Fatalf("%d requests sent", hits)
	}
	// The server's own refusal of a mixed call is an invalid input too.
	mixed, _ := replayCase(t, "update_ticket", "mixed")
	if _, ans := mixed.UpdateTicket(context.Background(), website, export, TicketChange{Status: "new"}, "key-0005"); ans.Class != transport.InvalidInput || !ans.Sent {
		t.Fatalf("mixed: %v sent %v", ans.Class, ans.Sent)
	}
}

// The recorded refusals keep their class and reason through the adapter,
// and an older server's sentence for a missing ticket is recognised, as for
// every adapter of one record.
func TestUpdateTicketPassesTheRecordedRefusalsOn(t *testing.T) {
	cases := map[string]struct {
		change TicketChange
		class  transport.Class
		reason string
	}{
		"status_not_allowed": {TicketChange{Status: "erledigt"}, transport.Refused, "value_not_allowed"},
		"value_not_allowed":  {TicketChange{AddMe: true}, transport.Refused, "value_not_allowed"},
		"no_status_field":    {TicketChange{Status: "new"}, transport.Refused, "no_status_field"},
		"empty":              {TicketChange{Status: "new"}, transport.NotFound, ""},
		"hidden":             {TicketChange{Status: "new"}, transport.NotFound, ""},
		"forbidden":          {TicketChange{Status: "new"}, transport.NoAccess, ""},
		"not_found":          {TicketChange{Status: "new"}, transport.NotFound, ""},
		"read_only":          {TicketChange{Status: "new"}, transport.ReadOnly, ""},
	}
	for name, tc := range cases {
		api, _ := replayCase(t, "update_ticket", name)
		_, ans := api.UpdateTicket(context.Background(), website, export, tc.change, "key-0006")
		if ans.Class != tc.class || ans.Reason != tc.reason || !ans.Sent {
			t.Errorf("%s: got %v %q sent %v", name, ans.Class, ans.Reason, ans.Sent)
		}
	}
	if _, ans := replayOlder(t, "update_ticket", "empty").UpdateTicket(context.Background(), website, missing, TicketChange{Status: "new"}, "key-0007"); ans.Class != transport.NotFound {
		t.Errorf("older empty: got %v %q", ans.Class, ans.Message)
	}
}

// A summary that lacks a field the client relies on is a protocol error,
// kept sent: the write may have been carried out.
func TestAnUpdateAnswerWithoutItsFieldsIsAProtocolErrorKeptSent(t *testing.T) {
	_, ans := replayText(t, `{"id":"x"}`).UpdateTicket(context.Background(), website, export, TicketChange{Status: "new"}, "key-0008")
	if ans.Class != transport.Protocol || !ans.Sent {
		t.Fatalf("got %v sent %v", ans.Class, ans.Sent)
	}
}

// PostComment sends format only when asked for plain text: without it the
// request is the request of an older client, which a server before 0.11.0
// understands.
func TestPostCommentSendsFormatOnlyForPlainText(t *testing.T) {
	ctx := context.Background()
	api, sent := replayCase(t, "post_comment_formats", "plain")
	body := "line one\nline two\n\nsee <file.txt> &lt;tag&gt;"
	c, ans := api.PostComment(ctx, website, login, body, CommentOptions{Plain: true}, "key-0009")
	if !ans.OK() || c.TicketID != login || c.BodyText != body || c.ID == "" || c.IsPrivate {
		t.Fatalf("%v %q %+v", ans.Class, ans.Message, c)
	}
	sentExactly(t, *sent, map[string]any{"project_id": website, "ticket_id": login, "body": body, "format": "plain", "idempotency_key": "key-0009"})
	assertSentDeclared(t, CommentSpec, *sent)

	api, sent = replayCase(t, "post_comment", "ok")
	if _, ans := api.PostComment(ctx, website, login, "Behoben in 2.4.1.", CommentOptions{}, "key-0010"); !ans.OK() {
		t.Fatalf("%v %q", ans.Class, ans.Message)
	}
	sentExactly(t, *sent, map[string]any{"project_id": website, "ticket_id": login, "body": "Behoben in 2.4.1.", "idempotency_key": "key-0010"})
}

// The recorded refusals of a plain comment keep their class.
func TestPlainCommentRefusalsKeepTheirClass(t *testing.T) {
	for name, class := range map[string]transport.Class{
		"invalid_format": transport.InvalidInput, "format_not_string": transport.InvalidInput, "nul": transport.InvalidInput,
		"whitespace_only": transport.InvalidInput, "read_only": transport.ReadOnly, "hidden": transport.NotFound, "forbidden": transport.NoAccess,
	} {
		api, _ := replayCase(t, "post_comment_formats", name)
		if _, ans := api.PostComment(context.Background(), website, login, "x", CommentOptions{Plain: true}, "key-0011"); ans.Class != class || !ans.Sent {
			t.Errorf("%s: got %v sent %v", name, ans.Class, ans.Sent)
		}
	}
}

// A key of the wrong form is refused before sending for the new write too.
func TestUpdateTicketRefusesAKeyOfTheWrongFormBeforeSending(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits++ }))
	defer srv.Close()
	c, _ := transport.New(srv.URL, "lalab_pat_test")
	for _, key := range []string{"", "short", "key with spaces"} {
		if _, ans := New(c).UpdateTicket(context.Background(), website, export, TicketChange{Status: "new"}, key); ans.Class != transport.InvalidInput || ans.Sent {
			t.Errorf("%q: got %v", key, ans.Class)
		}
	}
	if hits != 0 {
		t.Fatalf("%d requests sent", hits)
	}
}
