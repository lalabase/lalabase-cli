package ui

import (
	"context"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/transport"
)

// A real request observer runs before either its result or the mailbox Wake
// reaches Update. An already queued key may be processed first.
func TestReviewObserved401BeforeQueuedKeyMustBlockCacheAndRequest(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.start()
	h.drive(h.choose(orgA))
	h.drive(h.choose(orgB))
	before := len(h.f.inboxCalls())
	h.events.Observe(func(context.Context) transport.Answer {
		return failing(transport.Unauthenticated, 401)
	})(context.Background())
	cmd := h.choose(orgA)
	if v := h.m.app.active; v != nil && v.cached && len(v.rows) > 0 {
		t.Errorf("cache replayed after observed 401: org=%q rows=%d expired=%v revoked=%v", v.org, len(v.rows), h.m.app.expired, h.events.State().Revoked)
	}
	if cmd != nil {
		messages(t, cmd)
	}
	if got := len(h.f.inboxCalls()); got != before {
		t.Errorf("read after observed 401: before=%d after=%d", before, got)
	}
	if !h.m.app.expired {
		t.Error("key processed after observed 401 did not expire session")
	}
}

func TestReviewRealCanceled401BeforeQueuedKeyMustBlock(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.start()
	// Real live mailbox: consume the wake produced by its supervised worker,
	// while an earlier queued key reaches Update before that wake.
	h.events = gateway.NewEvents(h.live)
	h.m.app.deps.Events = h.events
	h.m.app.session = h.events.Session()
	h.m.app.deps.Gateway = gateway.New(h.live, h.q, h.f, gateway.WithEvents(h.events))
	wakeResult := h.background(h.events.Wait())
	gate := make(chan struct{})
	h.f.gate, h.f.gateOrg, h.f.ignoreCancel = gate, orgA, true
	h.f.set(func(q api.NotificationQuery) transport.Answer {
		if q.OrganisationID == orgA {
			return failing(transport.Unauthenticated, 401)
		}
		return h.d.defaultInbox(q)
	})
	first := h.background(h.choose(orgA))
	h.waitFor("A on wire", func() bool { return len(h.f.inboxCalls()) == 2 })
	h.drive(h.choose(orgB))
	_ = h.receive(first) // canceled waiter result intentionally remains undelivered
	close(gate)
	h.waitFor("401 observation", func() bool { return h.events.State().Revoked })
	wake := h.receive(wakeResult)
	before := len(h.f.inboxCalls())
	cmd := h.press("R")
	messages(t, cmd)
	if got := len(h.f.inboxCalls()); got != before {
		t.Errorf("R after real observed canceled 401 sent another read: before=%d after=%d expired=%v revoked=%v", before, got, h.m.app.expired, h.events.State().Revoked)
	}
	h.feed(wake)
	if !h.m.app.expired {
		t.Fatal("eventually delivered live wake did not expire")
	}
}
