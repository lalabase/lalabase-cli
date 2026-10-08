package worker

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

const limit = 2 * time.Second

func closed(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(limit):
		t.Fatalf("%s did not happen", what)
	}
}

// A panic in started work is recovered in its goroutine, kept as the first
// fatal error with its place only, and ends the lifetime.
func TestAPanicInStartedWorkIsKeptNotPrinted(t *testing.T) {
	s := New(context.Background())
	if !s.Start("queue: shared read", func(context.Context) { panic("secret token lalab_pat_x") }) {
		t.Fatal("Start refused")
	}
	closed(t, s.Failed(), "the fatal error")
	closed(t, s.Context().Done(), "the end of the lifetime")
	var fatal *FatalError
	if err := s.Err(); !errors.As(err, &fatal) || fatal.Where != "queue: shared read" || strings.Contains(err.Error(), "secret") {
		t.Fatalf("got %v", err)
	}
	// The record is durable: the first error stays, a later one does not
	// replace it.
	Run(s, "gateway: inbox", func() int { panic("second") })
	if !errors.As(s.Err(), &fatal) || fatal.Where != "queue: shared read" {
		t.Fatalf("the first error was replaced: %v", s.Err())
	}
	if s.Start("late", func(context.Context) {}) {
		t.Fatal("work started after the lifetime ended")
	}
}

func TestRunGivesNoHalfMadeValue(t *testing.T) {
	s := New(context.Background())
	if v, ok := Run(s, "x", func() []int { return []int{1} }); !ok || len(v) != 1 || s.Err() != nil {
		t.Fatalf("%v %v %v", v, ok, s.Err())
	}
	v, ok := Run(s, "gateway: inbox", func() []int { panic(nil) })
	if ok || v != nil || s.Err() == nil {
		t.Fatalf("%v %v %v", v, ok, s.Err())
	}
}

// Recording never waits for anybody: many panics at once all return.
func TestRecordingDoesNotBlock(t *testing.T) {
	s := New(context.Background())
	var wg sync.WaitGroup
	done := make(chan struct{})
	for range 50 {
		wg.Add(1)
		go func() { defer wg.Done(); Run(s, "load", func() int { panic("x") }) }()
	}
	go func() { wg.Wait(); close(done) }()
	closed(t, done, "every recording")
}

// Stop ends the lifetime, waits for work that follows it, and gives up on
// work that ignores it after the limit.
func TestStopIsBounded(t *testing.T) {
	s := New(context.Background())
	s.Start("follows", func(ctx context.Context) { <-ctx.Done() })
	if !s.Stop(limit) {
		t.Fatal("work that follows its context was not waited for")
	}
	if s.Err() != nil {
		t.Fatal("stopping is no fatal error")
	}

	hung := New(context.Background())
	block := make(chan struct{})
	defer close(block)
	hung.Start("ignores", func(context.Context) { <-block })
	start := time.Now()
	if hung.Stop(50 * time.Millisecond) {
		t.Fatal("work that ignores its context counted as stopped")
	}
	if took := time.Since(start); took > limit {
		t.Fatalf("Stop took %s", took)
	}
	if hung.Start("after", func(context.Context) {}) {
		t.Fatal("work started after Stop")
	}
}

// Review #9, finding 4: Stop waits for work running in Run on another
// goroutine (a command Bubble Tea runs), so its panic is recorded before
// the command reads Err.
func TestStopWaitsForRunningCalls(t *testing.T) {
	s := New(context.Background())
	entered, release := make(chan struct{}), make(chan struct{})
	go Run(s, "gateway: inbox", func() int {
		close(entered)
		<-release
		panic("late")
	})
	<-entered
	go func() { time.Sleep(50 * time.Millisecond); close(release) }()
	if !s.Stop(limit) {
		t.Fatal("Stop gave up on a running call")
	}
	if s.Err() == nil {
		t.Fatal("Stop returned before the running call's panic was recorded")
	}
	// Calls after Stop still run and count; a second Stop stays safe.
	if v, ok := Run(s, "after", func() int { return 1 }); !ok || v != 1 {
		t.Fatal("a call after Stop did not run")
	}
	s.Stop(limit)
}
