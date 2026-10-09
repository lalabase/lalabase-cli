package ui

import (
	"testing"

	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/transport"
)

// Adapted after round 1: there is no deferred read any more (a read is
// issued at once and the queue shares it). The intent stays: a read of A
// the selection left behind must not reach the cache or issue anything
// once B is active, whenever its canceled result arrives.
func TestR1AbandonedReissueDoesNotReadOrCache(t *testing.T) {
	h := newHarness(t)
	h.start()
	oldA := h.choose(orgA)
	oldB := h.choose(orgB)
	thirdA := h.choose(orgA)
	h.drive(h.choose(orgB))
	before := len(h.f.inboxCalls())
	h.drive(oldA)
	h.drive(thirdA)
	h.drive(oldB)
	a := h.m.app
	_, cached := a.store.Get(a.inboxKey(orgA, 1))
	if a.active.org != orgB || cached || len(a.active.rows) != 3 {
		t.Fatalf("active=%s cachedA=%v rows=%d", a.active.org, cached, len(a.active.rows))
	}
	// The canceled results issued nothing of their own: after the last
	// choice at most the two canceled commands of A themselves reach the
	// source (the fake answers a canceled context; a transport would not
	// send it), and their answers went nowhere (cachedA above).
	readsOfA := 0
	for _, q := range h.f.inboxCalls()[before:] {
		if q.OrganisationID == orgA {
			readsOfA++
		}
	}
	if readsOfA > 2 {
		t.Fatalf("%d reads of A went out after the last choice, more than the two canceled commands", readsOfA)
	}
	if n := len(a.waiters); n != 0 {
		t.Fatalf("%d waiters left behind", n)
	}
}

func TestR1SecondIndependentShrinkCanRecoverWithR(t *testing.T) {
	h := newHarness(t)
	total := 60
	h.f.set(func(q api.NotificationQuery) transport.Answer {
		return pageAnswer(q.Page, q.PerPage, total, total, slice(h.d.rows(orgA, 1, total), q.Page, q.PerPage), map[string]int{orgA: total})
	})
	h.start()
	h.drive(h.choose(orgA))
	h.drive(h.press("]"))
	h.drive(h.press("]"))
	total = 30
	h.drive(h.press("R"))
	total = 10
	before := len(h.f.inboxCalls())
	h.drive(h.press("R"))
	t.Logf("after later explicit R: page=%d total=%d corrected=%v reads=%d notice=%s", h.m.app.active.page, h.m.app.active.total, h.m.app.active.corrected, len(h.f.inboxCalls())-before, h.m.screen().List.Notice.String())
	h.drive(h.press("R"))
	if h.m.app.active.page != 1 {
		t.Fatalf("R cannot recover from an independent second shrink: page %d of 1", h.m.app.active.page)
	}
}

func TestR1JoinAfterCompletedFlightGetsNewSequence(t *testing.T) {
	for _, newerFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "old-first", true: "new-first"}[newerFirst], func(t *testing.T) {
			h := newHarness(t)
			counters := h.startHolding()
			oldMsgs := messages(t, counters) // actual flight completed, result waiting in UI channel
			old := oldMsgs[0].(gateway.InboxResult)
			h.f.set(func(q api.NotificationQuery) transport.Answer {
				return pageAnswer(q.Page, q.PerPage, 1, 1, h.d.rows(orgA, 401, 1), map[string]int{orgA: 1})
			})
			newerMsgs := messages(t, h.choose(""))
			newer := newerMsgs[0].(gateway.InboxResult)
			if old.Flight == newer.Flight {
				t.Fatal("need separate actual flights")
			}
			if newerFirst {
				h.feed(newer)
				h.feed(old)
			} else {
				h.feed(old)
				h.feed(newer)
			}
			a := h.m.app
			e, _ := a.store.Get(a.active.key)
			cached, ans := api.DecodeNotifications(transport.Answer{Class: transport.Object, Payload: e.Payload})
			if !ans.OK() {
				t.Fatal(ans)
			}
			t.Logf("oldFlight=%d newFlight=%d count=%d cachedTotal=%d visibleTotal=%d", old.Flight, newer.Flight, a.countOf(orgA).value, cached.Meta.Total, a.active.total)
			if a.countOf(orgA).value != 1 || cached.Meta.Total != 1 {
				t.Fatal("newer actual observation lost to older answer")
			}
		})
	}
}

func TestR1AbandonedReissueWithOnWireRequest(t *testing.T) {
	h := newHarness(t)
	h.start()
	gate := make(chan struct{})
	h.f.gate, h.f.gateOrg = gate, orgA
	before := len(h.f.inboxCalls())
	first := h.background(h.choose(orgA))
	h.waitFor("A on wire", func() bool { return len(h.f.inboxCalls()) == before+1 })
	h.drive(h.choose(orgB))
	h.choose(orgA)          // defer return to A until canceled result consumed
	h.drive(h.choose(orgB)) // leave deferred A again
	close(gate)
	canceled := h.receive(first)
	beforeFollow := len(h.f.inboxCalls())
	h.drive(h.feed(canceled)...)
	a := h.m.app
	_, cached := a.store.Get(a.inboxKey(orgA, 1))
	added := h.f.inboxCalls()[beforeFollow:]
	t.Logf("active=%s follow-up=%+v cachedA=%v", a.active.org, added, cached)
	if len(added) != 0 || cached {
		t.Fatal("canceled A result resurrected an abandoned deferred A read while B is active")
	}
}
