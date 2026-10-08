package gateway

import (
	"strconv"
	"sync/atomic"
	"time"

	"github.com/gboyke/lalabase-cli/internal/cache"
	"github.com/gboyke/lalabase-cli/internal/queue"
	"github.com/gboyke/lalabase-cli/internal/transport"
)

// Area is a part of the selection with a generation of its own.
type Area uint8

const (
	Organisations Area = iota // the list of organisations and their counts
	Inbox                     // the inbox of one organisation or of all, one page
	areas
)

// Waiter is one consumer of a read (a preview, an open).
type Waiter uint64

// Gens are the generations a ticket was issued in.
type Gens struct{ Session, Area uint64 }

// Ticket describes one waiter's read. It does not change after Begin.
type Ticket struct {
	Scope    uint64 // the scope that issued it, unique in the process
	Waiter   Waiter
	Area     Area
	Key      cache.Key
	Gens     Gens
	Epoch    uint64 // of Key.Resource()
	Priority queue.Priority
}

// ShareKey is the key two waiters share a request under: the data, the
// scope, the session and the resource's epoch. Waiter and selection
// generation are not part of it, so a preview and an open of the same page
// send one request. The scope is: the queue outlives a scope, and a new
// scope starts its epochs at zero.
func (t Ticket) ShareKey() string {
	return t.Key.String() + "|" + strconv.FormatUint(t.Scope, 10) + "|" + strconv.FormatUint(t.Gens.Session, 10) + "|" + strconv.FormatUint(t.Epoch, 10)
}

// scopes numbers the scopes of the process.
var scopes atomic.Uint64

// Scope decides which results may still be accepted. The root model owns it
// and changes it only in its update step. Generations and epochs only grow:
// returning to an organisation gives a new generation, and epochs live apart
// from cache entries, so evicting an entry forgets no invalidation.
type Scope struct {
	id      uint64
	ns      cache.Namespace
	session uint64
	area    [areas]uint64
	epochs  map[cache.Key]uint64
	// removed holds, per resource, the share key of the answer that removed
	// it last: its other copies are the same answer, not older ones.
	removed map[cache.Key]string
	pending map[Waiter]Ticket
	next    Waiter
}

// NewScope starts a session for a confirmed identity.
func NewScope(ns cache.Namespace) *Scope {
	return &Scope{id: scopes.Add(1), ns: ns, epochs: map[cache.Key]uint64{}, removed: map[cache.Key]string{}, pending: map[Waiter]Ticket{}}
}

// Begin issues a ticket for a new waiter.
func (s *Scope) Begin(area Area, key cache.Key, prio queue.Priority) Ticket {
	s.next++
	t := Ticket{Scope: s.id, Waiter: s.next, Area: area, Key: key, Gens: Gens{Session: s.session, Area: s.area[area]},
		Epoch: s.epochs[key.Resource()], Priority: prio}
	s.pending[t.Waiter] = t
	return t
}

// Move says the selection of an area moved on (another organisation, another
// page). Its waiters are returned, for the caller to cancel. They stay
// pending: their results still arrive (canceled or late) and are refused by
// the generation, a check of its own.
func (s *Scope) Move(area Area) []Waiter {
	s.area[area]++
	var moved []Waiter
	for w, t := range s.pending {
		if t.Area == area && t.Gens.Area != s.area[area] {
			moved = append(moved, w)
		}
	}
	return moved
}

// Leave drops a waiter that gave up.
func (s *Scope) Leave(w Waiter) { delete(s.pending, w) }

// Invalidate refuses every result for the resource begun before now (a
// write changed it), copies of a removal included.
func (s *Scope) Invalidate(resource cache.Key) {
	res := resource.Resource()
	s.epochs[res]++
	delete(s.removed, res)
}

// EndSession refuses everything begun so far and empties the identity's
// cache (the token is no longer accepted).
func (s *Scope) EndSession(store *cache.Store) {
	s.session++
	store.RemoveNamespace(s.ns)
}

// accept checks a result's ticket, each check on its own: session, waiter
// and data, generation, epoch. A waiter gets one result: its ticket is used
// up by the first result that names it exactly, accepted or not.
//
// After a removal, the copies of its answer (the waiters that shared its
// request) still carry the old epoch. They are accepted when their share
// key is the removal's and they are a removal themselves: copies of one
// answer have one class, so an older object under the same key stays out.
func (s *Scope) accept(t Ticket, removal bool) bool {
	issued, ok := s.pending[t.Waiter]
	known := ok && issued == t
	if known {
		delete(s.pending, t.Waiter)
	}
	if t.Gens.Session != s.session || t.Key.Namespace() != s.ns {
		return false
	}
	if !known {
		return false
	}
	if t.Gens.Area != s.area[t.Area] {
		return false
	}
	res := t.Key.Resource()
	return t.Epoch == s.epochs[res] || (removal && s.removed[res] != "" && t.ShareKey() == s.removed[res])
}

// Receive is the one way an inbox result reaches the cache: checked first,
// then filed by the decision its class gives. ok is false for a result that
// must be dropped; then nothing changed. An unauthenticated answer ends the
// session.
func (s *Scope) Receive(store *cache.Store, r InboxResult, now time.Time) (cache.Decision, bool) {
	d := cache.Decide(r.Answer.Class, cache.Read)
	if !s.accept(r.Ticket, d == cache.Remove) {
		return cache.Drop, false
	}
	if r.Answer.Class == transport.Unauthenticated {
		s.EndSession(store)
		return d, true
	}
	store.Apply(r.Key, d, r.Answer.Payload, now)
	if d == cache.Remove && r.ShareKey() != s.removed[r.Key.Resource()] {
		// A removal is an invalidation: an older answer for another page
		// of the resource must not bring back what the server took away.
		// The answer's other copies (the waiters that shared its request)
		// still arrive and are accepted; they invalidate nothing again.
		s.Invalidate(r.Key)
		s.removed[r.Key.Resource()] = r.ShareKey()
	}
	return d, true
}
