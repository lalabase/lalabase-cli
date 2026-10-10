package ui

import (
	"time"

	"github.com/gboyke/lalabase-cli/internal/cache"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/uitext"
)

// status is the load state every view shares: the inbox page, the
// projects, the ticket page and the ticket. One rule for what a decision
// does to a view and what the notice says, instead of one per view.
type status struct {
	// known: the data was shown at least once, from the cache or the server.
	known bool
	// loading: a read of the view's key is out for somebody.
	loading bool
	// cached: shown from the cache, its one reload not landed yet.
	cached bool
	// stale: a refresh failed for a temporary reason.
	stale    bool
	err      present.Inline
	loadedAt time.Time
}

// fresh records a successful load from the server.
func (s *status) fresh(now time.Time) {
	s.known, s.cached, s.stale, s.err, s.loadedAt = true, false, false, present.Inline{}, now
}

// fromCache records a load from the cache entry.
func (s *status) fromCache(e cache.Entry) {
	s.known, s.cached, s.stale, s.loadedAt = true, true, e.Stale, e.LoadedAt
}

// removed records that the server took the data away.
func (s *status) removed() {
	s.known, s.cached, s.stale, s.err = false, false, false, uitext.Inline(uitext.NotFoundRemoved)
}

// failed records an answer that brought no data: temporary ones keep what
// is shown, possibly stale; the rest keep it with the server's message.
func (s *status) failed(d cache.Decision, ans transport.Answer) {
	switch d {
	case cache.KeepStale:
		s.stale = true
		if !s.known {
			s.err = uitext.Inline(uitext.ServerUnreachable)
		}
	case cache.KeepShowMessage:
		s.err = messageText(ans)
	}
}

// notice is the one line under the title: what the data is (loading,
// cached, stale) or what went wrong, the most important first. empty is
// the line for data that is known and holds nothing.
func (s status) notice(empty uitext.ID, isEmpty bool) present.Inline {
	switch {
	case s.err.String() != "":
		return s.err
	// A cached view says so while its one reload runs; "Refreshing" is for
	// data the server gave.
	case s.cached && s.stale:
		return uitext.Inline(uitext.CachedStale)
	case s.cached:
		return uitext.Inline(uitext.Cached)
	case s.loading && !s.known:
		return uitext.Inline(uitext.Loading)
	case s.loading:
		return uitext.Inline(uitext.Refreshing)
	case s.stale:
		return uitext.Inline(uitext.PossiblyStale)
	case s.known && isEmpty:
		return uitext.Inline(empty)
	}
	return present.Inline{}
}

// messageText is the error shown for an answer that is neither an object
// nor temporary: the server's refusal, or an answer the client cannot place.
func messageText(ans transport.Answer) present.Inline {
	if ans.Class == transport.Protocol {
		return uitext.Fill(uitext.UnexpectedResponse, detail(ans))
	}
	return uitext.Fill(uitext.RefusedWith, detail(ans))
}

// lastPage is the last page of total rows at perPage each: 1 for an empty
// list, never a division by zero.
func lastPage(total, perPage int) int {
	if perPage <= 0 || total <= 0 {
		return 1
	}
	n := total / perPage
	if total%perPage != 0 {
		n++
	}
	return max(1, n)
}
