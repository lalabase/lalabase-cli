package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/cache"
	"github.com/gboyke/lalabase-cli/internal/contract"
	"github.com/gboyke/lalabase-cli/internal/features"
	"github.com/gboyke/lalabase-cli/internal/queue"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/worker"
)

// K1: the reads of the ticket view take the way the session's report
// gives their feature. The scope stamps it on every ticket it issues, the
// gateway sends and decodes by it, and what is filed has one form,
// whichever way read it.

const comment = "00000000-0000-4000-8000-000000000051"

// restRecorder serves the recorded REST answers of the ticket view by
// route (the list, a ticket, one comment) and the recorded tool answers
// by tool, and records every request.
type restRecorder struct {
	mu   sync.Mutex
	sent []string
	// cases names the recorded case per route ("list", "ticket", "post")
	// or tool.
	cases map[string]string
	// edit changes a recorded REST body before it is served.
	edit func(route string, body map[string]any)
	api  *api.API
}

func recordREST(t *testing.T, cases map[string]string) *restRecorder {
	t.Helper()
	file, err := contract.Load()
	if err != nil {
		t.Fatal(err)
	}
	s := &restRecorder{cases: cases}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.sent = append(s.sent, r.Method+" "+r.URL.RequestURI())
		edit := s.edit
		s.mu.Unlock()
		if r.URL.Path == "/api/mcp" {
			var req struct {
				ID     json.RawMessage `json:"id"`
				Params struct {
					Name string `json:"name"`
				} `json:"params"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			ex := file.Contract.MCP.Calls[req.Params.Name][cases[req.Params.Name]]
			var body map[string]any
			_ = json.Unmarshal(ex.Response.Body, &body)
			body["id"] = req.ID
			out, _ := json.Marshal(body)
			w.WriteHeader(ex.Response.Status)
			_, _ = w.Write(out)
			return
		}
		route := "ticket"
		switch {
		case strings.HasSuffix(r.URL.Path, "/tickets"):
			route = "list"
		case strings.Contains(r.URL.Path, "/posts/"):
			route = "post"
		}
		ex := file.Contract.REST["ticket_view"][cases[route]]
		out := []byte(ex.Response.Body)
		if edit != nil && ex.Response.Status == http.StatusOK {
			var body map[string]any
			_ = json.Unmarshal(ex.Response.Body, &body)
			edit(route, body)
			out, _ = json.Marshal(body)
		}
		w.WriteHeader(ex.Response.Status)
		_, _ = w.Write(out)
	}))
	t.Cleanup(srv.Close)
	c, err := transport.New(srv.URL, "lalab_pat_synthetic")
	if err != nil {
		t.Fatal(err)
	}
	s.api = api.New(c)
	return s
}

func (s *restRecorder) requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.sent...)
}

type restReads struct {
	srv   *restRecorder
	gw    *Gateway
	scope *Scope
	ns    cache.Namespace
}

func setupRESTReads(t *testing.T, cases map[string]string, routes Routes) *restReads {
	t.Helper()
	life := worker.New(context.Background())
	t.Cleanup(func() { life.Stop(wall) })
	q := queue.New(queue.WithLifetime(life))
	srv := recordREST(t, cases)
	ns := confirmed(t)
	scope := NewScope(ns)
	scope.Route(routes)
	return &restReads{srv: srv, gw: New(life, q, srv.api), scope: scope, ns: ns}
}

// asPage makes the recorded page of a ticket the first page of
// PostsPageSize comments, newest first, as the client asks for it.
func asPage(route string, body map[string]any) {
	if route != "ticket" {
		return
	}
	data := body["data"].(map[string]any)
	data["posts_page"], data["posts_per_page"], data["posts_order"] = 1, PostsPageSize, "newest"
}

var allREST = Routes{List: features.REST, Ticket: features.REST, Whole: features.REST}

// The routes are the ways of the features whose reads they carry: the
// list (and its search, which needs the same), the ticket's pages, its
// texts whole. A feature that is off keeps the tools.
func TestRoutesFollowTheReport(t *testing.T) {
	report := features.Report{Via: map[string]features.Way{
		"tickets": features.REST, "tickets_search": features.REST, "ticket": features.MCP,
		"ticket_posts_paged": features.MCP, "ticket_full_text": features.REST, "inbox": features.MCP,
	}}
	if got := RoutesOf(report); got != (Routes{List: features.REST, Ticket: features.MCP, Whole: features.REST}) {
		t.Fatalf("%+v", got)
	}
	if got := RoutesOf(features.Report{}); got != (Routes{}) {
		t.Fatalf("no report: %+v", got)
	}
	// The ticket's way decides its pages: a report with the pages alone
	// over REST is not one a server gives (they need the same
	// capabilities), and the ticket's way wins.
	report.Via["ticket_posts_paged"] = features.REST
	if got := RoutesOf(report); got.Ticket != features.MCP {
		t.Fatalf("%+v", got)
	}
}

// Every ticket names the way of its read, stamped when it is issued; the
// same data read two ways is two requests.
func TestATicketCarriesTheWayOfItsRead(t *testing.T) {
	ns := confirmed(t)
	s := NewScope(ns)
	page := detailKey(ns, website, login, true)
	whole := page
	whole.FullText = true
	one := detailKey(ns, website, login, false)
	one.Post = comment
	keys := map[string]cache.Key{
		"list": ticketsKey(ns, website, "", 1), "search": ticketsKey(ns, website, "x", 1),
		"page": page, "plain": detailKey(ns, website, login, false), "whole": whole, "one": one,
		"projects": projectsKey(ns, orgA), "inbox": unreadPage(ns, "", 1),
	}
	for name, k := range keys {
		if got := s.Begin(Detail, k, queue.User).Way; got != features.MCP {
			t.Errorf("%s before any routes: %v", name, got)
		}
	}
	s.Route(Routes{List: features.REST, Ticket: features.MCP, Whole: features.REST})
	want := map[string]features.Way{"list": features.REST, "search": features.REST, "page": features.MCP, "plain": features.MCP, "whole": features.REST, "one": features.REST}
	for name, k := range keys {
		if got := s.Begin(Detail, k, queue.User).Way; got != want[name] {
			t.Errorf("%s: %v, want %v", name, got, want[name])
		}
	}
	before := s.Begin(Detail, page, queue.User)
	s.Route(allREST)
	after := s.Begin(Detail, page, queue.User)
	if before.Way == after.Way || before.ShareKey() == after.ShareKey() {
		t.Fatalf("two ways share one request: %v %v", before.Way, after.Way)
	}
}

// Over REST the list and a page of a ticket are read on their routes and
// filed in the tools' form: an entry reads the same whichever way wrote
// it. A ticket is always asked for a page.
func TestTheTicketViewOverRESTFilesTheToolsForm(t *testing.T) {
	f := setupRESTReads(t, map[string]string{"list": "list", "ticket": "show_paged"}, allREST)
	f.srv.edit = asPage
	list := f.scope.Begin(Tickets, ticketsKey(f.ns, website, "", 1), queue.User)
	l := result(t, run(f.gw.Read(context.Background(), list))).(TicketsResult)
	if !l.Answer.OK() || len(l.Page.Data) != 1 || l.Page.Data[0].ID != login || len(l.Page.Meta.AvailableStatuses) != 5 {
		t.Fatalf("list: %v %q %+v", l.Answer.Class, l.Answer.Message, l.Page)
	}
	key := detailKey(f.ns, website, login, true)
	d := result(t, run(f.gw.Read(context.Background(), f.scope.Begin(Detail, key, queue.User)))).(TicketResult)
	if !d.Answer.OK() || d.Record.ID != login || d.Record.PostsTotal != 7 || d.Record.PostsOrder != "newest" || d.Post.ID != "" {
		t.Fatalf("ticket: %v %q %+v", d.Answer.Class, d.Answer.Message, d.Record)
	}
	want := []string{
		"GET /api/v1/projects/" + website + "/tickets?page=1&per_page=25",
		"GET /api/v1/tickets/" + login + "?attachments_page=1&posts_order=newest&posts_page=1&posts_per_page=25",
	}
	if got := f.srv.requests(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("sent %q", got)
	}
	store := cache.NewStore()
	for _, r := range []Received{l, d} {
		if dec, ok := f.scope.Receive(store, r, time.Now()); !ok || dec != cache.Replace {
			t.Fatalf("%v %v", dec, ok)
		}
	}
	filed := func(k cache.Key) transport.Answer {
		e, ok := store.Get(k)
		if !ok {
			t.Fatalf("nothing filed under %v", k)
		}
		return transport.Answer{Class: transport.Object, Payload: e.Payload, Status: 200, Sent: true}
	}
	if page, ok := api.DecodeTickets(filed(list.Key)); !ok.OK() || page.Data[0].ID != login {
		t.Fatalf("the filed list: %v %q", ok.Class, ok.Message)
	}
	record, ok := api.DecodeTicket(filed(key), DetailOptions(key))
	if !ok.OK() || CheckDetail(key, record) != nil || record.PostsTotal != 7 {
		t.Fatalf("the filed ticket: %v %q", ok.Class, ok.Message)
	}
	// The description whole is the page over REST: REST has every text
	// whole, and full_text is not sent.
	whole := key
	whole.FullText = true
	w := result(t, run(f.gw.Read(context.Background(), f.scope.Begin(Expansion, whole, queue.User)))).(TicketResult)
	if !w.Answer.OK() || w.Record.DescriptionText == "" {
		t.Fatalf("whole: %v %q", w.Answer.Class, w.Answer.Message)
	}
	if got := f.srv.requests(); got[len(got)-1] != want[1] {
		t.Fatalf("the description whole sent %q", got[len(got)-1])
	}
}

// A REST answer to a ticket without its page (what a server gives that
// ignores the page) lacks the counts: a protocol error, never a ticket
// shown with no comments.
func TestATicketOverRESTWithoutItsPageIsRefused(t *testing.T) {
	f := setupRESTReads(t, map[string]string{"ticket": "show"}, allREST)
	key := detailKey(f.ns, website, "00000000-0000-4000-8000-000000000042", true)
	d := result(t, run(f.gw.Read(context.Background(), f.scope.Begin(Detail, key, queue.User)))).(TicketResult)
	if d.Answer.Class != transport.Protocol || !strings.Contains(d.Answer.Message, "posts_total") || d.Record.ID != "" {
		t.Fatalf("%v %q", d.Answer.Class, d.Answer.Message)
	}
	// Another ticket's page answered for this one is a contradiction, as
	// on the tools.
	f = setupRESTReads(t, map[string]string{"ticket": "show_paged"}, allREST)
	f.srv.edit = asPage
	other := detailKey(f.ns, website, "00000000-0000-4000-8000-000000000042", true)
	d = result(t, run(f.gw.Read(context.Background(), f.scope.Begin(Detail, other, queue.User)))).(TicketResult)
	if d.Answer.Class != transport.Protocol || !strings.Contains(d.Answer.Message, "another ticket") {
		t.Fatalf("%v %q", d.Answer.Class, d.Answer.Message)
	}
}

// One comment, whole: over REST on its own route, over the tools with
// post_id. Either way the result is the comment and the comment alone is
// filed; the ticket the tool sends along is no part of it.
func TestOneCommentIsTheCommentOnBothWays(t *testing.T) {
	for name, routes := range map[string]Routes{"rest": allREST, "mcp": {}} {
		t.Run(name, func(t *testing.T) {
			f := setupRESTReads(t, map[string]string{"post": "post", "get_ticket": "one_post"}, routes)
			k := detailKey(f.ns, website, login, false)
			k.Post = comment
			ticket := f.scope.Begin(Expansion, k, queue.User)
			r := result(t, run(f.gw.Read(context.Background(), ticket))).(TicketResult)
			if !r.Answer.OK() || r.Post.ID != comment || r.Post.BodyText != "Tritt nur in Safari auf." || r.Record.ID != "" {
				t.Fatalf("%v %q post %+v record %+v", r.Answer.Class, r.Answer.Message, r.Post, r.Record.ID)
			}
			sent := f.srv.requests()
			if name == "rest" && (len(sent) != 1 || sent[0] != "GET /api/v1/tickets/"+login+"/posts/"+comment) {
				t.Fatalf("sent %q", sent)
			}
			if name == "mcp" && (len(sent) != 1 || sent[0] != "POST /api/mcp") {
				t.Fatalf("sent %q", sent)
			}
			store := cache.NewStore()
			if dec, ok := f.scope.Receive(store, r, time.Now()); !ok || dec != cache.Replace {
				t.Fatalf("%v %v", dec, ok)
			}
			e, _ := store.Get(k)
			post, ok := api.DecodePost(transport.Answer{Class: transport.Object, Payload: e.Payload, Status: 200, Sent: true})
			if !ok.OK() || CheckPost(k, post) != nil || post.BodyText != r.Post.BodyText {
				t.Fatalf("filed %v %q", ok.Class, ok.Message)
			}
		})
	}
}

// A missing or hidden comment is the refusal CommentNotFound on both ways,
// so the ticket stays; a comment with another id is a contradiction.
func TestAMissingCommentIsARefusalOnBothWays(t *testing.T) {
	for name, c := range map[string]struct {
		routes Routes
		cases  map[string]string
	}{
		"rest": {allREST, map[string]string{"post": "post_not_found"}},
		"mcp":  {Routes{}, map[string]string{"get_ticket": "post_not_found"}},
	} {
		f := setupRESTReads(t, c.cases, c.routes)
		k := detailKey(f.ns, website, login, false)
		k.Post = "00000000-0000-4000-8000-000000000999"
		r := result(t, run(f.gw.Read(context.Background(), f.scope.Begin(Expansion, k, queue.User)))).(TicketResult)
		if r.Answer.Class != transport.Refused || r.Answer.Reason != api.CommentNotFound || r.Post.ID != "" {
			t.Errorf("%s: %v %q", name, r.Answer.Class, r.Answer.Reason)
		}
	}
	f := setupRESTReads(t, map[string]string{"post": "post"}, allREST)
	k := detailKey(f.ns, website, login, false)
	k.Post = "00000000-0000-4000-8000-000000000052"
	r := result(t, run(f.gw.Read(context.Background(), f.scope.Begin(Expansion, k, queue.User)))).(TicketResult)
	if r.Answer.Class != transport.Protocol || r.Post.ID != "" {
		t.Fatalf("another comment: %v %q", r.Answer.Class, r.Answer.Message)
	}
}

// REST reads a ticket by its id alone: a ticket moved to another project
// (the core keeps its id) answers from there. Under a key of the old
// project it is not found, as the tools say in their project's scope, so
// every variant leaves the cache; another ticket answered is still a
// contradiction (review round 2 of #48, finding 1).
func TestOverRESTATicketMovedToAnotherProjectIsNotFoundHere(t *testing.T) {
	const handbuch = "00000000-0000-4000-8000-000000000033"
	f := setupRESTReads(t, map[string]string{"ticket": "show_paged"}, allREST)
	f.srv.edit = asPage
	moved := result(t, run(f.gw.Read(context.Background(), f.scope.Begin(Detail, detailKey(f.ns, handbuch, login, true), queue.User)))).(TicketResult)
	if moved.Answer.Class != transport.NotFound || moved.Record.ID != "" || !moved.Answer.Sent {
		t.Fatalf("moved: %v %q", moved.Answer.Class, moved.Answer.Message)
	}
	store := cache.NewStore()
	if d, ok := f.scope.Receive(store, moved, time.Now()); !ok || d != cache.Remove {
		t.Fatalf("filed as %v %v", d, ok)
	}
	// The expansion of its description too.
	whole := detailKey(f.ns, handbuch, login, true)
	whole.FullText = true
	w := result(t, run(f.gw.Read(context.Background(), f.scope.Begin(Expansion, whole, queue.User)))).(TicketResult)
	if w.Answer.Class != transport.NotFound {
		t.Fatalf("moved, whole: %v", w.Answer.Class)
	}
	// Another ticket of another project answered is no move: still a
	// contradiction.
	other := result(t, run(f.gw.Read(context.Background(), f.scope.Begin(Detail, detailKey(f.ns, handbuch, "00000000-0000-4000-8000-000000000042", true), queue.User)))).(TicketResult)
	if other.Answer.Class != transport.Protocol {
		t.Fatalf("another ticket: %v %q", other.Answer.Class, other.Answer.Message)
	}
	// Over the tools a ticket of another project answered is the server's
	// contradiction: they look it up in the project asked.
	f = setupRESTReads(t, map[string]string{"get_ticket": "page_newest"}, Routes{})
	tools := result(t, run(f.gw.Read(context.Background(), f.scope.Begin(Detail, detailKey(f.ns, handbuch, login, false), queue.User)))).(TicketResult)
	if tools.Answer.Class != transport.Protocol {
		t.Fatalf("tools: %v %q", tools.Answer.Class, tools.Answer.Message)
	}
}
