package gateway

import (
	"context"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/worker"
)

// Review round 1, finding 5: once the mailbox knows the 401, Observe sends
// nothing more, not even the queue's retry or a read issued before the
// model woke up; the answer is "canceled", so the cache changes nothing.
func TestObserveSendsNothingAfterARevocation(t *testing.T) {
	life := worker.New(context.Background())
	defer life.Stop(wall)
	ev := NewEvents(life)
	calls := 0
	run := ev.Observe(func(context.Context) transport.Answer {
		calls++
		return transport.Answer{Class: transport.Object, Status: 200, Sent: true}
	})
	if ans := run(context.Background()); !ans.OK() || calls != 1 {
		t.Fatalf("before: %v, %d calls", ans.Class, calls)
	}
	ev.Revoke()
	ans := run(context.Background())
	if ans.Class != transport.Canceled || ans.Sent || calls != 1 {
		t.Fatalf("after: %v sent %v, %d calls", ans.Class, ans.Sent, calls)
	}
}

// Every class a 401 can come with is observed: the clean one, a broken
// body (temporary), a body too large (protocol) and a canceled read.
func TestEvery401IsObservedWhateverItsClass(t *testing.T) {
	for _, class := range []transport.Class{transport.Unauthenticated, transport.Temporary, transport.Protocol, transport.Canceled, transport.Refused} {
		life := worker.New(context.Background())
		ev := NewEvents(life)
		ev.Observe(func(context.Context) transport.Answer { return transport.Answer{Class: class, Status: 401} })(context.Background())
		if !ev.State().Revoked {
			t.Errorf("%v with status 401 was not observed", class)
		}
		life.Stop(wall)
	}
}
