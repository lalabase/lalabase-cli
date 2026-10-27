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
//   - background work starts at most BackgroundBudget times per rolling
//     BudgetWindow, retries counted (C1 design: previews, refreshes and their
//     retries at most six per minute). What is held waits for the window to
//     roll on; the person's actions are never counted or held, and a
//     background read a person joins starts as theirs;
//   - idle work (C9b: the titles of the agents' tickets) is background work
//     below the background: it starts only while no background job waits
//     and fewer than IdleShare starts are in the window, so it never spends
//     the budget's other half, which stays the previews'. A read a preview
//     or a person joins starts as theirs;
//   - the queue's own goroutines (a shared read, the wake-up after a pause)
//     run under one lifetime (WithLifetime, internal/worker). It ends them;
//     a panic in them is recovered there and becomes its fatal error, and
//     on its way up gives back the place, the lock and the waiters' answer
//     (canceled, never an empty "object").
package queue

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
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
	// Idle: work nobody looks at yet (C9b), after every background job and
	// within IdleShare of the budget.
	Idle
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

// WithOnRateLimit is told of every received 429 that closes the window or
// extends it, whatever is waiting: the interface shows every pause, also
// one that only background work ran into. It runs outside the lock, after
// the place was given back, so a panic in it loses nothing of the queue.
// WithOnPause stays what it is: a person's own wait, for the auth commands.
func WithOnRateLimit(f func(until time.Time)) Option { return func(q *Queue) { q.onRateLimit = f } }

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
	// BackgroundBudget is the number of background starts in one
	// BudgetWindow, a retry being a start of its own.
	BackgroundBudget = 6
	BudgetWindow     = time.Minute
	// IdleShare is the part of the budget idle work may spend in a window.
	IdleShare = BackgroundBudget / 2
)

// Queue schedules jobs.
type Queue struct {
	mu          sync.Mutex
	clock       Clock
	life        *worker.Supervisor
	onPause     func(time.Time)
	onRateLimit func(time.Time)
	retryDelay  time.Duration
	running     int
	bgRunning   int
	pausedUntil time.Time
	timerFor    time.Time
	waiting     []*ticket
	shared      map[string]*flight
	// starts are the moments background work was started in the current
	// window, oldest first; budgetFor is the end of the window a timer waits
	// for, so one wake-up serves it.
	starts    []time.Time
	budgetFor time.Time
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
	// started is the moment the budget counted this grant; zero for a
	// person's grant. A grant abandoned before it ran gives it back.
	started time.Time
}

// flight is a read shared by its callers. Its priority follows the callers
// still waiting: a person joining lifts it, the last person leaving lets it
// fall back to background work, and the last background caller to idle.
type flight struct {
	id     atomic.Uint64 // its number, given at each send; 0 before the first
	ctx    context.Context
	cancel context.CancelFunc
	refs   int
	// callers counts refs by their priority; the flight has the highest.
	callers [Idle + 1]int
	prio    Priority
	ticket  *ticket // the place it waits for, while it waits
	done    chan struct{}
	ans     transport.Answer
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
	ans, _ := q.DoShared(ctx, job)
	return ans
}

// Flight names one request a shared read sent, numbered at the moment the
// request is handed to the transport (so the numbers follow the order of
// sending, as far as two goroutines at that moment can be told apart). Its
// waiters get the same flight; a later request under the same key, and a
// retry, get another. 0 is no shared read, or one not sent yet.
type Flight uint64

// flights numbers the requests of the process's shared reads.
var flights atomic.Uint64

// DoShared is Do that also names the request the answer came from, so a
// copy of one answer can be told from a separate answer under the same key.
func (q *Queue) DoShared(ctx context.Context, job Job) (transport.Answer, Flight) {
	if job.Kind == Read && job.Key != "" {
		return q.doShared(ctx, job)
	}
	return q.run(ctx, job, nil), 0
}

func (q *Queue) doShared(ctx context.Context, job Job) (transport.Answer, Flight) {
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
		return f.ans, f.flight()
	case <-ctx.Done():
		q.leave(job, f)
		return canceled(), f.flight()
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
		f.callers[job.Priority]++
		tell, mustTell = q.setPrio(f, f.highest())
		return f, false, tell, mustTell
	}
	// The request belongs to the queue's lifetime, never to its first caller.
	runCtx, cancel := context.WithCancel(q.life.Context())
	f = &flight{ctx: runCtx, cancel: cancel, refs: 1, prio: job.Priority, done: make(chan struct{})}
	f.callers[job.Priority] = 1
	q.shared[job.Key] = f
	return f, true, time.Time{}, false
}

// leave takes a caller who gave up off its shared read.
func (q *Queue) leave(job Job, f *flight) {
	q.mu.Lock()
	defer q.mu.Unlock()
	f.refs--
	f.callers[job.Priority]--
	if f.refs > 0 {
		q.setPrio(f, f.highest())
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

// highest is the priority of the flight's most urgent caller still there.
func (f *flight) highest() Priority {
	for p := User; p < Idle; p++ {
		if f.callers[p] > 0 {
			return p
		}
	}
	return Idle
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

// flight is the read's current number.
func (f *flight) flight() Flight { return Flight(f.id.Load()) }

// run performs a job with at most one retry. f is the shared read it runs
// for, or nil. Each send, the retry too, gets the number of its moment:
// an answer is ranked by when its request went out, not by when the read
// was created or joined.
func (q *Queue) run(ctx context.Context, job Job, f *flight) transport.Answer {
	for attempt := 0; ; attempt++ {
		t, err := q.acquire(ctx, job.Priority, f)
		if err != nil {
			return canceled()
		}
		if f != nil {
			f.id.Store(flights.Add(1))
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
	// A 401 is never tried again, whatever became of its body (a broken one
	// is "temporary" by class): the token is gone.
	if ans.Class != transport.Temporary || ans.Status == http.StatusUnauthorized {
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
// earlier, longer one. Every window that closed or grew is told to the
// rate-limit observer; a person already waiting is told as well.
func (q *Queue) finish(t *ticket, ans transport.Answer) {
	until, tell, paused := q.giveBack(t, ans)
	if paused && q.onRateLimit != nil {
		q.onRateLimit(until)
	}
	if tell && q.onPause != nil {
		q.onPause(until)
	}
}

// giveBack returns the window's end, whether a waiting person is to be
// told, and whether this answer closed or extended the window.
func (q *Queue) giveBack(t *ticket, ans transport.Answer) (time.Time, bool, bool) {
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
	return until, paused && q.clock.Now().Before(until) && q.personWaits(), paused
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
// back, and so does the budget it took: nothing was started.
func (q *Queue) abandon(t *ticket) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if t.granted {
		q.running--
		if t.background {
			q.bgRunning--
			q.unstart(t)
		}
		q.dispatch()
		return
	}
	q.remove(t)
}

// unstart takes a grant's moment out of the window again. Called with q.mu
// held.
func (q *Queue) unstart(t *ticket) {
	if t.started.IsZero() {
		return
	}
	for i, at := range q.starts {
		if at.Equal(t.started) {
			q.starts = append(q.starts[:i], q.starts[i+1:]...)
			break
		}
	}
	t.started = time.Time{}
}

// budgetFree says whether a background start is within the budget now,
// after the starts that fell out of the window are forgotten. Called with
// q.mu held.
func (q *Queue) budgetFree(now time.Time) bool {
	for len(q.starts) > 0 && !now.Before(q.starts[0].Add(BudgetWindow)) {
		q.starts = q.starts[1:]
	}
	return len(q.starts) < BackgroundBudget
}

// waitForBudget wakes dispatch when the oldest start falls out of the
// window; one timer per window end. Called with q.mu held.
func (q *Queue) waitForBudget(now time.Time) {
	if len(q.starts) == 0 {
		return
	}
	until := q.starts[0].Add(BudgetWindow)
	if q.budgetFor.Equal(until) {
		return
	}
	q.budgetFor = until
	wake := q.clock.After(until.Sub(now))
	q.life.Start("queue: budget", func(ctx context.Context) {
		select {
		case <-wake:
		case <-ctx.Done():
			return
		}
		q.mu.Lock()
		defer q.mu.Unlock()
		if q.budgetFor.Equal(until) {
			q.budgetFor = time.Time{}
		}
		q.dispatch()
	})
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
		t := q.next(now)
		if t == nil {
			return
		}
		q.remove(t)
		q.running++
		if t.prio != User {
			t.background = true
			q.bgRunning++
			t.started = now
			q.starts = append(q.starts, now)
		}
		t.granted = true
		close(t.ready)
	}
}

// next is the wait to grant next, or nil: the first of the person's, else the
// first background wait if a background place is free and the budget has
// a start left, else the first idle wait while the window holds fewer than
// IdleShare starts; without one, the wait goes on until the window rolls.
func (q *Queue) next(now time.Time) *ticket {
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
			if !q.budgetFree(now) {
				q.waitForBudget(now)
				return nil
			}
			return t
		}
	}
	for _, t := range q.waiting {
		if t.prio == Idle {
			if q.budgetFree(now); len(q.starts) >= IdleShare {
				q.waitForBudget(now)
				return nil
			}
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
