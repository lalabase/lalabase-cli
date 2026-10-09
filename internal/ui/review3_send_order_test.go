package ui

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/queue"
	"github.com/gboyke/lalabase-cli/internal/transport"
)

// Adapted in round 3: a read of the person's priority holds one of the
// queue's two places for the whole test, so the two reads of each round
// go out one after the other (two handovers at the same instant can carry
// their numbers in either order; the number is given at the handover).
func TestR3NewestActualSendWinsVisibleCounters(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.drive(h.choose(""))
	h.drive(h.press("]"))
	release, started := make(chan struct{}), make(chan struct{})
	if !h.live.Start("review3: blocker", func(ctx context.Context) {
		h.q.Do(ctx, queue.Job{Priority: queue.User, Kind: queue.Read, Key: "review3-blocker", Run: func(context.Context) transport.Answer {
			close(started)
			<-release
			return transport.Answer{Class: transport.Object, Status: 200, Sent: true}
		}})
	}) {
		t.Fatal("the lifetime is over")
	}
	defer close(release)
	<-started
	for attempt := 0; attempt < 200; attempt++ { // 200 rounds: the sends are serialized, more rounds prove nothing more
		var sent atomic.Int32
		h.f.set(func(q api.NotificationQuery) transport.Answer {
			count := 33 + int(sent.Add(1))
			rows := h.d.rows(orgA, 1, count)
			return pageAnswer(q.Page, q.PerPage, count, count, slice(rows, q.Page, q.PerPage), map[string]int{orgA: count})
		})
		h.press("esc")
		batch := h.press("R")().(tea.BatchMsg)
		h.press("enter")
		page2 := h.press("R")
		if page2 == nil {
			t.Fatal("page-2 refresh missing")
		}
		commands := append(batch, page2)
		ready := make(chan struct{})
		var pending []<-chan tea.Msg
		for _, cmd := range commands {
			pending = append(pending, h.background(func() tea.Msg { <-ready; return cmd() }))
		}
		close(ready)
		var trace []string
		for _, ch := range pending {
			msg := h.receive(ch)
			if r, ok := msg.(gateway.InboxResult); ok {
				trace = append(trace, fmt.Sprintf("page=%d send=%d count=%d flight=%d", r.Key.Page, r.Page.Meta.UnreadTotal-33, r.Page.Meta.UnreadTotal, r.Flight))
			}
			h.drive(h.feed(msg)...)
		}
		if sent.Load() != 2 {
			t.Fatalf("%d inbox sends", sent.Load())
		}
		if got := h.m.app.countOf("").value; got != 35 {
			t.Fatalf("iteration %d: newest actual send observed 35, visible global counter=%d; %v", attempt, got, trace)
		}
	}
}
