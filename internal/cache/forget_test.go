package cache

import (
	"testing"
	"time"
)

// C1b-2: an entry the root cannot use (it does not decode any more, or a
// refresh showed its page no longer exists) is dropped on its own; the other
// pages of the resource stay. Invalidating is the scope's step, not this one.
func TestForgetDropsOneEntryAndNothingElse(t *testing.T) {
	ns, _ := Confirmed("default", "https://example.invalid", "token-1")
	s := NewStore()
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	page := func(n int) Key {
		k := ns.Key("list_notifications")
		k.Organisation, k.Status, k.Page, k.PerPage = "org-a", "unread", n, 25
		return k
	}
	s.Apply(page(1), Replace, []byte(`{"p":1}`), now)
	s.Apply(page(2), Replace, []byte(`{"p":2}`), now)
	s.Forget(page(1))
	if _, ok := s.Get(page(1)); ok {
		t.Fatal("page 1 was kept")
	}
	if e, ok := s.Get(page(2)); !ok || string(e.Payload) != `{"p":2}` {
		t.Fatal("page 2 went with it")
	}
	if s.Len() != 1 || s.Bytes() != len(`{"p":2}`) {
		t.Fatalf("len %d bytes %d", s.Len(), s.Bytes())
	}
	s.Forget(page(3)) // absent: nothing happens
	if s.Len() != 1 {
		t.Fatal("forgetting an absent key changed the store")
	}
}
