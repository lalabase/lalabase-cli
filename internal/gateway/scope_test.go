package gateway

import (
	"fmt"
	"testing"
	"time"

	"github.com/gboyke/lalabase-cli/internal/cache"
	"github.com/gboyke/lalabase-cli/internal/queue"
	"github.com/gboyke/lalabase-cli/internal/transport"
)

var now = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

func object(t Ticket, payload string) InboxResult {
	return InboxResult{Ticket: t, Answer: transport.Answer{Class: transport.Object, Payload: []byte(payload), Sent: true, Status: 200}}
}

// refused fails unless the result is dropped and the cache keeps exactly
// what it had.
func refused(t *testing.T, s *Scope, store *cache.Store, r InboxResult, why string) {
	t.Helper()
	before, bytes := store.Len(), store.Bytes()
	if d, ok := s.Receive(store, r, now); ok || d != cache.Drop {
		t.Fatalf("%s: accepted (%v)", why, d)
	}
	if store.Len() != before || store.Bytes() != bytes {
		t.Fatalf("%s: the cache changed", why)
	}
	if _, ok := store.Get(r.Key); ok && before == 0 {
		t.Fatalf("%s: filed", why)
	}
}

func accepted(t *testing.T, s *Scope, store *cache.Store, r InboxResult) {
	t.Helper()
	if _, ok := s.Receive(store, r, now); !ok {
		t.Fatal("a current result was dropped")
	}
	if e, ok := store.Get(r.Key); !ok || string(e.Payload) != string(r.Answer.Payload) {
		t.Fatal("a current result was not filed")
	}
}

// Back to the first organisation: the old request's answer stays old.
func TestReturningToAnOrganisationDoesNotReviveItsOldRequest(t *testing.T) {
	ns := confirmed(t)
	s, store := NewScope(ns), cache.NewStore()
	a1 := s.Begin(Inbox, unreadPage(ns, "org-a", 1), queue.User)
	s.Move(Inbox)
	b := s.Begin(Inbox, unreadPage(ns, "org-b", 1), queue.User)
	s.Move(Inbox)
	a2 := s.Begin(Inbox, unreadPage(ns, "org-a", 1), queue.User)
	if a1.ShareKey() != a2.ShareKey() {
		t.Fatal("the same data under a new generation does not share")
	}
	refused(t, s, store, object(a1, `{"old":1}`), "A, first visit")
	refused(t, s, store, object(b, `{"b":1}`), "B")
	accepted(t, s, store, object(a2, `{"new":1}`))
}

func TestOnlyTheLastOfQuickPageSwitchesCounts(t *testing.T) {
	ns := confirmed(t)
	s, store := NewScope(ns), cache.NewStore()
	var tickets []Ticket
	for page := 1; page <= 3; page++ {
		s.Move(Inbox)
		tickets = append(tickets, s.Begin(Inbox, unreadPage(ns, "org-a", page), queue.User))
	}
	accepted(t, s, store, object(tickets[2], `{"page":3}`))
	refused(t, s, store, object(tickets[0], `{"page":1}`), "page 1")
	refused(t, s, store, object(tickets[1], `{"page":2}`), "page 2")
	// Another area moving on keeps this one's waiters.
	other := s.Begin(Organisations, func() cache.Key { k := ns.Key("organisations"); k.Global = true; return k }(), queue.User)
	keep := s.Begin(Inbox, unreadPage(ns, "org-a", 3), queue.Background)
	if dropped := s.Move(Organisations); len(dropped) != 1 || dropped[0] != other.Waiter {
		t.Fatalf("dropped %v", dropped)
	}
	accepted(t, s, store, object(keep, `{"page":3,"again":1}`))
}

func TestAWaiterThatLeftGetsNothing(t *testing.T) {
	ns := confirmed(t)
	s, store := NewScope(ns), cache.NewStore()
	t1 := s.Begin(Inbox, unreadPage(ns, "org-a", 1), queue.User)
	s.Leave(t1.Waiter)
	refused(t, s, store, object(t1, `{}`), "after leaving")
	// A waiter gets one result.
	t2 := s.Begin(Inbox, unreadPage(ns, "org-a", 1), queue.User)
	accepted(t, s, store, object(t2, `{"one":1}`))
	refused(t, s, store, object(t2, `{"two":1}`), "a second result")
}

// A result must match the ticket its waiter was given, in every field.
func TestAResultUnderAnotherIdentityIsRefused(t *testing.T) {
	ns := confirmed(t)
	other, _ := cache.Confirmed("default", "http://127.0.0.1:1", "token-synthetic-2")
	s, store := NewScope(ns), cache.NewStore()
	issued := s.Begin(Inbox, unreadPage(ns, "org-a", 1), queue.User)
	for name, edit := range map[string]func(*Ticket){
		"waiter":    func(t *Ticket) { t.Waiter++ },
		"page":      func(t *Ticket) { t.Key.Page = 2 },
		"org":       func(t *Ticket) { t.Key.Organisation = "org-b" },
		"area":      func(t *Ticket) { t.Area = Organisations },
		"namespace": func(t *Ticket) { t.Key = unreadPage(other, "org-a", 1) },
		"gen":       func(t *Ticket) { t.Gens.Area++ },
		"epoch":     func(t *Ticket) { t.Epoch++ },
		"session":   func(t *Ticket) { t.Gens.Session++ },
	} {
		forged := issued
		edit(&forged)
		refused(t, s, store, object(forged, `{}`), name)
	}
	accepted(t, s, store, object(issued, `{"real":1}`))
}

// An invalidation refuses what was asked before it, also once the cache has
// evicted the resource's entries: epochs do not live in the cache.
func TestInvalidationOutlivesEviction(t *testing.T) {
	ns := confirmed(t)
	s, store := NewScope(ns), cache.NewStore()
	key := unreadPage(ns, "org-a", 1)
	accepted(t, s, store, object(s.Begin(Inbox, key, queue.User), `{"v":1}`))
	before := s.Begin(Inbox, key, queue.User)
	s.Invalidate(key)
	for i := range cache.MaxEntries {
		k := unreadPage(ns, fmt.Sprintf("org-%d", i), 1)
		accepted(t, s, store, object(s.Begin(Inbox, k, queue.User), `{}`))
	}
	if _, ok := store.Get(key); ok {
		t.Fatal("the entry was not evicted")
	}
	refused(t, s, store, object(before, `{"stale":1}`), "begun before the invalidation")
	after := s.Begin(Inbox, unreadPage(ns, "org-a", 2), queue.User)
	if after.Epoch != before.Epoch+1 || after.ShareKey() == before.ShareKey() {
		t.Fatal("another page of the resource kept the old epoch")
	}
	accepted(t, s, store, object(after, `{"v":2}`))
}

// An ended session empties its namespace and refuses every answer asked in
// it, a 401 too.
func TestAnEndedSessionIsNotRefilled(t *testing.T) {
	ns := confirmed(t)
	s, store := NewScope(ns), cache.NewStore()
	k1, k2 := unreadPage(ns, "org-a", 1), unreadPage(ns, "org-b", 1)
	accepted(t, s, store, object(s.Begin(Inbox, k1, queue.User), `{"a":1}`))
	pending := s.Begin(Inbox, k2, queue.Background)
	unauth := s.Begin(Inbox, k1, queue.User)
	if _, ok := s.Receive(store, InboxResult{Ticket: unauth, Answer: transport.Answer{Class: transport.Unauthenticated, Status: 401}}, now); !ok {
		t.Fatal("the 401 was dropped")
	}
	if store.Len() != 0 {
		t.Fatal("the namespace was not emptied")
	}
	refused(t, s, store, object(pending, `{"b":1}`), "begun in the ended session")
	again := s.Begin(Inbox, k2, queue.User)
	if again.ShareKey() == pending.ShareKey() {
		t.Fatal("a new session shares a request of the old one")
	}
	accepted(t, s, store, object(again, `{"b":2}`))
}

// Every class of answer is filed by its decision, through Receive.
func TestEveryClassIsFiledByItsDecision(t *testing.T) {
	ns := confirmed(t)
	key := unreadPage(ns, "org-a", 1)
	want := map[transport.Class]cache.Decision{
		transport.Object: cache.Replace, transport.NotFound: cache.Remove, transport.NoAccess: cache.Remove,
		transport.Temporary: cache.KeepStale, transport.Canceled: cache.Drop, transport.ReadOnly: cache.KeepShowMessage,
		transport.Refused: cache.KeepShowMessage, transport.InvalidInput: cache.KeepShowMessage,
		transport.Protocol: cache.KeepShowMessage, transport.Unauthenticated: cache.KeepShowMessage,
	}
	if len(want) != int(transport.Canceled)+1 {
		t.Fatal("a class is missing")
	}
	for class, decision := range want {
		s, store := NewScope(ns), cache.NewStore()
		accepted(t, s, store, object(s.Begin(Inbox, key, queue.User), `{"old":1}`))
		r := InboxResult{Ticket: s.Begin(Inbox, key, queue.User), Answer: transport.Answer{Class: class, Payload: []byte(`{"new":1}`)}}
		d, ok := s.Receive(store, r, now.Add(time.Minute))
		if !ok || d != decision {
			t.Errorf("%v: %v %v", class, d, ok)
			continue
		}
		e, has := store.Get(key)
		switch decision {
		case cache.Replace:
			if string(e.Payload) != `{"new":1}` || !e.LoadedAt.Equal(now.Add(time.Minute)) {
				t.Errorf("%v: %+v", class, e)
			}
		case cache.Remove:
			if has {
				t.Errorf("%v: kept", class)
			}
		default:
			if class == transport.Unauthenticated {
				if has {
					t.Errorf("%v: the namespace was kept", class)
				}
				continue
			}
			// A failed refresh is no load.
			if !has || string(e.Payload) != `{"old":1}` || !e.LoadedAt.Equal(now) || e.Stale != (decision == cache.KeepStale) {
				t.Errorf("%v: %+v", class, e)
			}
		}
	}
}
