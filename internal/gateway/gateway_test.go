package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/cache"
	"github.com/gboyke/lalabase-cli/internal/contract"
	"github.com/gboyke/lalabase-cli/internal/queue"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/worker"
)

// wall is the real deadline of every test with goroutines: a hang fails.
const wall = 3 * time.Second

// server replays the recorded inbox answer. Every request waits for release
// and reports when its client went away.
type server struct {
	requests atomic.Int32
	release  chan struct{}
	aborted  chan struct{}
	api      *api.API
}

func replay(t *testing.T, name string) *server {
	t.Helper()
	file, err := contract.Load()
	if err != nil {
		t.Fatal(err)
	}
	ex, ok := file.Contract.MCP.Calls["list_notifications"][name]
	if !ok {
		t.Fatalf("no list_notifications/%s", name)
	}
	s := &server{release: make(chan struct{}), aborted: make(chan struct{}, 4)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.requests.Add(1)
		var req struct {
			ID json.RawMessage `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		select {
		case <-s.release:
		case <-r.Context().Done():
			s.aborted <- struct{}{}
			return
		case <-time.After(wall):
			return
		}
		var body map[string]any
		_ = json.Unmarshal(ex.Response.Body, &body)
		body["id"] = req.ID
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

func confirmed(t *testing.T) cache.Namespace {
	t.Helper()
	ns, ok := cache.Confirmed("default", "http://127.0.0.1:1", "token-synthetic-1")
	if !ok {
		t.Fatal("no namespace")
	}
	return ns
}

func unreadPage(ns cache.Namespace, org string, page int) cache.Key {
	k := ns.Key("list_notifications")
	if org == "" {
		k.Global = true
	} else {
		k.Organisation = org
	}
	k.Status, k.Page, k.PerPage = "unread", page, 25
	return k
}

// run runs a command on its own goroutine, as Bubble Tea does.
func run(cmd tea.Cmd) <-chan tea.Msg {
	out := make(chan tea.Msg, 1)
	go func() { out <- cmd() }()
	return out
}

func result(t *testing.T, ch <-chan tea.Msg) tea.Msg {
	t.Helper()
	select {
	case msg := <-ch:
		return msg
	case <-time.After(wall):
		t.Fatal("no message")
	}
	return nil
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(wall)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("%s did not happen", what)
		}
		time.Sleep(time.Millisecond)
	}
}

type fixture struct {
	life  *worker.Supervisor
	queue *queue.Queue
	srv   *server
	gw    *Gateway
	scope *Scope
	ns    cache.Namespace
}

func setup(t *testing.T, name string) *fixture {
	t.Helper()
	life := worker.New(context.Background())
	t.Cleanup(func() { life.Stop(wall) })
	q := queue.New(queue.WithLifetime(life))
	srv := replay(t, name)
	ns := confirmed(t)
	return &fixture{life: life, queue: q, srv: srv, gw: New(life, q, srv.api), scope: NewScope(ns), ns: ns}
}

// The preview and the open of one inbox page share one HTTP request, and each
// gets the whole page, decoded for itself, under its own waiter.
func TestPreviewAndOpenGetFilledResultsFromOneRequest(t *testing.T) {
	f := setup(t, "ok")
	key := unreadPage(f.ns, "", 1)
	preview := f.scope.Begin(Inbox, key, queue.Background)
	open := f.scope.Begin(Inbox, key, queue.User)
	if preview.Waiter == open.Waiter || preview.ShareKey() != open.ShareKey() {
		t.Fatal("the two waiters do not share one read")
	}
	first := run(f.gw.Inbox(context.Background(), preview))
	waitFor(t, "the request", func() bool { return f.srv.requests.Load() == 1 })
	second := run(f.gw.Inbox(context.Background(), open))
	waitFor(t, "the second waiter joining", func() bool { return f.queue.Sharers(open.ShareKey()) == 2 })
	close(f.srv.release)

	a, ok1 := result(t, first).(InboxResult)
	b, ok2 := result(t, second).(InboxResult)
	if !ok1 || !ok2 || !a.Answer.OK() || !b.Answer.OK() {
		t.Fatalf("results %+v / %+v", a, b)
	}
	if a.Waiter != preview.Waiter || b.Waiter != open.Waiter || a.Ticket != preview || b.Ticket != open {
		t.Fatal("a result carries another waiter's ticket")
	}
	if len(a.Page.Data) != 1 || a.Page.Data[0].Ticket == nil || a.Page.Meta.UnreadTotal != 1 || !reflect.DeepEqual(a.Page, b.Page) {
		t.Fatalf("pages differ or are empty: %+v / %+v", a.Page, b.Page)
	}
	if n := f.srv.requests.Load(); n != 1 {
		t.Fatalf("%d HTTP requests", n)
	}
	a.Page.Data[0].Message = "changed"
	a.Answer.Payload[0] = 'X'
	if b.Page.Data[0].Message == "changed" || b.Answer.Payload[0] == 'X' {
		t.Fatal("the waiters share data")
	}
	store := cache.NewStore()
	for _, r := range []InboxResult{a, b} {
		if d, ok := f.scope.Receive(store, r, time.Now()); !ok || d != cache.Replace {
			t.Fatalf("waiter %d: %v %v", r.Waiter, d, ok)
		}
	}
}

// The first waiter leaving costs the other nothing; the last one leaving
// aborts the HTTP request.
func TestLeavingWaitersCancelOnlyAsTheLast(t *testing.T) {
	f := setup(t, "ok")
	key := unreadPage(f.ns, "", 1)
	preview, open := f.scope.Begin(Inbox, key, queue.Background), f.scope.Begin(Inbox, key, queue.User)
	ctx1, cancel1 := context.WithCancel(context.Background())
	first := run(f.gw.Inbox(ctx1, preview))
	waitFor(t, "the request", func() bool { return f.srv.requests.Load() == 1 })
	second := run(f.gw.Inbox(context.Background(), open))
	waitFor(t, "the second waiter joining", func() bool { return f.queue.Sharers(open.ShareKey()) == 2 })
	cancel1()
	if r := result(t, first).(InboxResult); r.Answer.Class != transport.Canceled || !reflect.DeepEqual(r.Page, api.NotificationPage{}) {
		t.Fatalf("the waiter that left got %v", r.Answer.Class)
	}
	close(f.srv.release)
	if r := result(t, second).(InboxResult); !r.Answer.OK() || len(r.Page.Data) != 1 {
		t.Fatalf("the remaining waiter got %v", r.Answer.Class)
	}

	g := setup(t, "ok")
	only := g.scope.Begin(Inbox, unreadPage(g.ns, "org-1", 1), queue.User)
	ctx2, cancel2 := context.WithCancel(context.Background())
	last := run(g.gw.Inbox(ctx2, only))
	waitFor(t, "the request", func() bool { return g.srv.requests.Load() == 1 })
	cancel2()
	result(t, last)
	select {
	case <-g.srv.aborted:
	case <-time.After(wall):
		t.Fatal("the last waiter leaving did not abort the request")
	}
}

type panicDoer struct{}

func (panicDoer) Do(context.Context, queue.Job) transport.Answer { panic("gateway fault") }

// A panic in a command is recovered there and becomes the fatal error and
// its message; nothing of the panic is passed on.
func TestAPanicInACommandBecomesTheFatalMessage(t *testing.T) {
	life := worker.New(context.Background())
	defer life.Stop(wall)
	ns := confirmed(t)
	gw := New(life, panicDoer{}, nil)
	msg := result(t, run(gw.Inbox(context.Background(), NewScope(ns).Begin(Inbox, unreadPage(ns, "", 1), queue.User))))
	if _, ok := msg.(worker.Fatal); !ok {
		t.Fatalf("got %#v", msg)
	}
	var fatal *worker.FatalError
	if !errors.As(life.Err(), &fatal) || fatal.Where != "gateway: inbox" {
		t.Fatalf("recorded %v", life.Err())
	}
}
