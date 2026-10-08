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
// key belongs to one organisation or, explicitly, to all of them (Global).
type Key struct {
	ns           Namespace
	Operation    string
	Organisation string
	Global       bool
	Status       string
	Page         int
	PerPage      int
	Project      string
}

// Key starts a key of this namespace for an operation.
func (n Namespace) Key(operation string) Key { return Key{ns: n, Operation: operation} }

// Namespace is the identity the key belongs to.
func (k Key) Namespace() Namespace { return k.ns }

// Resource is the key without its page: what a write or a removal affects
// as a whole.
func (k Key) Resource() Key {
	k.Page, k.PerPage = 0, 0
	return k
}

// String encodes every field, for sharing a read in the queue.
func (k Key) String() string {
	out, _ := json.Marshal([]any{k.ns.profile, k.ns.server, k.ns.tokenID, k.Operation, k.Organisation, k.Global, k.Status, k.Page, k.PerPage, k.Project})
	return string(out)
}

func (k Key) valid() bool {
	return k.ns.tokenID != "" && k.Operation != "" && k.Global == (k.Organisation == "")
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
}

func NewStore() *Store { return &Store{items: map[Key]*list.Element{}, order: list.New()} }

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
	case KeepStale:
		if el, ok := s.items[k]; ok {
			el.Value.(*item).entry.Stale = true
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
		return // valid, but too large to keep; the older entry is outdated
	}
	s.items[k] = s.order.PushFront(&item{key: k, entry: Entry{Payload: bytes.Clone(payload), LoadedAt: now}})
	s.bytes += len(payload)
	for s.order.Len() > MaxEntries || s.bytes > MaxBytes {
		s.remove(s.order.Back().Value.(*item).key)
	}
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

// RemoveNamespace drops every entry of an identity (the session ended).
func (s *Store) RemoveNamespace(n Namespace) {
	for key := range s.items {
		if key.ns == n {
			s.remove(key)
		}
	}
}
