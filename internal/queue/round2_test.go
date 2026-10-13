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
	// A broken queue fails the test within the deadline instead of hanging
	// it for the whole package's timeout, where it would look like a race.
	receive := func(who string, ch <-chan shared) shared {
		t.Helper()
		select {
		case got := <-ch:
			return got
		case <-time.After(2 * time.Second):
			t.Fatalf("%s: no answer", who)
		}
		return shared{}
	}
	a := doShared("a", ga)
	waitStarted(t, ga)
	ga.release <- transport.Answer{Class: transport.Temporary, Status: 503, Sent: true} // a retries after the delay
	// b is sent while a's retry waits on the clock, so a retry numbered at
	// its failure instead of at its send is behind b every time.
	waitTimers(t, clock, 1)
	b := doShared("b", gb)
	waitStarted(t, gb)
	gb.release <- ok()
	between := receive("b", b)
	if !between.ans.OK() || between.flight == 0 {
		t.Fatalf("b: %v, flight %d", between.ans.Class, between.flight)
	}
	clock.Advance(time.Second)
	waitStarted(t, ga) // the retry
	ga.release <- ok()
	retried := receive("a", a)
	if !retried.ans.OK() {
		t.Fatalf("a: %v", retried.ans.Class)
	}
	if retried.flight <= between.flight {
		t.Fatalf("the retry kept flight %d, sent after flight %d", retried.flight, between.flight)
	}
	idle(t, q)
}
