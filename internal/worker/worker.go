// Package worker is the one place where the client's own code starts a
// goroutine (Supervisor.Start; internal/sourceguard holds the rule). A
// supervisor gives its work one lifetime and keeps the first fatal error:
//
//   - a panic in supervised work is recovered where it happens, recorded once
//     and never printed: its value is foreign to us and may carry anything,
//     so only the place it happened in is kept;
//   - the record is durable, so a fatal error that loses the race against the
//     person's "q" still ends the program with exit 1;
//   - recording never blocks and never writes to the terminal; the interface
//     learns of it through Failed and quits, the command reads Err after the
//     terminal is restored;
//   - Stop ends the lifetime and waits a bounded time for the work to return.
package worker

import (
	"context"
	"sync"
	"time"
)

// Fatal is the message the interface gets when supervised work failed.
type Fatal struct{}

// FatalError says where the first fatal error happened, never what the panic
// carried.
type FatalError struct{ Where string }

func (e *FatalError) Error() string { return "fatal error in " + e.Where }

// Supervisor owns a lifetime and the first fatal error in it.
type Supervisor struct {
	ctx    context.Context
	cancel context.CancelFunc
	failed chan struct{}
	once   sync.Once

	mu       sync.Mutex
	err      error
	stopping bool
	active   int
	idle     chan struct{} // closed when stopping and no work is left
}

// New returns a supervisor whose lifetime ends with parent or with Stop.
func New(parent context.Context) *Supervisor {
	ctx, cancel := context.WithCancel(parent)
	return &Supervisor{ctx: ctx, cancel: cancel, failed: make(chan struct{}), idle: make(chan struct{})}
}

// Context is the lifetime. It ends with Stop and with the first fatal error.
func (s *Supervisor) Context() context.Context { return s.ctx }

// Failed is closed when the first fatal error was recorded.
func (s *Supervisor) Failed() <-chan struct{} { return s.failed }

// Err is the first fatal error, or nil.
func (s *Supervisor) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// fail records the first fatal error and ends the lifetime. It never blocks
// on anybody but a concurrent fail.
func (s *Supervisor) fail(where string) {
	s.once.Do(func() {
		s.mu.Lock()
		s.err = &FatalError{Where: where}
		s.mu.Unlock()
		close(s.failed) // before cancel: who sees the lifetime end can ask Failed
		s.cancel()
	})
}

// Run calls f on the caller's goroutine. A panic in f becomes the fatal
// error: ok is false and v the zero value, never a half-made result.
func Run[T any](s *Supervisor, where string, f func() T) (v T, ok bool) {
	defer func() {
		if recover() != nil {
			s.fail(where)
			var zero T
			v, ok = zero, false
		}
	}()
	return f(), true
}

// Start runs f in a goroutine of its own, with the lifetime as its context.
// It returns false, without running f, when the lifetime is over: the caller
// then finishes what f would have finished.
func (s *Supervisor) Start(where string, f func(ctx context.Context)) bool {
	s.mu.Lock()
	if s.stopping || s.ctx.Err() != nil {
		s.mu.Unlock()
		return false
	}
	s.active++
	s.mu.Unlock()
	go func() {
		defer s.done()
		Run(s, where, func() struct{} { f(s.ctx); return struct{}{} })
	}()
	return true
}

func (s *Supervisor) done() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active--
	if s.stopping && s.active == 0 {
		close(s.idle)
	}
}

// Stop ends the lifetime and waits up to limit for started work to return.
// It reports whether all of it did; work that ignores its context is left
// behind, and the process ends without it.
func (s *Supervisor) Stop(limit time.Duration) bool {
	s.mu.Lock()
	if !s.stopping {
		s.stopping = true
		if s.active == 0 {
			close(s.idle)
		}
	}
	s.mu.Unlock()
	s.cancel()
	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case <-s.idle:
		return true
	case <-timer.C:
		return false
	}
}
