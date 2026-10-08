package queue

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/worker"
)

func where(t *testing.T, s *worker.Supervisor) string {
	t.Helper()
	var fatal *worker.FatalError
	if !errors.As(s.Err(), &fatal) {
		t.Fatalf("no fatal error: %v", s.Err())
	}
	return fatal.Where
}

// idle fails unless every place is free, nothing waits, nothing is shared and
// the lock is free.
func idle(t *testing.T, q *Queue) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if q.mu.TryLock() {
			ok := q.running == 0 && q.bgRunning == 0 && len(q.waiting) == 0 && len(q.shared) == 0
			q.mu.Unlock()
			if ok {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("the queue is not idle: running %d, waiting %d, shared %d", q.running, len(q.waiting), len(q.shared))
}

// A shared request belongs to the queue: its first caller leaving cancels
// nothing while another still waits, the last one leaving cancels it.
func TestASharedRequestRunsOnTheQueuesContext(t *testing.T) {
	life := worker.New(context.Background())
	q := New(WithClock(newClock()), WithLifetime(life))
	g := newGate()
	first, cancelFirst := context.WithCancel(context.Background())
	second, cancelSecond := context.WithCancel(context.Background())
	defer cancelSecond()
	r1 := do(q, first, Job{Priority: User, Kind: Read, Key: "k", Run: g.run})
	waitStarted(t, g)
	r2 := do(q, second, Job{Priority: User, Kind: Read, Key: "k", Run: g.run})
	waitJoined(t, q, "k", 2)
	shared := <-g.ctxs

	cancelFirst()
	if a := answer(t, r1); a.Class != transport.Canceled {
		t.Fatalf("first: %v", a.Class)
	}
	if shared.Err() != nil {
		t.Fatal("the first caller leaving canceled the shared request")
	}
	g.release <- ok()
	if a := answer(t, r2); !a.OK() {
		t.Fatalf("second: %v", a.Class)
	}

	r3 := do(q, second, Job{Priority: User, Kind: Read, Key: "k2", Run: g.run})
	waitStarted(t, g)
	last := <-g.ctxs
	cancelSecond()
	answer(t, r3)
	select {
	case <-last.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("the last caller leaving did not cancel the request")
	}
	idle(t, q)
}

// A canceled request that returns late does not remove the request that
// replaced it under the same key: a new caller still joins the replacement.
func TestALateRequestDoesNotRemoveItsSuccessor(t *testing.T) {
	q := New(WithClock(newClock()))
	stuck, landed := make(chan struct{}), make(chan struct{})
	slow := func(context.Context) transport.Answer { <-stuck; return ok() } // ignores its context
	ctx, cancel := context.WithCancel(context.Background())
	r1 := do(q, ctx, Job{Priority: User, Kind: Read, Key: "k", Run: slow})
	waitJoined(t, q, "k", 1)
	q.mu.Lock()
	old := q.shared["k"]
	q.mu.Unlock()
	cancel()
	answer(t, r1)

	g := newGate()
	r2 := do(q, context.Background(), Job{Priority: User, Kind: Read, Key: "k", Run: g.run})
	waitStarted(t, g)
	go func() { <-old.done; close(landed) }()
	close(stuck) // the old request ends now
	select {
	case <-landed:
	case <-time.After(2 * time.Second):
		t.Fatal("the old request did not end")
	}
	q.mu.Lock()
	successor := q.shared["k"]
	q.mu.Unlock()
	if successor == nil || successor == old {
		t.Fatal("the late request removed its successor")
	}
	r3 := do(q, context.Background(), Job{Priority: User, Kind: Read, Key: "k", Run: g.run})
	waitJoined(t, q, "k", 2)
	g.release <- ok()
	if !answer(t, r2).OK() || !answer(t, r3).OK() || g.calls.Load() != 1 {
		t.Fatalf("calls %d", g.calls.Load())
	}
}

// Ending the lifetime ends shared requests and the wake-up of a pause, and
// Stop waits for both.
func TestTheLifetimeEndsSharedRequestsAndPauses(t *testing.T) {
	life := worker.New(context.Background())
	clock := newClock()
	q := New(WithClock(clock), WithLifetime(life))
	g, h := newGate(), newGate()
	r := do(q, context.Background(), Job{Priority: User, Kind: Read, Key: "k", Run: g.run})
	waitStarted(t, g)
	p := do(q, context.Background(), Job{Priority: User, Kind: Read, Run: h.run})
	waitStarted(t, h)
	h.release <- throttled(clock, time.Hour) // a pause nobody advances past
	waitPaused(t, q, clock.Now().Add(time.Hour))

	if !life.Stop(2 * time.Second) {
		t.Fatal("the queue's goroutines outlived its lifetime")
	}
	if a := answer(t, r); a.Class != transport.Canceled {
		t.Fatalf("shared: %v", a.Class)
	}
	if a := answer(t, p); a.Class != transport.Canceled {
		t.Fatalf("paused retry: %v", a.Class)
	}
	if life.Err() != nil {
		t.Fatal(life.Err())
	}
	// After the lifetime nothing starts and nothing waits forever.
	if a := answer(t, do(q, context.Background(), Job{Priority: User, Kind: Read, Key: "k", Run: g.run})); a.Class != transport.Canceled {
		t.Fatalf("after the end: %v", a.Class)
	}
}

// A panic in a shared request is the lifetime's fatal error; its waiters get
// "canceled", never an empty object, and the place, the lock and the key are
// free again.
func TestAPanicInASharedRequestFreesEverything(t *testing.T) {
	life := worker.New(context.Background())
	q := New(WithClock(newClock()), WithLifetime(life))
	g := newGate()
	boom := func(ctx context.Context) transport.Answer {
		g.run(ctx) // waits for the release, so a second waiter can join
		panic("flight fault")
	}
	r1 := do(q, context.Background(), Job{Priority: User, Kind: Read, Key: "k", Run: boom})
	waitStarted(t, g)
	r2 := do(q, context.Background(), Job{Priority: Background, Kind: Read, Key: "k", Run: boom})
	waitJoined(t, q, "k", 2)
	g.release <- ok()
	for _, r := range []<-chan transport.Answer{r1, r2} {
		if a := answer(t, r); a.Class != transport.Canceled || a.OK() {
			t.Fatalf("a waiter got %v", a.Class)
		}
	}
	if got := where(t, life); got != "queue: shared read" {
		t.Fatalf("recorded at %q", got)
	}
	idle(t, q)
}

// A panic in an unshared job rises to its caller (the gateway recovers it)
// and gives the place back on its way.
func TestAPanicInAJobGivesItsPlaceBack(t *testing.T) {
	q := New(WithClock(newClock()))
	func() {
		defer func() { _ = recover() }()
		q.Do(context.Background(), Job{Priority: User, Kind: Read, Run: func(context.Context) transport.Answer { panic("job fault") }})
	}()
	idle(t, q)
	g := newGate()
	r := do(q, context.Background(), Job{Priority: User, Run: g.run})
	waitStarted(t, g)
	g.release <- ok()
	if !answer(t, r).OK() {
		t.Fatal("the queue did not run the next job")
	}
}

// panicClock panics once, on the first Now after one of its timers fired:
// that is the pause's wake-up asking whether the window is open.
type panicClock struct {
	*fakeClock
	armed atomic.Bool
}

func (c *panicClock) Now() time.Time {
	if c.armed.CompareAndSwap(true, false) {
		panic("pause fault")
	}
	return c.fakeClock.Now()
}

func TestAPanicInThePauseWorkerFreesTheLock(t *testing.T) {
	life := worker.New(context.Background())
	clock := &panicClock{fakeClock: newClock()}
	q := New(WithClock(clock), WithLifetime(life))
	g := newGate()
	r := do(q, context.Background(), Job{Priority: User, Kind: Read, Key: "k", Run: g.run})
	waitStarted(t, g)
	g.release <- throttled(clock.fakeClock, time.Second)
	waitPaused(t, q, clock.fakeClock.Now().Add(time.Second))
	clock.armed.Store(true)
	clock.Advance(time.Second)

	select {
	case <-life.Failed():
	case <-time.After(2 * time.Second):
		t.Fatal("the pause worker's panic was not recorded")
	}
	if got := where(t, life); got != "queue: pause" {
		t.Fatalf("recorded at %q", got)
	}
	if a := answer(t, r); a.Class != transport.Canceled {
		t.Fatalf("the waiter got %v", a.Class)
	}
	idle(t, q)
}

// Recording a fatal error needs no lock of the queue: it returns while the
// queue is held.
func TestRecordingAFatalErrorDoesNotWaitForTheQueue(t *testing.T) {
	life := worker.New(context.Background())
	q := New(WithLifetime(life))
	q.mu.Lock()
	defer q.mu.Unlock()
	done := make(chan struct{})
	go func() {
		worker.Run(q.life, "gateway: inbox", func() int { panic("x") })
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("recording waited for the queue's lock")
	}
}
