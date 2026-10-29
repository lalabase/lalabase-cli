package queue

import (
	"context"
	"testing"
	"time"

	"github.com/gboyke/lalabase-cli/internal/transport"
)

// C1c: background work starts at most BackgroundBudget times per rolling
// BudgetWindow, retries counted; the person's actions are not counted and
// not held. Previews have a window of their own since #44
// (preview_test.go).

// spend starts n background jobs one after the other, a second apart on
// the clock, and lets each one finish at once.
func spend(t *testing.T, q *Queue, c *fakeClock, n int) {
	t.Helper()
	for i := range n {
		if i > 0 {
			c.Advance(time.Second)
		}
		g := newGate()
		r := do(q, context.Background(), Job{Priority: Background, Run: g.run})
		waitStarted(t, g)
		g.release <- ok()
		if a := answer(t, r); a.Class != transport.Object {
			t.Fatalf("background job %d: %v", i, a.Class)
		}
	}
}

func TestTheBudgetIsTheDocumentedOne(t *testing.T) {
	if BackgroundBudget != 6 || BudgetWindow != time.Minute {
		t.Fatalf("budget %d per %s", BackgroundBudget, BudgetWindow)
	}
}

// The seventh background start within a minute waits for the window to
// roll on; it starts by itself when the oldest start falls out of it.
func TestBackgroundStartsAreBudgetedPerRollingMinute(t *testing.T) {
	c := newClock()
	q := New(WithClock(c))
	spend(t, q, c, BackgroundBudget) // starts at 0, 1, 2, 3, 4 and 5 s
	g := newGate()
	r := do(q, context.Background(), Job{Priority: Background, Run: g.run})
	notStarted(t, g)
	c.Advance(BudgetWindow - 6*time.Second) // 59 s: the first start is not a minute old
	notStarted(t, g)
	c.Advance(time.Second) // 60 s: it is
	waitStarted(t, g)
	g.release <- ok()
	if a := answer(t, r); a.Class != transport.Object {
		t.Fatalf("%v", a.Class)
	}
	// The window rolls: the next start is free when the second start is a
	// minute old, not all at once.
	g2 := newGate()
	do(q, context.Background(), Job{Priority: Background, Run: g2.run})
	notStarted(t, g2)
	c.Advance(time.Second) // 61 s
	waitStarted(t, g2)
}

// A retry is a start of its own and counts.
func TestARetryCountsAgainstTheBudget(t *testing.T) {
	c := newClock()
	q := New(WithClock(c), WithRetryDelay(0))
	spend(t, q, c, BackgroundBudget-1)
	g := newGate()
	r := do(q, context.Background(), Job{Priority: Background, Kind: Read, Run: g.run})
	waitStarted(t, g) // the sixth start
	g.release <- transport.Answer{Class: transport.Temporary, Status: 503, Sent: true}
	notStarted(t, g) // the retry would be the seventh
	c.Advance(BudgetWindow)
	waitStarted(t, g)
	g.release <- ok()
	if a := answer(t, r); a.Class != transport.Object || g.calls.Load() != 2 {
		t.Fatalf("%v after %d calls", a.Class, g.calls.Load())
	}
}

// The person is never held by the budget: a job of theirs starts at once,
// and so does a waiting background read a person joins.
func TestThePersonIsNotBudgeted(t *testing.T) {
	c := newClock()
	q := New(WithClock(c))
	spend(t, q, c, BackgroundBudget)
	g := newGate()
	r := do(q, context.Background(), Job{Priority: User, Run: g.run})
	waitStarted(t, g)
	g.release <- ok()
	answer(t, r)
	// A background read held by the budget, then joined by a person.
	shared := newGate()
	held := do(q, context.Background(), Job{Priority: Background, Kind: Read, Key: "k", Run: shared.run})
	notStarted(t, shared)
	joined := do(q, context.Background(), Job{Priority: User, Kind: Read, Key: "k", Run: shared.run})
	waitStarted(t, shared)
	shared.release <- ok()
	if a, b := answer(t, held), answer(t, joined); a.Class != transport.Object || b.Class != transport.Object || shared.calls.Load() != 1 {
		t.Fatalf("%v %v after %d calls", a.Class, b.Class, shared.calls.Load())
	}
	// The person's starts did not use up the budget either: the next
	// background start is still held until the window rolls.
	g2 := newGate()
	do(q, context.Background(), Job{Priority: Background, Run: g2.run})
	notStarted(t, g2)
	c.Advance(BudgetWindow)
	waitStarted(t, g2)
}

// A place granted to a caller who left at that moment was never a start:
// its budget comes back.
func TestAnAbandonedGrantIsNoStart(t *testing.T) {
	c := newClock()
	q := New(WithClock(c))
	for range BackgroundBudget * 3 {
		ticket, _, _, _ := q.enqueue(Background, nil)
		if !ticket.granted {
			t.Fatal("not granted")
		}
		q.abandon(ticket)
	}
	spend(t, q, c, BackgroundBudget)
	g := newGate()
	do(q, context.Background(), Job{Priority: Background, Run: g.run})
	notStarted(t, g)
}

// A rate-limit pause and the budget hold work together: after the pause a
// background job still needs budget; the budget is not consumed by waiting.
func TestThePauseAndTheBudgetAreSeparate(t *testing.T) {
	c := newClock()
	q := New(WithClock(c))
	spend(t, q, c, BackgroundBudget-1)
	g := newGate()
	r := do(q, context.Background(), Job{Priority: Background, Run: g.run})
	waitStarted(t, g)
	g.release <- transport.Answer{Class: transport.Temporary, Status: 429, RetryAt: c.Now().Add(10 * time.Second), Sent: true}
	notStarted(t, g) // the retry: in the pause, and out of budget
	c.Advance(10 * time.Second)
	notStarted(t, g) // the pause is over, the budget is not
	c.Advance(BudgetWindow - 10*time.Second)
	waitStarted(t, g)
	g.release <- ok()
	if a := answer(t, r); a.Class != transport.Object {
		t.Fatalf("%v", a.Class)
	}
}

// Review round 1, finding 2 (the reviewer's R6): the person's starts are
// not counted, told apart from "fell out of the window meanwhile": two of
// theirs between the fourth and the fifth background start leave the
// seventh background start waiting, not the ninth.
func TestThePersonsStartsAreNotCounted(t *testing.T) {
	c := newClock()
	q := New(WithClock(c))
	spend(t, q, c, BackgroundBudget-2) // four background starts
	for range 2 {
		g := newGate()
		r := do(q, context.Background(), Job{Priority: User, Run: g.run})
		waitStarted(t, g)
		g.release <- ok()
		answer(t, r)
	}
	c.Advance(time.Second)
	for i := range 2 {
		g := newGate()
		r := do(q, context.Background(), Job{Priority: Background, Run: g.run})
		waitStarted(t, g) // the fifth and sixth background start
		g.release <- ok()
		if a := answer(t, r); a.Class != transport.Object {
			t.Fatalf("background start %d: %v", 5+i, a.Class)
		}
	}
	g := newGate()
	do(q, context.Background(), Job{Priority: Background, Run: g.run})
	notStarted(t, g) // the seventh waits
	c.Advance(BudgetWindow)
	waitStarted(t, g)
}
