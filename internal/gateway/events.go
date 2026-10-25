package gateway

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/worker"
)

// Session names one run of the interface, unique in the process. The
// messages of the mailbox carry it, so a message of another run (a test's,
// an older model's) changes nothing in a model that is not its own.
type Session uint64

// sessions numbers the sessions of the process.
var sessions atomic.Uint64

// NewSession issues a session nobody had before.
func NewSession() Session { return Session(sessions.Add(1)) }

// Events is the mailbox between the callbacks of the queue and the
// transport and the model. What a callback observes (a 429 that closes or
// extends the window, a 401 the server really sent) is recorded here, under
// a lock, never in the model or the cache: those are the model's, changed
// only in its update step. A supervised command (Wait) carries one wake-up
// to the model, which then reads the state. The state is durable: a
// revocation stays recorded whatever results arrive later.
type Events struct {
	sup     *worker.Supervisor
	session Session
	mu      sync.Mutex
	pause   time.Time // the latest end of a rate-limit window told so far
	revoked bool      // the server answered 401 in this session
	wake    chan struct{}
}

// NewEvents starts a mailbox for a new session.
func NewEvents(sup *worker.Supervisor) *Events {
	return &Events{sup: sup, session: NewSession(), wake: make(chan struct{}, 1)}
}

// Session is the session the mailbox belongs to.
func (e *Events) Session() Session { return e.session }

// RateLimit is the queue's rate-limit observer (queue.WithOnRateLimit): a
// window that ends later than the one recorded is kept and wakes the
// model; an earlier one changes nothing.
func (e *Events) RateLimit(until time.Time) {
	e.mu.Lock()
	changed := until.After(e.pause)
	if changed {
		e.pause = until
	}
	e.mu.Unlock()
	if changed {
		e.signal()
	}
}

// Revoke records that the server no longer accepts the token. Once.
func (e *Events) Revoke() {
	e.mu.Lock()
	was := e.revoked
	e.revoked = true
	e.mu.Unlock()
	if !was {
		e.signal()
	}
}

// signal wakes the model once; a wake-up not yet taken is enough for every
// change until then.
func (e *Events) signal() {
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

// State is what the mailbox holds, for the model to read after a wake-up
// and before it acts on any result.
type State struct {
	Pause   time.Time
	Revoked bool
}

func (e *Events) State() State {
	e.mu.Lock()
	defer e.mu.Unlock()
	return State{Pause: e.pause, Revoked: e.revoked}
}

// Wake is the message of one wake-up. The model reads State then and issues
// Wait again.
type Wake struct{ Session Session }

// Wait is the command that carries the next wake-up to the model. It ends
// without a message when the lifetime ends.
func (e *Events) Wait() tea.Cmd {
	return func() tea.Msg {
		msg, ok := worker.Run(e.sup, "gateway: events", func() tea.Msg {
			select {
			case <-e.wake:
				return Wake{Session: e.session}
			case <-e.sup.Context().Done():
				return nil
			}
		})
		if !ok {
			if e.sup.Err() != nil {
				return worker.Fatal{}
			}
			return nil
		}
		return msg
	}
}

// Expired says a deadline the model set with Expiry has come: a pause's
// or a hint's (L2). The model clears only what still shows this deadline,
// so a later pause or a newer hint is not cleared by it.
type Expired struct {
	Session Session
	Until   time.Time
}

// Expiry wakes the model when until has come, by the real clock (now is
// the model's clock at issue, for the wait). This is status maintenance of
// one notice (a rate-limit pause, a hint), not polling: nothing is asked
// of the server.
func (e *Events) Expiry(until, now time.Time) tea.Cmd {
	wait := until.Sub(now)
	return func() tea.Msg {
		msg, ok := worker.Run(e.sup, "gateway: expiry", func() tea.Msg {
			select {
			case <-time.After(wait):
				return Expired{Session: e.session, Until: until}
			case <-e.sup.Context().Done():
				return nil
			}
		})
		if !ok {
			if e.sup.Err() != nil {
				return worker.Fatal{}
			}
			return nil
		}
		return msg
	}
}

// Due is the message of a timer the model set (Timer): the token names
// what fell due (a preview of the row the cursor rested on).
type Due struct {
	Session Session
	Token   uint64
}

// Timer wakes the model after d with the token, by the real clock. Like
// Expiry it is maintenance of the model's own state, no request: the model
// decides on the wake-up whether the token still means anything. It ends
// without a message when the lifetime ends.
func (e *Events) Timer(d time.Duration, token uint64) tea.Cmd {
	return func() tea.Msg {
		msg, ok := worker.Run(e.sup, "gateway: timer", func() tea.Msg {
			select {
			case <-time.After(d):
				return Due{Session: e.session, Token: token}
			case <-e.sup.Context().Done():
				return nil
			}
		})
		if !ok {
			if e.sup.Err() != nil {
				return worker.Fatal{}
			}
			return nil
		}
		return msg
	}
}

// Observe wraps a job's request so that a 401 is recorded for the session
// on the request itself, whatever becomes of its answer afterwards: the
// waiter may have left (its answer is "canceled"), the selection may have
// moved on (Scope.Receive refuses the result, rightly), the body may have
// broken off (the class is "temporary" or "canceled", the status still
// 401). The strict acceptance of ordinary data stays untouched; this is
// the one trusted path of a revocation. A nil Events observes nothing.
func (e *Events) Observe(run func(context.Context) transport.Answer) func(context.Context) transport.Answer {
	if e == nil {
		return run
	}
	return func(ctx context.Context) transport.Answer {
		if e.State().Revoked {
			// The token is gone: nothing goes out any more, not even the
			// queue's retry or a read issued before the model woke up.
			return transport.Answer{Class: transport.Canceled, Message: "the session is revoked"}
		}
		ans := run(ctx)
		if ans.Class == transport.Unauthenticated || ans.Status == http.StatusUnauthorized {
			e.Revoke()
		}
		return ans
	}
}
