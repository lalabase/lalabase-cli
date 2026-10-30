package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/cache"
	"github.com/gboyke/lalabase-cli/internal/contract"
	"github.com/gboyke/lalabase-cli/internal/queue"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/worker"
)

// C1c: the reads of the projects and tickets section go through one entry,
// Read, which sends the request of the key's operation and decodes for
// every waiter on its own.

const (
	orgA    = "00000000-0000-4000-8000-000000000001"
	website = "00000000-0000-4000-8000-000000000031"
	login   = "00000000-0000-4000-8000-000000000041"
)

// recorder serves recorded answers by request: the REST path, or the MCP
// tool and its case; every request waits for release.
type recorder struct {
	requests atomic.Int32
	release  chan struct{}
	api      *api.API
	cases    map[string]string // tool or path → recorded case
	// edit changes the payload of a tool's recorded answer before it is
	// served (a page size the contract did not record).
	edit func(tool string, payload map[string]any)
}

func record(t *testing.T, cases map[string]string) *recorder {
	t.Helper()
	file, err := contract.Load()
	if err != nil {
		t.Fatal(err)
	}
	s := &recorder{release: make(chan struct{}), cases: cases}
	edited := func(tool string, body map[string]any) {
		if s.edit == nil {
			return
		}
		result, _ := body["result"].(map[string]any)
		content, _ := result["content"].([]any)
		if len(content) == 0 {
			return
		}
		first, _ := content[0].(map[string]any)
		text, _ := first["text"].(string)
		var payload map[string]any
		if json.Unmarshal([]byte(text), &payload) != nil {
			return
		}
		s.edit(tool, payload)
		out, _ := json.Marshal(payload)
		first["text"] = string(out)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.requests.Add(1)
		select {
		case <-s.release:
		case <-r.Context().Done():
			return
		case <-time.After(wall):
			return
		}
		if r.URL.Path != "/api/mcp" {
			ex := file.Contract.REST["GET "+r.URL.Path][cases[r.URL.Path]]
			w.WriteHeader(ex.Response.Status)
			_, _ = w.Write(ex.Response.Body)
			return
		}
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
		edited(req.Params.Name, body)
		out, _ := json.Marshal(body)
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

type reads struct {
	life   *worker.Supervisor
	queue  *queue.Queue
	srv    *recorder
	events *Events
	gw     *Gateway
	scope  *Scope
	ns     cache.Namespace
}

func setupReads(t *testing.T, cases map[string]string) *reads {
	t.Helper()
	life := worker.New(context.Background())
	t.Cleanup(func() { life.Stop(wall) })
	q := queue.New(queue.WithLifetime(life))
	srv := record(t, cases)
	ns := confirmed(t)
	ev := NewEvents(life)
	return &reads{life: life, queue: q, srv: srv, events: ev, gw: New(life, q, srv.api, WithEvents(ev)), scope: NewScope(ns), ns: ns}
}

func projectsKey(ns cache.Namespace, org string) cache.Key {
	k := ns.Key(api.ProjectsSpec.Name)
	k.Organisation = org
	return k
}

func ticketsKey(ns cache.Namespace, project, q string, page int) cache.Key {
	k := ns.Key(api.TicketsSpec.Tool)
	k.Project, k.Query, k.Page, k.PerPage = project, q, page, TicketPageSize
	return k
}

func detailKey(ns cache.Namespace, project, id string, paged bool) cache.Key {
	k := ns.Key(api.TicketSpec.Tool)
	k.Project, k.Ticket = project, id
	if paged {
		k.PostsPage, k.PostsPerPage, k.Newest, k.AttachmentsPage = 1, PostsPageSize, true, 1
	}
	return k
}

// The preview (background) and the open (the person) of one ticket share
// one HTTP request; each waiter gets the whole ticket, decoded for itself.
func TestPreviewAndOpenOfATicketShareOneRequestAndEachDecodes(t *testing.T) {
	f := setupReads(t, map[string]string{"get_ticket": "page_newest"})
	// The recorded page was asked with one comment a page; C1c asks for
	// PostsPageSize, so the answer is served as if it had been.
	f.srv.edit = func(_ string, payload map[string]any) { payload["posts_per_page"] = PostsPageSize }
	key := detailKey(f.ns, website, login, true)
	preview := f.scope.Begin(Detail, key, queue.Background)
	open := f.scope.Begin(Detail, key, queue.User)
	if preview.ShareKey() != open.ShareKey() {
		t.Fatal("the two waiters do not share one read")
	}
	first := run(f.gw.Read(context.Background(), preview))
	waitFor(t, "the request", func() bool { return f.srv.requests.Load() == 1 })
	second := run(f.gw.Read(context.Background(), open))
	waitFor(t, "the second waiter joining", func() bool { return f.queue.Sharers(open.ShareKey()) == 2 })
	close(f.srv.release)
	a, ok1 := result(t, first).(TicketResult)
	b, ok2 := result(t, second).(TicketResult)
	if !ok1 || !ok2 || !a.Answer.OK() || !b.Answer.OK() {
		t.Fatalf("results %+v / %+v", a, b)
	}
	if a.Ticket != preview || b.Ticket != open {
		t.Fatal("a result carries another waiter's ticket")
	}
	if a.Record.ID != login || len(a.Record.Posts) != 1 || a.Record.PostsPages != 2 || !reflect.DeepEqual(a.Record, b.Record) {
		t.Fatalf("records differ or are empty: %+v / %+v", a.Record, b.Record)
	}
	if n := f.srv.requests.Load(); n != 1 {
		t.Fatalf("%d HTTP requests", n)
	}
	if a.Flight == 0 || a.Flight != b.Flight {
		t.Fatalf("one request, flights %d and %d", a.Flight, b.Flight)
	}
	a.Record.Posts[0].BodyText = "changed"
	a.Answer.Payload[0] = 'X'
	if b.Record.Posts[0].BodyText == "changed" || b.Answer.Payload[0] == 'X' {
		t.Fatal("the waiters share data")
	}
	store := cache.NewStore()
	for _, r := range []TicketResult{a, b} {
		if d, ok := f.scope.Receive(store, r, time.Now()); !ok || d != cache.Replace {
			t.Fatalf("waiter %d: %v %v", r.Waiter, d, ok)
		}
	}
	if e, ok := store.Get(key); !ok || len(e.Payload) == 0 {
		t.Fatal("the ticket was not filed under its key")
	}
}

// Read sends the request of the key's operation: projects and ticket lists
// come as their own results; an operation Read does not know is refused
// before anything is sent, with the ticket, so its waiter is freed.
func TestReadDispatchesByOperation(t *testing.T) {
	f := setupReads(t, map[string]string{"/api/v1/projects": "ok_one_organisation", "list_tickets": "ok", "list_notifications": "ok"})
	close(f.srv.release)
	p, ok := result(t, run(f.gw.Read(context.Background(), f.scope.Begin(Projects, projectsKey(f.ns, orgA), queue.User)))).(ProjectsResult)
	if !ok || !p.Answer.OK() || len(p.Projects) != 4 || p.Flight == 0 {
		t.Fatalf("projects: %+v", p)
	}
	l, ok := result(t, run(f.gw.Read(context.Background(), f.scope.Begin(Tickets, ticketsKey(f.ns, website, "", 1), queue.User)))).(TicketsResult)
	if !ok || !l.Answer.OK() || len(l.Page.Data) != 2 || l.Page.Meta.Total != 2 {
		t.Fatalf("tickets: %+v", l)
	}
	i, ok := result(t, run(f.gw.Read(context.Background(), f.scope.Begin(Organisations, unreadPage(f.ns, "", 1), queue.User)))).(InboxResult)
	if !ok || !i.Answer.OK() || len(i.Page.Data) != 1 {
		t.Fatalf("inbox: %+v", i)
	}
	if n := f.srv.requests.Load(); n != 3 {
		t.Fatalf("%d requests", n)
	}
	unknown := f.scope.Begin(Projects, f.ns.Key("search_knowledge"), queue.User)
	r, ok := result(t, run(f.gw.Read(context.Background(), unknown))).(Result)
	if !ok || r.Ticket != unknown || r.Answer.Class != transport.InvalidInput || f.srv.requests.Load() != 3 {
		t.Fatalf("unknown operation: %+v after %d requests", r, f.srv.requests.Load())
	}
}

// A page that contradicts its key is a protocol error before the model
// sees it: another project's tickets, another organisation's projects,
// another ticket.
func TestAnAnswerToAnotherQuestionIsAProtocolError(t *testing.T) {
	f := setupReads(t, map[string]string{"/api/v1/projects": "ok_one_organisation", "list_tickets": "ok", "get_ticket": "ok"})
	close(f.srv.release)
	p := result(t, run(f.gw.Read(context.Background(), f.scope.Begin(Projects, projectsKey(f.ns, "another-org"), queue.User)))).(ProjectsResult)
	if p.Answer.Class != transport.Protocol || !strings.Contains(p.Answer.Message, "organisation") || len(p.Projects) != 0 {
		t.Fatalf("projects of another organisation: %v %q", p.Answer.Class, p.Answer.Message)
	}
	l := result(t, run(f.gw.Read(context.Background(), f.scope.Begin(Tickets, ticketsKey(f.ns, website, "", 2), queue.User)))).(TicketsResult)
	if l.Answer.Class != transport.Protocol || !strings.Contains(l.Answer.Message, "page") || len(l.Page.Data) != 0 {
		t.Fatalf("page 1 for page 2: %v %q", l.Answer.Class, l.Answer.Message)
	}
	d := result(t, run(f.gw.Read(context.Background(), f.scope.Begin(Detail, detailKey(f.ns, website, "another-ticket", false), queue.User)))).(TicketResult)
	if d.Answer.Class != transport.Protocol || !strings.Contains(d.Answer.Message, "ticket") || d.Record.ID != "" {
		t.Fatalf("another ticket: %v %q", d.Answer.Class, d.Answer.Message)
	}
	// The recorded plain answer carries the page fields of a page the
	// client did not ask for: a plain read ignores them.
	plain := result(t, run(f.gw.Read(context.Background(), f.scope.Begin(Detail, detailKey(f.ns, website, login, false), queue.User)))).(TicketResult)
	if !plain.Answer.OK() || plain.Record.ID != login || len(plain.Record.Posts) != 2 {
		t.Fatalf("plain: %v %+v", plain.Answer.Class, plain.Record)
	}
}

// The checks of the tickets C1c sends: anything else is refused before a
// request is made. Later pages, the order and the expansions are C1d's
// (TestTheChecksOfTheTicketsC1dSends).
func TestTheChecksOfTheTicketsC1cSends(t *testing.T) {
	ns := confirmed(t)
	s := NewScope(ns)
	good := []Ticket{
		s.Begin(Projects, projectsKey(ns, orgA), queue.User),
		s.Begin(Tickets, ticketsKey(ns, website, "", 1), queue.User),
		s.Begin(Tickets, ticketsKey(ns, website, "login", 3), queue.User),
		s.Begin(Detail, detailKey(ns, website, login, true), queue.Background),
		s.Begin(Detail, detailKey(ns, website, login, false), queue.User),
		// C9b: the title of an agent's ticket, read on its own.
		s.Begin(Titles, detailKey(ns, website, login, true), queue.Background),
		s.Begin(Titles, detailKey(ns, website, login, false), queue.Background),
	}
	for _, g := range good {
		if err := CheckReadTicket(g); err != nil {
			t.Errorf("%+v: %v", g.Key, err)
		}
	}
	bad := map[string]Ticket{
		"projects without an organisation": s.Begin(Projects, projectsKey(ns, ""), queue.User),
		"projects with a page":             s.Begin(Projects, func() cache.Key { k := projectsKey(ns, orgA); k.Page = 2; return k }(), queue.User),
		"projects in another area":         s.Begin(Tickets, projectsKey(ns, orgA), queue.User),
		"tickets without a project":        s.Begin(Tickets, ticketsKey(ns, "", "", 1), queue.User),
		"tickets page 0":                   s.Begin(Tickets, ticketsKey(ns, website, "", 0), queue.User),
		"tickets of another page size":     s.Begin(Tickets, func() cache.Key { k := ticketsKey(ns, website, "", 1); k.PerPage = 50; return k }(), queue.User),
		"tickets with a blank status":      s.Begin(Tickets, func() cache.Key { k := ticketsKey(ns, website, "", 1); k.Status = " "; return k }(), queue.User),
		"tickets with a control character": s.Begin(Tickets, func() cache.Key { k := ticketsKey(ns, website, "", 1); k.Status = "a\x1bb"; return k }(), queue.User),
		"tickets in another area":          s.Begin(Detail, ticketsKey(ns, website, "", 1), queue.User),
		"a ticket without an id":           s.Begin(Detail, detailKey(ns, website, "", true), queue.User),
		"a ticket without a project":       s.Begin(Detail, detailKey(ns, "", login, true), queue.User),
		"a ticket of another page size":    s.Begin(Detail, func() cache.Key { k := detailKey(ns, website, login, true); k.PostsPerPage = 10; return k }(), queue.User),
		"a ticket with full text":          s.Begin(Detail, func() cache.Key { k := detailKey(ns, website, login, true); k.FullText = true; return k }(), queue.User),
		"a ticket with one comment":        s.Begin(Detail, func() cache.Key { k := detailKey(ns, website, login, true); k.Post = "c-1"; return k }(), queue.User),
		"a ticket in another area":         s.Begin(Tickets, detailKey(ns, website, login, true), queue.User),
		"a title with full text":           s.Begin(Titles, func() cache.Key { k := detailKey(ns, website, login, true); k.FullText = true; return k }(), queue.User),
		"a title with one comment":         s.Begin(Titles, func() cache.Key { k := detailKey(ns, website, login, false); k.Post = "c-1"; return k }(), queue.User),
		"an inbox read":                    s.Begin(Inbox, func() cache.Key { k := unreadPage(ns, "org-a", 1); k.PerPage = 10; return k }(), queue.User),
	}
	for name, b := range bad {
		if err := CheckReadTicket(b); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	// The queries carry every option of the key and nothing else.
	k := ticketsKey(ns, website, "login", 3)
	if got := TicketsQuery(k); got != (api.TicketQuery{ProjectID: website, Q: "login", Page: 3, PerPage: TicketPageSize}) {
		t.Fatalf("tickets query %+v", got)
	}
	if got := DetailOptions(detailKey(ns, website, login, true)); got != (api.TicketOptions{PostsPage: 1, PostsPerPage: PostsPageSize, Newest: true, AttachmentsPage: 1}) {
		t.Fatalf("detail options %+v", got)
	}
	if got := DetailOptions(detailKey(ns, website, login, false)); got != (api.TicketOptions{}) {
		t.Fatalf("plain options %+v", got)
	}
	if TicketPageSize != 25 || PostsPageSize != 25 {
		t.Fatalf("page sizes %d and %d", TicketPageSize, PostsPageSize)
	}
}

// Every kind of result is filed through Receive under the same rules as
// the inbox: the right ticket is accepted and filed, a forged one refused.
func TestEveryKindOfResultIsFiledThroughReceive(t *testing.T) {
	ns := confirmed(t)
	s, store := NewScope(ns), cache.NewStore()
	for name, r := range map[string]Received{
		"projects": ProjectsResult{Ticket: s.Begin(Projects, projectsKey(ns, orgA), queue.User), Answer: transport.Answer{Class: transport.Object, Payload: []byte(`{"data":[]}`), Status: 200, Sent: true}},
		"tickets":  TicketsResult{Ticket: s.Begin(Tickets, ticketsKey(ns, website, "", 1), queue.User), Answer: transport.Answer{Class: transport.Object, Payload: []byte(`{"data":[]}`), Status: 200, Sent: true}},
		"ticket":   TicketResult{Ticket: s.Begin(Detail, detailKey(ns, website, login, true), queue.Background), Answer: transport.Answer{Class: transport.Object, Payload: []byte(`{"id":"x"}`), Status: 200, Sent: true}},
	} {
		h := r.Header()
		if d, ok := s.Receive(store, r, now); !ok || d != cache.Replace {
			t.Fatalf("%s: %v %v", name, d, ok)
		}
		if e, ok := store.Get(h.Key); !ok || string(e.Payload) != string(h.Answer.Payload) {
			t.Fatalf("%s: not filed", name)
		}
		forged := h.Ticket
		forged.Gens.Area++
		if _, ok := s.Receive(store, TicketResult{Ticket: forged, Answer: h.Answer}, now); ok {
			t.Fatalf("%s: a forged ticket was accepted", name)
		}
	}
	// Moving the detail on cancels the detail's waiters and no other's.
	keep := s.Begin(Tickets, ticketsKey(ns, website, "", 2), queue.User)
	gone := s.Begin(Detail, detailKey(ns, website, "t-2", true), queue.Background)
	if moved := s.Move(Detail); len(moved) != 1 || moved[0] != gone.Waiter {
		t.Fatalf("moved %v", moved)
	}
	if _, ok := s.Receive(store, TicketsResult{Ticket: keep, Answer: transport.Answer{Class: transport.Object, Payload: []byte(`{}`)}}, now); !ok {
		t.Fatal("another area's waiter was refused")
	}
}

// A 401 on any read reaches the mailbox, whatever becomes of the result.
func TestA401OnAnyReadIsRecorded(t *testing.T) {
	f := setupReads(t, map[string]string{"/api/v1/projects": "unauthorized"})
	close(f.srv.release)
	r := result(t, run(f.gw.Read(context.Background(), f.scope.Begin(Projects, projectsKey(f.ns, orgA), queue.User)))).(ProjectsResult)
	if r.Answer.Class != transport.Unauthenticated || !f.events.State().Revoked {
		t.Fatalf("%v, revoked %v", r.Answer.Class, f.events.State().Revoked)
	}
}

// A timer carries its token to the model after its delay, under the
// session; once the lifetime is over it says nothing.
func TestATimerCarriesItsToken(t *testing.T) {
	life := worker.New(context.Background())
	ev := NewEvents(life)
	msg, ok := result(t, run(ev.Timer(time.Millisecond, 7))).(Due)
	if !ok || msg.Session != ev.Session() || msg.Token != 7 {
		t.Fatalf("got %#v", msg)
	}
	life.Stop(wall)
	if got := result(t, run(ev.Timer(time.Millisecond, 8))); got != nil {
		t.Fatalf("after the lifetime: %#v", got)
	}
}

// Review round 1, finding 3 (the reviewer's R7): a result for a waiter of
// a moved area is refused by the generation alone, its waiter still
// pending.
func TestAMovedAreaRefusesItsWaiterByGeneration(t *testing.T) {
	ns := confirmed(t)
	s, store := NewScope(ns), cache.NewStore()
	left := s.Begin(Detail, detailKey(ns, website, login, true), queue.Background)
	if moved := s.Move(Detail); len(moved) != 1 || moved[0] != left.Waiter {
		t.Fatalf("moved %v", moved)
	}
	r := TicketResult{Ticket: left, Answer: transport.Answer{Class: transport.Object, Payload: []byte(`{"id":"x"}`), Status: 200, Sent: true}, Flight: 1}
	if d, ok := s.Receive(store, r, now); ok {
		t.Fatalf("a result for a left selection was accepted: %v", d)
	}
	if _, ok := store.Get(left.Key); ok {
		t.Fatal("the late answer was filed")
	}
}
