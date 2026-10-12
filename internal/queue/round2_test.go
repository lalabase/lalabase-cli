package queue

import (
	"context"
	"testing"
	"time"

	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/worker"
)

// Review round 2, finding A3: a retry is a later send and carries a later
// flight number than everything sent in between. The number orders the
// answers for the callers; a retried answer is the server's newer word.
func TestARetryCarriesALaterFlightNumber(t *testing.T) {
	life := worker.New(context.Background())
	defer life.Stop(time.Second)
	clock := newClock()
	q := New(WithClock(clock), WithLifetime(life))
	// One gate each: the gate's answers are buffered, so with one gate an
	// answer meant for a could be taken by b if b's job starts before a's
	// takes it (b then waits for a retry the test clock never lets run).
	ga, gb := newGate(), newGate()
	type shared struct {
		ans    transport.Answer
		flight Flight
	}
	doShared := func(key string, g *gate) <-chan shared {
		out := make(chan shared, 1)
		go func() {
			ans, f := q.DoShared(context.Background(), Job{Priority: User, Kind: Read, Key: key, Run: g.run})
			out <- shared{ans, f}
		}()
		return out
	}
	a := doShared("a", ga)
	waitStarted(t, ga)
	ga.release <- transport.Answer{Class: transport.Temporary, Status: 503, Sent: true} // a retries after the delay
	b := doShared("b", gb)
	waitStarted(t, gb)
	gb.release <- ok()
	between := <-b
	if !between.ans.OK() || between.flight == 0 {
		t.Fatalf("b: %v, flight %d", between.ans.Class, between.flight)
	}
	for deadline := time.Now().Add(2 * time.Second); clock.pending() == 0; {
		if time.Now().After(deadline) {
			t.Fatal("the retry is not waiting on the clock")
		}
		time.Sleep(time.Millisecond)
	}
	clock.Advance(time.Second)
	waitStarted(t, ga) // the retry
	ga.release <- ok()
	retried := <-a
	if !retried.ans.OK() {
		t.Fatalf("a: %v", retried.ans.Class)
	}
	if retried.flight <= between.flight {
		t.Fatalf("the retry kept flight %d, sent after flight %d", retried.flight, between.flight)
	}
	idle(t, q)
}
