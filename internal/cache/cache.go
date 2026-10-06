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
	}
	return "unknown"
}

// Refreshed reports whether the entry now counts as freshly loaded. A failed
// refresh never counts as a successful one.
func (d Decision) Refreshed() bool { return d == Replace }

// Decide maps an answer's class to a decision. A class it does not know keeps
// the entry: an answer nobody planned for never deletes what the person sees.
func Decide(class transport.Class) Decision {
	switch class {
	case transport.Object:
		return Replace
	case transport.NotFound, transport.NoAccess:
		return Remove
	case transport.Temporary:
		return KeepStale
	default:
		return KeepShowMessage
	}
}
