package queue

import (
	"context"
	"testing"
	"time"

	"github.com/gboyke/lalabase-cli/internal/transport"
)

// C9b: idle work (the titles of the agents' tickets) is background work
// below the background: it starts only while no background job waits, and
// only while fewer than IdleShare starts are in the window, so other
// background work keeps at least the budget's other half; one running
// holds the background place while it runs. (Previews have a window of
// their own since #44: preview_test.go.)

// Of an idle and a background job waiting, the background one starts.
func TestBackgroundGoesBeforeIdle(t *testing.T) {
	q := New(WithClock(newClock()))
	busy := newGate()
	do(q, context.Background(), Job{Priority: Background, Run: busy.run})
	waitStarted(t, busy)
	idle, preview := newGate(), newGate()
	do(q, context.Background(), Job{Priority: Idle, Run: idle.run})
	waitWaiting(t, q, 1)
	do(q, context.Background(), Job{Priority: Background, Run: preview.run})
	waitWaiting(t, q, 2)
	busy.release <- ok()
	waitStarted(t, preview)
	notStarted(t, idle)
	preview.release <- ok()
	waitStarted(t, idle)
}

// Idle work takes a background place and counts against the budget, but
// starts only while fewer than IdleShare starts are in the window: the
// rest is the other background work's.
func TestIdleWorkLeavesTheBudgetsOtherHalf(t *testing.T) {
	if IdleShare*2 != BackgroundBudget {
		t.Fatalf("idle share %d of %d", IdleShare, BackgroundBudget)
	}
	c := newClock()
	q := New(WithClock(c))
	for i := range IdleShare {
		if i > 0 {
			c.Advance(time.Second)
		}
		g := newGate()
		r := do(q, context.Background(), Job{Priority: Idle, Run: g.run})
		waitStarted(t, g)
		g.release <- ok()
		answer(t, r)
	}
	held := newGate()
	do(q, context.Background(), Job{Priority: Idle, Run: held.run})
	notStarted(t, held)
	// Other background work finds the other half.
	for range BackgroundBudget - IdleShare {
		g := newGate()
		r := do(q, context.Background(), Job{Priority: Background, Run: g.run})
		waitStarted(t, g)
		g.release <- ok()
		answer(t, r)
	}
	notStarted(t, held)
	// The window rolls past the idle starts and the others' too.
	c.Advance(BudgetWindow)
	waitStarted(t, held)
}

// A preview joining a waiting idle read lifts it to the background, a
// person to theirs; leaving lets it fall back to idle.
func TestJoiningAnIdleReadLiftsIt(t *testing.T) {
	q := New(WithClock(newClock()))
	busy := newGate()
	do(q, context.Background(), Job{Priority: Background, Run: busy.run})
	waitStarted(t, busy)
	other, title := newGate(), newGate()
	do(q, context.Background(), Job{Priority: Background, Run: other.run})
	waitWaiting(t, q, 1)
	do(q, context.Background(), Job{Priority: Idle, Kind: Read, Key: "ticket-6", Run: title.run})
	waitWaiting(t, q, 2)
	ctx, leave := context.WithCancel(context.Background())
	do(q, ctx, Job{Priority: Background, Kind: Read, Key: "ticket-6", Run: title.run})
	waitJoined(t, q, "ticket-6", 2)
	if p := q.prioOf("ticket-6"); p != Background {
		t.Fatalf("joined: %d", p)
	}
	leave()
	waitJoined(t, q, "ticket-6", 1)
	if p := q.prioOf("ticket-6"); p != Idle {
		t.Fatalf("left: %d", p)
	}
	do(q, context.Background(), Job{Priority: User, Kind: Read, Key: "ticket-6", Run: title.run})
	waitJoined(t, q, "ticket-6", 2)
	waitStarted(t, title)
	notStarted(t, other)
}

// prioOf is the priority of the shared read of key.
func (q *Queue) prioOf(key string) Priority {
	q.mu.Lock()
	defer q.mu.Unlock()
	if f := q.shared[key]; f != nil {
		return f.prio
	}
	return -1
}

// Review round 2 of #36: the retry of idle work is idle work too: a
// background job queued while the retry waits starts before it.
func TestAnIdleRetryStaysIdle(t *testing.T) {
	c := newClock()
	q := New(WithClock(c), WithRetryDelay(time.Second))
	title := newGate()
	r := do(q, context.Background(), Job{Priority: Idle, Kind: Read, Key: "ticket-6", Run: title.run})
	waitStarted(t, title)
	title.release <- transportTemporary()
	waitTimers(t, c, 1)
	busy := newGate()
	do(q, context.Background(), Job{Priority: Background, Run: busy.run})
	waitStarted(t, busy)
	c.Advance(time.Second)
	waitWaiting(t, q, 1) // the retry
	preview := newGate()
	do(q, context.Background(), Job{Priority: Background, Run: preview.run})
	waitWaiting(t, q, 2)
	busy.release <- ok()
	waitStarted(t, preview)
	notStarted(t, title)
	preview.release <- ok()
	// Three starts in the window (the title, busy, the preview): the retry,
	// idle, waits for it to roll on.
	notStarted(t, title)
	c.Advance(BudgetWindow)
	waitStarted(t, title)
	title.release <- ok()
	answer(t, r)
}

func transportTemporary() transport.Answer {
	return transport.Answer{Class: transport.Temporary, Status: 503, Sent: true}
}
