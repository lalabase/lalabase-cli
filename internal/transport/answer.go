// Package transport talks to a Lalabase server: the REST readers under
// /api/v1 and the MCP tools under /api/mcp (JSON-RPC). Every answer, whatever
// went wrong, comes back as an Answer with one Class; the cache and the
// interface decide from the class alone, never from the server's wording.
//
// Foreign text (Message, Payload) is passed on raw. It is cleaned once, where
// it is shown (internal/clean), not here.
package transport

import (
	"encoding/json"
	"time"
)

// Class is what an answer means for the client.
type Class int

const (
	// Object: the server answered with what was asked for (an empty list too).
	Object Class = iota
	// NotFound: the record is missing or not visible to this token; the
	// server gives both the same answer.
	NotFound
	// NoAccess: the record is visible but this action on it is not allowed.
	NoAccess
	// ReadOnly: a write with a token that may only read.
	ReadOnly
	// Refused: the server declined for a reason of its own (Reason names it,
	// e.g. "archived"). Every answer the client cannot place is a refusal:
	// shown, never retried.
	Refused
	// InvalidInput: the request itself was malformed.
	InvalidInput
	// Unauthenticated: the token is unknown, expired or revoked (HTTP 401).
	Unauthenticated
	// Protocol: an answer that is not what the protocol promises (no JSON, a
	// redirect, a missing field, an unknown JSON-RPC error).
	Protocol
	// Temporary: the network failed, the server is overloaded (5xx) or the
	// rate limit was hit (429, see RetryAt). The only class worth retrying.
	Temporary
	// Canceled: the client itself gave up on the request (the selection moved
	// on). No statement about the server; the answer is dropped.
	Canceled
)

func (c Class) String() string {
	switch c {
	case Object:
		return "object"
	case NotFound:
		return "not_found"
	case NoAccess:
		return "no_access"
	case ReadOnly:
		return "read_only"
	case Refused:
		return "refused"
	case InvalidInput:
		return "invalid_input"
	case Unauthenticated:
		return "unauthenticated"
	case Protocol:
		return "protocol"
	case Temporary:
		return "temporary"
	case Canceled:
		return "canceled"
	}
	return "unknown"
}

// Answer is one classified answer.
type Answer struct {
	Class Class
	// Reason names a refusal (from the server's lalabase/reason), else empty.
	Reason string
	// Code is the server's machine code as sent (lalabase/code), else empty.
	Code string
	// Message is the server's text for anything but an object. Raw.
	Message string
	// Payload is the object: the REST body, or the text of a tool result.
	Payload json.RawMessage
	// Status is the HTTP status, 0 when no answer arrived.
	Status int
	// RetryAt is when the rate limit window opens again (429 only).
	RetryAt time.Time
	// Err is the cause on the client side (network, decoding), if any.
	Err error
	// Sent: the request left the client, so a write may have been carried
	// out even without an answer. A write whose outcome is unclear is
	// reloaded, never sent again (plan, Writes).
	Sent bool
}

// OK reports whether the answer is an object.
func (a Answer) OK() bool { return a.Class == Object }
