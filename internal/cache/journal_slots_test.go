package cache

import (
	"fmt"
	"testing"
	"time"
)

// Review round 3 of C1e: a change the journal let go is not kept alive by
// its backing array. A journal drained in batches of 32 would otherwise
// hold every payload it ever carried until its slots were written again.

// tailIsClear fails when a slot past the journal's length still holds a
// change.
func tailIsClear(t *testing.T, j *Journal, step string) {
	t.Helper()
	for i, op := range j.ops[len(j.ops):cap(j.ops)] {
		if op.Entry.Payload != nil || op.Key != (Key{}) {
			t.Fatalf("%s: slot %d past the length still holds %s %+v", step, len(j.ops)+i, op.Kind, op.Key)
		}
	}
}

func TestTheJournalLetsGoOfWhatItDrops(t *testing.T) {
	ns := namespace(t, "work", "https://b.example", "tok-1")
	key := func(i int) Key {
		k := ns.Key("list_notifications")
		k.Organisation = fmt.Sprintf("org-%d", i)
		return k
	}
	put := func(i int) Change {
		return Change{Kind: ChangePut, Key: key(i), Entry: Entry{Payload: []byte("payload"), LoadedAt: time.Unix(1, 0)}}
	}

	j := NewJournal()
	for i := range 64 {
		j.Add(put(i))
	}
	j.Take(32)
	tailIsClear(t, j, "take")
	j.Take(32)
	tailIsClear(t, j, "take all")

	j = NewJournal()
	for i := range 3 {
		j.Add(put(i))
	}
	j.Add(put(0)) // supersedes the first
	tailIsClear(t, j, "coalesce")
	j.Add(Change{Kind: ChangeRemoveNamespace, Key: Key{ns: ns}})
	tailIsClear(t, j, "namespace removal")

	j = NewJournal()
	for i := range MaxPending + 1 {
		j.Add(put(i))
	}
	tailIsClear(t, j, "bound")

	j = NewJournal()
	for i := range MaxPending + 1 {
		j.Add(Change{Kind: ChangeRemoveResource, Key: key(i)})
	}
	if j.Len() != 1 {
		t.Fatalf("%d changes after the collapse", j.Len())
	}
	tailIsClear(t, j, "collapse")
}

// A read issued after a change waits for it, or for the change that took
// its place: a later put of the key, a removal of its resource or of its
// namespace, or the collapse of the journal.
func TestAMarkWaitsForTheChangeOrWhatTookItsPlace(t *testing.T) {
	ns := namespace(t, "work", "https://b.example", "tok-1")
	other := namespace(t, "work", "https://b.example", "tok-2")
	key := func(n Namespace, i int) Key {
		k := n.Key("list_notifications")
		k.Organisation = fmt.Sprintf("org-%d", i)
		return k
	}
	forget := Change{Kind: ChangeForget, Key: key(ns, 0)}
	for name, later := range map[string]Change{
		"put of the key":      {Kind: ChangePut, Key: key(ns, 0), Entry: Entry{Payload: []byte("x")}},
		"resource removal":    {Kind: ChangeRemoveResource, Key: key(ns, 0).Resource()},
		"namespace removal":   {Kind: ChangeRemoveNamespace, Key: Key{ns: ns}},
		"an unrelated change": {Kind: ChangePut, Key: key(other, 1), Entry: Entry{Payload: []byte("x")}},
	} {
		j := NewJournal()
		if !j.Settled(j.Mark()) {
			t.Fatalf("%s: an empty journal is not settled", name)
		}
		j.Add(Change{Kind: ChangePut, Key: key(other, 9), Entry: Entry{Payload: []byte("x")}})
		j.Add(forget)
		mark := j.Mark()
		j.Add(later)
		if j.Settled(mark) {
			t.Fatalf("%s: settled with the forget's effect pending", name)
		}
		for j.Len() > 1 {
			j.Take(1)
		}
		settled := j.Settled(mark)
		if want := name == "an unrelated change"; settled != want {
			t.Fatalf("%s: with the last change pending, settled %v", name, settled)
		}
		j.Take(1)
		if !j.Settled(mark) {
			t.Fatalf("%s: not settled when empty", name)
		}
	}

	// The collapse stands for the oldest change of its namespace.
	j := NewJournal()
	j.Add(Change{Kind: ChangeRemoveResource, Key: key(ns, 0).Resource()})
	mark := j.Mark()
	for i := 1; i <= MaxPending; i++ {
		j.Add(Change{Kind: ChangeRemoveResource, Key: key(ns, i).Resource()})
	}
	if j.Len() != 1 || j.Settled(mark) {
		t.Fatalf("after the collapse: %d changes, settled %v", j.Len(), j.Settled(mark))
	}

	// A put that took a forget's place, dropped for room, is that forget
	// again and still stands for it.
	j = NewJournal()
	before := j.Mark()
	j.Add(Change{Kind: ChangeForget, Key: key(ns, 0)})
	mark = j.Mark()
	j.Add(Change{Kind: ChangePut, Key: key(ns, 0), Entry: Entry{Payload: []byte("x")}})
	for i := 1; i <= MaxPending; i++ {
		j.Add(Change{Kind: ChangePut, Key: key(other, i), Entry: Entry{Payload: []byte("x")}})
	}
	if j.Settled(mark) {
		t.Fatal("the forget a dropped put became does not stand for the forget")
	}
	if !j.Settled(before) {
		t.Fatal("a mark before the forget waits for it")
	}
}
