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

// where is the place of the supervisor's fatal error. The waiters of a
// shared read are released by the deferred landing before the panic reaches
// the supervisor's recovery, so the record may come a moment after them.
func where(t *testing.T, s *worker.Supervisor) string {
	t.Helper()
	select {
	case <-s.Failed():
	case <-time.After(2 * time.Second):
		t.Fatalf("no fatal error: %v", s.Err())
	}
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

// Review #9, finding 5: a panic in the pause callback gives back the place
// and the share it took on its way up.
func TestAPanicInOnPauseLosesNoPlace(t *testing.T) {
	clock := newClock()
	var armed atomic.Bool
	q := New(WithClock(clock), WithOnPause(func(time.Time) {
		if armed.Load() {
			panic("onPause fault")
		}
	}))
	g := newGate()
	r := do(q, context.Background(), Job{Priority: Background, Kind: Read, Key: "bg", Run: g.run})
	waitStarted(t, g)
	g.release <- throttled(clock, time.Second)
	waitPaused(t, q, clock.Now().Add(time.Second))
	armed.Store(true)

	call := func(job Job) {
		defer func() { _ = recover() }()
		q.Do(context.Background(), job)
	}
	call(Job{Priority: User, Kind: Write, Run: g.run})           // waits in the pause: acquire tells
	call(Job{Priority: User, Kind: Read, Key: "bg", Run: g.run}) // joins the waiting retry: join tells
	if n := q.Sharers("bg"); n != 1 {
		t.Fatalf("the shared read keeps %d callers", n)
	}
	armed.Store(false)
	clock.Advance(time.Second)
	waitStarted(t, g) // the retry
	g.release <- ok()
	answer(t, r)
	idle(t, q)
}

// Review #9, finding 6: once the lifetime ended no job starts, even when its
// place is granted at the same moment.
func TestNoJobStartsAfterTheLifetimeEnded(t *testing.T) {
	life := worker.New(context.Background())
	q := New(WithLifetime(life))
	life.Stop(time.Second)
	var ran atomic.Int32
	for range 200 {
		ans := q.Do(context.Background(), Job{Priority: User, Kind: Write, Run: func(context.Context) transport.Answer { ran.Add(1); return ok() }})
		if ans.Class != transport.Canceled {
			t.Fatalf("got %v", ans.Class)
		}
	}
	if n := ran.Load(); n != 0 {
		t.Fatalf("%d writes ran after the lifetime ended", n)
	}
	idle(t, q)
}

// Review #9, round 3: DoShared names the request an answer came from. The
// waiters of one request get one flight; a later request under the same key
// is another flight, so a copy of an answer is never mistaken for a
// separate answer that happens to share its key. Unshared work has none.
func TestDoSharedNamesTheRequest(t *testing.T) {
	q := New()
	g := newGate()
	type got struct {
		ans    transport.Answer
		flight Flight
	}
	shared := func() <-chan got {
		out := make(chan got, 1)
		go func() {
			ans, f := q.DoShared(context.Background(), Job{Priority: User, Kind: Read, Key: "k", Run: g.run})
			out <- got{ans, f}
		}()
		return out
	}
	receive := func(ch <-chan got) got {
		t.Helper()
		select {
		case v := <-ch:
			return v
		case <-time.After(2 * time.Second):
			t.Fatal("no answer")
		}
		return got{}
	}
	first := shared()
	waitStarted(t, g)
	second := shared()
	for deadline := time.Now().Add(2 * time.Second); q.Sharers("k") != 2; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the second waiter did not join")
		}
	}
	g.release <- ok()
	a, b := receive(first), receive(second)
	if a.flight == 0 || a.flight != b.flight || !a.ans.OK() || !b.ans.OK() {
		t.Fatalf("one request, flights %d and %d", a.flight, b.flight)
	}
	later := shared()
	waitStarted(t, g)
	g.release <- ok()
	if c := receive(later); c.flight == 0 || c.flight == a.flight {
		t.Fatalf("a separate request reused flight %d", c.flight)
	}
	if _, f := q.DoShared(context.Background(), Job{Priority: User, Kind: Write, Run: func(context.Context) transport.Answer { return ok() }}); f != 0 {
		t.Fatalf("a write got flight %d", f)
	}
	idle(t, q)
}

// The wait before a retry ends with the lifetime, also for work that is not
// shared and runs on its caller's context, which the lifetime does not end.
func TestARetryWaitEndsWithTheLifetime(t *testing.T) {
	life := worker.New(context.Background())
	q := New(WithLifetime(life), WithClock(newClock()), WithRetryDelay(time.Hour))
	g := newGate()
	r := do(q, context.Background(), Job{Priority: User, Kind: Read, Run: g.run}) // no key: not shared
	waitStarted(t, g)
	g.release <- transport.Answer{Class: transport.Temporary, Status: 503, Sent: true}
	life.Stop(0)
	if ans := answer(t, r); ans.Class != transport.Canceled {
		t.Fatalf("got %v", ans.Class)
	}
	if n := g.calls.Load(); n != 1 {
		t.Fatalf("%d calls after the lifetime ended", n)
	}
	idle(t, q)
}

// A place granted to a caller who left goes to the next one waiting at once,
// not when some other job happens to finish.
func TestAnAbandonedPlaceGoesToTheNextWaiter(t *testing.T) {
	q := New()
	holder := newGate()
	held := do(q, context.Background(), Job{Priority: User, Kind: Write, Run: holder.run})
	waitStarted(t, holder)
	left, _, _, _ := q.enqueue(User, nil) // the second place, granted at once
	if !left.granted {
		t.Fatal("the second place was not granted")
	}
	next := newGate()
	waiting := do(q, context.Background(), Job{Priority: User, Kind: Write, Run: next.run})
	for deadline := time.Now().Add(2 * time.Second); ; time.Sleep(time.Millisecond) {
		q.mu.Lock()
		n := len(q.waiting)
		q.mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the next caller is not waiting")
		}
	}
	q.abandon(left) // the caller left after its place was granted
	waitStarted(t, next)
	next.release <- ok()
	holder.release <- ok()
	answer(t, waiting)
	answer(t, held)
	idle(t, q)
}
