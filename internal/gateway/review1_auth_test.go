package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/queue"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/worker"
)

// Real HTTP response has a 401 header and a truncated body. Message delivery
// may lag behind a worker; the queue must never retry an observed 401.
func TestReviewBrokenBody401MustNotRetry(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":`))
	}))
	defer srv.Close()
	client, err := transport.New(srv.URL, "lalabase-inbox-fixture-only")
	if err != nil {
		t.Fatal(err)
	}
	life := worker.New(context.Background())
	defer life.Stop(time.Second)
	events := NewEvents(life)
	q := queue.New(queue.WithLifetime(life))
	startup := NewStartup(life, q, client, api.New(client), events)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r := startup.Run(ctx, StartupTicket{Session: events.Session(), Step: StepMe, Attempt: 1})().(StartupResult)
	if !events.State().Revoked {
		t.Fatal("401 not recorded")
	}
	if n := requests.Load(); n != 1 {
		t.Fatalf("server saw %d /me requests after broken-body 401; result class=%v status=%d revoked=%v", n, r.Answer.Class, r.Answer.Status, events.State().Revoked)
	}
}

func TestReviewMailboxFullStillDeliversRevocation(t *testing.T) {
	life := worker.New(context.Background())
	defer life.Stop(time.Second)
	events := NewEvents(life)
	events.RateLimit(time.Now().Add(time.Minute)) // fill wake before Revoke
	events.Revoke()
	if msg := events.Wait()(); msg != (Wake{Session: events.Session()}) {
		t.Fatalf("wake=%#v", msg)
	}
	if !events.State().Revoked {
		t.Fatal("revocation lost behind pending pause")
	}
}

func TestReviewOversized401StillRevokes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer srv.Close()
	client, err := transport.New(srv.URL, "lalabase-inbox-fixture-only", transport.WithMaxBody(8))
	if err != nil {
		t.Fatal(err)
	}
	life := worker.New(context.Background())
	defer life.Stop(time.Second)
	events := NewEvents(life)
	startup := NewStartup(life, queue.New(queue.WithLifetime(life)), client, api.New(client), events)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	r := startup.Run(ctx, StartupTicket{Session: events.Session(), Step: StepMe, Attempt: 1})().(StartupResult)
	if r.Answer.Class != transport.Protocol || r.Answer.Status != 401 {
		t.Fatalf("expected oversized401, got %+v", r.Answer)
	}
	if !events.State().Revoked {
		t.Fatalf("oversized HTTP401 did not revoke: class=%v status=%d", r.Answer.Class, r.Answer.Status)
	}
}
