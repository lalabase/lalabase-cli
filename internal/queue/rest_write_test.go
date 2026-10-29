package queue

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gboyke/lalabase-cli/internal/transport"
)

// K0: a REST write (transport.Client.Write) runs through the queue like an
// MCP write. Its answer, not a stand-in, decides the retry: a write whose
// outcome is unclear is never sent again; one the server cannot have
// acted on (a 429) is sent once more, with the same key and the same
// bytes.
func TestARestWriteThroughTheQueue(t *testing.T) {
	body := json.RawMessage(`{"post":{"body":"Hallo"}}`)
	const key = "key-queue-0001"
	cases := []struct {
		name     string
		answer   func(w http.ResponseWriter, n int)
		requests int
		class    transport.Class
	}{
		{"the answer is lost after sending", func(w http.ResponseWriter, _ int) {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
		}, 1, transport.Temporary},
		{"the server is overloaded", func(w http.ResponseWriter, _ int) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}, 1, transport.Temporary},
		{"a refusal with its code", func(w http.ResponseWriter, _ int) {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":"stale_object","code":"refused","reason":"stale_object"}`))
		}, 1, transport.Refused},
		{"a 429, then the object", func(w http.ResponseWriter, n int) {
			if n == 1 {
				w.Header().Set("Retry-After", "30")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"data":{"id":"p1"}}`))
		}, 2, transport.Object},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var sent []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				mu.Lock()
				sent = append(sent, r.Method+" "+r.Header.Get("Idempotency-Key")+" "+string(b))
				n := len(sent)
				mu.Unlock()
				tc.answer(w, n)
			}))
			defer srv.Close()
			clock := newClock()
			c, err := transport.New(srv.URL, "lalab_pat_test", transport.WithClock(clock.Now))
			if err != nil {
				t.Fatal(err)
			}
			q := New(WithClock(clock), WithRetryDelay(0))
			r := do(q, context.Background(), Job{Priority: User, Kind: Write, Run: func(ctx context.Context) transport.Answer {
				return c.Write(ctx, http.MethodPost, "/api/v1/tickets/x/posts", body, key)
			}})
			if tc.requests == 2 {
				waitPaused(t, q, clock.Now().Add(30*time.Second))
				clock.Advance(30 * time.Second)
			}
			got := answer(t, r)
			if got.Class != tc.class || !got.Sent {
				t.Fatalf("got %v sent=%v", got.Class, got.Sent)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(sent) != tc.requests {
				t.Fatalf("%d requests, want %d: %q", len(sent), tc.requests, sent)
			}
			for _, s := range sent {
				if s != "POST "+key+" "+string(body) {
					t.Fatalf("sent %q", s)
				}
			}
		})
	}
}
