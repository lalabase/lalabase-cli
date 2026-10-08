package gateway

import (
	"strconv"
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
	Waiter   Waiter
	Area     Area
	Key      cache.Key
	Gens     Gens
	Epoch    uint64 // of Key.Resource()
	Priority queue.Priority
}

// ShareKey is the key two waiters share a request under: the data, the session
// and the resource's epoch. Waiter and selection generation are not part of
// it, so a preview and an open of the same page send one request.
func (t Ticket) ShareKey() string {
	return t.Key.String() + "|" + strconv.FormatUint(t.Gens.Session, 10) + "|" + strconv.FormatUint(t.Epoch, 10)
}

// Scope decides which results may still be accepted. The root model owns it
// and changes it only in its update step. Generations and epochs only grow:
// returning to an organisation gives a new generation, and epochs live apart
// from cache entries, so evicting an entry forgets no invalidation.
type Scope struct {
	ns      cache.Namespace
	session uint64
	area    [areas]uint64
	epochs  map[cache.Key]uint64
	pending map[Waiter]Ticket
	next    Waiter
}

// NewScope starts a session for a confirmed identity.
func NewScope(ns cache.Namespace) *Scope {
	return &Scope{ns: ns, epochs: map[cache.Key]uint64{}, pending: map[Waiter]Ticket{}}
}

// Begin issues a ticket for a new waiter.
func (s *Scope) Begin(area Area, key cache.Key, prio queue.Priority) Ticket {
	s.next++
	t := Ticket{Waiter: s.next, Area: area, Key: key, Gens: Gens{Session: s.session, Area: s.area[area]},
		Epoch: s.epochs[key.Resource()], Priority: prio}
	s.pending[t.Waiter] = t
	return t
}

// Move says the selection of an area moved on (another organisation, another
// page). Its waiters are dropped and returned, for the caller to cancel.
func (s *Scope) Move(area Area) []Waiter {
	s.area[area]++
	var dropped []Waiter
	for w, t := range s.pending {
		if t.Area == area {
			dropped = append(dropped, w)
			delete(s.pending, w)
		}
	}
	return dropped
}

// Leave drops a waiter that gave up.
func (s *Scope) Leave(w Waiter) { delete(s.pending, w) }

// Invalidate refuses every result for the resource begun before now (a
// write changed it).
func (s *Scope) Invalidate(resource cache.Key) { s.epochs[resource.Resource()]++ }

// EndSession refuses everything begun so far and empties the identity's
// cache (the token is no longer accepted).
func (s *Scope) EndSession(store *cache.Store) {
	s.session++
	clear(s.pending)
	store.RemoveNamespace(s.ns)
}

// accept checks a result's ticket: session, waiter and data, generation,
// epoch. A waiter gets one result; its ticket is used up either way.
func (s *Scope) accept(t Ticket) bool {
	if t.Gens.Session != s.session || t.Key.Namespace() != s.ns {
		return false
	}
	issued, ok := s.pending[t.Waiter]
	if !ok || issued != t {
		return false
	}
	delete(s.pending, t.Waiter)
	return t.Gens.Area == s.area[t.Area] && t.Epoch == s.epochs[t.Key.Resource()]
}

// Receive is the one way an inbox result reaches the cache: checked first,
// then filed by the decision its class gives. ok is false for a result that
// must be dropped; then nothing changed. An unauthenticated answer ends the
// session.
func (s *Scope) Receive(store *cache.Store, r InboxResult, now time.Time) (cache.Decision, bool) {
	if !s.accept(r.Ticket) {
		return cache.Drop, false
	}
	d := cache.Decide(r.Answer.Class, cache.Read)
	if r.Answer.Class == transport.Unauthenticated {
		s.EndSession(store)
		return d, true
	}
	store.Apply(r.Key, d, r.Answer.Payload, now)
	return d, true
}
