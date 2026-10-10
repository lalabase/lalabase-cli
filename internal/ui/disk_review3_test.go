package ui

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/cache"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

// Review round 3 of C1e.

// A disk read sees every change journaled before it: with more pending
// than one batch applies, the read waits for the batch that holds its
// predecessors, or the disk answers with an entry memory already removed.
func TestR3ADiskReadWaitsForTheChangesBeforeIt(t *testing.T) {
	h := newDiskHarness(t, t.TempDir(), nil)
	h.start()
	h.drive(h.choose(orgA))
	a := h.m.app
	for a.disk.busy {
		h.drive(h.feed(nil)...)
	}
	key := a.inboxKey(orgA, 1)
	e, ok := a.store.Get(key)
	if !ok || !onDisk(t, a.disk.cache, key, h.now) {
		t.Fatal("the page is not in memory and on disk")
	}
	// A full batch of unrelated changes, then the page's removal.
	for i := range diskBatch {
		k := key
		k.Organisation = fmt.Sprintf("other-%d", i)
		a.store.Apply(k, cache.Replace, e.Payload, h.now)
	}
	a.store.Apply(key, cache.Remove, nil, h.now)
	a.diskRead(gateway.Inbox, key, forInbox)
	for cmd := a.flushDisk(); cmd != nil; cmd = a.flushDisk() {
		msg := cmd().(diskDone)
		if msg.err != nil {
			t.Fatal(msg.err)
		}
		for _, r := range msg.reads {
			if r.hit {
				t.Fatal("the read ran before the removal journaled ahead of it")
			}
		}
		h.m, _ = h.m.receiveDiskDone(msg)
	}
	if len(a.disk.reads) != 0 {
		t.Fatalf("%d reads never sent", len(a.disk.reads))
	}
	if _, ok := a.store.Get(key); ok {
		t.Fatal("the removed page is back in memory")
	}
	if onDisk(t, a.disk.cache, key, h.now) {
		t.Fatal("the removed page is still on disk")
	}
}

// The reload fails for a temporary reason before the disk hit lands: the
// entry the hit files says so, in memory and on disk, not only the view.
// Leaving and coming back must not drop the warning without a reload that
// succeeded.
func TestR3AFailedReloadBeforeADiskHitMarksTheEntryStale(t *testing.T) {
	root := t.TempDir()
	h := newDiskHarness(t, root, nil)
	h.start()
	h.drive(h.choose(orgA))
	h2 := h.restart(root)
	h2.start()
	a := h2.m.app
	key := a.inboxKey(orgA, 1)
	h2.f.set(func(q api.NotificationQuery) transport.Answer {
		if q.OrganisationID == orgA {
			return failing(transport.Temporary, 503)
		}
		return h2.d.inboxWithout(q, h2.f.readIDs())
	})
	var disk, network tea.Cmd
	for _, ch := range h2.spawn(tea.Batch(h2.feed(func() tea.Msg {
		h2.m.focus = view.Navigation
		a.highlight, a.highlightSection = orgA, sectionInbox
		return press("enter")
	}())...)) {
		msg := h2.receive(ch)
		switch msg.(type) {
		case diskDone:
			disk = func() tea.Msg { return msg }
		default:
			network = func() tea.Msg { return msg }
		}
	}
	if disk == nil || network == nil {
		t.Fatal("no disk read or no network read")
	}
	h2.drive(network)
	h2.drive(disk)
	if v := a.active; !v.cached || !v.stale {
		t.Fatalf("the view: %+v", v.status)
	}
	if e, ok := a.store.Get(key); !ok || !e.Stale {
		t.Fatalf("the entry in memory: stale %v, there %v", e.Stale, ok)
	}
	for a.disk.busy {
		h2.drive(h2.feed(nil)...)
	}
	if e, ok, err := a.disk.cache.Read(key, h2.now); err != nil || !ok || !e.Stale {
		t.Fatalf("the entry on disk: stale %v, there %v, %v", e.Stale, ok, err)
	}
}

// A read waits for the changes before it, not for those after it: it goes
// with the first batch, however many changes follow it.
func TestR3ADiskReadDoesNotWaitForLaterChanges(t *testing.T) {
	h := newDiskHarness(t, t.TempDir(), nil)
	h.start()
	h.drive(h.choose(orgA))
	a := h.m.app
	for a.disk.busy {
		h.drive(h.feed(nil)...)
	}
	key := a.inboxKey(orgA, 1)
	e, _ := a.store.Get(key)
	a.store.Forget(key)
	for a.disk.busy || a.disk.journal.Len() > 0 {
		if cmd := a.flushDisk(); cmd != nil {
			h.m, _ = h.m.receiveDiskDone(cmd().(diskDone))
		}
	}
	a.diskRead(gateway.Inbox, key, forInbox)
	for i := range 2 * diskBatch {
		k := key
		k.Organisation = fmt.Sprintf("other-%d", i)
		a.store.Apply(k, cache.Replace, e.Payload, h.now)
	}
	cmd := a.flushDisk()
	if cmd == nil {
		t.Fatal("no batch")
	}
	if msg := cmd().(diskDone); msg.err != nil || len(msg.reads) != 1 {
		t.Fatalf("the first batch answered %d reads (%v)", len(msg.reads), msg.err)
	}
}
