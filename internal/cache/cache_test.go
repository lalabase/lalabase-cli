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

// The answer to a write decides nothing about the record it touched: a
// refused write ("not found", "no access") keeps the record, a successful one
// keeps it too and asks for a reload, because a write answers with a part (a
// read state) or another object (the new comment), never the whole record.
func TestTheDecisionsForAnAnswerToAWrite(t *testing.T) {
	want := map[transport.Class]Decision{
		transport.Object:          Reload,
		transport.NotFound:        KeepShowMessage,
		transport.NoAccess:        KeepShowMessage,
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
		if got := Decide(class, Write); got != d {
			t.Errorf("%v: got %v, want %v", class, got, d)
		}
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
	for _, d := range []Decision{Replace, Remove, KeepStale, KeepShowMessage, Drop, Reload} {
		if got := d.Refreshed(); got != (d == Replace) {
			t.Errorf("%v.Refreshed() = %v", d, got)
		}
	}
}
