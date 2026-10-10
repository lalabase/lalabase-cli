package cache

import (
	"bytes"
	"container/list"
	"encoding/json"
	"net/url"
	"strings"
	"time"
)

// Limits of the memory cache. They bound what the cache holds, not the
// process: an answer larger than MaxEntryBytes is valid, just not kept.
const (
	MaxEntries    = 64
	MaxBytes      = 16 << 20
	MaxEntryBytes = 2 << 20
)

// Namespace is one confirmed identity: a profile, its normalised server and
// the token id the server confirmed through /me. Only Confirmed makes one, so
// no data is cached before /me succeeded, and the token itself (or a
// fingerprint of it) is never part of a key.
type Namespace struct{ profile, server, tokenID string }

// Confirmed returns the namespace of a successful /me: tokenID is the
// answer's api.Me.Token.ID, never something the profile says about itself.
func Confirmed(profile, server, tokenID string) (Namespace, bool) {
	normal, ok := normalServer(server)
	if profile == "" || !ok || tokenID == "" {
		return Namespace{}, false
	}
	return Namespace{profile: profile, server: normal, tokenID: tokenID}, true
}

func normalServer(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", false
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host) + strings.TrimRight(u.Path, "/"), true
}

// Key names one cached answer. Every option of the query is part of it; a
// key is scoped by one organisation, explicitly by all of them (Global), or
// by a project (a ticket list, a ticket).
type Key struct {
	ns           Namespace
	Operation    string
	Organisation string
	Global       bool
	Status       string
	Page         int
	PerPage      int
	Project      string
	// C1c: the search of a ticket list, and the options of one ticket
	// (internal/api, TicketOptions). A key of a ticket names it.
	Query           string
	Ticket          string
	PostsPage       int
	PostsPerPage    int
	Newest          bool
	AttachmentsPage int
	FullText        bool
	Post            string
}

// Key starts a key of this namespace for an operation.
func (n Namespace) Key(operation string) Key { return Key{ns: n, Operation: operation} }

// Namespace is the identity the key belongs to.
func (k Key) Namespace() Namespace { return k.ns }

// Resource is the key without its page, its search and the options of one
// record: what a write or a removal affects as a whole. For a ticket that
// is the ticket in every variant it was read in; for a ticket list the
// project, every search and page of it.
func (k Key) Resource() Key {
	k.Page, k.PerPage, k.Query = 0, 0, ""
	k.PostsPage, k.PostsPerPage, k.Newest, k.AttachmentsPage, k.FullText, k.Post = 0, 0, false, 0, false, ""
	return k
}

// String encodes every field, for sharing a read in the queue.
func (k Key) String() string {
	out, _ := json.Marshal([]any{k.ns.profile, k.ns.server, k.ns.tokenID, k.Operation, k.Organisation, k.Global, k.Status, k.Page, k.PerPage, k.Project,
		k.Query, k.Ticket, k.PostsPage, k.PostsPerPage, k.Newest, k.AttachmentsPage, k.FullText, k.Post})
	return string(out)
}

// valid: a confirmed identity, an operation, and one scope: an
// organisation or all of them (never both), or a project; a ticket needs
// its project.
func (k Key) valid() bool {
	if k.ns.tokenID == "" || k.Operation == "" || (k.Global && k.Organisation != "") {
		return false
	}
	if k.Ticket != "" && k.Project == "" {
		return false
	}
	return k.Global || k.Organisation != "" || k.Project != ""
}

// Entry is a cached answer, a copy the caller may change.
type Entry struct {
	Payload []byte
	// LoadedAt is the last successful load; a failed refresh keeps it.
	LoadedAt time.Time
	// Stale: a refresh failed for a temporary reason.
	Stale bool
}

type item struct {
	key   Key
	entry Entry
}

// Store is the memory cache. It has no lock: its owner, the root model,
// changes it only in its update step.
type Store struct {
	items map[Key]*list.Element
	order *list.List // front: used last
	bytes int
	// journal, when set, is told every change that is a decision about
	// data (C1e): the disk cache replays it. An eviction is not one.
	journal *Journal
}

// StoreOption changes a Store at construction.
type StoreOption func(*Store)

// WithJournal makes the store tell j its changes.
func WithJournal(j *Journal) StoreOption { return func(s *Store) { s.journal = j } }

func NewStore(opts ...StoreOption) *Store {
	s := &Store{items: map[Key]*list.Element{}, order: list.New()}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// note tells the journal of a change.
func (s *Store) note(op Change) {
	if s.journal != nil {
		s.journal.Add(op)
	}
}

// Len and Bytes report the size, for tests and limits.
func (s *Store) Len() int   { return s.order.Len() }
func (s *Store) Bytes() int { return s.bytes }

// Get returns a copy of the entry and marks it as used.
func (s *Store) Get(k Key) (Entry, bool) {
	el, ok := s.items[k]
	if !ok {
		return Entry{}, false
	}
	s.order.MoveToFront(el)
	e := el.Value.(*item).entry
	e.Payload = bytes.Clone(e.Payload)
	return e, true
}

// Apply carries out a decision on an answer for k. payload is the answer's
// checked object, used by Replace only.
func (s *Store) Apply(k Key, d Decision, payload []byte, now time.Time) {
	switch d {
	case Replace:
		s.replace(k, payload, now)
	case Remove:
		res := k.Resource()
		for key := range s.items {
			if key.Resource() == res {
				s.remove(key)
			}
		}
		s.note(Change{Kind: ChangeRemoveResource, Key: res})
	case KeepStale:
		if el, ok := s.items[k]; ok {
			el.Value.(*item).entry.Stale = true
			s.note(Change{Kind: ChangePut, Key: k, Entry: s.copyOf(el)})
		}
	}
	// KeepShowMessage, Drop and Reload change no data and no load time.
}

func (s *Store) replace(k Key, payload []byte, now time.Time) {
	if !k.valid() {
		return
	}
	s.remove(k)
	if len(payload) > MaxEntryBytes {
		// Valid, but too large to keep; the older entry is outdated, on
		// disk too.
		s.note(Change{Kind: ChangeForget, Key: k})
		return
	}
	s.insert(k, Entry{Payload: bytes.Clone(payload), LoadedAt: now})
	s.note(Change{Kind: ChangePut, Key: k, Entry: s.copyOf(s.items[k])})
}

// insert files an entry at the front and evicts the least recently used
// past the limits. An eviction is the memory's own housekeeping: the
// journal is not told, the disk keeps what memory let go.
func (s *Store) insert(k Key, e Entry) {
	s.items[k] = s.order.PushFront(&item{key: k, entry: e})
	s.bytes += len(e.Payload)
	for s.order.Len() > MaxEntries || s.bytes > MaxBytes {
		s.remove(s.order.Back().Value.(*item).key)
	}
}

// copyOf is the entry of an element, as Get hands it out.
func (s *Store) copyOf(el *list.Element) Entry {
	e := el.Value.(*item).entry
	e.Payload = bytes.Clone(e.Payload)
	return e
}

// Promote files an entry the disk cache read (C1e), as it was: its load
// time and its stale mark stay, so a disk hit extends no lifetime. It
// files nothing over an entry already there (a network answer that landed
// meanwhile wins), nothing under an invalid key and nothing above the
// entry limit, and it is no change the journal is told of: the entry came
// from the disk. It reports whether the entry was filed.
func (s *Store) Promote(k Key, e Entry) bool {
	if !k.valid() || len(e.Payload) > MaxEntryBytes {
		return false
	}
	if _, ok := s.items[k]; ok {
		return false
	}
	e.Payload = bytes.Clone(e.Payload)
	s.insert(k, e)
	return true
}

func (s *Store) remove(k Key) {
	el, ok := s.items[k]
	if !ok {
		return
	}
	s.bytes -= len(el.Value.(*item).entry.Payload)
	s.order.Remove(el)
	delete(s.items, k)
}

// Forget drops one entry the root cannot use any more: one that does not
// decode, or a page a refresh showed no longer exists. The other pages of
// the resource stay; refusing results begun before is the scope's step
// (Invalidate), not the store's.
func (s *Store) Forget(k Key) {
	s.remove(k)
	s.note(Change{Kind: ChangeForget, Key: k})
}

// RemoveNamespace drops every entry of an identity (the session ended).
func (s *Store) RemoveNamespace(n Namespace) {
	for key := range s.items {
		if key.ns == n {
			s.remove(key)
		}
	}
	s.note(Change{Kind: ChangeRemoveNamespace, Key: Key{ns: n}})
}
