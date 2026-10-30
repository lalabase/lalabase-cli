package ui

import (
	"strconv"

	"github.com/gboyke/lalabase-cli/internal/cache"
	"github.com/gboyke/lalabase-cli/internal/transport"
)

// What every write of the interface shares: who may, the key of an action,
// and the one reading of what the answer says of it.

// writeEnd is what an answer says became of a write.
type writeEnd uint8

const (
	// writeConfirmed: the server did it, and the gateway saw it in the answer.
	writeConfirmed writeEnd = iota + 1
	// writeRefused: the server said no.
	writeRefused
	// writeNotSent: it never reached the server.
	writeNotSent
	// writeRateLimited: the server rate-limited the queue's one retry too;
	// neither attempt was acted on.
	writeRateLimited
	// writeUnknown: it left and no usable answer came back: the server may
	// have acted. It is reloaded, never sent again.
	writeUnknown
)

// placeWrite reads an answer to a write, from its class alone (cache.Decide
// with Write, so a refused write never removes a record). An answer the
// client cannot place, after a request that left, says nothing about the
// write; a decision a write never takes counts as unknown, the safe side.
func placeWrite(ans transport.Answer) writeEnd {
	switch cache.Decide(ans.Class, cache.Write) {
	case cache.Reload:
		return writeConfirmed
	case cache.KeepStale:
		// The server was not reached, or it answered 429 to the retry as
		// well; anything else after the request left may have been carried
		// out.
		switch {
		case !ans.Sent:
			return writeNotSent
		case ans.Status == 429:
			return writeRateLimited
		}
	case cache.Drop:
		// Canceled by the client: the session ended or the program stops.
		if !ans.Sent {
			return writeNotSent
		}
	case cache.KeepShowMessage:
		if ans.Class != transport.Protocol || !ans.Sent {
			return writeRefused
		}
	}
	return writeUnknown
}

// tokenWrites: /me succeeded and said the token may write. A token that does
// not say (a server before MCP 0.7.0) does not write. The profile's
// allow_write is for agent commands and never reaches here.
func (a *app) tokenWrites() bool {
	return a.steps[stepMe].state == ready && a.me.Token.ReadOnly != nil && !*a.me.Token.ReadOnly
}

// idempotencyKey is the key of one action: the session and the operation
// number make it unique in this run, the clock across runs. Its form is
// what the server keeps unchanged (letters, digits and -, 8 to 128).
func (a *app) idempotencyKey(kind string, op uint64) string {
	return "lalabase-" + kind + "-" + strconv.FormatUint(uint64(a.session), 10) + "-" + strconv.FormatUint(op, 10) + "-" + strconv.FormatInt(a.now().UnixNano(), 10)
}
