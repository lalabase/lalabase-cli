package cache

import (
	"testing"

	"github.com/gboyke/lalabase-cli/internal/transport"
)

func TestTheDecisionsForAnAnswerToARead(t *testing.T) {
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
		transport.Canceled:        Drop,
	}
	for class := transport.Object; class <= transport.Canceled; class++ {
		d, ok := want[class]
		if !ok {
			t.Fatalf("class %v has no decision in this table", class)
		}
		if got := Decide(class, Read); got != d {
			t.Errorf("%v: got %v, want %v", class, got, d)
		}
	}
}

// A write that is refused as "not found" or "no access" says something about
// the write, not about the record: a person who may read a ticket but not
// comment on it keeps seeing the ticket.
func TestAnAnswerToAWriteNeverRemovesTheRecord(t *testing.T) {
	for class := transport.Object; class <= transport.Canceled; class++ {
		got := Decide(class, Write)
		if got == Remove {
			t.Errorf("%v of a write removes the record", class)
		}
		if class != transport.NotFound && class != transport.NoAccess && got != Decide(class, Read) {
			t.Errorf("%v: a write decides %v, a read %v", class, got, Decide(class, Read))
		}
	}
	if got := Decide(transport.NotFound, Write); got != KeepShowMessage {
		t.Errorf("not found on a write: %v", got)
	}
}

// A class the cache does not know keeps the entry and shows the message: an
// unknown answer never deletes what the person sees, and never counts as a
// successful refresh.
func TestAnUnknownClassKeepsTheEntry(t *testing.T) {
	for _, op := range []Op{Read, Write} {
		if got := Decide(transport.Class(99), op); got != KeepShowMessage {
			t.Fatalf("got %v", got)
		}
	}
}

// A failed refresh never counts as a successful one: only Replace refreshes.
func TestOnlyReplaceRefreshes(t *testing.T) {
	for _, d := range []Decision{Replace, Remove, KeepStale, KeepShowMessage, Drop} {
		if got := d.Refreshed(); got != (d == Replace) {
			t.Errorf("%v.Refreshed() = %v", d, got)
		}
	}
}
