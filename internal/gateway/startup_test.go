package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/cache"
	"github.com/gboyke/lalabase-cli/internal/contract"
	"github.com/gboyke/lalabase-cli/internal/queue"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/worker"
)

// startupServer replays the recorded startup answers of the contract: /me,
// /organisations, initialize and tools/list. Every request is logged by
// its path and method; failures can be injected per method, by count.
type startupServer struct {
	t        *testing.T
	file     *contract.File
	mu       atomic.Int32
	log      []string
	failures map[string]int // method or path → remaining failures (503)
	client   *transport.Client
	api      *api.API
}

func newStartupServer(t *testing.T) *startupServer {
	t.Helper()
	file, err := contract.Load()
	if err != nil {
		t.Fatal(err)
	}
	s := &startupServer{t: t, file: file, failures: map[string]int{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Path
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if r.Method == http.MethodPost {
			_ = json.NewDecoder(r.Body).Decode(&req)
			name = req.Method
		}
		s.mu.Add(1)
		s.log = append(s.log, name)
		if s.failures[name] > 0 {
			s.failures[name]--
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		var body []byte
		switch name {
		case "/api/v1/me":
			body = file.Contract.REST["GET /api/v1/me"]["ok"].Response.Body
		case "/api/v1/organisations":
			body = file.Contract.REST["GET /api/v1/organisations"]["ok"].Response.Body
		case "initialize", "tools/list":
			ex := file.Contract.MCP.Initialize
			if name == "tools/list" {
				ex = file.Contract.MCP.ToolsList
			}
			var msg map[string]any
			_ = json.Unmarshal(ex.Response.Body, &msg)
			msg["id"] = req.ID
			body, _ = json.Marshal(msg)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not_found"}`))
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	s.client, err = transport.New(srv.URL, "lalab_pat_synthetic")
	if err != nil {
		t.Fatal(err)
	}
	s.api = api.New(s.client)
	return s
}

// recordingDoer runs jobs through a real queue and keeps what it was given.
type recordingDoer struct {
	q    *queue.Queue
	jobs []queue.Job
}

func (d *recordingDoer) DoShared(ctx context.Context, job queue.Job) (transport.Answer, queue.Flight) {
	d.jobs = append(d.jobs, job)
	return d.q.DoShared(ctx, job)
}

// Every startup command is one unshared read of the person's priority, and
// its result carries the ticket it was issued for and the typed value.
func TestStartupCommandsAreUnsharedAndTyped(t *testing.T) {
	life := worker.New(context.Background())
	defer life.Stop(wall)
	srv := newStartupServer(t)
	doer := &recordingDoer{q: queue.New(queue.WithLifetime(life))}
	st := NewStartup(life, doer, srv.client, srv.api, NewEvents(life))
	session := NewSession()
	for i, step := range []Step{StepMe, StepInitialize, StepTools, StepOrganisations} {
		ticket := StartupTicket{Session: session, Step: step, Attempt: uint64(i + 1)}
		r, ok := result(t, run(st.Run(context.Background(), ticket))).(StartupResult)
		if !ok || r.StartupTicket != ticket || !r.Answer.OK() {
			t.Fatalf("%v: %#v", step, r)
		}
		switch step {
		case StepMe:
			if r.Me.User.DisplayName != "Anna Albers" || r.Me.Token.ID == "" || r.Me.Token.ReadOnly == nil {
				t.Fatalf("me: %+v", r.Me)
			}
		case StepInitialize:
			if r.Server.Name != "lalabase" || r.Server.Version != "0.12.0" {
				t.Fatalf("server: %+v", r.Server)
			}
		case StepTools:
			if len(r.Tools) != 15 {
				t.Fatalf("%d tools", len(r.Tools))
			}
		case StepOrganisations:
			if len(r.Organisations) != 1 || r.Organisations[0].Name != "Nordlicht GmbH" {
				t.Fatalf("organisations: %+v", r.Organisations)
			}
		}
		job := doer.jobs[i]
		if job.Key != "" || job.Kind != queue.Read || job.Priority != queue.User {
			t.Fatalf("%v: job %+v is shared or not the person's", step, job)
		}
	}
	if got := strings.Join(srv.log, " "); got != "/api/v1/me initialize tools/list /api/v1/organisations" {
		t.Fatalf("requests %q", got)
	}
}

// A retry assigns the value again; the result publishes it only when the
// last attempt succeeded. A step whose attempts all fail carries no data.
func TestAStartupResultPublishesOnlyAFinalSuccess(t *testing.T) {
	life := worker.New(context.Background())
	defer life.Stop(wall)
	srv := newStartupServer(t)
	q := queue.New(queue.WithLifetime(life), queue.WithRetryDelay(0))
	st := NewStartup(life, q, srv.client, srv.api, nil)
	session := NewSession()
	srv.failures["/api/v1/organisations"] = 1 // the first attempt fails, the retry succeeds
	r, _ := result(t, run(st.Run(context.Background(), StartupTicket{Session: session, Step: StepOrganisations, Attempt: 1}))).(StartupResult)
	if !r.Answer.OK() || len(r.Organisations) != 1 {
		t.Fatalf("after a retry: %v %+v", r.Answer.Class, r.Organisations)
	}
	srv.failures["/api/v1/me"] = 2 // both attempts fail
	r, _ = result(t, run(st.Run(context.Background(), StartupTicket{Session: session, Step: StepMe, Attempt: 1}))).(StartupResult)
	if r.Answer.Class != transport.Temporary || r.Me.Token.ID != "" || r.Me.User.ID != "" {
		t.Fatalf("after two failures: %v %+v", r.Answer.Class, r.Me)
	}
	if n := strings.Count(strings.Join(srv.log, " "), "/api/v1/me"); n != 2 {
		t.Fatalf("%d attempts of /me", n)
	}
}

// A command that comes after Stop is a canceled result with its ticket, not
// a fatal error; a panic in the identity reader is fatal.
func TestAStartupCommandTellsCancellationFromAFatalError(t *testing.T) {
	stopped := worker.New(context.Background())
	stopped.Stop(wall)
	srv := newStartupServer(t)
	st := NewStartup(stopped, queue.New(), srv.client, srv.api, nil)
	ticket := StartupTicket{Session: NewSession(), Step: StepMe, Attempt: 3}
	r, ok := result(t, run(st.Run(context.Background(), ticket))).(StartupResult)
	if !ok || r.StartupTicket != ticket || r.Answer.Class != transport.Canceled || r.Me.User.ID != "" {
		t.Fatalf("after Stop: %#v", r)
	}
	if stopped.Err() != nil {
		t.Fatalf("recorded %v", stopped.Err())
	}

	life := worker.New(context.Background())
	defer life.Stop(wall)
	st = NewStartup(life, queue.New(queue.WithLifetime(life)), srv.client, panicIdentity{}, nil)
	msg := result(t, run(st.Run(context.Background(), StartupTicket{Session: NewSession(), Step: StepMe, Attempt: 1})))
	if _, ok := msg.(worker.Fatal); !ok {
		t.Fatalf("got %#v", msg)
	}
	if life.Err() == nil || !strings.Contains(life.Err().Error(), "gateway: startup") {
		t.Fatalf("recorded %v", life.Err())
	}
}

type panicIdentity struct{}

func (panicIdentity) Me(context.Context) (api.Me, transport.Answer) { panic("identity fault") }
func (panicIdentity) Organisations(context.Context) ([]api.Ref, transport.Answer) {
	panic("identity fault")
}

// A 401 on a startup request is observed like one on an inbox request.
func TestA401OnStartupIsObserved(t *testing.T) {
	life := worker.New(context.Background())
	defer life.Stop(wall)
	srv := server401(t)
	close(srv.release)
	ev := NewEvents(life)
	c, _ := transport.New("http://127.0.0.1:1", "lalab_pat_synthetic")
	st := NewStartup(life, queue.New(queue.WithLifetime(life)), c, srv.api, ev)
	r, _ := result(t, run(st.Run(context.Background(), StartupTicket{Session: NewSession(), Step: StepMe, Attempt: 1}))).(StartupResult)
	if r.Answer.Class != transport.Unauthenticated || !ev.State().Revoked {
		t.Fatalf("%v, revoked %v", r.Answer.Class, ev.State().Revoked)
	}
}

// The inbox request is checked before it is sent and its page before it
// reaches the model: the status, the page and its size, the project filter,
// the area, and the consistency of the answer with the question.
func TestInboxTicketsAndPagesAreChecked(t *testing.T) {
	ns := confirmed(t)
	good := unreadPage(ns, "org-a", 2)
	for name, edit := range map[string]func(*Ticket){
		"read status":   func(t *Ticket) { t.Key.Status = "read" },
		"page zero":     func(t *Ticket) { t.Key.Page = 0 },
		"page size":     func(t *Ticket) { t.Key.PerPage = 50 },
		"project":       func(t *Ticket) { t.Key.Project = "p-1" },
		"wrong area":    func(t *Ticket) { t.Area = Organisations }, // a filtered read in the counters' area
		"no operation":  func(t *Ticket) { t.Key.Operation = "list_tickets" },
		"global and id": func(t *Ticket) { t.Key.Global = true },
	} {
		ticket := Ticket{Area: Inbox, Key: good, Priority: queue.User}
		edit(&ticket)
		if err := CheckInboxTicket(ticket); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := CheckInboxTicket(Ticket{Area: Inbox, Key: good, Priority: queue.User}); err != nil {
		t.Fatal(err)
	}
	if err := CheckInboxTicket(Ticket{Area: Organisations, Key: unreadPage(ns, "", 1), Priority: queue.User}); err != nil {
		t.Fatalf("the global counter read: %v", err)
	}

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
	_ = json.Unmarshal(file.Contract.MCP.Calls["list_notifications"]["by_organisation"].Response.Body, &body)
	page, ans := api.DecodeNotifications(transport.Answer{Class: transport.Object, Payload: []byte(body.Result.Content[0].Text), Status: 200, Sent: true})
	if !ans.OK() {
		t.Fatal(ans.Message)
	}
	org := page.Data[0].Organisation.ID
	filtered := unreadPage(ns, org, 1)
	if err := CheckInboxPage(filtered, page); err != nil {
		t.Fatalf("the recorded page was refused: %v", err)
	}
	for name, tc := range map[string]struct {
		key  cache.Key
		edit func(*api.NotificationPage)
	}{
		"another page":    {unreadPage(ns, org, 2), func(*api.NotificationPage) {}},
		"another size":    {filtered, func(p *api.NotificationPage) { p.Meta.PerPage = 50 }},
		"foreign row":     {filtered, func(p *api.NotificationPage) { p.Data[1].Organisation.ID = "org-other" }},
		"foreign count":   {filtered, func(p *api.NotificationPage) { p.Meta.ByOrganisation[0].ID = "org-other" }},
		"page size zero":  {filtered, func(p *api.NotificationPage) { p.Meta.PerPage = 0 }},
		"row id twice":    {filtered, func(p *api.NotificationPage) { p.Data[1].ID = p.Data[0].ID }},
		"global with org": {unreadPage(ns, "", 1), func(p *api.NotificationPage) {}},
	} {
		copied := page
		copied.Data = append([]api.Notification(nil), page.Data...)
		copied.Meta.ByOrganisation = append([]api.OrganisationCount(nil), page.Meta.ByOrganisation...)
		tc.edit(&copied)
		if name == "global with org" {
			// A global page carries rows of any organisation: fine.
			if err := CheckInboxPage(tc.key, copied); err != nil {
				t.Errorf("%s: %v", name, err)
			}
			continue
		}
		if err := CheckInboxPage(tc.key, copied); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// The gateway refuses a page that contradicts its question before the
// model sees it: the answer becomes a protocol error with an empty page.
func TestTheGatewayRefusesAPageThatContradictsItsQuestion(t *testing.T) {
	f := setup(t, "ok")            // page 1 of 25, one row of org ...0001
	key := unreadPage(f.ns, "", 2) // asked for page 2
	ticket := f.scope.Begin(Inbox, key, queue.User)
	res := run(f.gw.Inbox(context.Background(), ticket))
	close(f.srv.release)
	r, _ := result(t, res).(InboxResult)
	if r.Answer.Class != transport.Protocol || len(r.Page.Data) != 0 || !strings.Contains(r.Answer.Message, "page") {
		t.Fatalf("%v %q %d rows", r.Answer.Class, r.Answer.Message, len(r.Page.Data))
	}
	if !r.Answer.Sent || r.Answer.Status != 200 {
		t.Fatalf("the transport's knowledge was lost: %+v", r.Answer)
	}
}

var _ = cache.NewStore
