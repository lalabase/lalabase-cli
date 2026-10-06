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
//     sent). A retry runs the same job, so it carries the same idempotency key.
package queue

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/gboyke/lalabase-cli/internal/transport"
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

// WithRetryDelay sets the wait before retrying a read after a temporary
// failure that was no 429 (a 429 waits for its window instead).
func WithRetryDelay(d time.Duration) Option { return func(q *Queue) { q.retryDelay = d } }

const (
	slots           = 2
	backgroundSlots = 1
)

// Queue schedules jobs.
type Queue struct {
	mu          sync.Mutex
	clock       Clock
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
	q.mu.Lock()
	var tell time.Time
	var mustTell bool
	f, ok := q.shared[job.Key]
	if ok {
		f.refs++
		if job.Priority == User {
			f.users++
			tell, mustTell = q.setPrio(f, User)
		}
	} else {
		runCtx, cancel := context.WithCancel(context.Background())
		f = &flight{cancel: cancel, refs: 1, prio: job.Priority, done: make(chan struct{})}
		if job.Priority == User {
			f.users = 1
		}
		q.shared[job.Key] = f
		go func() {
			f.ans = q.run(runCtx, job, f)
			cancel()
			q.mu.Lock()
			if q.shared[job.Key] == f {
				delete(q.shared, job.Key)
			}
			q.mu.Unlock()
			close(f.done)
		}()
	}
	q.mu.Unlock()
	if mustTell && q.onPause != nil {
		q.onPause(tell)
	}

	select {
	case <-f.done:
		return f.ans
	case <-ctx.Done():
		q.mu.Lock()
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
		q.mu.Unlock()
		return canceled()
	}
}

// run performs a job with at most one retry. f is the shared read it runs
// for, or nil.
func (q *Queue) run(ctx context.Context, job Job, f *flight) transport.Answer {
	for attempt := 0; ; attempt++ {
		t, err := q.acquire(ctx, job.Priority, f)
		if err != nil {
			return canceled()
		}
		ans := job.Run(ctx)
		q.finish(t, ans)

		if attempt >= 1 || !retryable(job.Kind, ans) {
			return ans
		}
		if ans.Status != 429 && q.retryDelay > 0 {
			select {
			case <-q.clock.After(q.retryDelay):
			case <-ctx.Done():
				return canceled()
			}
		}
	}
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
	q.mu.Lock()
	paused := false
	if ans.Class == transport.Temporary && ans.Status == 429 && ans.RetryAt.After(q.pausedUntil) {
		q.pausedUntil = ans.RetryAt
		paused = true
	}
	q.running--
	if t.background {
		q.bgRunning--
	}
	q.dispatch()
	until := q.pausedUntil
	tell := paused && q.clock.Now().Before(until) && q.personWaits()
	q.mu.Unlock()
	if tell && q.onPause != nil {
		q.onPause(until)
	}
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
	q.mu.Lock()
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
	q.mu.Unlock()

	if paused && prio == User && q.onPause != nil {
		q.onPause(until)
	}

	select {
	case <-t.ready:
		q.clearTicket(f)
		return t, nil
	case <-ctx.Done():
		q.mu.Lock()
		if t.granted {
			// Granted while the caller left: give the place back.
			q.mu.Unlock()
			q.release(t)
		} else {
			q.remove(t)
			q.mu.Unlock()
		}
		q.clearTicket(f)
		return nil, ctx.Err()
	}
}

func (q *Queue) clearTicket(f *flight) {
	if f == nil {
		return
	}
	q.mu.Lock()
	f.ticket = nil
	q.mu.Unlock()
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
			go func() {
				<-wake
				q.mu.Lock()
				// A timer for an earlier, since extended window only
				// dispatches; the marker belongs to the current one, so no
				// second timer is started for the same window.
				if q.timerFor.Equal(until) {
					q.timerFor = time.Time{}
				}
				q.dispatch()
				q.mu.Unlock()
			}()
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
