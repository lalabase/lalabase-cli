// Package queue is the one queue every request to the server goes through,
// REST and MCP alike (plan, Request control):
//
//   - at most two requests at once; background work takes at most one of the
//     two places, so it always leaves one for the person's actions;
//   - the person's actions go first; background work is canceled through its
//     context when the selection moves on;
//   - identical reads in flight share one request, which belongs to none of
//     its callers and is canceled only when all of them left;
//   - after a 429 nothing starts until the window opens; a person's action
//     waits too and is told so (WithOnPause);
//   - a job is retried at most once: a read on any temporary failure, a write
//     only when the server cannot have acted on it (a 429, or nothing was
//     sent). A retry runs the same job, so it carries the same idempotency key;
//   - the queue's own goroutines (a shared read, the wake-up after a pause)
//     run under one lifetime (WithLifetime, internal/worker). It ends them;
//     a panic in them is recovered there and becomes its fatal error, and
//     on its way up gives back the place, the lock and the waiters' answer
//     (canceled, never an empty "object").
package queue

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/worker"
)

// Priority orders waiting jobs.
type Priority int

const (
	// User: an action of the person (opening, writing).
	User Priority = iota
	// Background: a refresh or a preview.
	Background
)

// Kind decides what may be shared and retried.
type Kind int

const (
	Read Kind = iota
	Write
)

// Job is one request.
type Job struct {
	Priority Priority
	Kind     Kind
	// Key identifies a read for sharing (see ReadKey); empty: never shared.
	// A write is never shared, whatever its key.
	Key string
	// Run performs the request.
	Run func(ctx context.Context) transport.Answer
}

// Clock is time as the queue sees it (tests replace it).
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Option changes a Queue at construction.
type Option func(*Queue)

// WithClock replaces the clock.
func WithClock(c Clock) Option { return func(q *Queue) { q.clock = c } }

// WithOnPause is told when a person's action has to wait for a rate limit
// window, and until when.
func WithOnPause(f func(until time.Time)) Option { return func(q *Queue) { q.onPause = f } }

// WithLifetime runs the queue's goroutines under s: shared reads and pause
// timers end with its lifetime, and a panic in them becomes its fatal error.
// Without it the queue has a lifetime of its own that never ends.
func WithLifetime(s *worker.Supervisor) Option { return func(q *Queue) { q.life = s } }

// WithRetryDelay sets the wait before retrying a read after a temporary
// failure that was no 429 (a 429 waits for its window instead).
func WithRetryDelay(d time.Duration) Option { return func(q *Queue) { q.retryDelay = d } }

const (
	slots           = 2
	backgroundSlots = 1
	// defaultPause is the window of a 429 that names none in the future:
	// never zero, or the retry would run straight into it.
	defaultPause = 5 * time.Second
)

// Queue schedules jobs.
type Queue struct {
	mu          sync.Mutex
	clock       Clock
	life        *worker.Supervisor
	onPause     func(time.Time)
	retryDelay  time.Duration
	running     int
	bgRunning   int
	pausedUntil time.Time
	timerFor    time.Time
	waiting     []*ticket
	shared      map[string]*flight
}

// New returns an empty queue.
func New(opts ...Option) *Queue {
	q := &Queue{clock: realClock{}, retryDelay: time.Second, shared: map[string]*flight{}}
	for _, opt := range opts {
		opt(q)
	}
	if q.life == nil {
		q.life = worker.New(context.Background())
	}
	return q
}

// ticket is one wait for a place.
type ticket struct {
	prio       Priority
	ready      chan struct{}
	granted    bool
	background bool // holds a background place
}

// flight is a read shared by its callers. Its priority follows the callers
// still waiting: a person joining lifts it, the last person leaving lets it
// fall back to background work.
type flight struct {
	ctx    context.Context
	cancel context.CancelFunc
	refs   int
	users  int // callers of priority User among refs
	prio   Priority
	ticket *ticket // the place it waits for, while it waits
	done   chan struct{}
	ans    transport.Answer
}

// setPrio changes a waiting flight's priority and lets the queue act on it.
// Returns the pause end when a person now waits in a closed window, for the
// caller to report outside the lock. Called with q.mu held.
func (q *Queue) setPrio(f *flight, prio Priority) (time.Time, bool) {
	if f.prio == prio {
		return time.Time{}, false
	}
	f.prio = prio
	if f.ticket == nil || f.ticket.granted {
		return time.Time{}, false
	}
	f.ticket.prio = prio
	q.dispatch()
	if prio == User && q.clock.Now().Before(q.pausedUntil) {
		return q.pausedUntil, true
	}
	return time.Time{}, false
}

func canceled() transport.Answer {
	return transport.Answer{Class: transport.Canceled, Message: "canceled before it was sent"}
}

// Do runs a job through the queue and returns its answer.
func (q *Queue) Do(ctx context.Context, job Job) transport.Answer {
	if job.Kind == Read && job.Key != "" {
		return q.doShared(ctx, job)
	}
	return q.run(ctx, job, nil)
}

func (q *Queue) doShared(ctx context.Context, job Job) transport.Answer {
	f, fresh, tell, mustTell := q.join(job)
	if fresh {
		runCtx := f.ctx
		started := q.life.Start("queue: shared read", func(context.Context) {
			ans := canceled()
			// Deferred, so a panic in the job still frees every waiter.
			defer func() { q.land(job.Key, f, ans) }()
			ans = q.run(runCtx, job, f)
		})
		if !started {
			q.land(job.Key, f, canceled())
		}
	}
	if mustTell && q.onPause != nil {
		q.tell(tell, func() { q.leave(job, f) })
	}

	select {
	case <-f.done:
		return f.ans
	case <-ctx.Done():
		q.leave(job, f)
		return canceled()
	}
}

// join adds the caller to the shared read of its key, or makes a new one
// (fresh) for the caller to start.
func (q *Queue) join(job Job) (f *flight, fresh bool, tell time.Time, mustTell bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	f, ok := q.shared[job.Key]
	if ok {
		f.refs++
		if job.Priority == User {
			f.users++
			tell, mustTell = q.setPrio(f, User)
		}
		return f, false, tell, mustTell
	}
	// The request belongs to the queue's lifetime, never to its first caller.
	runCtx, cancel := context.WithCancel(q.life.Context())
	f = &flight{ctx: runCtx, cancel: cancel, refs: 1, prio: job.Priority, done: make(chan struct{})}
	if job.Priority == User {
		f.users = 1
	}
	q.shared[job.Key] = f
	return f, true, time.Time{}, false
}

// leave takes a caller who gave up off its shared read.
func (q *Queue) leave(job Job, f *flight) {
	q.mu.Lock()
	defer q.mu.Unlock()
	f.refs--
	if job.Priority == User {
		f.users--
		if f.users == 0 {
			q.setPrio(f, Background)
		}
	}
	if f.refs == 0 {
		// Nobody waits any more: cancel it, and let a new caller start
		// afresh instead of joining a canceled request.
		f.cancel()
		if q.shared[job.Key] == f {
			delete(q.shared, job.Key)
		}
	}
}

// land ends a shared read with its answer. A late read never removes the
// one that replaced it under the same key.
func (q *Queue) land(key string, f *flight, ans transport.Answer) {
	f.cancel()
	q.mu.Lock()
	if q.shared[key] == f {
		delete(q.shared, key)
	}
	q.mu.Unlock()
	f.ans = ans
	close(f.done)
}

// run performs a job with at most one retry. f is the shared read it runs
// for, or nil.
func (q *Queue) run(ctx context.Context, job Job, f *flight) transport.Answer {
	for attempt := 0; ; attempt++ {
		t, err := q.acquire(ctx, job.Priority, f)
		if err != nil {
			return canceled()
		}
		ans := q.attempt(ctx, t, job)

		if attempt >= 1 || !retryable(job.Kind, ans) {
			return ans
		}
		if ans.Status != 429 && q.retryDelay > 0 {
			select {
			case <-q.clock.After(q.retryDelay):
			case <-ctx.Done():
				return canceled()
			case <-q.life.Context().Done():
				return canceled()
			}
		}
	}
}

// attempt runs the job on its place. A panic in the job gives the place
// back on its way up, so it is never lost.
func (q *Queue) attempt(ctx context.Context, t *ticket, job Job) transport.Answer {
	ran := false
	defer func() {
		if !ran {
			q.release(t)
		}
	}()
	ans := job.Run(ctx)
	ran = true
	q.finish(t, ans)
	return ans
}

// retryable: a read on any temporary failure; a write only when the server
// cannot have acted on it.
func retryable(kind Kind, ans transport.Answer) bool {
	if ans.Class != transport.Temporary {
		return false
	}
	if kind == Read {
		return true
	}
	return ans.Status == 429 || !ans.Sent
}

// finish gives a place back after a job ran. A 429 sets the pause in the
// same step, before the place goes to anybody: a job already waiting must not
// slip into the closed window in between. A later window never shortens an
// earlier, longer one. A person already waiting is told.
func (q *Queue) finish(t *ticket, ans transport.Answer) {
	until, tell := q.giveBack(t, ans)
	if tell && q.onPause != nil {
		q.onPause(until)
	}
}

func (q *Queue) giveBack(t *ticket, ans transport.Answer) (time.Time, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	paused := false
	// Every received 429 pauses, whatever its class (a body too large, a
	// caller that left); a window not in the future (Retry-After: 0, a past
	// date, a clock ahead of the server's) pauses the default.
	if ans.Status == 429 {
		until := ans.RetryAt
		if now := q.clock.Now(); !until.After(now) {
			until = now.Add(defaultPause)
		}
		if until.After(q.pausedUntil) {
			q.pausedUntil = until
			paused = true
		}
	}
	q.running--
	if t.background {
		q.bgRunning--
	}
	q.dispatch()
	until := q.pausedUntil
	return until, paused && q.clock.Now().Before(until) && q.personWaits()
}

// personWaits reports a person's action waiting for a place. Called with q.mu
// held.
func (q *Queue) personWaits() bool {
	for _, t := range q.waiting {
		if t.prio == User {
			return true
		}
	}
	return false
}

// acquire waits for a place.
func (q *Queue) acquire(ctx context.Context, prio Priority, f *flight) (*ticket, error) {
	t, prio, paused, until := q.enqueue(prio, f)
	if paused && prio == User && q.onPause != nil {
		q.tell(until, func() { q.abandon(t); q.clearTicket(f) })
	}

	select {
	case <-t.ready:
		if q.life.Context().Err() != nil {
			// Granted as the lifetime ended: select picked either.
			q.abandon(t)
			q.clearTicket(f)
			return nil, context.Canceled
		}
		q.clearTicket(f)
		return t, nil
	case <-ctx.Done():
		q.abandon(t)
		q.clearTicket(f)
		return nil, ctx.Err()
	case <-q.life.Context().Done():
		q.abandon(t)
		q.clearTicket(f)
		return nil, context.Canceled
	}
}

// tell reports a pause to the person. A panic in the callback undoes, on its
// way up, what the caller took (a place, a share), so none is lost.
func (q *Queue) tell(until time.Time, undo func()) {
	told := false
	defer func() {
		if !told {
			undo()
		}
	}()
	q.onPause(until)
	told = true
}

func (q *Queue) enqueue(prio Priority, f *flight) (*ticket, Priority, bool, time.Time) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if f != nil {
		prio = f.prio
	}
	t := &ticket{prio: prio, ready: make(chan struct{})}
	if f != nil {
		f.ticket = t
	}
	q.waiting = append(q.waiting, t)
	paused, until := q.clock.Now().Before(q.pausedUntil), q.pausedUntil
	q.dispatch()
	return t, prio, paused, until
}

// abandon withdraws a wait whose caller left; a place granted meanwhile goes
// back.
func (q *Queue) abandon(t *ticket) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if t.granted {
		q.running--
		if t.background {
			q.bgRunning--
		}
		q.dispatch()
		return
	}
	q.remove(t)
}

func (q *Queue) clearTicket(f *flight) {
	if f == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	f.ticket = nil
}

func (q *Queue) release(t *ticket) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.running--
	if t.background {
		q.bgRunning--
	}
	q.dispatch()
}

func (q *Queue) remove(t *ticket) {
	for i, w := range q.waiting {
		if w == t {
			q.waiting = append(q.waiting[:i], q.waiting[i+1:]...)
			return
		}
	}
}

// dispatch grants places, the person's waits first, each kind in order of
// arrival. While a rate limit window is closed nothing is granted and a timer
// calls dispatch again when it opens. Called with q.mu held.
func (q *Queue) dispatch() {
	now := q.clock.Now()
	if now.Before(q.pausedUntil) {
		if !q.timerFor.Equal(q.pausedUntil) {
			until := q.pausedUntil
			q.timerFor = until
			wake := q.clock.After(until.Sub(now))
			q.life.Start("queue: pause", func(ctx context.Context) {
				select {
				case <-wake:
				case <-ctx.Done():
					return
				}
				q.mu.Lock()
				defer q.mu.Unlock()
				// A timer for an earlier, since extended window only
				// dispatches; the marker belongs to the current one, so no
				// second timer is started for the same window.
				if q.timerFor.Equal(until) {
					q.timerFor = time.Time{}
				}
				q.dispatch()
			})
		}
		return
	}
	for q.running < slots {
		t := q.next()
		if t == nil {
			return
		}
		q.remove(t)
		q.running++
		if t.prio == Background {
			t.background = true
			q.bgRunning++
		}
		t.granted = true
		close(t.ready)
	}
}

// next is the wait to grant next, or nil: the first of the person's, else the
// first background wait if a background place is free.
func (q *Queue) next() *ticket {
	for _, t := range q.waiting {
		if t.prio == User {
			return t
		}
	}
	if q.bgRunning >= backgroundSlots {
		return nil
	}
	for _, t := range q.waiting {
		if t.prio == Background {
			return t
		}
	}
	return nil
}

// Sharers is the number of callers waiting for the shared read of key, for
// tests that must know a caller joined before the answer comes.
func (q *Queue) Sharers(key string) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	if f := q.shared[key]; f != nil {
		return f.refs
	}
	return 0
}

// ReadKey identifies a read for sharing: the server, the token's identity,
// the operation and every argument. Two reads share a request only when all
// of these are equal.
func ReadKey(server, tokenID, operation string, args map[string]any) string {
	encoded, err := json.Marshal(args) // map keys are sorted
	if err != nil {
		return ""
	}
	key, _ := json.Marshal([]string{server, tokenID, operation, string(encoded)})
	return string(key)
}
