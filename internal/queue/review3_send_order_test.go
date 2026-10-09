package queue

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/worker"
)

// Adapted in round 3: a job of the person's priority holds one of the two
// places, so the two reads go out one after the other. Two reads handed to
// the transport at the same instant by two goroutines can carry their
// numbers in either order; the number is given at the handover, not when
// the read was joined, and that is what this proves.
func TestR3FlightRankMatchesUserSendOrder(t *testing.T) {
	life := worker.New(context.Background())
	defer life.Stop(time.Second)
	q := New(WithLifetime(life))
	blocker := newGate()
	held := do(q, context.Background(), Job{Priority: User, Kind: Read, Key: "blocker", Run: blocker.run})
	waitStarted(t, blocker)
	defer func() { blocker.release <- ok(); answer(t, held) }()
	type result struct {
		key    string
		sent   uint64
		flight Flight
	}
	for attempt := 0; attempt < 10000; attempt++ {
		var sends atomic.Uint64
		ready := make(chan struct{})
		results := make(chan result, 2)
		for i := 0; i < 2; i++ {
			key := fmt.Sprintf("%d/page-%d", attempt, i)
			go func() {
				<-ready
				var sent uint64
				_, f := q.DoShared(context.Background(), Job{Priority: User, Kind: Read, Key: key, Run: func(context.Context) transport.Answer {
					sent = sends.Add(1)
					return ok()
				}})
				results <- result{key, sent, f}
			}()
		}
		close(ready)
		var a, b result
		for i := 0; i < 2; i++ {
			select {
			case r := <-results:
				if i == 0 {
					a = r
				} else {
					b = r
				}
			case <-time.After(time.Second):
				t.Fatal("user sends did not complete")
			}
		}
		if a.sent > b.sent {
			a, b = b, a
		}
		if a.flight >= b.flight {
			t.Fatalf("iteration %d: actual first send %s flight=%d; actual second send %s flight=%d (later observation ranked older)", attempt, a.key, a.flight, b.key, b.flight)
		}
	}
}
