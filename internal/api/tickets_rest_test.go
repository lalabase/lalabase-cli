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

// K1: the ticket list, a ticket and one comment over REST (the core's R1).
// Every answer here is a recorded one of the contract's ticket_view; what
// the client sends is recorded beside it, because the client's question
// can differ from the recorded one (it always asks a ticket for a page).

// restRecorded serves one recorded REST answer of the contract for any
// request and records the request URIs the client sent.
func restRecorded(t *testing.T, group, name string) (*API, *[]string) {
	t.Helper()
	file, err := contract.Load()
	if err != nil {
		t.Fatal(err)
	}
	ex, ok := file.Contract.REST[group][name]
	if !ok {
		t.Fatalf("the contract has no %s/%s", group, name)
	}
	var sent []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("a read sent %s", r.Method)
		}
		sent = append(sent, r.URL.RequestURI())
		w.WriteHeader(ex.Response.Status)
		_, _ = w.Write(ex.Response.Body)
	}))
	t.Cleanup(srv.Close)
	c, err := transport.New(srv.URL, "lalab_pat_test")
	if err != nil {
		t.Fatal(err)
	}
	return New(c), &sent
}

// recordedREST is the body of a recorded REST answer.
func recordedREST(t *testing.T, group, name string) []byte {
	t.Helper()
	file, err := contract.Load()
	if err != nil {
		t.Fatal(err)
	}
	ex, ok := file.Contract.REST[group][name]
	if !ok {
		t.Fatalf("the contract has no %s/%s", group, name)
	}
	return ex.Response.Body
}

// filedAlone says the payload filed is the record itself, not the
// envelope it came in.
func filedAlone(t *testing.T, payload []byte, id string) {
	t.Helper()
	var obj map[string]any
	if err := json.Unmarshal(payload, &obj); err != nil || obj["id"] != id || obj["data"] != nil || obj["permissions"] != nil {
		t.Fatalf("filed %.80s (%v)", payload, err)
	}
}

func sentOnce(t *testing.T, sent []string, want string) {
	t.Helper()
	if len(sent) != 1 || sent[0] != want {
		t.Fatalf("sent %q, want %q", sent, want)
	}
}

func TestTheTicketListOverRESTSendsTheQueryAndReadsTheRecordedPage(t *testing.T) {
	ctx := context.Background()
	api, sent := restRecorded(t, "ticket_view", "list")
	ans := api.TicketsRESTAnswer(ctx, TicketQuery{ProjectID: website, Q: "Login bricht", Page: 2, PerPage: 25})
	if !ans.OK() {
		t.Fatalf("%v %s", ans.Class, ans.Message)
	}
	sentOnce(t, *sent, "/api/v1/projects/"+website+"/tickets?page=2&per_page=25&q=Login+bricht")
	page, decoded := DecodeTicketsREST(ans)
	if !decoded.OK() || len(page.Data) != 1 || page.Data[0].Number != 1 || page.Data[0].Title != "Login bricht ab" ||
		page.Meta.Total != 1 || !slices.Equal(page.Meta.AvailableStatuses, []string{"new", "in_progress", "resolved", "closed", "backlog"}) {
		t.Fatalf("%v %+v", decoded.Class, page)
	}
	// The list has the tool's form: its payload is filed as it came.
	if string(decoded.Payload) != string(ans.Payload) {
		t.Fatal("the list's payload changed")
	}
	if same, ok := DecodeTickets(decoded); !ok.OK() || same.Data[0].ID != page.Data[0].ID {
		t.Fatalf("the filed list does not read as the tool's: %v %q", ok.Class, ok.Message)
	}
	// A blank search and an unset page are not sent.
	api, sent = restRecorded(t, "ticket_view", "list")
	api.TicketsRESTAnswer(ctx, TicketQuery{ProjectID: website})
	sentOnce(t, *sent, "/api/v1/projects/"+website+"/tickets")
	// A list without the status catalogue (a server before R1) is not the
	// list the client works with.
	r0 := ans
	r0.Payload = []byte(strings.Replace(string(ans.Payload), `"available_statuses"`, `"statuses"`, 1))
	if _, bad := DecodeTicketsREST(r0); bad.Class != transport.Protocol || !strings.Contains(bad.Message, "meta.available_statuses") || !strings.HasPrefix(bad.Message, TicketsRESTSpec.Name+": ") {
		t.Fatalf("got %v %q", bad.Class, bad.Message)
	}
	// Two decodes of one answer share nothing; a failure decodes to nothing.
	first, _ := DecodeTicketsREST(ans)
	second, _ := DecodeTicketsREST(ans)
	first.Data[0].Title = "changed"
	if second.Data[0].Title == "changed" {
		t.Fatal("two decodes share a row")
	}
	if page, failed := DecodeTicketsREST(transport.Answer{Class: transport.NotFound}); len(page.Data) != 0 || failed.Class != transport.NotFound {
		t.Fatalf("%+v %v", page, failed.Class)
	}
}

// GET /api/v1/tickets/:id without a page answers every comment without
// the counts; the client therefore always asks for a page, the first one
// when its options name none.
func TestATicketOverRESTIsAlwaysAskedForAPage(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		o    TicketOptions
		want string
	}{
		{TicketOptions{}, "?posts_page=1"},
		{TicketOptions{FullText: true}, "?posts_page=1"},
		{TicketOptions{PostsPage: 2, PostsPerPage: 25, Newest: true, AttachmentsPage: 3}, "?attachments_page=3&posts_order=newest&posts_page=2&posts_per_page=25"},
		{TicketOptions{PostsPerPage: 1, Newest: true}, "?posts_order=newest&posts_page=1&posts_per_page=1"},
		{TicketOptions{AttachmentsPage: 2, FullText: true}, "?attachments_page=2&posts_page=1"},
	}
	for _, c := range cases {
		api, sent := restRecorded(t, "ticket_view", "show_paged")
		api.TicketRESTAnswer(ctx, login, c.o)
		sentOnce(t, *sent, "/api/v1/tickets/"+login+c.want)
	}
	// One comment is read on its own route, a page below zero not at all.
	for _, o := range []TicketOptions{{PostID: comment}, {PostsPage: -1}, {PostsPerPage: -1}, {AttachmentsPage: -1}} {
		api, sent := restRecorded(t, "ticket_view", "show_paged")
		if bad := api.TicketRESTAnswer(ctx, login, o); bad.Class != transport.InvalidInput || bad.Sent || len(*sent) != 0 {
			t.Errorf("%+v: got %v sent %v", o, bad.Class, *sent)
		}
	}
}

func TestTheRecordedPageOfATicketReadsAsTheToolsTicket(t *testing.T) {
	ctx := context.Background()
	o := TicketOptions{PostsPage: 2, PostsPerPage: 1, AttachmentsPage: 1}
	api, sent := restRecorded(t, "ticket_view", "show_paged")
	ans := api.TicketRESTAnswer(ctx, login, o)
	sentOnce(t, *sent, "/api/v1/tickets/"+login+"?attachments_page=1&posts_page=2&posts_per_page=1")
	ticket, decoded := DecodeTicketREST(ans, o)
	if !decoded.OK() || ticket.ID != login || ticket.Project.ID != website || len(ticket.Posts) != 1 || ticket.PostsTotal != 7 ||
		ticket.PostsPage != 2 || ticket.PostsPages != 7 || ticket.PostsOrder != "oldest" || !ticket.PostsTruncated ||
		len(ticket.Attachments) != 1 || ticket.Attachments[0].Filename != "test_document.pdf" || ticket.AttachmentsPages != 1 {
		t.Fatalf("%v %q %+v", decoded.Class, decoded.Message, ticket)
	}
	if err := CheckTicket(ticket, o); err != nil {
		t.Fatal(err)
	}
	// The answer filed is the ticket alone, in the tool's form: an entry
	// from either way reads the same.
	filedAlone(t, decoded.Payload, login)
	again, ok := DecodeTicket(decoded, o)
	if !ok.OK() || again.ID != ticket.ID || again.PostsTotal != ticket.PostsTotal || again.Posts[0].BodyText != ticket.Posts[0].BodyText {
		t.Fatalf("the filed ticket reads as %v %q %+v", ok.Class, ok.Message, again)
	}
	// Two decodes of one answer share nothing.
	a, _ := DecodeTicketREST(ans, o)
	b, _ := DecodeTicketREST(ans, o)
	a.Posts[0].BodyText = "changed"
	if b.Posts[0].BodyText == "changed" {
		t.Fatal("two decodes share a comment")
	}
}

// The answer without a page (what GET /api/v1/tickets/:id gives when no
// page is asked) lacks the counts: never read as a ticket with no
// comments.
func TestATicketWithoutItsPageIsAProtocolError(t *testing.T) {
	for _, name := range []string{"show", "by_number"} {
		api, _ := restRecorded(t, "ticket_view", name)
		ans := api.TicketRESTAnswer(context.Background(), login, TicketOptions{})
		_, bad := DecodeTicketREST(ans, TicketOptions{})
		if bad.Class != transport.Protocol || !strings.Contains(bad.Message, "data.posts_total") || !bad.Sent {
			t.Errorf("%s: got %v %q", name, bad.Class, bad.Message)
		}
	}
	// No data, or data that is no ticket.
	for _, body := range []string{`{"permissions":{}}`, `{"data":[1]}`, `{"data":null}`} {
		raw := transport.Answer{Class: transport.Object, Payload: []byte(body), Status: 200, Sent: true}
		if _, bad := DecodeTicketREST(raw, TicketOptions{}); bad.Class != transport.Protocol {
			t.Errorf("%s: got %v", body, bad.Class)
		}
	}
}

// A refused option and a missing or hidden ticket keep the class the
// transport gave them.
func TestTheRecordedRefusalsOfATicketOverREST(t *testing.T) {
	ctx := context.Background()
	api, _ := restRecorded(t, "ticket_view", "show_invalid_paging")
	if _, ans := DecodeTicketREST(api.TicketRESTAnswer(ctx, login, TicketOptions{}), TicketOptions{}); ans.Class != transport.InvalidInput || ans.Code != "invalid_input" {
		t.Fatalf("got %v %q", ans.Class, ans.Code)
	}
	for _, name := range []string{"by_number_not_found", "by_number_hidden"} {
		api, _ := restRecorded(t, "ticket_view", name)
		if _, ans := api.TicketByNumberREST(ctx, website, 999); ans.Class != transport.NotFound {
			t.Errorf("%s: got %v", name, ans.Class)
		}
	}
}

func TestATicketByNumberOverRESTAsksForAPage(t *testing.T) {
	ctx := context.Background()
	// The recorded answer by number has no page (the recorder asked for
	// none); a page of it is the page of show, the same serializer and
	// pages (core R1), served here for the number's route.
	file, _ := contract.Load()
	paged := file.Contract.REST["ticket_view"]["show_paged"].Response.Body
	var sent []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent = append(sent, r.URL.RequestURI())
		_, _ = w.Write(paged)
	}))
	defer srv.Close()
	c, _ := transport.New(srv.URL, "lalab_pat_test")
	ticket, ans := New(c).TicketByNumberREST(ctx, website, 1)
	if !ans.OK() || ticket.Number != 1 || ticket.ID != login || ticket.PostsTotal != 7 {
		t.Fatalf("%v %q %+v", ans.Class, ans.Message, ticket)
	}
	sentOnce(t, sent, "/api/v1/projects/"+website+"/tickets/number/1?posts_page=1")
	// The recorded answer without a page is refused like any such answer.
	api, _ := restRecorded(t, "ticket_view", "by_number")
	if _, bad := api.TicketByNumberREST(ctx, website, 2); bad.Class != transport.Protocol {
		t.Fatalf("got %v", bad.Class)
	}
	// A number below 1 is no number.
	for _, n := range []int{0, -1} {
		if _, bad := New(c).TicketByNumberREST(ctx, website, n); bad.Class != transport.InvalidInput || bad.Sent {
			t.Errorf("%d: got %v", n, bad.Class)
		}
	}
}

func TestOneCommentOverRESTIsTheCommentWhole(t *testing.T) {
	ctx := context.Background()
	api, sent := restRecorded(t, "ticket_view", "post")
	ans := api.PostRESTAnswer(ctx, login, comment)
	sentOnce(t, *sent, "/api/v1/tickets/"+login+"/posts/"+comment)
	post, decoded := DecodePostREST(ans)
	if !decoded.OK() || post.ID != comment || post.BodyText != "Tritt nur in Safari auf." || post.Author == nil || post.Author.Name != "Ben Berger" || post.IsPrivate {
		t.Fatalf("%v %q %+v", decoded.Class, decoded.Message, post)
	}
	// The comment is filed alone, the form either way files.
	kept, ok := DecodePost(decoded)
	if !ok.OK() || kept.ID != post.ID || kept.BodyText != post.BodyText || kept.Author == nil || kept.Author.Name != "Ben Berger" {
		t.Fatalf("%v %q %+v", ok.Class, ok.Message, kept)
	}
	filedAlone(t, decoded.Payload, comment)
	// "Not found" for one comment is not about the ticket: the refusal
	// the tool's way gives, so the cache keeps the ticket.
	api, _ = restRecorded(t, "ticket_view", "post_not_found")
	if _, refused := DecodePostREST(api.PostRESTAnswer(ctx, login, missing)); refused.Class != transport.Refused || refused.Reason != CommentNotFound {
		t.Fatalf("got %v %q", refused.Class, refused.Reason)
	}
	// A comment without its text is no comment.
	broken := ans
	broken.Payload = []byte(`{"data":{"id":"x","author":null,"is_private":false,"created_at":"2026-01-05T09:25:00Z"}}`)
	if _, bad := DecodePostREST(broken); bad.Class != transport.Protocol || !strings.Contains(bad.Message, "data.body_text") {
		t.Fatalf("got %v %q", bad.Class, bad.Message)
	}
}

// The tool's answer for one comment is filed as that comment alone, the
// form REST files: one entry, whichever way read it.
func TestTheToolsOneCommentIsFiledAsTheComment(t *testing.T) {
	ctx := context.Background()
	o := TicketOptions{PostID: comment}
	api, _ := replayCase(t, "get_ticket", "one_post")
	ticket, ans := DecodeTicket(api.TicketAnswer(ctx, website, login, o), o)
	if !ans.OK() || ticket.Title != "Login bricht ab" || len(ticket.Posts) != 1 {
		t.Fatalf("%v %q %+v", ans.Class, ans.Message, ticket)
	}
	post, ok := DecodePost(ans)
	if !ok.OK() || post.ID != comment || post.BodyText != ticket.Posts[0].BodyText {
		t.Fatalf("%v %q %+v", ok.Class, ok.Message, post)
	}
	// The REST answer of the same comment files the same comment.
	restAPI, _ := restRecorded(t, "ticket_view", "post")
	_, rest := DecodePostREST(restAPI.PostRESTAnswer(ctx, login, comment))
	var a, b map[string]any
	_ = json.Unmarshal(ans.Payload, &a)
	_ = json.Unmarshal(rest.Payload, &b)
	for _, k := range []string{"id", "body_text", "is_private"} {
		if a[k] != b[k] {
			t.Errorf("%s: %v by the tool, %v over REST", k, a[k], b[k])
		}
	}
	// A filed ticket is no comment: an entry of the tool's form from an
	// earlier run is dropped, never read as one.
	if _, bad := DecodePost(transport.Answer{Class: transport.Object, Payload: []byte(recordedText(t, "get_ticket", func(map[string]any) {})), Status: 200, Sent: true}); bad.Class != transport.Protocol {
		t.Fatalf("a ticket read as a comment: %v", bad.Class)
	}
}

// An id the server gave is a path segment of a REST read: one that would
// leave its segment is refused before anything is sent.
func TestAnIDThatIsNoPathSegmentIsRefused(t *testing.T) {
	ctx := context.Background()
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits++ }))
	defer srv.Close()
	c, _ := transport.New(srv.URL, "lalab_pat_test")
	api := New(c)
	for _, id := range []string{"", ".", "..", "a/b", "../me", "a\\b"} {
		answers := map[string]transport.Answer{
			"list":    api.TicketsRESTAnswer(ctx, TicketQuery{ProjectID: id}),
			"ticket":  api.TicketRESTAnswer(ctx, id, TicketOptions{}),
			"post":    api.PostRESTAnswer(ctx, login, id),
			"post of": api.PostRESTAnswer(ctx, id, comment),
		}
		_, byNumber := api.TicketByNumberREST(ctx, id, 1)
		answers["by number"] = byNumber
		for what, ans := range answers {
			if ans.Class != transport.InvalidInput || ans.Sent {
				t.Errorf("%s with %q: got %v sent %v", what, id, ans.Class, ans.Sent)
			}
		}
	}
	if hits != 0 {
		t.Fatalf("%d requests sent", hits)
	}
	// An id with characters a path escapes reaches the server in its
	// segment.
	var got string
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.EscapedPath()
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not_found","code":"not_found"}`))
	}))
	defer srv2.Close()
	c2, _ := transport.New(srv2.URL, "lalab_pat_test")
	New(c2).TicketRESTAnswer(ctx, "a b?c#d", TicketOptions{})
	if got != "/api/v1/tickets/a%20b%3Fc%23d" {
		t.Fatalf("sent %q", got)
	}
}

// Rule 3 for the REST way: each REST spec is the tool's spec under data
// (the list has the tool's form as it is), so a field the client comes to
// rely on reaches both ways, and the one spec is what the recorded answers
// are checked against here and every answer at run time.
func TestTheRESTSpecsAreTheToolsFieldsAndTheContractHasThem(t *testing.T) {
	inData := func(fields []string) []string {
		var out []string
		for _, f := range fields {
			out = append(out, "data."+f)
		}
		return out
	}
	if !slices.Equal(TicketsRESTSpec.Required, TicketsSpec.Required) {
		t.Errorf("list: %v", TicketsRESTSpec.Required)
	}
	for _, spec := range []Spec{TicketRESTSpec, TicketByNumberRESTSpec} {
		if !slices.Equal(spec.Required, inData(TicketSpec.Required)) {
			t.Errorf("%s: %v", spec.Name, spec.Required)
		}
	}
	var postsOfTicket []string
	for _, f := range TicketSpec.Required {
		if rest, ok := strings.CutPrefix(f, "posts[]."); ok {
			postsOfTicket = append(postsOfTicket, rest)
		}
	}
	if !slices.Equal(PostRESTSpec.Required, inData(postsOfTicket)) {
		t.Errorf("post: %v, the ticket's comments %v", PostRESTSpec.Required, postsOfTicket)
	}
	paged := TicketOptions{PostsPage: 2, PostsPerPage: 1, AttachmentsPage: 1}
	for _, c := range []struct {
		spec Spec
		name string
	}{
		{TicketsRESTSpec, "list"},
		{ticketRESTSpec(TicketRESTSpec, paged), "show_paged"},
		{PostRESTSpec, "post"},
	} {
		missing, err := c.spec.Missing(recordedREST(t, "ticket_view", c.name))
		if err != nil || len(missing) > 0 {
			t.Errorf("%s: the recorded %s lacks %v (%v)", c.spec.Name, c.name, missing, err)
		}
	}
	// Without a page the counts are missing, and only they.
	for _, name := range []string{"show", "by_number"} {
		missing, _ := TicketRESTSpec.Missing(recordedREST(t, "ticket_view", name))
		if !slices.Equal(missing, []string{"data.posts_total", "data.posts_truncated", "data.attachments[]", "data.attachments_total", "data.attachments_truncated"}) {
			t.Errorf("%s without a page lacks %v", name, missing)
		}
	}
	for _, spec := range []Spec{TicketsRESTSpec, TicketRESTSpec, TicketByNumberRESTSpec, PostRESTSpec} {
		if !slices.ContainsFunc(Specs, func(s Spec) bool { return s.Name == spec.Name }) {
			t.Errorf("%s is not among Specs", spec.Name)
		}
		if spec.Tool != "" || spec.Method != "" || !strings.HasPrefix(spec.Name, "GET "+spec.Path) {
			t.Errorf("%s is no REST read: %+v", spec.Name, spec)
		}
	}
}
