package queue

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/worker"
)

func TestReviewQueueBrokenBody401MustNotRetry(t *testing.T) {
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
	q := New(WithLifetime(life))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r := q.Do(ctx, Job{Priority: User, Kind: Read, Run: func(ctx context.Context) transport.Answer { return client.Get(ctx, "/api/v1/me") }})
	if r.Status != 401 {
		t.Fatalf("expected 401, got %+v", r)
	}
	if n := requests.Load(); n != 1 {
		t.Fatalf("server saw %d requests after broken-body 401; result class=%v status=%d", n, r.Class, r.Status)
	}
}
