package cache

import (
	"testing"

	"github.com/gboyke/lalabase-cli/internal/transport"
)

func TestTheFourDecisions(t *testing.T) {
	want := map[transport.Class]Decision{
		transport.Object:          Replace,
		transport.NotFound:        Remove,
		transport.NoAccess:        Remove,
		transport.Temporary:       KeepStale,
		transport.ReadOnly:        KeepShowMessage,
		transport.Refused:         KeepShowMessage,
		transport.InvalidInput:    KeepShowMessage,
		transport.Unauthenticated: KeepShowMessage,
		transport.Protocol:        KeepShowMessage,
	}
	for class := transport.Object; class <= transport.Temporary; class++ {
		d, ok := want[class]
		if !ok {
			t.Fatalf("class %v has no decision in this table", class)
		}
		if got := Decide(class); got != d {
			t.Errorf("%v: got %v, want %v", class, got, d)
		}
	}
}

// A class the cache does not know keeps the entry and shows the message: an
// unknown answer never deletes what the person sees, and never counts as a
// successful refresh.
func TestAnUnknownClassKeepsTheEntry(t *testing.T) {
	if got := Decide(transport.Class(99)); got != KeepShowMessage {
		t.Fatalf("got %v", got)
	}
}

// A failed refresh never counts as a successful one: only Replace refreshes.
func TestOnlyReplaceRefreshes(t *testing.T) {
	for _, d := range []Decision{Replace, Remove, KeepStale, KeepShowMessage} {
		if got := d.Refreshed(); got != (d == Replace) {
			t.Errorf("%v.Refreshed() = %v", d, got)
		}
	}
}
