package gateway

import (
	"testing"

	"github.com/gboyke/lalabase-cli/internal/cache"
	"github.com/gboyke/lalabase-cli/internal/queue"
	"github.com/gboyke/lalabase-cli/internal/transport"
)

func TestR3RemovalCopyDoesNotInvalidatePendingReload(t *testing.T) {
	ns := confirmed(t)
	s, store := NewScope(ns), cache.NewStore()
	key := unreadPage(ns, "org-a", 1)
	first, copy := s.Begin(Inbox, key, queue.User), s.Begin(Inbox, key, queue.User)
	removed := transport.Answer{Class: transport.NoAccess}
	if d, ok := s.Receive(store, InboxResult{Ticket: first, Answer: removed, Flight: 17}, now); !ok || d != cache.Remove {
		t.Fatal("first removal was not accepted")
	}
	page2 := unreadPage(ns, "org-a", 2)
	reload := s.Begin(Inbox, page2, queue.User)
	if d, ok := s.Receive(store, InboxResult{Ticket: copy, Answer: removed, Flight: 17}, now); !ok || d != cache.Remove {
		t.Fatal("shared copy was not accepted")
	}
	after := s.Begin(Inbox, page2, queue.User)
	if reload.ShareKey() != after.ShareKey() {
		t.Fatalf("one removal invalidated twice: reload epoch %d, after-copy epoch %d", reload.Epoch, after.Epoch)
	}
	accepted(t, s, store, object(reload, `{"fresh-page-2":true}`))
}

func TestR3RemovalCopyCannotBypassSelectionGeneration(t *testing.T) {
	ns := confirmed(t)
	s, store := NewScope(ns), cache.NewStore()
	key := unreadPage(ns, "org-a", 1)
	first, copy := s.Begin(Inbox, key, queue.User), s.Begin(Inbox, key, queue.User)
	removed := transport.Answer{Class: transport.NotFound}
	if _, ok := s.Receive(store, InboxResult{Ticket: first, Answer: removed, Flight: 21}, now); !ok {
		t.Fatal("first removal was not accepted")
	}
	s.Move(Inbox)
	refused(t, s, store, InboxResult{Ticket: copy, Answer: removed, Flight: 21}, "copy of an abandoned selection")
}
