package queue

import (
	"context"
	"testing"
	"time"

	"github.com/gboyke/lalabase-cli/internal/transport"
)

// C1b-2: the interface shows every rate-limit pause, also one that only
// background work ran into. WithOnPause reports a person's wait only (the
// auth commands rely on that), so a second observer is told of every
// received 429 that closes or extends the window, whatever is waiting.
func TestEvery429ThatClosesOrExtendsTheWindowIsToldToOnRateLimit(t *testing.T) {
	clock := newClock()
	told := make(chan time.Time, 10)
	pauses := make(chan time.Time, 10)
	q := New(WithClock(clock),
		WithOnRateLimit(func(until time.Time) { told <- until }),
		WithOnPause(func(until time.Time) { pauses <- until }))
	g := newGate()
	// Background work alone: nobody waits, WithOnPause stays silent.
	r := do(q, context.Background(), Job{Priority: Background, Kind: Read, Run: g.run})
	waitStarted(t, g)
	first := clock.Now().Add(time.Second)
	g.release <- throttled(clock, time.Second)
	if got := receive(t, told); !got.Equal(first) {
		t.Fatalf("told %v, want %v", got, first)
	}
	select {
	case <-pauses:
		t.Fatal("WithOnPause was told without a person waiting")
	default:
	}
	// The retry waits for the window; when it runs into a longer one, the
	// extension is told too.
	waitPaused(t, q, first)
	clock.Advance(time.Second)
	waitStarted(t, g)
	second := clock.Now().Add(3 * time.Second)
	g.release <- throttled(clock, 3*time.Second)
	if got := receive(t, told); !got.Equal(second) {
		t.Fatalf("told %v, want %v", got, second)
	}
	if a := answer(t, r); a.Status != 429 {
		t.Fatalf("got %v", a.Class)
	}
	// A person's write that runs into a 429 is told like background work;
	// the queue retries it after the window (the server cannot have acted).
	clock.Advance(3 * time.Second)
	h := newGate()
	r2 := do(q, context.Background(), Job{Priority: User, Kind: Write, Run: h.run})
	waitStarted(t, h)
	later := clock.Now().Add(time.Hour)
	h.release <- throttled(clock, time.Hour)
	if got := receive(t, told); !got.Equal(later) {
		t.Fatalf("told %v, want %v", got, later)
	}
	clock.Advance(time.Hour)
	waitStarted(t, h)
	h.release <- ok()
	if !answer(t, r2).OK() {
		t.Fatal("the retry did not run")
	}
	// A 429 whose window ends before the current one changes nothing and
	// is not told.
	i := newGate()
	r3 := do(q, context.Background(), Job{Priority: User, Kind: Write, Run: i.run})
	waitStarted(t, i)
	q.mu.Lock()
	q.pausedUntil = clock.Now().Add(time.Minute) // test seam: a window already closed
	q.mu.Unlock()
	i.release <- throttled(clock, -time.Minute) // in the past: the default pause from now, shorter than the minute
	select {
	case v := <-told:
		t.Fatalf("told of a shorter window: %v", v)
	case <-time.After(50 * time.Millisecond):
	}
	clock.Advance(time.Minute)
	waitStarted(t, i)
	i.release <- ok()
	if !answer(t, r3).OK() {
		t.Fatal("the retry did not run after the minute")
	}
	select {
	case v := <-told:
		t.Fatalf("told once more: %v", v)
	default:
	}
}

func receive(t *testing.T, ch <-chan time.Time) time.Time {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(2 * time.Second):
		t.Fatal("the observer was not told")
	}
	return time.Time{}
}

// The place is given back before the observer runs: a panic in it loses no
// place, and the next job runs once the window opens.
func TestAPanicInOnRateLimitLosesNoPlace(t *testing.T) {
	clock := newClock()
	q := New(WithClock(clock), WithOnRateLimit(func(time.Time) { panic("observer fault") }))
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("the panic was swallowed")
			}
		}()
		// Unshared: the job runs on this goroutine, so the panic rises here.
		q.Do(context.Background(), Job{Priority: User, Kind: Write, Run: func(context.Context) transport.Answer {
			return throttled(clock, time.Second)
		}})
	}()
	clock.Advance(time.Second)
	h := newGate()
	r := do(q, context.Background(), Job{Priority: User, Kind: Write, Run: h.run})
	waitStarted(t, h)
	h.release <- ok()
	if !answer(t, r).OK() {
		t.Fatal("the next job did not run")
	}
	idle(t, q)
}

// A 429 that pauses tells both observers in one step, the pause being set
// before either runs: a job waiting for a place cannot slip into the window
// between them.
func TestTheWindowIsClosedBeforeAnyObserverRuns(t *testing.T) {
	clock := newClock()
	var q *Queue
	seen := make(chan time.Time, 2)
	q = New(WithClock(clock), WithOnRateLimit(func(until time.Time) {
		q.mu.Lock()
		seen <- q.pausedUntil
		q.mu.Unlock()
	}))
	g := newGate()
	r := do(q, context.Background(), Job{Priority: User, Kind: Write, Run: g.run})
	waitStarted(t, g)
	until := clock.Now().Add(time.Second)
	g.release <- throttled(clock, time.Second)
	if got := receive(t, seen); !got.Equal(until) {
		t.Fatalf("the window was %v when the observer ran, want %v", got, until)
	}
	clock.Advance(time.Second) // the write is retried after the window
	waitStarted(t, g)
	g.release <- ok()
	if !answer(t, r).OK() {
		t.Fatal("the retry did not run")
	}
}

var _ = transport.Object
