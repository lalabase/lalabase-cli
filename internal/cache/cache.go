// Package cache holds the four decisions the client's cache takes on an
// answer. They follow from the answer's class alone (internal/transport), so
// the wording of a server's message never decides what the person keeps
// seeing.
package cache

import "github.com/gboyke/lalabase-cli/internal/transport"

// Decision is what happens to a cached entry when an answer for it arrives.
type Decision int

const (
	// Replace: the answer is the new entry.
	Replace Decision = iota
	// Remove: the record is gone or no longer visible; drop the entry.
	Remove
	// KeepStale: the server could not be asked; keep the entry, marked as
	// possibly stale.
	KeepStale
	// KeepShowMessage: the server answered, but not with the record; keep the
	// entry unchanged and show the message.
	KeepShowMessage
	// Drop: the client gave up on the request itself; the answer says nothing
	// about the record and is thrown away, the entry stays as it was.
	Drop
	// Reload: a write succeeded. Its answer is a part of the record (a read
	// state) or another object (the new comment), so it replaces nothing;
	// the entry stays and is loaded again (plan, Cache: after a successful
	// write the affected detail, list and counters reload).
	Reload
)

// Op says whether the answer came from reading the entry or from a write on
// it.
type Op int

const (
	Read Op = iota
	Write
)

func (d Decision) String() string {
	switch d {
	case Replace:
		return "replace"
	case Remove:
		return "remove"
	case KeepStale:
		return "keep_stale"
	case KeepShowMessage:
		return "keep_show_message"
	case Drop:
		return "drop"
	case Reload:
		return "reload"
	}
	return "unknown"
}

// Refreshed reports whether the entry now counts as freshly loaded. A failed
// refresh never counts as a successful one.
func (d Decision) Refreshed() bool { return d == Replace }

// Decide maps an answer's class to a decision. A class it does not know keeps
// the entry: an answer nobody planned for never deletes what the person sees.
//
// An answer to a write never removes the record: "not found" or "no access"
// for a comment says the comment was refused, not that the ticket is gone (a
// person may read a ticket and not comment on it). The write is shown as
// refused; reloading the record after a write is the caller's step.
func Decide(class transport.Class, op Op) Decision {
	switch class {
	case transport.Object:
		if op == Write {
			return Reload
		}
		return Replace
	case transport.NotFound, transport.NoAccess:
		if op == Write {
			return KeepShowMessage
		}
		return Remove
	case transport.Temporary:
		return KeepStale
	case transport.Canceled:
		return Drop
	default:
		return KeepShowMessage
	}
}
