package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/transport"
)

func TestR2PagingActuallySharesPendingRead(t *testing.T) {
	h := newHarness(t)
	h.f.set(func(q api.NotificationQuery) transport.Answer {
		if q.Page == 2 {
			return pageAnswer(2, q.PerPage, 33, 33, h.d.rows(orgB, 101, 3), map[string]int{orgA: 30, orgB: 3})
		}
		return h.d.defaultInbox(q)
	})
	h.start()
	h.drive(h.choose(""))
	h.drive(h.press("]"))
	gate := make(chan struct{})
	h.f.gate, h.f.gateOrg, h.f.gatePage = gate, "", 1
	h.press("esc")
	before := len(h.f.inboxCalls())
	batch := h.press("R")().(tea.BatchMsg)
	var refresh []<-chan tea.Msg
	for _, c := range batch {
		refresh = append(refresh, h.background(c))
	}
	h.waitFor("first source call", func() bool { return len(h.f.inboxCalls()) == before+1 })
	h.drive(h.press("enter"))
	back := h.background(h.press("["))
	var share string
	for _, w := range h.m.app.waiters {
		if w.ticket.Key.Global && w.ticket.Key.Page == 1 {
			share = w.ticket.ShareKey()
		}
	}
	h.waitFor("actual shared queue flight", func() bool { return h.q.Sharers(share) == 2 })
	close(gate)
	var copies []gateway.InboxResult
	for _, ch := range refresh {
		msg := h.receive(ch)
		if r, ok := msg.(gateway.InboxResult); ok {
			copies = append(copies, r)
		}
		h.feed(msg)
	}
	r := h.receive(back).(gateway.InboxResult)
	copies = append(copies, r)
	h.feed(r)
	if len(copies) != 2 || copies[0].Flight != copies[1].Flight || len(h.f.inboxCalls()) != before+1 {
		t.Fatalf("copies=%+v calls delta=%d", copies, len(h.f.inboxCalls())-before)
	}
	if h.m.app.active.loading || len(h.m.app.active.rows) != 25 {
		t.Fatal("actual shared result did not settle view")
	}
	t.Logf("shared flight=%d source requests=1 copies=2 rows=25 loading=false", r.Flight)
}
