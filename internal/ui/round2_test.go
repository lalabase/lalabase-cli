package ui

import (
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/transport"
)

// Review round 2, finding A3: a request the queue retries after a temporary
// failure is a later send. Its answer, a removal, arrives after a newer page
// of the resource was filed; it is the server's newest word and must not be
// refused as older.
func TestARetriedRequestIsTheNewerOne(t *testing.T) {
	h := newHarness(t)
	page2 := func(q api.NotificationQuery) transport.Answer {
		return pageAnswer(2, q.PerPage, 33, 33, h.d.rows(orgB, 101, 3), map[string]int{orgA: 30, orgB: 3})
	}
	h.f.set(func(q api.NotificationQuery) transport.Answer {
		if q.Page == 2 {
			return page2(q)
		}
		return h.d.defaultInbox(q)
	})
	h.start()
	h.drive(h.choose(""))
	h.drive(h.press("]"))
	h.press("esc")
	// The chooser's R reads page 1: the first send is held at the source
	// and fails temporarily when released; the retry answers "no access".
	var mu sync.Mutex
	calls := 0
	first := make(chan struct{})
	h.f.set(func(q api.NotificationQuery) transport.Answer {
		if q.Page == 2 {
			return page2(q)
		}
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n == 1 {
			<-first
			return failing(transport.Temporary, 503)
		}
		return failing(transport.NoAccess, 200)
	})
	before := len(h.f.inboxCalls())
	batch, ok := h.press("R")().(tea.BatchMsg)
	if !ok {
		t.Fatal("R in the chooser is not a batch")
	}
	var refresh []<-chan tea.Msg
	for _, c := range batch {
		refresh = append(refresh, h.background(c))
	}
	h.waitFor("the first send at the source", func() bool { return len(h.f.inboxCalls()) == before+1 })
	// Meanwhile page 2 is read anew: sent after the first send, before the
	// retry.
	h.press("enter")
	h.drive(h.press("R"))
	a := h.m.app
	if a.active.page != 2 || len(a.active.rows) != 3 {
		t.Fatalf("page 2: page %d, %d rows", a.active.page, len(a.active.rows))
	}
	close(first)
	for _, ch := range refresh {
		h.feed(h.receive(ch))
	}
	if len(h.f.inboxCalls()) != before+3 {
		t.Fatalf("%d requests, not the first send, page 2 and the retry", len(h.f.inboxCalls())-before)
	}
	if len(a.active.rows) != 0 || a.active.err.String() != "Not found or no longer accessible." || a.countOf("").known {
		t.Fatalf("the retried removal was refused: %d rows, %q, all %+v", len(a.active.rows), a.active.err.String(), a.countOf(""))
	}
	if _, cached := a.store.Get(a.active.key); cached {
		t.Fatal("page 2 stayed cached")
	}
}

// After a page that failed, the next page is not offered (its total is
// unknown), the previous one is, and R reads the failed page again.
func TestAFailedPageOffersNoNextPage(t *testing.T) {
	h := newHarness(t)
	h.f.set(func(q api.NotificationQuery) transport.Answer {
		if q.Page == 2 {
			return failing(transport.Temporary, 503)
		}
		return h.d.defaultInbox(q)
	})
	h.start()
	h.drive(h.choose(""))
	h.drive(h.press("]"))
	a := h.m.app
	if a.active.page != 2 || a.active.known || a.active.loading || a.active.err.String() == "" {
		t.Fatalf("page 2: %+v", a.active)
	}
	allowed := h.m.allowed()
	if allowed[NextPage] || !allowed[PrevPage] {
		t.Fatalf("next %v, prev %v", allowed[NextPage], allowed[PrevPage])
	}
	before := len(h.f.inboxCalls())
	if cmd := h.press("]"); cmd != nil || a.active.page != 2 || len(h.f.inboxCalls()) != before {
		t.Fatal("] did something on a failed page")
	}
	h.f.set(func(q api.NotificationQuery) transport.Answer {
		if q.Page == 2 {
			return pageAnswer(2, q.PerPage, 33, 33, h.d.rows(orgB, 101, 3), map[string]int{orgA: 30, orgB: 3})
		}
		return h.d.defaultInbox(q)
	})
	// R from the page reads the page (and the counters, which the failed
	// global page marked failed as well).
	h.drive(h.press("R"))
	if a.active.page != 2 || len(a.active.rows) != 3 || !a.active.known || a.steps[stepCounters].state != ready {
		t.Fatalf("after R: rows %d, known %v, err %q, counters %d", len(a.active.rows), a.active.known, a.active.err.String(), a.steps[stepCounters].state)
	}
	if !h.m.allowed()[PrevPage] || h.m.allowed()[NextPage] {
		t.Fatal("page 2 of 2: [ expected, ] not")
	}
}
