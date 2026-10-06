package queue

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gboyke/lalabase-cli/internal/transport"
)

// fakeClock fires After channels only when the test advances it.
type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []fakeWaiter
}

type fakeWaiter struct {
	at time.Time
	ch chan time.Time
}

func newClock() *fakeClock { return &fakeClock{now: time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	if d <= 0 {
		ch <- c.now
		return ch
	}
	c.waiters = append(c.waiters, fakeWaiter{at: c.now.Add(d), ch: ch})
	return ch
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	var keep []fakeWaiter
	for _, w := range c.waiters {
		if !w.at.After(c.now) {
			w.ch <- c.now
		} else {
			keep = append(keep, w)
		}
	}
	c.waiters = keep
	c.mu.Unlock()
}

// gate is a job that reports when it starts and finishes when released.
type gate struct {
	started chan struct{}
	release chan transport.Answer
	calls   atomic.Int32
	ctxs    chan context.Context
}

func newGate() *gate {
	return &gate{started: make(chan struct{}, 10), release: make(chan transport.Answer, 10), ctxs: make(chan context.Context, 10)}
}

func (g *gate) run(ctx context.Context) transport.Answer {
	g.calls.Add(1)
	g.ctxs <- ctx
	g.started <- struct{}{}
	select {
	case ans := <-g.release:
		return ans
	case <-ctx.Done():
		return transport.Answer{Class: transport.Canceled}
	}
}

func ok() transport.Answer { return transport.Answer{Class: transport.Object, Sent: true} }

func waitStarted(t *testing.T, g *gate) {
	t.Helper()
	select {
	case <-g.started:
	case <-time.After(2 * time.Second):
		t.Fatal("the job did not start")
	}
}

func notStarted(t *testing.T, g *gate) {
	t.Helper()
	select {
	case <-g.started:
		t.Fatal("the job started")
	case <-time.After(50 * time.Millisecond):
	}
}

func do(q *Queue, ctx context.Context, job Job) <-chan transport.Answer {
	out := make(chan transport.Answer, 1)
	go func() { out <- q.Do(ctx, job) }()
	return out
}

func answer(t *testing.T, ch <-chan transport.Answer) transport.Answer {
	t.Helper()
	select {
	case a := <-ch:
		return a
	case <-time.After(2 * time.Second):
		t.Fatal("no answer")
	}
	return transport.Answer{}
}

func TestAtMostTwoRequestsAtOnce(t *testing.T) {
	q := New(WithClock(newClock()))
	a, b, c := newGate(), newGate(), newGate()
	ra := do(q, context.Background(), Job{Priority: User, Run: a.run})
	waitStarted(t, a)
	do(q, context.Background(), Job{Priority: User, Run: b.run})
	waitStarted(t, b)
	do(q, context.Background(), Job{Priority: User, Run: c.run})
	notStarted(t, c)
	a.release <- ok()
	answer(t, ra)
	waitStarted(t, c)
}

// Background work takes at most one of the two places, so one stays free for
// the person.
func TestBackgroundWorkLeavesAPlaceForThePerson(t *testing.T) {
	q := New(WithClock(newClock()))
	bg1, bg2, user := newGate(), newGate(), newGate()
	do(q, context.Background(), Job{Priority: Background, Run: bg1.run})
	waitStarted(t, bg1)
	do(q, context.Background(), Job{Priority: Background, Run: bg2.run})
	notStarted(t, bg2)
	do(q, context.Background(), Job{Priority: User, Run: user.run})
	waitStarted(t, user)
}

func TestThePersonGoesFirst(t *testing.T) {
	q := New(WithClock(newClock()))
	user1, bgRunning, bgWaiting, user2 := newGate(), newGate(), newGate(), newGate()
	r1 := do(q, context.Background(), Job{Priority: User, Run: user1.run})
	waitStarted(t, user1)
	do(q, context.Background(), Job{Priority: Background, Run: bgRunning.run})
	waitStarted(t, bgRunning)
	do(q, context.Background(), Job{Priority: Background, Run: bgWaiting.run})
	waitWaiting(t, q, 1) // queued before the person
	do(q, context.Background(), Job{Priority: User, Run: user2.run})
	waitWaiting(t, q, 2)
	notStarted(t, user2)
	user1.release <- ok()
	answer(t, r1)
	waitStarted(t, user2)
	notStarted(t, bgWaiting)
}

// Identical reads in flight share one request. Its context belongs to none
// of the callers: the first one leaving does not cancel it for the others;
// only when every caller is gone is it canceled.
func TestIdenticalReadsShareOneRequest(t *testing.T) {
	q := New(WithClock(newClock()))
	g := newGate()
	job := Job{Priority: User, Kind: Read, Key: "k", Run: g.run}
	ctx1, cancel1 := context.WithCancel(context.Background())
	r1 := do(q, ctx1, job)
	waitStarted(t, g)
	r2 := do(q, context.Background(), job)
	waitJoined(t, q, "k", 2)
	cancel1()
	if a := answer(t, r1); a.Class != transport.Canceled {
		t.Fatalf("the caller that left: %v", a.Class)
	}
	g.release <- ok()
	if a := answer(t, r2); a.Class != transport.Object {
		t.Fatalf("the caller that stayed: %v", a.Class)
	}
	if g.calls.Load() != 1 {
		t.Fatalf("%d requests for one read", g.calls.Load())
	}
}

func TestASharedReadIsCanceledWhenEveryCallerLeft(t *testing.T) {
	q := New(WithClock(newClock()))
	g := newGate()
	job := Job{Priority: User, Kind: Read, Key: "k", Run: g.run}
	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())
	r1, r2 := do(q, ctx1, job), do(q, ctx2, job)
	waitStarted(t, g)
	waitJoined(t, q, "k", 2)
	runCtx := <-g.ctxs
	cancel1()
	answer(t, r1)
	if runCtx.Err() != nil {
		t.Fatal("canceled while a caller still waits")
	}
	cancel2()
	answer(t, r2)
	select {
	case <-runCtx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("the shared request kept running for nobody")
	}
	// A new caller after that starts afresh, never joins the canceled one.
	g2 := newGate()
	r3 := do(q, context.Background(), Job{Priority: User, Kind: Read, Key: "k", Run: g2.run})
	waitStarted(t, g2)
	g2.release <- ok()
	if a := answer(t, r3); a.Class != transport.Object {
		t.Fatalf("the new caller: %v", a.Class)
	}
}

// A person joining a background read that is still waiting lifts it ahead
// of other background work.
func TestAPersonJoiningAWaitingReadRaisesIt(t *testing.T) {
	q := New(WithClock(newClock()))
	u1, u2 := newGate(), newGate()
	r1 := do(q, context.Background(), Job{Priority: User, Run: u1.run})
	waitStarted(t, u1)
	do(q, context.Background(), Job{Priority: User, Run: u2.run})
	waitStarted(t, u2)

	other, preview := newGate(), newGate()
	do(q, context.Background(), Job{Priority: Background, Kind: Read, Key: "other", Run: other.run})
	waitWaiting(t, q, 1)
	do(q, context.Background(), Job{Priority: Background, Kind: Read, Key: "ticket-7", Run: preview.run})
	waitWaiting(t, q, 2)
	joined := do(q, context.Background(), Job{Priority: User, Kind: Read, Key: "ticket-7", Run: preview.run})
	waitJoined(t, q, "ticket-7", 2)

	u1.release <- ok()
	answer(t, r1)
	waitStarted(t, preview)
	notStarted(t, other)
	preview.release <- ok()
	if a := answer(t, joined); a.Class != transport.Object {
		t.Fatalf("joined: %v", a.Class)
	}
}

func TestWritesAreNeverShared(t *testing.T) {
	q := New(WithClock(newClock()))
	g := newGate()
	job := Job{Priority: User, Kind: Write, Key: "same", Run: g.run}
	do(q, context.Background(), job)
	do(q, context.Background(), job)
	waitStarted(t, g)
	waitStarted(t, g)
	if g.calls.Load() != 2 {
		t.Fatalf("%d calls", g.calls.Load())
	}
}

func TestACanceledWaitIsCanceledWithoutSending(t *testing.T) {
	q := New(WithClock(newClock()))
	a, b, c := newGate(), newGate(), newGate()
	do(q, context.Background(), Job{Priority: User, Run: a.run})
	do(q, context.Background(), Job{Priority: User, Run: b.run})
	waitStarted(t, a)
	waitStarted(t, b)
	ctx, cancel := context.WithCancel(context.Background())
	r := do(q, ctx, Job{Priority: User, Run: c.run})
	cancel()
	if got := answer(t, r); got.Class != transport.Canceled || got.Sent {
		t.Fatalf("got %v sent=%v", got.Class, got.Sent)
	}
	if c.calls.Load() != 0 {
		t.Fatal("a canceled job ran")
	}
}

// waitWaiting waits until n jobs wait for a place.
func waitWaiting(t *testing.T, q *Queue, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		q.mu.Lock()
		got := len(q.waiting)
		q.mu.Unlock()
		if got == n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("%d jobs did not queue up", n)
}

// waitJoined waits until n callers share the read with key.
func waitJoined(t *testing.T, q *Queue, key string, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		q.mu.Lock()
		f := q.shared[key]
		refs := 0
		if f != nil {
			refs = f.refs
		}
		q.mu.Unlock()
		if refs == n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("%d callers did not join %s", n, key)
}

// waitPaused waits until the queue has taken a 429 in, so a job submitted
// next meets the closed window and not the race before it.
func waitPaused(t *testing.T, q *Queue, until time.Time) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		q.mu.Lock()
		got := q.pausedUntil
		q.mu.Unlock()
		if !got.Before(until) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("the queue did not pause")
}

func throttled(clock *fakeClock, after time.Duration) transport.Answer {
	return transport.Answer{Class: transport.Temporary, Status: 429, RetryAt: clock.Now().Add(after), Sent: true}
}

// After a 429 everything waits until the window opens; a person's action
// waits too and is told so; the throttled job is tried once more.
func TestA429PausesEverythingUntilTheWindowOpens(t *testing.T) {
	clock := newClock()
	var told []time.Time
	var mu sync.Mutex
	q := New(WithClock(clock), WithOnPause(func(until time.Time) { mu.Lock(); told = append(told, until); mu.Unlock() }))

	g := newGate()
	r := do(q, context.Background(), Job{Priority: User, Kind: Read, Run: g.run})
	waitStarted(t, g)
	g.release <- throttled(clock, 30*time.Second)
	waitPaused(t, q, clock.Now().Add(30*time.Second))

	other := newGate()
	ro := do(q, context.Background(), Job{Priority: User, Run: other.run})
	notStarted(t, g)
	notStarted(t, other)

	clock.Advance(29 * time.Second)
	notStarted(t, g)
	clock.Advance(1 * time.Second)
	waitStarted(t, g)
	waitStarted(t, other)
	g.release <- ok()
	other.release <- ok()
	if a := answer(t, r); a.Class != transport.Object {
		t.Fatalf("retried read: %v", a.Class)
	}
	answer(t, ro)
	mu.Lock()
	defer mu.Unlock()
	if len(told) == 0 || !told[0].Equal(clock.Now()) {
		t.Fatalf("the person was told %v", told)
	}
}

// A shorter 429 never shortens a longer pause.
func TestAShorterPauseNeverShortensALongerOne(t *testing.T) {
	clock := newClock()
	q := New(WithClock(clock))
	a, b := newGate(), newGate()
	ra := do(q, context.Background(), Job{Priority: User, Kind: Read, Key: "a", Run: a.run})
	rb := do(q, context.Background(), Job{Priority: User, Kind: Read, Key: "b", Run: b.run})
	waitStarted(t, a)
	waitStarted(t, b)
	a.release <- throttled(clock, 60*time.Second)
	waitPaused(t, q, clock.Now().Add(60*time.Second))
	b.release <- throttled(clock, 10*time.Second)
	waitWaiting(t, q, 2) // both retries wait for the window

	clock.Advance(30 * time.Second)
	notStarted(t, a)
	notStarted(t, b)
	clock.Advance(30 * time.Second)
	waitStarted(t, a)
	waitStarted(t, b)
	a.release <- ok()
	b.release <- ok()
	answer(t, ra)
	answer(t, rb)
}

func TestCancelingAWaitDuringAPause(t *testing.T) {
	clock := newClock()
	q := New(WithClock(clock))
	g := newGate()
	go q.Do(context.Background(), Job{Priority: User, Kind: Read, Key: "a", Run: g.run})
	waitStarted(t, g)
	g.release <- throttled(clock, time.Hour)
	waitPaused(t, q, clock.Now().Add(time.Hour))

	ctx, cancel := context.WithCancel(context.Background())
	waiting := newGate()
	r := do(q, ctx, Job{Priority: User, Run: waiting.run})
	notStarted(t, waiting)
	cancel()
	if a := answer(t, r); a.Class != transport.Canceled {
		t.Fatalf("got %v", a.Class)
	}
}

// What is retried, and how often: at most once per job, reads on any
// temporary failure, writes only when the server cannot have acted (429, or
// nothing was sent). Every retry runs the same job, so it carries the same
// idempotency key.
func TestRetryRules(t *testing.T) {
	temporary := func(status int, sent bool) transport.Answer {
		return transport.Answer{Class: transport.Temporary, Status: status, Sent: sent}
	}
	cases := []struct {
		name  string
		kind  Kind
		first transport.Answer
		calls int32
	}{
		{"read, temporary", Read, temporary(503, true), 2},
		{"read, refused", Read, transport.Answer{Class: transport.Refused, Sent: true}, 1},
		{"read, protocol", Read, transport.Answer{Class: transport.Protocol, Sent: true}, 1},
		{"read, canceled", Read, transport.Answer{Class: transport.Canceled}, 1},
		{"write, temporary after sending", Write, temporary(0, true), 1},
		{"write, temporary 5xx", Write, temporary(503, true), 1},
		{"write, nothing sent", Write, temporary(0, false), 2},
		// Its retry waits the default pause (TestEvery429Pauses).
		{"write, 429", Write, transport.Answer{Class: transport.Temporary, Status: 429, Sent: true}, 2},
		{"write, invalid input before sending", Write, transport.Answer{Class: transport.InvalidInput, Sent: false}, 1},
		{"write, unauthenticated", Write, transport.Answer{Class: transport.Unauthenticated, Sent: true}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			clock := newClock()
			q := New(WithClock(clock), WithRetryDelay(0))
			r := do(q, context.Background(), Job{Priority: User, Kind: tc.kind, Run: func(context.Context) transport.Answer {
				if calls.Add(1) == 1 {
					return tc.first
				}
				return ok()
			}})
			if tc.first.Status == 429 {
				waitPaused(t, q, clock.Now().Add(defaultPause))
				clock.Advance(defaultPause)
			}
			got := answer(t, r)
			if calls.Load() != tc.calls {
				t.Fatalf("%d calls, want %d", calls.Load(), tc.calls)
			}
			if tc.calls == 1 && got.Class != tc.first.Class {
				t.Fatalf("got %v, want the first answer %v", got.Class, tc.first.Class)
			}
		})
	}
}

// A second failure is returned, never retried again: a job runs at most
// twice, a second 429 included (it still lengthens the pause).
func TestAJobRunsAtMostTwice(t *testing.T) {
	clock := newClock()
	q := New(WithClock(clock), WithRetryDelay(0))
	var calls atomic.Int32
	r := do(q, context.Background(), Job{Priority: User, Kind: Write, Run: func(context.Context) transport.Answer {
		calls.Add(1)
		return transport.Answer{Class: transport.Temporary, Status: 429, RetryAt: clock.Now().Add(10 * time.Second), Sent: true}
	}})
	waitPaused(t, q, clock.Now().Add(10*time.Second))
	waitWaiting(t, q, 1) // the retry waits for the window
	clock.Advance(10 * time.Second)
	got := answer(t, r)
	if got.Status != 429 || calls.Load() != 2 {
		t.Fatalf("got %v, %d calls", got.Status, calls.Load())
	}
	// The second 429 paused the queue again.
	next := newGate()
	do(q, context.Background(), Job{Priority: User, Run: next.run})
	notStarted(t, next)
	clock.Advance(10 * time.Second)
	waitStarted(t, next)
}

func TestReadKeysNameServerTokenOperationAndArguments(t *testing.T) {
	base := ReadKey("https://a.example", "tok-1", "get_ticket", map[string]any{"project_id": "p", "ticket_id": "t"})
	if again := ReadKey("https://a.example", "tok-1", "get_ticket", map[string]any{"ticket_id": "t", "project_id": "p"}); again != base {
		t.Error("argument order changed the key")
	}
	for _, other := range []string{
		ReadKey("https://b.example", "tok-1", "get_ticket", map[string]any{"project_id": "p", "ticket_id": "t"}),
		ReadKey("https://a.example", "tok-2", "get_ticket", map[string]any{"project_id": "p", "ticket_id": "t"}),
		ReadKey("https://a.example", "tok-1", "list_tickets", map[string]any{"project_id": "p", "ticket_id": "t"}),
		ReadKey("https://a.example", "tok-1", "get_ticket", map[string]any{"project_id": "p", "ticket_id": "u"}),
	} {
		if other == base {
			t.Errorf("a different request has the same key: %s", other)
		}
	}
}

// The case of the review: two jobs run, a third already waits, the first is
// throttled. The waiting job must not slip into the closed window between
// the place being freed and the pause being set; the person is told.
func TestA429StopsAJobThatAlreadyWaits(t *testing.T) {
	clock := newClock()
	var mu sync.Mutex
	var told []time.Time
	q := New(WithClock(clock), WithOnPause(func(until time.Time) { mu.Lock(); told = append(told, until); mu.Unlock() }))
	// The throttled job is background work: its own retry must not be what
	// tells the person, the waiting person's action must be.
	a, b, c := newGate(), newGate(), newGate()
	ra := do(q, context.Background(), Job{Priority: Background, Kind: Read, Key: "a", Run: a.run})
	rb := do(q, context.Background(), Job{Priority: User, Kind: Read, Key: "b", Run: b.run})
	waitStarted(t, a)
	waitStarted(t, b)
	rc := do(q, context.Background(), Job{Priority: User, Kind: Read, Key: "c", Run: c.run})
	waitWaiting(t, q, 1)

	until := clock.Now().Add(30 * time.Second)
	a.release <- throttled(clock, 30*time.Second)
	notStarted(t, c)
	b.release <- ok()
	answer(t, rb)
	notStarted(t, c)

	mu.Lock()
	wasTold := len(told) > 0 && told[len(told)-1].Equal(until)
	mu.Unlock()
	if !wasTold {
		t.Fatalf("the waiting person was not told of the pause: %v", told)
	}

	clock.Advance(30 * time.Second)
	waitStarted(t, c)
	waitStarted(t, a) // the throttled job's one retry
	a.release <- ok()
	c.release <- ok()
	answer(t, ra)
	answer(t, rc)
}

// The background place is given back: background work keeps running after
// its first job.
func TestBackgroundJobsRunOneAfterTheOther(t *testing.T) {
	q := New(WithClock(newClock()))
	for i := range 3 {
		g := newGate()
		r := do(q, context.Background(), Job{Priority: Background, Run: g.run})
		waitStarted(t, g)
		g.release <- ok()
		if a := answer(t, r); a.Class != transport.Object {
			t.Fatalf("background job %d: %v", i, a.Class)
		}
	}
}

// A caller that leaves at the very moment its place is granted gives the
// place back. With a context already canceled and a free place, the queue
// grants and the caller leaves in the same instant; repeated, a lost place
// would leave the queue with none.
func TestAPlaceIsNeverLostWhenACallerLeavesAtTheGrant(t *testing.T) {
	q := New(WithClock(newClock()))
	done, cancel := context.WithCancel(context.Background())
	cancel()
	for range 200 {
		answer(t, do(q, done, Job{Priority: User, Run: func(context.Context) transport.Answer { return ok() }}))
	}
	q.mu.Lock()
	running := q.running
	q.mu.Unlock()
	if running != 0 {
		t.Fatalf("%d places were never given back", running)
	}
	a, b := newGate(), newGate()
	do(q, context.Background(), Job{Priority: User, Run: a.run})
	do(q, context.Background(), Job{Priority: User, Run: b.run})
	waitStarted(t, a)
	waitStarted(t, b)
}

// A background read that waits only because the background place is taken
// starts at once when a person joins it and a place is free.
func TestARaisedReadStartsAtOnceWhenAPlaceIsFree(t *testing.T) {
	q := New(WithClock(newClock()))
	refresh, preview := newGate(), newGate()
	do(q, context.Background(), Job{Priority: Background, Run: refresh.run})
	waitStarted(t, refresh)
	do(q, context.Background(), Job{Priority: Background, Kind: Read, Key: "ticket-7", Run: preview.run})
	waitWaiting(t, q, 1)
	notStarted(t, preview)

	joined := do(q, context.Background(), Job{Priority: User, Kind: Read, Key: "ticket-7", Run: preview.run})
	waitStarted(t, preview)
	preview.release <- ok()
	answer(t, joined)
}

// When the person who lifted a waiting read leaves again, it falls back to
// background work: two background jobs never hold both places.
func TestARaisedReadFallsBackWhenThePersonLeaves(t *testing.T) {
	q := New(WithClock(newClock()))
	refresh, person, preview := newGate(), newGate(), newGate()
	do(q, context.Background(), Job{Priority: Background, Run: refresh.run})
	waitStarted(t, refresh)
	rp := do(q, context.Background(), Job{Priority: User, Run: person.run})
	waitStarted(t, person)

	do(q, context.Background(), Job{Priority: Background, Kind: Read, Key: "ticket-7", Run: preview.run})
	waitWaiting(t, q, 1)
	ctx, leave := context.WithCancel(context.Background())
	joined := do(q, ctx, Job{Priority: User, Kind: Read, Key: "ticket-7", Run: preview.run})
	waitJoined(t, q, "ticket-7", 2)
	leave()
	answer(t, joined)

	person.release <- ok()
	answer(t, rp)
	notStarted(t, preview) // the background place is still the refresh's
}

// A read retried after a temporary failure that was no 429 waits the retry
// delay first.
func TestARetryWaitsTheDelay(t *testing.T) {
	clock := newClock()
	q := New(WithClock(clock), WithRetryDelay(time.Second))
	g := newGate()
	r := do(q, context.Background(), Job{Priority: User, Kind: Read, Run: g.run})
	waitStarted(t, g)
	g.release <- transport.Answer{Class: transport.Temporary, Status: 503, Sent: true}
	notStarted(t, g)
	waitTimers(t, clock, 1)
	clock.Advance(time.Second)
	waitStarted(t, g)
	g.release <- ok()
	answer(t, r)
}

// waitTimers waits until n timers wait on the fake clock.
func waitTimers(t *testing.T, c *fakeClock, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		got := len(c.waiters)
		c.mu.Unlock()
		if got >= n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("%d timers did not start", n)
}

// A timer for a window that was extended meanwhile does not start a second
// timer for the new window: one window, one timer.
func TestAnExtendedPauseKeepsOneTimer(t *testing.T) {
	clock := newClock()
	q := New(WithClock(clock))
	a, b := newGate(), newGate()
	do(q, context.Background(), Job{Priority: User, Kind: Read, Key: "a", Run: a.run})
	do(q, context.Background(), Job{Priority: User, Kind: Read, Key: "b", Run: b.run})
	waitStarted(t, a)
	waitStarted(t, b)
	a.release <- throttled(clock, 10*time.Second)
	waitPaused(t, q, clock.Now().Add(10*time.Second))
	waitWaiting(t, q, 1)
	b.release <- throttled(clock, 60*time.Second)
	waitPaused(t, q, clock.Now().Add(60*time.Second))
	waitWaiting(t, q, 2)
	waitTimers(t, clock, 2)

	clock.Advance(10 * time.Second) // the first window's timer fires
	time.Sleep(50 * time.Millisecond)
	clock.mu.Lock()
	pending := len(clock.waiters)
	clock.mu.Unlock()
	if pending != 1 {
		t.Fatalf("%d timers wait for one window", pending)
	}
}

// A 429 that names no window (or lost it) still pauses: the default wait,
// never zero.
func TestA429WithoutAWindowPausesTheDefault(t *testing.T) {
	clock := newClock()
	q := New(WithClock(clock), WithRetryDelay(0))
	g := newGate()
	r := do(q, context.Background(), Job{Priority: User, Kind: Read, Run: g.run})
	waitStarted(t, g)
	g.release <- transport.Answer{Class: transport.Temporary, Status: 429, Sent: true}
	waitPaused(t, q, clock.Now().Add(defaultPause))
	notStarted(t, g)
	clock.Advance(defaultPause)
	waitStarted(t, g)
	g.release <- ok()
	answer(t, r)
}

// A shared read that every caller left can finish after a new one with the
// same key started; it must not remove the new one, or a third caller would
// start a third request.
func TestALateEndOfAnOldReadDoesNotRemoveItsSuccessor(t *testing.T) {
	q := New(WithClock(newClock()))
	var calls atomic.Int32
	releaseOld := make(chan struct{})
	startedNew := make(chan struct{}, 1)
	releaseNew := make(chan struct{})
	run := func(context.Context) transport.Answer {
		// Ignores its context, so the order of the two ends is the test's.
		if calls.Add(1) == 1 {
			<-releaseOld
		} else {
			startedNew <- struct{}{}
			<-releaseNew
		}
		return ok()
	}
	job := Job{Priority: User, Kind: Read, Key: "k", Run: run}

	ctx, leave := context.WithCancel(context.Background())
	first := do(q, ctx, job)
	waitJoined(t, q, "k", 1)
	leave()
	answer(t, first)

	second := do(q, context.Background(), job)
	select {
	case <-startedNew:
	case <-time.After(2 * time.Second):
		t.Fatal("the new read did not start")
	}
	close(releaseOld) // the old read ends after its successor started
	time.Sleep(50 * time.Millisecond)

	third := do(q, context.Background(), job)
	waitJoined(t, q, "k", 2)
	close(releaseNew)
	answer(t, second)
	answer(t, third)
	if calls.Load() != 2 {
		t.Fatalf("%d requests, want 2", calls.Load())
	}
}

// A person joining a background read that waits in a closed window is told
// of the pause, once, with its end.
func TestAPersonJoiningAReadInAPauseIsTold(t *testing.T) {
	clock := newClock()
	var mu sync.Mutex
	var told []time.Time
	q := New(WithClock(clock), WithOnPause(func(until time.Time) { mu.Lock(); told = append(told, until); mu.Unlock() }))
	a, b := newGate(), newGate()
	do(q, context.Background(), Job{Priority: Background, Kind: Read, Key: "a", Run: a.run})
	do(q, context.Background(), Job{Priority: User, Kind: Read, Key: "b", Run: b.run})
	waitStarted(t, a)
	waitStarted(t, b)
	until := clock.Now().Add(30 * time.Second)
	a.release <- throttled(clock, 30*time.Second) // background: its retry tells nobody
	waitPaused(t, q, until)
	b.release <- ok()

	preview := newGate()
	do(q, context.Background(), Job{Priority: Background, Kind: Read, Key: "p", Run: preview.run})
	waitJoined(t, q, "p", 1)
	mu.Lock()
	before := len(told)
	mu.Unlock()

	do(q, context.Background(), Job{Priority: User, Kind: Read, Key: "p", Run: preview.run})
	waitJoined(t, q, "p", 2)
	mu.Lock()
	defer mu.Unlock()
	if len(told) != before+1 || !told[len(told)-1].Equal(until) {
		t.Fatalf("told %v (before the person joined: %d)", told, before)
	}
}

// When one of two persons sharing a waiting read leaves, the other still
// waits: the read stays ahead of background work.
func TestASharedReadStaysAheadWhileAPersonStillWaits(t *testing.T) {
	q := New(WithClock(newClock()))
	refresh, person, preview := newGate(), newGate(), newGate()
	do(q, context.Background(), Job{Priority: Background, Run: refresh.run})
	waitStarted(t, refresh)
	rp := do(q, context.Background(), Job{Priority: User, Run: person.run})
	waitStarted(t, person)

	do(q, context.Background(), Job{Priority: Background, Kind: Read, Key: "t", Run: preview.run})
	waitWaiting(t, q, 1)
	ctx, leave := context.WithCancel(context.Background())
	gone := do(q, ctx, Job{Priority: User, Kind: Read, Key: "t", Run: preview.run})
	stays := do(q, context.Background(), Job{Priority: User, Kind: Read, Key: "t", Run: preview.run})
	waitJoined(t, q, "t", 3)
	leave()
	answer(t, gone)

	person.release <- ok()
	answer(t, rp)
	waitStarted(t, preview) // a person still waits for it
	preview.release <- ok()
	answer(t, stays)
}

// Every received 429 pauses, whatever its class: a body too large
// (Protocol) or a caller that left (Canceled) still met a closed window. A
// window that is not in the future (Retry-After: 0, a past date, a clock
// ahead of the server's) pauses the default; a longer pause stays.
func TestEvery429Pauses(t *testing.T) {
	cases := []struct {
		name string
		ans  func(now time.Time) transport.Answer
		want time.Duration
	}{
		{"too large", func(now time.Time) transport.Answer {
			return transport.Answer{Class: transport.Protocol, Status: 429, RetryAt: now.Add(30 * time.Second), Sent: true}
		}, 30 * time.Second},
		{"canceled", func(now time.Time) transport.Answer {
			return transport.Answer{Class: transport.Canceled, Status: 429, RetryAt: now.Add(30 * time.Second), Sent: true}
		}, 30 * time.Second},
		{"window now", func(now time.Time) transport.Answer {
			return transport.Answer{Class: transport.Temporary, Status: 429, RetryAt: now, Sent: true}
		}, defaultPause},
		{"window past", func(now time.Time) transport.Answer {
			return transport.Answer{Class: transport.Temporary, Status: 429, RetryAt: now.Add(-time.Hour), Sent: true}
		}, defaultPause},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clock := newClock()
			q := New(WithClock(clock), WithRetryDelay(0))
			first, next := newGate(), newGate()
			r := do(q, context.Background(), Job{Priority: Background, Kind: Write, Run: first.run})
			waitStarted(t, first)
			first.release <- tc.ans(clock.Now())
			waitPaused(t, q, clock.Now().Add(tc.want))
			n := do(q, context.Background(), Job{Priority: User, Kind: Read, Run: next.run})
			notStarted(t, next)
			// The first job may be retried (a Temporary write after a 429);
			// its retry finds its answer waiting.
			first.release <- ok()
			clock.Advance(tc.want)
			waitStarted(t, next)
			next.release <- ok()
			answer(t, n)
			answer(t, r)
		})
	}
}

// A 429 whose window is not in the future does not shorten a longer pause.
func TestADefaultPauseDoesNotShortenALongerOne(t *testing.T) {
	clock := newClock()
	q := New(WithClock(clock), WithRetryDelay(0))
	a, b := newGate(), newGate()
	ra := do(q, context.Background(), Job{Priority: Background, Kind: Read, Key: "a", Run: a.run})
	rb := do(q, context.Background(), Job{Priority: User, Kind: Read, Key: "b", Run: b.run})
	waitStarted(t, a)
	waitStarted(t, b)
	long := clock.Now().Add(time.Minute)
	a.release <- transport.Answer{Class: transport.Canceled, Status: 429, RetryAt: long, Sent: true}
	waitPaused(t, q, long)
	b.release <- transport.Answer{Class: transport.Canceled, Status: 429, RetryAt: clock.Now(), Sent: true}
	answer(t, ra)
	answer(t, rb)
	waitPaused(t, q, long)
}
