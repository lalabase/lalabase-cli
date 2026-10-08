package cache

import (
	"bytes"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

func namespace(t *testing.T, profile, server, token string) Namespace {
	t.Helper()
	ns, ok := Confirmed(profile, server, token)
	if !ok {
		t.Fatalf("not confirmed: %s %s %s", profile, server, token)
	}
	return ns
}

func inbox(ns Namespace, org string, page int) Key {
	k := ns.Key("list_notifications")
	k.Organisation, k.Status, k.Page, k.PerPage = org, "unread", page, 25
	return k
}

func TestOnlyAConfirmedIdentityOpensANamespace(t *testing.T) {
	for _, c := range [][3]string{{"", "https://a.example", "tok"}, {"default", "https://a.example", ""}, {"default", "not a url", "tok"}, {"default", "https://a.example?x=1", "tok"}} {
		if _, ok := Confirmed(c[0], c[1], c[2]); ok {
			t.Errorf("confirmed %v", c)
		}
	}
	if namespace(t, "default", "HTTPS://A.Example/", "tok") != namespace(t, "default", "https://a.example", "tok") {
		t.Fatal("the server is not normalised")
	}
}

// Every dimension of the key keeps its data apart.
func TestEveryKeyDimensionIsolates(t *testing.T) {
	ns := namespace(t, "default", "https://a.example", "tok-1")
	base := inbox(ns, "org-1", 1)
	variants := map[string]Key{}
	for name, other := range map[string]Namespace{
		"profile": namespace(t, "work", "https://a.example", "tok-1"),
		"server":  namespace(t, "default", "https://b.example", "tok-1"),
		"token":   namespace(t, "default", "https://a.example", "tok-2"),
	} {
		k := base
		k.ns = other
		variants[name] = k
	}
	set := func(name string, f func(*Key)) { k := base; f(&k); variants[name] = k }
	set("operation", func(k *Key) { k.Operation = "list_tickets" })
	set("organisation", func(k *Key) { k.Organisation = "org-2" })
	set("global", func(k *Key) { k.Organisation, k.Global = "", true })
	set("status", func(k *Key) { k.Status = "all" })
	set("page", func(k *Key) { k.Page = 2 })
	set("per page", func(k *Key) { k.PerPage = 50 })
	set("project", func(k *Key) { k.Project = "p-1" })

	s := NewStore()
	s.Apply(base, Replace, []byte(`base`), t0)
	for name, k := range variants {
		if k == base || k.String() == base.String() {
			t.Errorf("%s: equal to the base key", name)
		}
		if _, ok := s.Get(k); ok {
			t.Errorf("%s: sees the base entry", name)
		}
	}
	if len(variants) != 10 {
		t.Fatalf("%d dimensions", len(variants))
	}
	// A key needs one organisation or, explicitly, all of them.
	for _, k := range []Key{inbox(ns, "", 1), func() Key { k := inbox(ns, "org-1", 1); k.Global = true; return k }(), inbox(Namespace{}, "org-1", 1)} {
		s.Apply(k, Replace, []byte(`x`), t0)
		if _, ok := s.Get(k); ok {
			t.Errorf("an invalid key was stored: %+v", k)
		}
	}
}

func TestEntriesAreCopiesAtBothBorders(t *testing.T) {
	s := NewStore()
	k := inbox(namespace(t, "default", "https://a.example", "tok"), "org-1", 1)
	payload := []byte(`{"data":[]}`)
	s.Apply(k, Replace, payload, t0)
	payload[0] = 'X'
	e, _ := s.Get(k)
	e.Payload[1] = 'Y'
	again, _ := s.Get(k)
	if string(again.Payload) != `{"data":[]}` {
		t.Fatalf("stored %q", again.Payload)
	}
}

func TestTheEntryLimitEvictsTheLeastRecentlyUsed(t *testing.T) {
	ns := namespace(t, "default", "https://a.example", "tok")
	s := NewStore()
	for i := range MaxEntries {
		s.Apply(inbox(ns, "org-1", i+1), Replace, []byte(`x`), t0)
	}
	s.Get(inbox(ns, "org-1", 1)) // used: the next eviction takes page 2
	s.Apply(inbox(ns, "org-1", MaxEntries+1), Replace, []byte(`x`), t0)
	if s.Len() != MaxEntries {
		t.Fatalf("%d entries", s.Len())
	}
	if _, ok := s.Get(inbox(ns, "org-1", 1)); !ok {
		t.Fatal("the entry used last was evicted")
	}
	if _, ok := s.Get(inbox(ns, "org-1", 2)); ok {
		t.Fatal("the least recently used entry stayed")
	}
}

func TestTheByteLimitsHoldWithReplacement(t *testing.T) {
	ns := namespace(t, "default", "https://a.example", "tok")
	s := NewStore()
	big := bytes.Repeat([]byte("a"), MaxEntryBytes)
	for i := range MaxBytes / MaxEntryBytes {
		s.Apply(inbox(ns, "org-1", i+1), Replace, big, t0)
	}
	if s.Bytes() != MaxBytes || s.Len() != MaxBytes/MaxEntryBytes {
		t.Fatalf("%d bytes in %d entries", s.Bytes(), s.Len())
	}
	// Replacing an entry counts its new size only.
	s.Apply(inbox(ns, "org-1", 1), Replace, []byte(`small`), t0)
	if s.Bytes() != MaxBytes-MaxEntryBytes+5 {
		t.Fatalf("%d bytes after a replacement", s.Bytes())
	}
	s.Apply(inbox(ns, "org-1", 100), Replace, big, t0)
	s.Apply(inbox(ns, "org-1", 101), Replace, big, t0)
	if s.Bytes() > MaxBytes {
		t.Fatalf("%d bytes over the limit", s.Bytes())
	}
	// A valid answer above the entry limit is not kept, and the older entry
	// it replaces goes too.
	k := inbox(ns, "org-1", 1)
	s.Apply(k, Replace, append(big, 'b'), t0)
	if _, ok := s.Get(k); ok || s.Bytes() > MaxBytes {
		t.Fatalf("an oversized answer stayed (%d bytes)", s.Bytes())
	}
}

// The decisions: a failed refresh never counts as a load.
func TestDecisionsKeepTheLastSuccessfulLoad(t *testing.T) {
	ns := namespace(t, "default", "https://a.example", "tok")
	k, page2, other := inbox(ns, "org-1", 1), inbox(ns, "org-1", 2), inbox(ns, "org-2", 1)
	fresh := func() *Store {
		s := NewStore()
		for _, key := range []Key{k, page2, other} {
			s.Apply(key, Replace, []byte(`old`), t0)
		}
		return s
	}
	later := t0.Add(time.Minute)
	for _, d := range []Decision{KeepStale, KeepShowMessage, Drop, Reload} {
		s := fresh()
		s.Apply(k, d, []byte(`new`), later)
		e, ok := s.Get(k)
		if !ok || string(e.Payload) != "old" || !e.LoadedAt.Equal(t0) || e.Stale != (d == KeepStale) || s.Len() != 3 {
			t.Errorf("%v: %+v %v", d, e, ok)
		}
	}
	s := fresh()
	s.Apply(k, KeepStale, nil, later)
	s.Apply(k, Replace, []byte(`new`), later)
	if e, _ := s.Get(k); string(e.Payload) != "new" || !e.LoadedAt.Equal(later) || e.Stale {
		t.Fatalf("replace: %+v", e)
	}
	// Remove takes every page of the resource, nothing else.
	s.Apply(k, Remove, nil, later)
	if _, ok := s.Get(page2); ok || s.Len() != 1 {
		t.Fatalf("remove left %d entries", s.Len())
	}
	if _, ok := s.Get(other); !ok {
		t.Fatal("remove took another organisation")
	}
}

func TestRemoveNamespaceTakesOneIdentityOnly(t *testing.T) {
	a, b := namespace(t, "default", "https://a.example", "tok-1"), namespace(t, "default", "https://a.example", "tok-2")
	s := NewStore()
	s.Apply(inbox(a, "org-1", 1), Replace, []byte(`a`), t0)
	s.Apply(inbox(b, "org-1", 1), Replace, []byte(`b`), t0)
	s.RemoveNamespace(a)
	if _, ok := s.Get(inbox(a, "org-1", 1)); ok || s.Len() != 1 || s.Bytes() != 1 {
		t.Fatalf("%d entries, %d bytes", s.Len(), s.Bytes())
	}
}

// Review #9, round 2: over the byte limit, the least recently used entry
// goes, never the newest or one just read.
func TestTheByteLimitEvictsTheLeastRecentlyUsed(t *testing.T) {
	ns := namespace(t, "default", "https://a.example", "tok")
	s := NewStore()
	big := bytes.Repeat([]byte("a"), MaxEntryBytes)
	full := MaxBytes / MaxEntryBytes
	for i := range full {
		s.Apply(inbox(ns, "org-1", i+1), Replace, big, t0)
	}
	if _, ok := s.Get(inbox(ns, "org-1", 1)); !ok {
		t.Fatal("the oldest entry is missing")
	}
	s.Apply(inbox(ns, "org-1", full+1), Replace, big, t0)
	if s.Len() != full || s.Bytes() != MaxBytes {
		t.Fatalf("%d bytes in %d entries", s.Bytes(), s.Len())
	}
	for page, want := range map[int]bool{1: true, 2: false, 3: true, full + 1: true} {
		if _, ok := s.Get(inbox(ns, "org-1", page)); ok != want {
			t.Errorf("page %d kept: %v, want %v", page, ok, want)
		}
	}
}

// Review #9, round 3: an ended session empties its own namespace only. The
// same token id on another server or under another profile is another
// identity, and its entries stay.
func TestRemovingANamespaceKeepsTheSameTokenIDElsewhere(t *testing.T) {
	ended := namespace(t, "default", "https://a.example", "tok")
	others := []Namespace{
		namespace(t, "default", "https://b.example", "tok"),
		namespace(t, "work", "https://a.example", "tok"),
	}
	s := NewStore()
	s.Apply(inbox(ended, "org-1", 1), Replace, []byte(`ended`), t0)
	for _, ns := range others {
		s.Apply(inbox(ns, "org-1", 1), Replace, []byte(`kept`), t0)
	}
	s.RemoveNamespace(ended)
	if _, ok := s.Get(inbox(ended, "org-1", 1)); ok {
		t.Fatal("the ended namespace kept its entry")
	}
	for _, ns := range others {
		if e, ok := s.Get(inbox(ns, "org-1", 1)); !ok || string(e.Payload) != "kept" {
			t.Errorf("%+v lost its entry", ns)
		}
	}
}
