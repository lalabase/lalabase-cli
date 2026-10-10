package gateway

import (
	"testing"
	"time"

	"github.com/gboyke/lalabase-cli/internal/cache"
	"github.com/gboyke/lalabase-cli/internal/queue"
	"github.com/gboyke/lalabase-cli/internal/transport"
)

// C1e: an entry read from disk passes the scope's checks like a network
// result: a late one (the selection moved on, the resource was invalidated,
// the session ended) files nothing, and one for a key a network answer
// already filled files nothing over it.
func TestPromotionPassesTheScopesChecks(t *testing.T) {
	ns, _ := cache.Confirmed("default", "https://a.example", "tok")
	key := ns.Key("list_notifications")
	key.Organisation, key.Status, key.Page, key.PerPage = "org-1", "unread", 1, 25
	entry := cache.Entry{Payload: []byte(`{"data":[]}`), LoadedAt: time.Date(2026, 10, 10, 11, 0, 0, 0, time.UTC), Stale: true}
	now := entry.LoadedAt.Add(time.Hour)

	// Accepted: the entry as it was, the ticket used up.
	s, store := NewScope(ns), cache.NewStore()
	tk := s.Begin(Inbox, key, queue.Background)
	if !s.Promote(store, tk, entry) {
		t.Fatal("not promoted")
	}
	if e, ok := store.Get(key); !ok || !e.LoadedAt.Equal(entry.LoadedAt) || !e.Stale {
		t.Fatalf("filed as %+v %v", e, ok)
	}
	if s.Promote(store, tk, entry) {
		t.Fatal("a ticket was accepted twice")
	}
	if len(s.pending) != 0 {
		t.Fatal("the ticket stayed pending")
	}

	// Refused after the area moved on, after an invalidation of the
	// resource, after the session ended, and for a ticket of another scope.
	for name, prepare := range map[string]func(s *Scope, store *cache.Store, tk Ticket) Ticket{
		"moved":       func(s *Scope, _ *cache.Store, tk Ticket) Ticket { s.Move(Inbox); return tk },
		"invalidated": func(s *Scope, _ *cache.Store, tk Ticket) Ticket { s.Invalidate(key); return tk },
		"ended":       func(s *Scope, store *cache.Store, tk Ticket) Ticket { s.EndSession(store); return tk },
		"unknown":     func(_ *Scope, _ *cache.Store, tk Ticket) Ticket { tk.Waiter++; return tk },
		"other scope": func(_ *Scope, _ *cache.Store, _ Ticket) Ticket {
			return NewScope(ns).Begin(Inbox, key, queue.Background)
		},
	} {
		s, store := NewScope(ns), cache.NewStore()
		tk := prepare(s, store, s.Begin(Inbox, key, queue.Background))
		if s.Promote(store, tk, entry) {
			t.Errorf("%s: promoted", name)
		}
		if _, ok := store.Get(key); ok {
			t.Errorf("%s: filed", name)
		}
	}

	// A network answer that landed since wins: the disk entry is not filed
	// over it, and the load time stays the network's.
	s, store = NewScope(ns), cache.NewStore()
	tk = s.Begin(Inbox, key, queue.Background)
	network := s.Begin(Inbox, key, queue.User)
	if _, ok := s.Receive(store, Result{Ticket: network, Answer: transport.Answer{Class: transport.Object, Payload: []byte(`{"data":[1]}`), Status: 200, Sent: true}, Flight: 1}, now); !ok {
		t.Fatal("the network result was refused")
	}
	if s.Promote(store, tk, entry) {
		t.Fatal("promoted over a network answer")
	}
	if e, _ := store.Get(key); !e.LoadedAt.Equal(now) || string(e.Payload) != `{"data":[1]}` {
		t.Fatalf("the network entry changed: %+v", e)
	}
}
