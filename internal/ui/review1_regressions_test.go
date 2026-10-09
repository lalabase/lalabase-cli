package ui

import (
	"testing"

	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/transport"
)

func TestR1SharedFlightKeepsOriginalSequence(t *testing.T) {
	h := newHarness(t)
	h.f.set(func(q api.NotificationQuery) transport.Answer {
		n := 5
		if q.OrganisationID == orgA {
			n = 7
		}
		return pageAnswer(q.Page, q.PerPage, n, n, h.d.rows(orgA, 1, n), map[string]int{orgA: n})
	})
	gate := make(chan struct{})
	h.f.gate = gate
	counters := h.startHolding()
	var share string
	for _, w := range h.m.app.waiters {
		share = w.ticket.ShareKey()
	}
	first := h.background(counters)
	h.waitFor("first shared waiter", func() bool { return h.q.Sharers(share) == 1 })
	h.drive(h.choose(orgA))
	second := h.background(h.choose(""))
	h.waitFor("second shared waiter", func() bool { return h.q.Sharers(share) == 2 })
	close(gate)
	h.feed(h.receive(first), h.receive(second))
	if got := h.m.app.countOf(orgA).value; got != 7 {
		t.Fatalf("older shared read was resequenced after newer filtered read: count=%d want=7", got)
	}
}

func TestR1QuickReturnCompletesAfterCanceledWaiter(t *testing.T) {
	h := newHarness(t)
	h.start()
	oldA := h.choose(orgA)
	oldB := h.choose(orgB)
	// Adapted after round 1: the return to A is a read of its own, run
	// here; the canceled results of before arrive after it.
	h.drive(h.choose(orgA))
	h.drive(oldA)
	h.drive(oldB)
	v := h.m.app.active
	if v.loading || len(v.rows) != 25 {
		t.Fatalf("quick return stuck: loading=%v rows=%d waiters=%d", v.loading, len(v.rows), len(h.m.app.waiters))
	}
}

func TestR1ReloadAfterZeroShrinkRequestsPageOne(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.drive(h.choose(orgA))
	h.drive(h.press("]"))
	h.f.set(func(q api.NotificationQuery) transport.Answer {
		return pageAnswer(q.Page, q.PerPage, 0, 0, nil, map[string]int{orgA: 0})
	})
	h.drive(h.press("R"))
	h.drive(h.press("R"))
	calls := h.f.inboxCalls()
	got := calls[len(calls)-1].Page
	if got != 1 {
		t.Fatalf("display page=%d but reload asked page=%d", h.m.app.active.page, got)
	}
}
