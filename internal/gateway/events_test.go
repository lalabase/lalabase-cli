package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/cache"
	"github.com/gboyke/lalabase-cli/internal/queue"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/worker"
)

// The mailbox keeps the latest end of a rate-limit window and wakes the
// model once per change; a window that ends earlier changes nothing.
func TestTheMailboxKeepsTheLatestPauseAndWakesOnce(t *testing.T) {
	life := worker.New(context.Background())
	defer life.Stop(wall)
	ev := NewEvents(life)
	if ev.Session() == 0 || ev.Session() == NewEvents(life).Session() {
		t.Fatal("sessions are not unique")
	}
	later := now.Add(time.Minute)
	ev.RateLimit(later)
	ev.RateLimit(now.Add(time.Second)) // earlier: ignored
	if st := ev.State(); !st.Pause.Equal(later) || st.Revoked {
		t.Fatalf("state %+v", st)
	}
	if msg := result(t, run(ev.Wait())); msg != (Wake{Session: ev.Session()}) {
		t.Fatalf("got %#v", msg)
	}
	// One wake-up for two changes made before the model asked again.
	ev.RateLimit(later.Add(time.Minute))
	ev.RateLimit(later.Add(2 * time.Minute))
	if msg := result(t, run(ev.Wait())); msg != (Wake{Session: ev.Session()}) {
		t.Fatalf("got %#v", msg)
	}
	waiting := run(ev.Wait())
	select {
	case msg := <-waiting:
		t.Fatalf("woke without a change: %#v", msg)
	case <-time.After(50 * time.Millisecond):
	}
	ev.Revoke()
	if msg := result(t, waiting); msg != (Wake{Session: ev.Session()}) {
		t.Fatalf("got %#v", msg)
	}
	if st := ev.State(); !st.Revoked {
		t.Fatal("the revocation is not durable")
	}
	ev.Revoke() // a second one is no second wake-up
	waiting = run(ev.Wait())
	select {
	case msg := <-waiting:
		t.Fatalf("woke again: %#v", msg)
	case <-time.After(50 * time.Millisecond):
	}
	// Ending the lifetime ends the wait without a message.
	life.Stop(wall)
	if msg := result(t, waiting); msg != nil {
		t.Fatalf("after the end: %#v", msg)
	}
	if msg := result(t, run(ev.Wait())); msg != nil {
		t.Fatalf("a wait after the end: %#v", msg)
	}
}

// Observe records a 401 whatever became of the answer: the class, or the
// status of an answer that broke off. Anything else is no revocation.
func TestObserveRecordsEvery401AndNothingElse(t *testing.T) {
	for _, tc := range []struct {
		ans     transport.Answer
		revoked bool
	}{
		{transport.Answer{Class: transport.Unauthenticated, Status: 401}, true},
		{transport.Answer{Class: transport.Canceled, Status: 401}, true},
		{transport.Answer{Class: transport.Temporary, Status: 401}, true},
		{transport.Answer{Class: transport.Unauthenticated}, true},
		{transport.Answer{Class: transport.NoAccess, Status: 403}, false},
		{transport.Answer{Class: transport.Temporary, Status: 429}, false},
		{transport.Answer{Class: transport.Object, Status: 200}, false},
		{transport.Answer{Class: transport.Canceled}, false},
	} {
		life := worker.New(context.Background())
		ev := NewEvents(life)
		run := ev.Observe(func(context.Context) transport.Answer { return tc.ans })
		if got := run(context.Background()); got.Class != tc.ans.Class || got.Status != tc.ans.Status {
			t.Fatalf("the wrapper changed the answer: %+v", got)
		}
		if ev.State().Revoked != tc.revoked {
			t.Errorf("%v/%d: revoked %v", tc.ans.Class, tc.ans.Status, ev.State().Revoked)
		}
		life.Stop(wall)
	}
	var nilEvents *Events
	if nilEvents.Observe(nil) != nil {
		t.Fatal("no events, no wrapper")
	}
}

// The expiry of a pause wakes the model with the deadline it was issued
// for, so the model clears only a notice that still shows that deadline.
func TestTheExpiryNamesItsDeadline(t *testing.T) {
	life := worker.New(context.Background())
	defer life.Stop(wall)
	ev := NewEvents(life)
	until := time.Now().Add(20 * time.Millisecond)
	if msg := result(t, run(ev.Expiry(until, time.Now()))); msg != (Expired{Session: ev.Session(), Until: until}) {
		t.Fatalf("got %#v", msg)
	}
	far := time.Now().Add(time.Hour)
	waiting := run(ev.Expiry(far, time.Now()))
	life.Stop(wall)
	if msg := result(t, waiting); msg != nil {
		t.Fatalf("after the end: %#v", msg)
	}
}

// server401 answers every request with 401 once released.
func server401(t *testing.T) *server {
	t.Helper()
	s := &server{release: make(chan struct{}), aborted: make(chan struct{}, 4)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.requests.Add(1)
		select {
		case <-s.release:
		case <-r.Context().Done():
			s.aborted <- struct{}{}
			return
		case <-time.After(wall):
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	t.Cleanup(srv.Close)
	c, err := transport.New(srv.URL, "lalab_pat_synthetic")
	if err != nil {
		t.Fatal(err)
	}
	s.api = api.New(c)
	return s
}

// C1b-2 prerequisite (the owner's rule): a 401 the server really sent in
// this session revokes it, also when the selection that asked has moved on
// (Receive refuses the result, rightly: it is old data) and when the waiter
// that shared the request had been canceled before the answer came. The
// strict acceptance of ordinary data stays as it is; the revocation is
// observed on the request itself, by the gateway, not read off an accepted
// result.
func TestA401FromAnAbandonedOrCanceledRequestStillRevokesTheSession(t *testing.T) {
	life := worker.New(context.Background())
	t.Cleanup(func() { life.Stop(wall) })
	q := queue.New(queue.WithLifetime(life))
	srv := server401(t)
	ns := confirmed(t)
	ev := NewEvents(life)
	gw := New(life, q, srv.api, WithEvents(ev))
	scope, store := NewScope(ns), cache.NewStore()
	key := unreadPage(ns, "org-a", 1)

	canceledCtx, cancel := context.WithCancel(context.Background())
	first := scope.Begin(Inbox, key, queue.User)
	second := scope.Begin(Inbox, key, queue.User)
	r1 := run(gw.Inbox(canceledCtx, first))
	waitFor(t, "the request", func() bool { return srv.requests.Load() == 1 })
	r2 := run(gw.Inbox(context.Background(), second))
	waitFor(t, "the second waiter joining", func() bool { return q.Sharers(second.ShareKey()) == 2 })
	cancel() // the first waiter gives up; the request runs on for the second
	if a, _ := result(t, r1).(InboxResult); a.Answer.Class != transport.Canceled {
		t.Fatalf("the canceled waiter got %v", a.Answer.Class)
	}
	// The selection moves on: the second waiter is abandoned, not canceled
	// (the first stays pending too, until its canceled result names it).
	if moved := scope.Move(Inbox); !slices.Contains(moved, second.Waiter) {
		t.Fatalf("moved %v", moved)
	}
	close(srv.release)
	b, _ := result(t, r2).(InboxResult)
	if b.Answer.Class != transport.Unauthenticated {
		t.Fatalf("the abandoned waiter got %v", b.Answer.Class)
	}
	if d, ok := scope.Receive(store, b, now); ok || d != cache.Drop {
		t.Fatalf("an old result was accepted: %v %v", d, ok)
	}
	if next := scope.Begin(Inbox, key, queue.User); next.Gens.Session != second.Gens.Session {
		t.Fatal("Receive ended the session for an old result; acceptance must stay strict")
	}
	if !ev.State().Revoked {
		t.Fatal("the 401 the server sent did not revoke the session")
	}
	if msg := result(t, run(ev.Wait())); msg != (Wake{Session: ev.Session()}) {
		t.Fatalf("no wake-up: %#v", msg)
	}
}

// A gateway without events observes nothing and still works (C1b-1 tests).
func TestAGatewayWithoutEventsStillDelivers(t *testing.T) {
	f := setup(t, "ok")
	key := unreadPage(f.ns, "", 1)
	ticket := f.scope.Begin(Inbox, key, queue.User)
	res := run(f.gw.Inbox(context.Background(), ticket))
	close(f.srv.release)
	if r, _ := result(t, res).(InboxResult); !r.Answer.OK() {
		t.Fatal(r.Answer.Class)
	}
}

var _ atomic.Int32
