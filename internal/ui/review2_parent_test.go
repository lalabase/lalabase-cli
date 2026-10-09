package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/transport"
)

// The directory can lose an organisation while its first result waits for
// delivery. A newer global snapshot still makes its absent counter unknown.
func TestR2NewerGlobalOmissionCoversPendingDroppedOrganisation(t *testing.T) {
	h := newHarness(t)
	globalCmd := h.startHolding()
	old := messages(t, h.choose(orgA))[0].(gateway.InboxResult)
	h.press("esc")
	h.f.orgs = func() ([]api.Ref, transport.Answer) {
		return []api.Ref{{ID: orgB, Name: "B"}}, transport.Answer{Class: transport.Object, Status: 200, Sent: true}
	}
	h.drive(h.press("R"))
	if h.m.app.organisation(orgA) != nil || h.m.app.countOf(orgA).seq != 0 {
		t.Fatal("setup: A must have left the directory before its first count")
	}
	h.f.set(func(q api.NotificationQuery) transport.Answer {
		return pageAnswer(q.Page, q.PerPage, 3, 3, h.d.rows(orgB, 101, 3), map[string]int{orgB: 3})
	})
	newer := messages(t, globalCmd)[0].(gateway.InboxResult)
	if newer.Flight <= old.Flight {
		t.Fatal("setup: global must be younger")
	}
	h.feed(newer, old)
	c := h.m.app.countOf(orgA)
	t.Logf("old filtered flight=%d newer global flight=%d A known=%v value=%d seq=%d", old.Flight, newer.Flight, c.known, c.value, c.seq)
	// The directory can list A again before the next global read completes.
	h.f.orgs = func() ([]api.Ref, transport.Answer) {
		return h.d.orgs, transport.Answer{Class: transport.Object, Status: 200, Sent: true}
	}
	refresh := h.press("R")().(tea.BatchMsg)
	h.drive(refresh[0])
	t.Logf("A shown after directory returns: %q", h.m.screen().Chooser.Rows[1].Trailing.String())
	// Adapted: the snapshot's sequence is kept once (app.snapshot), not
	// recorded per organisation; what matters is that A stays unknown.
	if c.known || h.m.screen().Chooser.Rows[1].Trailing.String() != "?" {
		t.Fatal("older filtered answer defeated newer global omission")
	}
}

func TestR2RefusedAnswerConsumesScopeWaiter(t *testing.T) {
	h := newHarness(t)
	old := messages(t, h.startHolding())[0].(gateway.InboxResult)
	h.drive(h.choose(""))
	h.feed(old)
	if got := h.m.app.scope.Move(gateway.Organisations); len(got) != 0 {
		t.Fatalf("scope retains already consumed waiter(s): %v", got)
	}
}

func TestR2LatestFiledAnswerSettlesLoadingAfterOlderRefusal(t *testing.T) {
	h := newHarness(t)
	old := messages(t, h.startHolding())[0].(gateway.InboxResult)
	h.drive(h.choose(""))
	h.feed(old)
	a := h.m.app
	t.Logf("pending=%v waiters=%d loading=%v next-page-allowed=%v", a.pending(a.active.key), len(a.waiters), a.active.loading, h.m.allowed()[NextPage])
	if a.active.loading || !h.m.allowed()[NextPage] {
		t.Fatal("all answers consumed but page navigation remains loading")
	}
}

func TestR2OldAreaSuccessCannotFileFlightOrCounters(t *testing.T) {
	h := newHarness(t)
	h.start()
	old := messages(t, h.choose(orgA))[0].(gateway.InboxResult)
	h.drive(h.choose(orgB))
	a := h.m.app
	before := a.countOf(orgA)
	h.feed(old)
	if a.filed[old.Key] != 0 || a.countOf(orgA) != before {
		t.Fatal("old area success filed a flight or counter")
	}
	if _, ok := a.store.Get(old.Key); ok {
		t.Fatal("old area success reached cache")
	}
	h.drive(h.choose(orgA))
	if a.active.loading || len(a.active.rows) != 25 {
		t.Fatal("old area blocked new current read")
	}
}

func TestR2OldSessionResultCannotFileFlight(t *testing.T) {
	old := newHarness(t)
	r := messages(t, old.startHolding())[0].(gateway.InboxResult)
	old.events.Revoke()
	old.feed(gateway.Wake{Session: old.events.Session()})
	h := newHarness(t)
	first := h.startHolding()
	h.feed(r)
	if len(h.m.app.filed) != 0 || h.m.app.store.Len() != 0 || h.m.app.expired {
		t.Fatal("old session result affected replacement session")
	}
	h.drive(first)
	if h.m.app.store.Len() != 1 || h.m.app.countOf("").value != 33 {
		t.Fatal("old session result blocked first answer of new session")
	}
}

func TestR2YoungerRemovalFromBeforeCorrectionIsRejectedByEpoch(t *testing.T) {
	h := newHarness(t)
	total := 60
	h.f.set(func(q api.NotificationQuery) transport.Answer {
		return pageAnswer(q.Page, q.PerPage, total, total, slice(h.d.rows(orgA, 1, total), q.Page, q.PerPage), map[string]int{orgA: total})
	})
	h.start()
	h.drive(h.choose(""))
	h.drive(h.press("]"))
	h.drive(h.press("]"))
	total = 30
	shrink := messages(t, h.press("R"))[0].(gateway.InboxResult)
	h.press("esc")
	h.f.set(func(api.NotificationQuery) transport.Answer { return failing(transport.NoAccess, 200) })
	removalMessages := messages(t, h.press("R"))
	var removal gateway.InboxResult
	for _, msg := range removalMessages {
		if r, ok := msg.(gateway.InboxResult); ok {
			removal = r
		}
	}
	if removal.Flight <= shrink.Flight {
		t.Fatal("removal must be younger than shrink response")
	}
	correction := h.feed(shrink)
	if len(correction) != 1 || h.m.app.active.page != 2 {
		t.Fatal("need one correction to page two")
	}
	h.feed(removalMessages...)
	a := h.m.app
	if a.filed[removal.Key] == uint64(removal.Flight) || !a.global.known || a.global.value != 30 {
		t.Fatal("pre-correction removal crossed root invalidation")
	}
	h.f.set(func(q api.NotificationQuery) transport.Answer {
		return pageAnswer(q.Page, q.PerPage, total, total, slice(h.d.rows(orgA, 1, total), q.Page, q.PerPage), map[string]int{orgA: total})
	})
	h.drive(correction...)
	if a.active.loading || len(a.active.rows) != 5 {
		t.Fatal("correction reload was blocked")
	}
}

func TestR2UndeliveredCanceledWaiterNeverBecomesPendingAgain(t *testing.T) {
	h := newHarness(t)
	h.start()
	held := h.choose(orgA)
	a := h.m.app
	var w *waiter
	for _, x := range a.waiters {
		w = x
	}
	if w == nil {
		t.Fatal("no waiter")
	}
	canceled := 0
	cancel := w.cancel
	w.cancel = func() { canceled++; cancel() }
	h.drive(h.choose(orgB))
	h.drive(h.choose(""))
	if a.pending(a.inboxKey(orgA, 1)) || !w.canceled || canceled != 1 {
		t.Fatalf("undelivered canceled waiter pending or recanceled: %v %d", w.canceled, canceled)
	}
	h.events.Revoke()
	h.feed(gateway.Wake{Session: h.events.Session()})
	h.drive(held)
	if !a.expired || a.scope != nil || a.store != nil || len(a.waiters) != 0 {
		t.Fatal("canceled result reopened expired session")
	}
}
