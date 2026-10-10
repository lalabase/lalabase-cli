package cache

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

// C1e: what the disk cache needs of the memory cache: a record of the key
// that loses no field, a journal of every change that is not an eviction,
// and a promotion of a disk entry into memory.

// fullKey is a key with every field set to a value of its own.
func fullKey(t *testing.T) Key {
	t.Helper()
	k := namespace(t, "work", "https://b.example/base", "tok-9").Key("get_ticket")
	k.Organisation, k.Global, k.Status, k.Page, k.PerPage, k.Project = "org-1", false, "unread", 3, 25, "p-1"
	k.Query, k.Ticket, k.PostsPage, k.PostsPerPage, k.Newest, k.AttachmentsPage, k.FullText, k.Post = "login", "t-1", 2, 25, true, 4, true, "c-1"
	return k
}

// Every field of Key and of its namespace has a line in KeyRecord: a field
// added to the key without one would be lost on disk, and an entry read
// back would answer another question than it was filed under.
func TestEveryKeyFieldIsOnDisk(t *testing.T) {
	keyFields := reflect.TypeFor[Key]().NumField() - 1 + reflect.TypeFor[Namespace]().NumField()
	if got := reflect.TypeFor[KeyRecord]().NumField(); got != keyFields {
		t.Fatalf("KeyRecord has %d fields, Key and Namespace %d", got, keyFields)
	}
	k := fullKey(t)
	r := k.Record()
	// No field of the record is left at its zero value: every field of the
	// key reached it. Global and Organisation exclude each other, so the
	// global key covers the one field the full key cannot set.
	global := namespace(t, "work", "https://b.example", "tok").Key("list_notifications")
	global.Global, global.Status, global.Page, global.PerPage = true, "unread", 1, 25
	v, g := reflect.ValueOf(r), reflect.ValueOf(global.Record())
	for i := range v.NumField() {
		if v.Field(i).IsZero() && g.Field(i).IsZero() {
			t.Errorf("KeyRecord.%s is zero for a key with every field set", v.Type().Field(i).Name)
		}
	}
	if back, ok := global.Record().Key(); !ok || back != global {
		t.Fatalf("global round trip: %+v (ok %v)", back, ok)
	}
	back, ok := r.Key()
	if !ok || back != k {
		t.Fatalf("round trip: %+v -> %+v (ok %v)", k, back, ok)
	}
	// Through JSON too, as the disk writes it.
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var decoded KeyRecord
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	again, ok := decoded.Key()
	if !ok || again != k {
		t.Fatalf("JSON round trip: %+v", again)
	}
}

// Each field apart makes another key, through the record and back: a
// record with one field changed never rebuilds the original key.
func TestEveryRecordFieldIsolates(t *testing.T) {
	k := fullKey(t)
	base := k.Record()
	v := reflect.ValueOf(&base).Elem()
	for i := range v.NumField() {
		r := base
		f := reflect.ValueOf(&r).Elem().Field(i)
		switch f.Kind() {
		case reflect.String:
			f.SetString(f.String() + "x")
		case reflect.Int:
			f.SetInt(f.Int() + 1)
		case reflect.Bool:
			f.SetBool(!f.Bool())
		default:
			t.Fatalf("field %s of kind %s", v.Type().Field(i).Name, f.Kind())
		}
		other, ok := r.Key()
		if ok && other == k {
			t.Errorf("KeyRecord.%s changed and the key did not", v.Type().Field(i).Name)
		}
	}
}

// A record that names no confirmed identity, or an invalid key, rebuilds
// nothing: the disk never makes a key the store would refuse.
func TestARecordOfNoIdentityIsNoKey(t *testing.T) {
	k := fullKey(t)
	for name, change := range map[string]func(*KeyRecord){
		"no profile":  func(r *KeyRecord) { r.Profile = "" },
		"no server":   func(r *KeyRecord) { r.Server = "" },
		"bad server":  func(r *KeyRecord) { r.Server = "not a url" },
		"no token":    func(r *KeyRecord) { r.TokenID = "" },
		"no op":       func(r *KeyRecord) { r.Operation = "" },
		"org and all": func(r *KeyRecord) { r.Global = true },
		"no scope":    func(r *KeyRecord) { r.Organisation, r.Project, r.Ticket = "", "", "" },
	} {
		r := k.Record()
		change(&r)
		if _, ok := r.Key(); ok {
			t.Errorf("%s: a key", name)
		}
	}
	// The namespace of a record is normalised like a confirmed one.
	r := k.Record()
	r.Server = "HTTPS://B.Example/base/"
	back, ok := r.Key()
	if !ok || back != k {
		t.Fatalf("normalised server: %+v %v", back, ok)
	}
}

// --- the journal -------------------------------------------------------------

func put(k Key, payload string) Change {
	return Change{Kind: ChangePut, Key: k, Entry: Entry{Payload: []byte(payload), LoadedAt: t0}}
}

func kinds(ops []Change) []ChangeKind {
	var out []ChangeKind
	for _, op := range ops {
		out = append(out, op.Kind)
	}
	return out
}

// The store tells the journal every change but an eviction: the memory
// limits are the memory's, the disk keeps what memory let go.
func TestTheStoreJournalsEveryChangeButEvictions(t *testing.T) {
	ns := namespace(t, "default", "https://a.example", "tok")
	j := NewJournal()
	s := NewStore(WithJournal(j))
	k1, k2 := inbox(ns, "org-1", 1), inbox(ns, "org-1", 2)
	s.Apply(k1, Replace, []byte(`one`), t0)
	ops := j.Take(10)
	if len(ops) != 1 || ops[0].Kind != ChangePut || ops[0].Key != k1 || string(ops[0].Entry.Payload) != "one" || !ops[0].Entry.LoadedAt.Equal(t0) || ops[0].Entry.Stale {
		t.Fatalf("after a replace: %+v", ops)
	}
	// A failed refresh: the entry is stale in memory and on disk.
	s.Apply(k1, KeepStale, nil, t0.Add(time.Minute))
	ops = j.Take(10)
	if len(ops) != 1 || ops[0].Kind != ChangePut || !ops[0].Entry.Stale || !ops[0].Entry.LoadedAt.Equal(t0) {
		t.Fatalf("after keep stale: %+v", ops)
	}
	// Decisions that change no data journal nothing.
	s.Apply(k1, KeepShowMessage, nil, t0)
	s.Apply(k1, Drop, nil, t0)
	s.Apply(k1, Reload, nil, t0)
	s.Apply(k2, KeepStale, nil, t0) // not in memory: nothing to mark
	if j.Len() != 0 {
		t.Fatalf("journaled without a change: %+v", j.Take(10))
	}
	// A forget and a removal reach the disk by the key and the resource.
	s.Apply(k2, Replace, []byte(`two`), t0)
	if ops = j.Take(10); len(ops) != 1 || ops[0].Kind != ChangePut || ops[0].Key != k2 {
		t.Fatalf("a second replace: %+v", ops)
	}
	s.Forget(k2)
	if ops = j.Take(10); len(ops) != 1 || ops[0].Kind != ChangeForget || ops[0].Key != k2 {
		t.Fatalf("forget: %+v", ops)
	}
	s.Apply(k1, Remove, nil, t0)
	if ops = j.Take(10); len(ops) != 1 || ops[0].Kind != ChangeRemoveResource || ops[0].Key != k1.Resource() {
		t.Fatalf("remove: %+v", ops)
	}
	// The session's end removes the namespace.
	s.RemoveNamespace(ns)
	ops = j.Take(10)
	if len(ops) != 1 || ops[0].Kind != ChangeRemoveNamespace || ops[0].Key.Namespace() != ns {
		t.Fatalf("remove namespace: %+v", ops)
	}
	// An eviction is not a change: the disk keeps the entry memory let go.
	for i := range MaxEntries + 1 {
		s.Apply(inbox(ns, "org-1", i+1), Replace, []byte(`x`), t0)
	}
	ops = j.Take(1000)
	if len(ops) != MaxEntries+1 {
		t.Fatalf("%d ops for %d replaces", len(ops), MaxEntries+1)
	}
	for _, op := range ops {
		if op.Kind != ChangePut {
			t.Fatalf("an eviction was journaled: %+v", op)
		}
	}
	if s.Len() != MaxEntries {
		t.Fatalf("%d entries in memory", s.Len())
	}
	// An answer too large for memory is valid, just not kept: the older
	// entry under the key must not come back from disk either.
	s.Apply(k1, Replace, []byte(`small`), t0)
	j.Take(10)
	s.Apply(k1, Replace, bytes.Repeat([]byte("a"), MaxEntryBytes+1), t0)
	ops = j.Take(10)
	if len(ops) != 1 || ops[0].Kind != ChangeForget || ops[0].Key != k1 {
		t.Fatalf("an oversized answer: %+v", ops)
	}
	if _, ok := s.Get(k1); ok {
		t.Fatal("the small entry stayed in memory")
	}
}

// A store without a journal works as before.
func TestAStoreWithoutAJournalWorks(t *testing.T) {
	ns := namespace(t, "default", "https://a.example", "tok")
	s := NewStore()
	k := inbox(ns, "org-1", 1)
	s.Apply(k, Replace, []byte(`x`), t0)
	s.Apply(k, KeepStale, nil, t0)
	s.Forget(k)
	s.RemoveNamespace(ns)
}

// The journal keeps the order of what changes the disk and drops what a
// later change makes pointless: a put is superseded by a later put or
// forget of the key, by a removal of its resource and by the removal of
// its namespace; a removal in between stays and stays before.
func TestTheJournalCoalescesInOrder(t *testing.T) {
	ns := namespace(t, "default", "https://a.example", "tok")
	other := namespace(t, "default", "https://a.example", "tok-2")
	k1, k2 := inbox(ns, "org-1", 1), inbox(ns, "org-1", 2)
	j := NewJournal()
	j.Add(put(k1, "a"))
	j.Add(put(k1, "b"))
	ops := j.Take(10)
	if len(ops) != 1 || string(ops[0].Entry.Payload) != "b" {
		t.Fatalf("two puts: %+v", ops)
	}
	j.Add(put(k1, "a"))
	j.Add(Change{Kind: ChangeRemoveResource, Key: k1.Resource()})
	j.Add(put(k1, "c"))
	ops = j.Take(10)
	if !reflect.DeepEqual(kinds(ops), []ChangeKind{ChangeRemoveResource, ChangePut}) || string(ops[1].Entry.Payload) != "c" {
		t.Fatalf("put, remove, put: %+v", ops)
	}
	j.Add(put(k1, "a"))
	j.Add(put(k2, "b"))
	j.Add(Change{Kind: ChangeForget, Key: k1})
	ops = j.Take(10)
	if !reflect.DeepEqual(kinds(ops), []ChangeKind{ChangePut, ChangeForget}) || ops[0].Key != k2 || ops[1].Key != k1 {
		t.Fatalf("forget after put: %+v", ops)
	}
	j.Add(put(k1, "a"))
	j.Add(put(inbox(other, "org-1", 1), "o"))
	j.Add(Change{Kind: ChangeForget, Key: k2})
	j.Add(Change{Kind: ChangeRemoveNamespace, Key: Key{ns: ns}})
	ops = j.Take(10)
	if !reflect.DeepEqual(kinds(ops), []ChangeKind{ChangePut, ChangeRemoveNamespace}) || ops[0].Key.Namespace() != other || ops[1].Key.Namespace() != ns {
		t.Fatalf("remove namespace: %+v", ops)
	}
	// Take hands out at most n, oldest first, and keeps the rest.
	j.Add(put(k1, "a"))
	j.Add(put(k2, "b"))
	if ops := j.Take(1); len(ops) != 1 || ops[0].Key != k1 || j.Len() != 1 {
		t.Fatalf("take 1: %+v, %d left", ops, j.Len())
	}
	if ops := j.Take(1); len(ops) != 1 || ops[0].Key != k2 || j.Len() != 0 {
		t.Fatalf("take 1 again: %+v", ops)
	}
}

// The journal is bounded. Over the bound the oldest puts go first (a lost
// put is a miss later, nothing worse); when nothing but removals is left,
// the whole namespace is removed instead, in one op: stale data never
// stays on disk because the queue was full.
func TestTheJournalIsBounded(t *testing.T) {
	ns := namespace(t, "default", "https://a.example", "tok")
	j := NewJournal()
	for i := range MaxPending + 10 {
		j.Add(put(inbox(ns, "org-1", i+1), "x"))
	}
	if j.Len() != MaxPending {
		t.Fatalf("%d pending", j.Len())
	}
	ops := j.Take(MaxPending)
	if ops[0].Key.Page != 11 || ops[len(ops)-1].Key.Page != MaxPending+10 {
		t.Fatalf("the oldest puts did not go first: %d..%d", ops[0].Key.Page, ops[len(ops)-1].Key.Page)
	}
	// Removals alone: every resource of its own (a ticket each).
	for i := range MaxPending + 1 {
		k := ns.Key("get_ticket")
		k.Project, k.Ticket = "p-1", idOf(i)
		j.Add(Change{Kind: ChangeRemoveResource, Key: k})
	}
	ops = j.Take(MaxPending)
	if len(ops) != 1 || ops[0].Kind != ChangeRemoveNamespace || ops[0].Key.Namespace() != ns {
		t.Fatalf("too many removals: %d ops, first %+v", len(ops), ops[0])
	}
}

func idOf(i int) string {
	return string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + string(rune('a'+(i/676)%26))
}

// --- promotion ---------------------------------------------------------------

// A disk entry is promoted into memory as it was: its load time, its stale
// mark; a promotion is not a change (not journaled), and memory that
// already holds the key (a network answer that landed since) keeps what
// it holds.
func TestPromotionKeepsTheEntryAndChangesNothingElse(t *testing.T) {
	ns := namespace(t, "default", "https://a.example", "tok")
	j := NewJournal()
	s := NewStore(WithJournal(j))
	k := inbox(ns, "org-1", 1)
	old := Entry{Payload: []byte(`old`), LoadedAt: t0.Add(-time.Hour), Stale: true}
	if !s.Promote(k, old) {
		t.Fatal("not promoted")
	}
	if j.Len() != 0 {
		t.Fatalf("a promotion was journaled: %+v", j.Take(10))
	}
	e, ok := s.Get(k)
	if !ok || string(e.Payload) != "old" || !e.LoadedAt.Equal(old.LoadedAt) || !e.Stale {
		t.Fatalf("promoted entry %+v", e)
	}
	if s.Promote(k, Entry{Payload: []byte(`older`), LoadedAt: t0.Add(-2 * time.Hour)}) {
		t.Fatal("promoted over an entry")
	}
	if e, _ := s.Get(k); string(e.Payload) != "old" {
		t.Fatalf("the entry changed: %q", e.Payload)
	}
	// Neither an invalid key nor an oversized entry is promoted.
	if s.Promote(inbox(Namespace{}, "org-1", 1), old) {
		t.Fatal("an invalid key was promoted")
	}
	if s.Promote(inbox(ns, "org-1", 2), Entry{Payload: bytes.Repeat([]byte("a"), MaxEntryBytes+1), LoadedAt: t0}) {
		t.Fatal("an oversized entry was promoted")
	}
	// The memory limits hold for promotions too.
	for i := range MaxEntries + 5 {
		s.Promote(inbox(ns, "org-2", i+1), Entry{Payload: []byte(`x`), LoadedAt: t0})
	}
	if s.Len() != MaxEntries || j.Len() != 0 {
		t.Fatalf("%d entries, %d ops", s.Len(), j.Len())
	}
}

// Review round 1, backlog B1: a put that took a forget's place is dropped
// for room only as that forget, never without a trace: the file the
// forget was for would otherwise stay on disk as if the put had been
// applied.
func TestADroppedPutLeavesTheForgetItReplaced(t *testing.T) {
	ns := namespace(t, "default", "https://a.example", "tok")
	j := NewJournal()
	k := inbox(ns, "org-1", 1)
	j.Add(Change{Kind: ChangeForget, Key: k})
	j.Add(put(k, "again"))
	if j.Len() != 1 {
		t.Fatalf("%d ops", j.Len())
	}
	for i := range MaxPending + 1 {
		j.Add(put(inbox(ns, "org-2", i+1), "x"))
	}
	ops := j.Take(MaxPending)
	if ops[0].Kind != ChangeForget || ops[0].Key != k {
		t.Fatalf("the first op is %+v, want the forget of the dropped put", ops[0].Kind)
	}
	// A put after a put after a forget keeps the mark through the chain.
	j = NewJournal()
	j.Add(Change{Kind: ChangeForget, Key: k})
	j.Add(put(k, "one"))
	j.Add(put(k, "two"))
	for i := range MaxPending + 1 {
		j.Add(put(inbox(ns, "org-2", i+1), "x"))
	}
	if ops := j.Take(1); ops[0].Kind != ChangeForget || ops[0].Key != k {
		t.Fatalf("the chain lost the mark: %+v", ops[0].Kind)
	}
	// A plain put (no forget before it) is dropped without a trace.
	j = NewJournal()
	j.Add(put(k, "plain"))
	for i := range MaxPending + 1 {
		j.Add(put(inbox(ns, "org-2", i+1), "x"))
	}
	if ops := j.Take(1); ops[0].Key == k {
		t.Fatalf("a plain put left a trace: %+v", ops[0].Kind)
	}
}
