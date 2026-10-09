package queue

import (
	"context"
	"testing"
	"time"

	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/worker"
)

func TestR3RetrySharersKeepOneSendIdentity(t *testing.T) {
	life := worker.New(context.Background())
	defer life.Stop(time.Second)
	clock := newClock()
	q := New(WithLifetime(life), WithClock(clock))
	gate := newGate()
	type result struct {
		answer transport.Answer
		flight Flight
	}
	start := func(ctx context.Context, key string, run func(context.Context) transport.Answer) <-chan result {
		out := make(chan result, 1)
		go func() {
			a, f := q.DoShared(ctx, Job{Priority: User, Kind: Read, Key: key, Run: run})
			out <- result{a, f}
		}()
		return out
	}
	get := func(ch <-chan result) result {
		select {
		case r := <-ch:
			return r
		case <-time.After(2 * time.Second):
			t.Fatal("no shared result")
			return result{}
		}
	}
	wait := func(why string, condition func() bool) {
		deadline := time.Now().Add(2 * time.Second)
		for !condition() {
			if time.Now().After(deadline) {
				t.Fatal(why)
			}
			time.Sleep(time.Millisecond)
		}
	}
	current := func() Flight {
		q.mu.Lock()
		defer q.mu.Unlock()
		return q.shared["a"].flight()
	}
	first := start(context.Background(), "a", gate.run)
	waitStarted(t, gate)
	initial := current()
	leaverCtx, cancel := context.WithCancel(context.Background())
	leaver := start(leaverCtx, "a", gate.run)
	wait("leaver did not join", func() bool { return q.Sharers("a") == 2 })
	gate.release <- transport.Answer{Class: transport.Temporary, Status: 503, Sent: true}
	wait("retry delay did not begin", func() bool {
		clock.mu.Lock()
		defer clock.mu.Unlock()
		return len(clock.waiters) == 1
	})
	duringDelay := start(context.Background(), "a", gate.run)
	wait("delay sharer did not join", func() bool { return q.Sharers("a") == 3 })
	cancel()
	left := get(leaver)
	if left.answer.Class != transport.Canceled || left.flight != initial {
		t.Fatalf("leaver during delay: class %v, flight %d; first send %d", left.answer.Class, left.flight, initial)
	}
	between := get(start(context.Background(), "b", func(context.Context) transport.Answer { return ok() }))
	clock.Advance(time.Second)
	waitStarted(t, gate)
	retry := current()
	if retry <= between.flight {
		t.Fatalf("retry %d did not follow middle send %d", retry, between.flight)
	}
	duringRetry := start(context.Background(), "a", gate.run)
	wait("retry sharer did not join", func() bool { return q.Sharers("a") == 3 })
	gate.release <- transport.Answer{Class: transport.NoAccess, Status: 200, Sent: true}
	for i, ch := range []<-chan result{first, duringDelay, duringRetry} {
		r := get(ch)
		if r.answer.Class != transport.NoAccess || r.flight != retry {
			t.Fatalf("sharer %d: class %v, flight %d; retry %d", i, r.answer.Class, r.flight, retry)
		}
	}
	if gate.calls.Load() != 2 {
		t.Fatalf("%d sends, expected initial and retry", gate.calls.Load())
	}
}
