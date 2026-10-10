package ui

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/cache"
	"github.com/gboyke/lalabase-cli/internal/diskcache"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

// The disk cache in the session (C1e): the plan's tests 1 to 14 at the
// model's level, against a real cache directory of the test's own and
// restarts (a second session on the same directory, with the same fakes).

// newDiskHarness is a session with the disk cache under root.
func newDiskHarness(t *testing.T, root string, f *fakes) *harness {
	t.Helper()
	return buildHarness(t, f, &diskcache.Opener{Root: root})
}

// restart quits this session (q, which lets the disk go) and starts another
// on the same directory, with the same fakes: the server as it was.
func (h *harness) restart(root string) *harness {
	h.t.Helper()
	h.press("q")
	next := newDiskHarness(h.t, root, h.f)
	next.now = h.now
	return next
}

// entries lists the entry files of the profile's cache directory (through
// filepath alone: the root controller's tests import no os).
func entries(t *testing.T, root string) []string {
	t.Helper()
	list, err := filepath.Glob(filepath.Join(root, "default", "e-*"))
	if err != nil {
		t.Fatal(err)
	}
	return list
}

// exists says whether a path is there.
func exists(t *testing.T, path string) bool {
	t.Helper()
	list, err := filepath.Glob(path)
	if err != nil {
		t.Fatal(err)
	}
	return len(list) == 1
}

// spawn runs a command on supervised goroutines and returns one channel per
// leaf, each delivering that leaf's message when it comes. A batch is
// taken apart without running its leaves on the test's goroutine, so a
// leaf that waits for the network (a gated read) does not hold the test
// while the disk's answer is fed.
func (h *harness) spawn(cmd tea.Cmd) []<-chan tea.Msg {
	if cmd == nil {
		return nil
	}
	ch := h.background(cmd)
	select {
	case msg := <-ch:
		if batch, ok := msg.(tea.BatchMsg); ok {
			var out []<-chan tea.Msg
			for _, c := range batch {
				out = append(out, h.spawn(c)...)
			}
			return out
		}
		ready := make(chan tea.Msg, 1)
		ready <- msg
		return []<-chan tea.Msg{ready}
	case <-time.After(200 * time.Millisecond):
		return []<-chan tea.Msg{ch}
	}
}

// leaves is spawn as commands: each delivers its leaf's message when it
// comes (within the wall).
func (h *harness) leaves(cmd tea.Cmd) []tea.Cmd {
	var out []tea.Cmd
	for _, ch := range h.spawn(cmd) {
		out = append(out, func() tea.Msg { return h.receive(ch) })
	}
	return out
}

// diskOf opens the directory's cache after the session let it go, for a
// look at what is on disk, and closes it again.
func diskOf(t *testing.T, root string, now time.Time) *diskcache.Cache {
	t.Helper()
	c, err := (diskcache.Opener{Root: root}).Open("default", now)
	if err != nil {
		t.Fatalf("open the directory: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func onDisk(t *testing.T, c *diskcache.Cache, k cache.Key, now time.Time) bool {
	t.Helper()
	_, ok, err := c.Read(k, now)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

// Plan test 1: before /me succeeded no directory is made and nothing is
// read; a start that cannot verify the identity shows nothing of an earlier
// run, however much the disk holds (no offline start).
func TestTheDiskIsNotTouchedBeforeMeSucceeded(t *testing.T) {
	root := t.TempDir()
	h := newDiskHarness(t, root, nil)
	msgs := messages(t, h.m.Init())
	if exists(t, filepath.Join(root, "default")) {
		t.Fatal("the cache directory exists before /me")
	}
	if h.m.app.disk.cache != nil || h.m.app.disk.opening {
		t.Fatal("the disk was opened before /me")
	}
	h.drive(h.feed(msgs...)...)
	if h.m.app.disk.cache == nil {
		t.Fatal("the disk did not open after /me")
	}
	if !exists(t, filepath.Join(root, "default", "lock")) {
		t.Fatal("no cache directory after /me")
	}
	// A run that filled the disk, then one whose /me fails for a temporary
	// reason: nothing of the first is shown, nothing is read from disk.
	h.drive(h.choose(orgA))
	if len(entries(t, root)) == 0 {
		t.Fatal("nothing was written")
	}
	h.press("q")
	f := h.f
	f.me = func() (api.Me, transport.Answer) { return api.Me{}, failing(transport.Temporary, 503) }
	h2 := newDiskHarness(t, root, f)
	h2.start()
	if h2.m.app.disk.cache != nil || h2.m.app.disk.opening || h2.m.app.store != nil {
		t.Fatal("a disk or a store without a confirmed identity")
	}
	if cmd := h2.choose(orgA); cmd != nil || h2.m.app.active != nil {
		t.Fatal("an inbox opened without /me")
	}
	if got := ansi.Strip(h2.m.View().Content); strings.Contains(got, "Cached") || strings.Contains(got, "Message 0001") {
		t.Fatalf("an offline start:\n%s", got)
	}
}

// The start is what it was: five requests, none for the disk; the disk
// opens beside them and no request waits for it.
func TestTheStartIsStillFiveRequests(t *testing.T) {
	h := newDiskHarness(t, t.TempDir(), nil)
	h.start()
	if h.f.meCalls != 1 || h.f.initCall != 1 || h.f.toolCall != 1 || h.f.orgCalls != 1 || len(h.f.inboxCalls()) != 1 {
		t.Fatalf("me %d, init %d, tools %d, orgs %d, inbox %d", h.f.meCalls, h.f.initCall, h.f.toolCall, h.f.orgCalls, len(h.f.inboxCalls()))
	}
	if h.m.app.disk.cache == nil {
		t.Fatal("no disk after the start")
	}
}

// Plan test 4: a page written by one run is shown by the next as cached,
// with the load time of the network read, and reloaded exactly once.
func TestARestartShowsTheCachedPageWithItsLoadTime(t *testing.T) {
	root := t.TempDir()
	h := newDiskHarness(t, root, nil)
	h.start()
	h.drive(h.choose(orgA))
	loaded := h.now
	v := h.m.app.active
	if !v.known || v.cached || len(v.rows) != 25 {
		t.Fatalf("the first run: %+v", v.status)
	}
	h2 := h.restart(root)
	h2.now = loaded.Add(2 * time.Hour)
	h2.start()
	// The reload is held: what the person sees meanwhile comes from disk.
	gate := make(chan struct{})
	h2.f.gate, h2.f.gateOrg = gate, orgA
	before := len(h2.f.inboxCalls())
	cmds := h2.leaves(tea.Batch(h2.feed(func() tea.Msg {
		h2.m.focus = view.Navigation
		h2.m.app.highlight, h2.m.app.highlightSection = orgA, sectionInbox
		return press("enter")
	}())...))
	var network <-chan tea.Msg
	for _, cmd := range cmds {
		ch := h2.background(cmd)
		select {
		case msg := <-ch:
			h2.drive(h2.feed(msg)...)
		case <-time.After(300 * time.Millisecond):
			network = ch
		}
	}
	v = h2.m.app.active
	if !v.cached || !v.known || len(v.rows) != 25 || !v.loadedAt.Equal(loaded) || v.stale {
		t.Fatalf("after the restart: %+v, %d rows, loaded %s", v.status, len(v.rows), v.loadedAt)
	}
	if got := h2.m.listColumn().Notice.String(); got != "Cached" {
		t.Fatalf("notice %q", got)
	}
	if got := ansi.Strip(h2.m.View().Content); !strings.Contains(got, "Message 0001") {
		t.Fatalf("the cached rows are not shown:\n%s", got)
	}
	// Exactly one reload, held so far.
	if n := len(h2.f.inboxCalls()) - before; n != 1 {
		t.Fatalf("%d reloads", n)
	}
	close(gate)
	h2.drive(h2.feed(h2.receive(network))...)
	v = h2.m.app.active
	if v.cached || !v.known || !v.loadedAt.Equal(h2.now) {
		t.Fatalf("after the reload: %+v", v.status)
	}
	if n := len(h2.f.inboxCalls()) - before; n != 1 {
		t.Fatalf("%d reads after the reload", n)
	}
}

// Plan test 5: an entry older than a day is not shown after a restart.
func TestAnExpiredEntryIsNotShownAfterARestart(t *testing.T) {
	root := t.TempDir()
	h := newDiskHarness(t, root, nil)
	h.start()
	h.drive(h.choose(orgA))
	h2 := h.restart(root)
	h2.now = h.now.Add(diskcache.MaxAge + time.Minute)
	h2.start()
	gate := make(chan struct{})
	h2.f.gate, h2.f.gateOrg = gate, orgA
	cmds := h2.leaves(tea.Batch(h2.feed(func() tea.Msg {
		h2.m.focus = view.Navigation
		h2.m.app.highlight, h2.m.app.highlightSection = orgA, sectionInbox
		return press("enter")
	}())...))
	var network <-chan tea.Msg
	for _, cmd := range cmds {
		ch := h2.background(cmd)
		select {
		case msg := <-ch:
			h2.drive(h2.feed(msg)...)
		case <-time.After(300 * time.Millisecond):
			network = ch
		}
	}
	if v := h2.m.app.active; v.known || len(v.rows) != 0 {
		t.Fatalf("an expired entry was shown: %+v", v.status)
	}
	close(gate)
	h2.drive(h2.feed(h2.receive(network))...)
	if v := h2.m.app.active; !v.known || v.cached {
		t.Fatalf("after the reload: %+v", v.status)
	}
}

// Plan test 2: the same page under another token id is another entry: a
// restart whose /me confirms another token sees nothing of the first.
func TestAnotherTokenIdSeesNothingOfTheFirst(t *testing.T) {
	root := t.TempDir()
	h := newDiskHarness(t, root, nil)
	h.start()
	h.drive(h.choose(orgA))
	h.press("q")
	f := h.f
	me := h.d.me
	me.Token.ID = "another-token-id"
	f.me = func() (api.Me, transport.Answer) {
		return me, transport.Answer{Class: transport.Object, Status: 200, Sent: true}
	}
	h2 := newDiskHarness(t, root, f)
	h2.start()
	gate := make(chan struct{})
	h2.f.gate, h2.f.gateOrg = gate, orgA
	cmds := h2.leaves(tea.Batch(h2.feed(func() tea.Msg {
		h2.m.focus = view.Navigation
		h2.m.app.highlight, h2.m.app.highlightSection = orgA, sectionInbox
		return press("enter")
	}())...))
	var network <-chan tea.Msg
	for _, cmd := range cmds {
		ch := h2.background(cmd)
		select {
		case msg := <-ch:
			h2.drive(h2.feed(msg)...)
		case <-time.After(300 * time.Millisecond):
			network = ch
		}
	}
	if v := h2.m.app.active; v.known || len(v.rows) != 0 {
		t.Fatalf("another token saw the first one's page: %+v", v.status)
	}
	close(gate)
	h2.drive(h2.feed(h2.receive(network))...)
	// Both identities are on disk now, apart.
	if n := len(entries(t, root)); n < 3 {
		t.Fatalf("%d entries", n)
	}
}

// Plan tests 6 and 9: a disk entry that does not read as the page asked for
// (valid JSON of another shape) is forgotten on disk too, not only in
// memory; every read of it still out brings nothing back.
func TestADiskEntryThatDoesNotDecodeIsForgottenOnDisk(t *testing.T) {
	root := t.TempDir()
	h := newDiskHarness(t, root, nil)
	h.start()
	key := h.m.app.inboxKey(orgA, 1)
	// Planted on disk by the session's own cache: valid JSON, not a page.
	if err := h.m.app.disk.cache.Apply([]cache.Change{{Kind: cache.ChangePut, Key: key, Entry: cache.Entry{Payload: []byte(`{"data":"no page"}`), LoadedAt: h.now}}}, h.now); err != nil {
		t.Fatal(err)
	}
	if n := len(entries(t, root)); n != 2 {
		t.Fatalf("%d entries before", n)
	}
	gate := make(chan struct{})
	h.f.gate, h.f.gateOrg = gate, orgA
	cmds := h.leaves(tea.Batch(h.feed(func() tea.Msg {
		h.m.focus = view.Navigation
		h.m.app.highlight, h.m.app.highlightSection = orgA, sectionInbox
		return press("enter")
	}())...))
	var network <-chan tea.Msg
	for _, cmd := range cmds {
		ch := h.background(cmd)
		select {
		case msg := <-ch:
			h.drive(h.feed(msg)...)
		case <-time.After(300 * time.Millisecond):
			network = ch
		}
	}
	if v := h.m.app.active; v.known || len(v.rows) != 0 {
		t.Fatalf("a page that does not decode was shown: %+v", v.status)
	}
	if _, ok := h.m.app.store.Get(key); ok {
		t.Fatal("the entry stayed in memory")
	}
	if n := len(entries(t, root)); n != 1 {
		t.Fatalf("%d entries after: the bad one stayed on disk", n)
	}
	close(gate)
	h.drive(h.feed(h.receive(network))...)
	if v := h.m.app.active; !v.known || v.cached {
		t.Fatalf("after the reload: %+v", v.status)
	}
}

// Plan test 9: an eviction from memory keeps the disk entry; a removal by
// the server takes both.
func TestEvictionKeepsTheDiskARemovalTakesBoth(t *testing.T) {
	root := t.TempDir()
	h := newDiskHarness(t, root, nil)
	h.start()
	a := h.m.app
	first := a.inboxKey(orgA, 1)
	h.drive(h.choose(orgA))
	// Fill memory past its limit through the store, as answers would.
	for i := range cache.MaxEntries {
		h.drive(h.feed(func() tea.Msg {
			a.store.Apply(a.inboxKey(orgB, i+2), cache.Replace, []byte(`{"filler":true}`), h.now)
			return nil
		}())...)
	}
	if _, ok := a.store.Get(first); ok {
		t.Fatal("the first page was not evicted")
	}
	h.drive(h.feed(nil)...) // the flush
	for a.disk.busy {
		h.drive(h.feed(nil)...)
	}
	d := a.disk.cache
	if _, ok, err := d.Read(first, h.now); !ok || err != nil {
		t.Fatalf("the evicted page left the disk: %v %v", ok, err)
	}
	// The server removes the organisation's inbox: not found on R.
	h.f.set(func(q api.NotificationQuery) transport.Answer {
		if q.OrganisationID == orgA {
			return transport.Answer{Class: transport.NotFound, Message: "gone", Status: 200, Sent: true}
		}
		return h.d.inboxWithout(q, h.f.readIDs())
	})
	h.drive(h.press("R"))
	for a.disk.busy {
		h.drive(h.feed(nil)...)
	}
	if _, ok, _ := d.Read(first, h.now); ok {
		t.Fatal("the removed page stayed on disk")
	}
	if _, ok := a.store.Get(first); ok {
		t.Fatal("the removed page stayed in memory")
	}
}

// Plan test 10: a disk read that comes after a newer network answer changes
// nothing; a disk write journaled before an invalidation does not bring
// the data back.
func TestLateDiskWorkChangesNothing(t *testing.T) {
	root := t.TempDir()
	h := newDiskHarness(t, root, nil)
	h.start()
	h.drive(h.choose(orgA))
	h2 := h.restart(root)
	h2.start()
	a := h2.m.app
	key := a.inboxKey(orgA, 1)
	// The page from disk is held back; the network answers first.
	cmds := h2.leaves(tea.Batch(h2.feed(func() tea.Msg {
		h2.m.focus = view.Navigation
		a.highlight, a.highlightSection = orgA, sectionInbox
		return press("enter")
	}())...))
	var disk, network tea.Cmd
	for _, cmd := range cmds {
		msg := cmd()
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
	v := a.active
	if !v.known || v.cached || !v.loadedAt.Equal(h2.now) {
		t.Fatalf("the network answer: %+v", v.status)
	}
	h2.drive(disk)
	if v.cached || !v.loadedAt.Equal(h2.now) || !v.holdsServerData() {
		t.Fatalf("the late disk read changed the view: %+v", v.status)
	}
	if e, ok := a.store.Get(key); !ok || !e.LoadedAt.Equal(h2.now) {
		t.Fatalf("the late disk read changed memory: %+v %v", e, ok)
	}
	// A removal that lands before the disk read: the read brings nothing
	// back (the epoch moved on).
	h3 := h2.restart(root)
	h3.start()
	a = h3.m.app
	h3.f.set(func(q api.NotificationQuery) transport.Answer {
		if q.OrganisationID == orgA {
			return transport.Answer{Class: transport.NotFound, Message: "gone", Status: 200, Sent: true}
		}
		return h3.d.inboxWithout(q, h3.f.readIDs())
	})
	cmds = h3.leaves(tea.Batch(h3.feed(func() tea.Msg {
		h3.m.focus = view.Navigation
		a.highlight, a.highlightSection = orgA, sectionInbox
		return press("enter")
	}())...))
	disk, network = nil, nil
	for _, cmd := range cmds {
		msg := cmd()
		switch msg.(type) {
		case diskDone:
			disk = func() tea.Msg { return msg }
		default:
			network = func() tea.Msg { return msg }
		}
	}
	h3.drive(network)
	if v := a.active; v.known || v.err.String() == "" {
		t.Fatalf("the removal: %+v", v.status)
	}
	h3.drive(disk)
	if v := a.active; v.known || len(v.rows) != 0 {
		t.Fatalf("the disk read brought a removed page back: %+v", v.status)
	}
	if _, ok := a.store.Get(key); ok {
		t.Fatal("the removed page is back in memory")
	}
	for a.disk.busy {
		h3.drive(h3.feed(nil)...)
	}
	if _, ok, _ := a.disk.cache.Read(key, h3.now); ok {
		t.Fatal("the removed page is still on disk")
	}
	// A write journaled before an invalidation: the batch that holds it is
	// out when the mark invalidates the inbox; the removal follows it, and
	// the disk ends without the page.
	h4 := h3.restart(root)
	h4.start()
	a = h4.m.app
	h4.f.set(func(q api.NotificationQuery) transport.Answer { return h4.d.inboxWithout(q, h4.f.readIDs()) })
	cmds = h4.leaves(tea.Batch(h4.feed(func() tea.Msg {
		h4.m.focus = view.Navigation
		a.highlight, a.highlightSection = orgA, sectionInbox
		return press("enter")
	}())...))
	// Run the reads, feed the network answer, but hold the flush that
	// writes the page: feed collects it without running it.
	var pending []tea.Cmd
	for _, cmd := range cmds {
		msg := cmd()
		if _, isDisk := msg.(diskDone); isDisk {
			pending = append(pending, h4.feed(msg)...)
			continue
		}
		pending = append(pending, h4.feed(msg)...)
	}
	if !a.disk.busy || len(pending) == 0 {
		t.Fatal("no write is out")
	}
	// The mark: the inbox is invalidated while the write is out.
	v = a.active
	v.selected = v.rows[0].ID
	marked := v.selected
	markCmds := h4.feed(press("r"))
	h4.drive(markCmds...)
	// Now the held write lands, then the removal.
	h4.drive(pending...)
	for a.disk.busy {
		h4.drive(h4.feed(nil)...)
	}
	e, ok, err := a.disk.cache.Read(a.inboxKey(orgA, 1), h4.now)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		page, _ := api.DecodeNotifications(transport.Answer{Class: transport.Object, Payload: e.Payload, Status: 200, Sent: true})
		for _, row := range page.Data {
			if row.ID == marked {
				t.Fatal("the write before the mark brought the marked row back to disk")
			}
		}
	}
	// A network answer too large for memory: the view is the server's and
	// memory holds nothing; a disk read that comes after it must not file
	// the old page into memory either (the request filed since says so).
	h5 := h4.restart(root)
	h5.start()
	a = h5.m.app
	big := strings.Repeat("x", cache.MaxEntryBytes)
	h5.f.set(func(q api.NotificationQuery) transport.Answer {
		ans := h5.d.inboxWithout(q, h5.f.readIDs())
		if q.OrganisationID != orgA {
			return ans
		}
		page, _ := api.DecodeNotifications(ans)
		page.Data[0].Message = big
		return objectAnswer(page)
	})
	cmds = h5.leaves(tea.Batch(h5.feed(func() tea.Msg {
		h5.m.focus = view.Navigation
		a.highlight, a.highlightSection = orgA, sectionInbox
		return press("enter")
	}())...))
	disk, network = nil, nil
	for _, cmd := range cmds {
		msg := cmd()
		switch msg.(type) {
		case diskDone:
			disk = func() tea.Msg { return msg }
		default:
			network = func() tea.Msg { return msg }
		}
	}
	h5.drive(network)
	v = a.active
	if !v.holdsServerData() || len(v.rows) != 25 {
		t.Fatalf("the oversized answer: %+v", v.status)
	}
	if _, ok := a.store.Get(key); ok {
		t.Fatal("an oversized answer was kept in memory")
	}
	h5.drive(disk)
	if _, ok := a.store.Get(key); ok {
		t.Fatal("the late disk read filed the old page into memory")
	}
	if !v.holdsServerData() {
		t.Fatalf("the late disk read changed the view: %+v", v.status)
	}
}

// The reads waiting for the disk are bounded: past maxDiskReads the oldest
// waiting one is dropped, its ticket given back, and the flush sends only
// what waits.
func TestDiskReadsAreBounded(t *testing.T) {
	h := newDiskHarness(t, t.TempDir(), nil)
	h.start()
	a := h.m.app
	for i := range 3 * maxDiskReads {
		a.diskRead(gateway.Inbox, a.inboxKey(orgA, i+1), forInbox)
	}
	waiting := 0
	for _, r := range a.disk.reads {
		if !r.sent && !r.canceled {
			waiting++
		}
	}
	if waiting != maxDiskReads {
		t.Fatalf("%d reads waiting", waiting)
	}
	// The newest wait, the oldest were dropped.
	if r := a.disk.reads[a.disk.order[len(a.disk.order)-1]]; r.canceled {
		t.Fatal("the newest read was dropped")
	}
	if r := a.disk.reads[a.disk.order[0]]; !r.canceled {
		t.Fatal("the oldest read was kept")
	}
	cmd := a.flushDisk()
	if cmd == nil {
		t.Fatal("no batch")
	}
	if len(a.disk.reads) != maxDiskReads {
		t.Fatalf("%d reads known after the flush", len(a.disk.reads))
	}
	h.drive(cmd)
	if len(a.disk.reads) != 0 {
		t.Fatalf("%d reads left", len(a.disk.reads))
	}
}

// Plan test 11: a 401 removes every entry of the identity from the disk;
// a write out at the time fills nothing again; another identity's entries
// stay.
func TestA401ClearsTheNamespaceOnDisk(t *testing.T) {
	root := t.TempDir()
	h := newDiskHarness(t, root, nil)
	h.start()
	a := h.m.app
	// Another identity's entry in the same directory.
	other, _ := cache.Confirmed("default", "https://example.invalid", "someone-else")
	otherKey := other.Key(api.NotificationsSpec.Tool)
	otherKey.Organisation, otherKey.Status, otherKey.Page, otherKey.PerPage = orgA, "unread", 1, 25
	if err := a.disk.cache.Apply([]cache.Change{{Kind: cache.ChangePut, Key: otherKey, Entry: cache.Entry{Payload: []byte(`{"other":true}`), LoadedAt: h.now}}}, h.now); err != nil {
		t.Fatal(err)
	}
	h.drive(h.choose(orgA))
	h.drive(h.press("]"))
	if n := len(entries(t, root)); n != 4 {
		t.Fatalf("%d entries before the 401", n)
	}
	// A write is out when the 401 comes: the flush of this update is held.
	held := h.feed(func() tea.Msg {
		a.store.Apply(a.inboxKey(orgB, 1), cache.Replace, []byte(`{"late":true}`), h.now)
		return nil
	}())
	if !a.disk.busy {
		t.Fatal("no write is out")
	}
	h.f.set(func(q api.NotificationQuery) transport.Answer { return failing(transport.Unauthenticated, 401) })
	h.drive(h.press("R"))
	if !a.expired {
		t.Fatal("not expired")
	}
	if a.store != nil {
		t.Fatal("the store survived")
	}
	// The late write lands, then the removal of the namespace.
	h.drive(held...)
	for a.disk.busy {
		h.drive(h.feed(nil)...)
	}
	if a.disk.clearing || a.disk.clearFailed {
		t.Fatalf("clearing %v, failed %v", a.disk.clearing, a.disk.clearFailed)
	}
	if got := ansi.Strip(h.m.View().Content); !strings.Contains(got, "Session expired. Restart lalabase.") || strings.Contains(got, "could not be cleared") {
		t.Fatalf("status:\n%s", got)
	}
	// The directory holds the other identity's entry alone, and the lock.
	got := entries(t, root)
	if len(got) != 1 {
		t.Fatalf("after the 401: %v", got)
	}
	d := diskOf(t, root, h.now)
	if !onDisk(t, d, otherKey, h.now) {
		t.Fatal("the other identity's entry went")
	}
	// A late disk read fills nothing: the session is over.
	if cmd := h.m.app.flushDisk(); cmd != nil {
		t.Fatal("work after the end")
	}
}

// When the namespace cannot be removed (the file system fails) the expired
// session says so instead of claiming it was.
func TestA401ThatCannotClearTheDiskSaysSo(t *testing.T) {
	root := t.TempDir()
	fail := false
	o := &diskcache.Opener{Root: root, Faults: func(step, _ string) error {
		if fail && step == "remove" {
			return errors.New("injected remove")
		}
		return nil
	}}
	h := buildHarness(t, nil, o)
	h.start()
	h.drive(h.choose(orgA))
	fail = true
	h.f.set(func(q api.NotificationQuery) transport.Answer { return failing(transport.Unauthenticated, 401) })
	h.drive(h.press("R"))
	a := h.m.app
	for a.disk.busy {
		h.drive(h.feed(nil)...)
	}
	if !a.expired || !a.disk.clearFailed || !a.disk.off {
		t.Fatalf("expired %v, clear failed %v, off %v", a.expired, a.disk.clearFailed, a.disk.off)
	}
	if got := ansi.Strip(h.m.View().Content); !strings.Contains(got, "could not be cleared") {
		t.Fatalf("status:\n%s", got)
	}
	if n := len(entries(t, root)); n == 0 {
		t.Fatal("the entries went although the removal failed")
	}
}

// Plan test 12: a marked notification and a missing comment do not come
// back from disk after a restart: the mark takes every page of the inbox
// (not only the one shown), the missing comment takes its expansion.
func TestMarksAndMissingCommentsDoNotComeBackFromDisk(t *testing.T) {
	root := t.TempDir()
	h := newDiskHarness(t, root, nil)
	h.start()
	a := h.m.app
	h.drive(h.choose(orgA))
	h.drive(h.press("]"))
	page2 := a.active.key
	h.drive(h.press("["))
	for a.disk.busy {
		h.drive(h.feed(nil)...)
	}
	if _, ok, _ := a.disk.cache.Read(page2, h.now); !ok {
		t.Fatal("page 2 is not on disk before the mark")
	}
	a.active.selected = a.active.rows[0].ID
	h.drive(h.press("r"))
	for a.disk.busy {
		h.drive(h.feed(nil)...)
	}
	if _, ok, _ := a.disk.cache.Read(page2, h.now); ok {
		t.Fatal("the mark left page 2 on disk")
	}
	// The expansion of a comment, then the comment is gone.
	h.drive(h.chooseTickets(orgA))
	h.drive(h.openProject(website))
	a.tickets.selected = ticketID(website, 6)
	h.drive(h.press("enter"))
	h.drive(h.press("tab"))
	h.drive(h.press("e"))
	e := a.detail.expansion
	if e == nil || !e.known {
		t.Fatalf("no expansion: %+v", e)
	}
	expansionKey := e.key
	for a.disk.busy {
		h.drive(h.feed(nil)...)
	}
	if _, ok, _ := a.disk.cache.Read(expansionKey, h.now); !ok {
		t.Fatal("the expansion is not on disk")
	}
	h.drive(h.press("esc"))
	h.f.setTicket(func(project, id string, o api.TicketOptions) transport.Answer {
		if o.PostID != "" {
			return transport.Answer{Class: transport.Refused, Reason: api.CommentNotFound, Message: "No comment found.", Status: 200, Sent: true}
		}
		return h.d.defaultTicket(project, id, o)
	})
	h.drive(h.press("e"))
	if e := a.detail.expansion; e == nil || !e.unavailable {
		t.Fatalf("the comment is not missing: %+v", e)
	}
	for a.disk.busy {
		h.drive(h.feed(nil)...)
	}
	if _, ok, _ := a.disk.cache.Read(expansionKey, h.now); ok {
		t.Fatal("the missing comment's expansion stayed on disk")
	}
	// After a restart neither comes back as cached.
	h2 := h.restart(root)
	h2.start()
	a = h2.m.app
	gate := make(chan struct{})
	h2.f.gate, h2.f.gateOrg, h2.f.gatePage = gate, orgA, 2
	h2.drive(h2.choose(orgA))
	cmds := h2.leaves(tea.Batch(h2.feed(press("]"))...))
	var network <-chan tea.Msg
	for _, cmd := range cmds {
		ch := h2.background(cmd)
		select {
		case msg := <-ch:
			h2.drive(h2.feed(msg)...)
		case <-time.After(300 * time.Millisecond):
			network = ch
		}
	}
	if v := a.active; v.known || v.cached {
		t.Fatalf("page 2 came back from disk after the mark: %+v", v.status)
	}
	close(gate)
	h2.drive(h2.feed(h2.receive(network))...)
}

// Plan test 14: a failing file system keeps the network and the interface,
// says so once, and the session goes on with memory alone.
func TestADiskFailureFallsBackToMemoryWithOneNotice(t *testing.T) {
	root := t.TempDir()
	fail := false
	o := &diskcache.Opener{Root: root, Faults: func(step, _ string) error {
		if fail && step == "rename" {
			return errors.New("injected rename")
		}
		return nil
	}}
	h := buildHarness(t, nil, o)
	h.start()
	a := h.m.app
	fail = true
	h.drive(h.choose(orgA))
	for a.disk.busy {
		h.drive(h.feed(nil)...)
	}
	if !a.disk.off || a.disk.cache != nil {
		t.Fatal("the disk is still on")
	}
	v := a.active
	if !v.known || len(v.rows) != 25 {
		t.Fatalf("the inbox did not load: %+v", v.status)
	}
	want := "Disk cache unavailable; using memory only"
	if got := h.m.screen().Status.String(); got != want {
		t.Fatalf("status %q", got)
	}
	// It goes on: another page, the tickets; memory caches as before, the
	// journal does not pile up.
	h.drive(h.press("]"))
	if v := a.active; !v.known || v.page != 2 || len(v.rows) != 5 {
		t.Fatalf("page 2 after the fallback: %+v, %d rows", v.status, len(v.rows))
	}
	h.drive(h.press("["))
	if v := a.active; !v.known || v.page != 1 || len(v.rows) != 25 {
		t.Fatalf("page 1 after the fallback: %+v", v.status)
	}
	h.drive(h.chooseTickets(orgA))
	if a.projects == nil || !a.projects.known {
		t.Fatal("the tickets did not open")
	}
	if a.disk.journal.Len() != 0 {
		t.Fatalf("%d changes piled up", a.disk.journal.Len())
	}
	if got := h.m.screen().Status.String(); got != want {
		t.Fatalf("status later %q", got)
	}
	// A directory that cannot be used at all: the same fallback, the same
	// notice; and a lock held elsewhere its own.
	busyRoot := t.TempDir()
	holder := diskOf(t, busyRoot, h.now)
	hb := newDiskHarness(t, busyRoot, nil)
	hb.start()
	if got := hb.m.screen().Status.String(); got != "Disk cache in use by another lalabase; using memory only" {
		t.Fatalf("busy status %q", got)
	}
	_ = holder.Close()
	hb.drive(hb.choose(orgA))
	if v := hb.m.app.active; !v.known {
		t.Fatal("the inbox did not load with the disk busy")
	}
	// A root that cannot name a directory (relative) opens nothing.
	hu := newDiskHarness(t, "relative-root", nil)
	hu.start()
	if got := hu.m.screen().Status.String(); got != want {
		t.Fatalf("unusable status %q", got)
	}
}

// Plan test 14: foreign text persisted by one run reaches the terminal of
// the next through present alone: no dangerous byte of it.
func TestHostileTextFromDiskIsCleanedAgain(t *testing.T) {
	root := t.TempDir()
	h := newDiskHarness(t, root, nil)
	const hostile = "\x1b[2J\x1b]8;;https://evil.invalid\x07LINK\x1b]8;;\x1b\\\u202eEVIL\u2066"
	h.f.set(func(q api.NotificationQuery) transport.Answer {
		ans := h.d.inboxWithout(q, h.f.readIDs())
		page, _ := api.DecodeNotifications(ans)
		for i := range page.Data {
			page.Data[i].Message = "Hostile " + hostile
		}
		return objectAnswer(page)
	})
	h.start()
	h.drive(h.choose(orgA))
	h2 := h.restart(root)
	h2.start()
	gate := make(chan struct{})
	h2.f.gate, h2.f.gateOrg = gate, orgA
	cmds := h2.leaves(tea.Batch(h2.feed(func() tea.Msg {
		h2.m.focus = view.Navigation
		h2.m.app.highlight, h2.m.app.highlightSection = orgA, sectionInbox
		return press("enter")
	}())...))
	var network <-chan tea.Msg
	for _, cmd := range cmds {
		ch := h2.background(cmd)
		select {
		case msg := <-ch:
			h2.drive(h2.feed(msg)...)
		case <-time.After(300 * time.Millisecond):
			network = ch
		}
	}
	if v := h2.m.app.active; !v.cached || len(v.rows) == 0 {
		t.Fatalf("not shown from disk: %+v", v.status)
	}
	// The rows are shown (cut to the column), their dangerous bytes gone.
	out := h2.m.View().Content
	if !strings.Contains(out, "Hostile") {
		t.Fatalf("the cached rows are not shown:\n%s", ansi.Strip(out))
	}
	for _, bad := range []string{"\x1b[2J", "\x1b]8;;", "\x07", "\u202e", "\u2066"} {
		if strings.Contains(out, bad) {
			t.Fatalf("%q reached the terminal from disk", bad)
		}
	}
	close(gate)
	h2.drive(h2.feed(h2.receive(network))...)
}

// A quit lets the disk go at once: the next run on the same directory
// opens it.
func TestAQuitReleasesTheDisk(t *testing.T) {
	root := t.TempDir()
	h := newDiskHarness(t, root, nil)
	h.start()
	if h.m.app.disk.cache == nil {
		t.Fatal("no disk")
	}
	h.press("q")
	if h.m.app.disk.cache != nil {
		t.Fatal("the disk was not let go")
	}
	c, err := (diskcache.Opener{Root: root}).Open("default", h.now)
	if err != nil {
		t.Fatalf("after the quit: %v", err)
	}
	_ = c.Close()
	// Ctrl-C too.
	h3 := newDiskHarness(t, root, nil)
	h3.start()
	h3.press("ctrl+c")
	if h3.m.app.disk.cache != nil {
		t.Fatal("the disk was not let go on ctrl+c")
	}
}

// A cached ticket shows from disk at once for an open; the projects, the
// list, another page of comments and the expansion too, each while its
// reload is still out.
func TestTicketsPagesAndExpansionsComeFromDisk(t *testing.T) {
	root := t.TempDir()
	h := newDiskHarness(t, root, nil)
	h.openTickets(website)
	a := h.m.app
	a.tickets.selected = ticketID(website, 6)
	h.drive(h.press("enter"))
	h.drive(h.press("tab"))
	h.drive(h.press("]"))
	h.drive(h.press("tab"))
	h.drive(h.press("tab"))
	h.drive(h.press("e"))
	if a.detail == nil || a.detail.expansion == nil || !a.detail.expansion.known {
		t.Fatal("the first run did not expand")
	}
	h2 := h.restart(root)
	h2.start()
	a = h2.m.app
	// open presses a key whose reads are gated: what shows comes from disk;
	// the reads still out are returned, to be drained once the gate opens.
	open := func(cmd tea.Cmd) []<-chan tea.Msg {
		t.Helper()
		var network []<-chan tea.Msg
		for _, ch := range h2.spawn(cmd) {
			select {
			case msg := <-ch:
				h2.drive(h2.feed(msg)...)
			case <-time.After(300 * time.Millisecond):
				network = append(network, ch)
			}
		}
		if len(network) == 0 {
			t.Fatal("nothing waited for the network")
		}
		return network
	}
	drain := func(pending []<-chan tea.Msg) {
		t.Helper()
		for _, ch := range pending {
			h2.drive(h2.feed(h2.receive(ch))...)
		}
	}
	gate := make(chan struct{})
	h2.f.setGate(gate, "", false)
	h2.f.gateTickets, h2.f.gateProjects = true, true
	var pending []<-chan tea.Msg
	pending = append(pending, open(tea.Batch(h2.feed(func() tea.Msg {
		h2.m.focus = view.Navigation
		a.highlight, a.highlightSection = orgA, sectionTickets
		return press("enter")
	}())...))...)
	if p := a.projects; p == nil || !p.cached || len(p.rows) != 4 {
		t.Fatalf("projects from disk: %+v", p)
	}
	a.projects.selected = website
	h2.m.focus = view.List
	pending = append(pending, open(tea.Batch(h2.feed(press("enter"))...))...)
	if v := a.tickets; v == nil || !v.cached || len(v.rows) != 25 {
		t.Fatalf("tickets from disk: %+v", v)
	}
	// The list from disk starts the preview of its first row, as a list
	// from memory does (review round 1, B9).
	if a.previewToken == 0 || a.tickets.selected != a.tickets.rows[0].ID {
		t.Fatalf("no preview after the list from disk: token %d, selected %q", a.previewToken, a.tickets.selected)
	}
	h2.f.setGate(gate, ticketID(website, 6), false)
	a.tickets.selected = ticketID(website, 6)
	pending = append(pending, open(tea.Batch(h2.feed(press("enter"))...))...)
	d := a.detail
	if d == nil || !d.cached || d.record.ID != ticketID(website, 6) {
		t.Fatalf("the ticket from disk: %+v", d)
	}
	if got := h2.m.objectColumn().Notice.String(); got != "Cached" {
		t.Fatalf("notice %q", got)
	}
	// The reloads land: everything is the server's now.
	close(gate)
	drain(pending)
	if d.cached || d.loading || a.tickets.cached || a.projects.cached {
		t.Fatalf("after the reloads: ticket %+v, tickets %+v, projects %+v", d.status, a.tickets.status, a.projects.status)
	}
	// Comments page 2, cached in the first run, shows at once while its
	// reload is out; the expansion of the description too.
	gate = make(chan struct{})
	h2.f.setGate(gate, ticketID(website, 6), false)
	h2.drive(h2.feed(press("tab"))...)
	pending = open(tea.Batch(h2.feed(press("]"))...))
	if d.shown.PostsPage != 2 || !d.cached {
		t.Fatalf("page 2 from disk: shown %d, %+v", d.shown.PostsPage, d.status)
	}
	close(gate)
	drain(pending)
	gate = make(chan struct{})
	h2.f.setGate(gate, ticketID(website, 6), false)
	h2.drive(h2.feed(press("tab"))...)
	h2.drive(h2.feed(press("tab"))...)
	pending = open(tea.Batch(h2.feed(press("e"))...))
	if e := d.expansion; e == nil || !e.cached || !strings.Contains(e.text, "whole rest") {
		t.Fatalf("the expansion from disk: %+v", e)
	}
	close(gate)
	drain(pending)
}

// --- review round 1 ----------------------------------------------------------

// gatedOpener holds the open at its first step until gate is closed, and
// then fails it when *fail is set: a slow or failing disk at the start.
func gatedOpener(root string, gate chan struct{}, fail *bool) *diskcache.Opener {
	return &diskcache.Opener{Root: root, Faults: func(step, _ string) error {
		if step == "mkdir" {
			<-gate
			if *fail {
				return errors.New("injected open failure")
			}
		}
		return nil
	}}
}

// startGated runs the start with the open held: the /me result, then
// initialize and the organisations and what follows them on the test's
// goroutine, the open on a supervised one. It returns the open's channel.
func (h *harness) startGated() <-chan tea.Msg {
	h.t.Helper()
	cmds := h.feed(messages(h.t, h.m.Init())...)
	var opened <-chan tea.Msg
	for _, ch := range h.spawn(tea.Batch(cmds...)) {
		select {
		case msg := <-ch:
			h.drive(h.feed(msg)...)
		case <-time.After(300 * time.Millisecond):
			opened = ch
		}
	}
	if opened == nil {
		h.t.Fatal("the open was not held")
	}
	return opened
}

// Finding 1: a 401 on /me ends a session that confirmed no identity: no
// directory was made, nothing is claimed about clearing one.
func TestA401OnMeClaimsNothingAboutTheDisk(t *testing.T) {
	root := t.TempDir()
	h := newDiskHarness(t, root, nil)
	h.f.me = func() (api.Me, transport.Answer) { return api.Me{}, failing(transport.Unauthenticated, 401) }
	h.start()
	a := h.m.app
	if !a.expired || a.disk.clearing || a.disk.clearFailed {
		t.Fatalf("expired %v, clearing %v, failed %v", a.expired, a.disk.clearing, a.disk.clearFailed)
	}
	if got := h.m.screen().Status.String(); got != "Session expired. Restart lalabase." {
		t.Fatalf("status %q", got)
	}
	if exists(t, filepath.Join(root, "default")) {
		t.Fatal("a directory was made without an identity")
	}
}

// Finding 3: the disk commands run under the guarded root like every
// command of the model: a panic in them is the supervisor's fatal error,
// never Bubble Tea's print.
func TestAPanicInADiskCommandIsTheSupervisorsFatalError(t *testing.T) {
	root := t.TempDir()
	explode := false
	o := &diskcache.Opener{Root: root, Faults: func(step, _ string) error {
		if explode && step == "create" {
			panic("disk fault")
		}
		return nil
	}}
	h := buildHarness(t, nil, o)
	h.start()
	a := h.m.app
	explode = true
	// A change to write; the update's flush writes it, and the guarded root
	// runs that command as it runs every command of the model.
	cmds := h.feed(func() tea.Msg {
		a.store.Apply(a.inboxKey(orgA, 1), cache.Replace, []byte(`{"data":[],"meta":{}}`), h.now)
		return nil
	}())
	if len(cmds) == 0 {
		t.Fatal("no flush")
	}
	sup := h.live
	g := guarded{model: h.m, sup: sup}
	msg := g.guard(tea.Batch(cmds...))()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			if m := c(); m != nil {
				t.Fatalf("a message after the panic: %+v", m)
			}
		}
	} else if msg != nil {
		t.Fatalf("a message after the panic: %+v", msg)
	}
	if err := sup.Err(); err == nil || !strings.Contains(err.Error(), "ui: command") {
		t.Fatalf("the supervisor's error: %v", err)
	}
}

// Probe M3: the reload fails for a temporary reason before the disk hit
// lands: the page shows as cached and possibly stale, not as unreachable.
func TestADiskHitAfterATemporaryFailureIsCachedAndStale(t *testing.T) {
	root := t.TempDir()
	h := newDiskHarness(t, root, nil)
	h.start()
	h.drive(h.choose(orgA))
	h2 := h.restart(root)
	h2.start()
	a := h2.m.app
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
	h2.drive(network)
	v := a.active
	if v.known || !v.stale || v.err.String() == "" {
		t.Fatalf("after the failure: %+v", v.status)
	}
	h2.drive(disk)
	if !v.known || !v.cached || !v.stale || v.err.String() != "" || len(v.rows) != 25 {
		t.Fatalf("after the disk hit: %+v", v.status)
	}
	if got := h2.m.listColumn().Notice.String(); got != "Cached, possibly stale" {
		t.Fatalf("notice %q", got)
	}
}

// Probe M4: changes journaled while the open is still out wait for it;
// nothing is thrown away before the disk said whether it opens.
func TestChangesWaitForTheOpen(t *testing.T) {
	root := t.TempDir()
	gate, fail := make(chan struct{}), false
	h := buildHarness(t, nil, gatedOpener(root, gate, &fail))
	opened := h.startGated()
	a := h.m.app
	if a.disk.cache != nil || !a.disk.opening {
		t.Fatal("the open is not pending")
	}
	if a.disk.journal.Len() == 0 {
		t.Fatal("the counters' page is not journaled")
	}
	h.drive(h.choose(orgA))
	if a.disk.journal.Len() < 2 {
		t.Fatalf("%d changes journaled while the open is out", a.disk.journal.Len())
	}
	close(gate)
	h.drive(h.feed(h.receive(opened))...)
	for a.disk.busy {
		h.drive(h.feed(nil)...)
	}
	if _, ok, err := a.disk.cache.Read(a.inboxKey("", 1), h.now); !ok || err != nil {
		t.Fatalf("the counters' page did not reach the disk: %v %v", ok, err)
	}
	if _, ok, _ := a.disk.cache.Read(a.inboxKey(orgA, 1), h.now); !ok {
		t.Fatal("A's page did not reach the disk")
	}
}

// Probes M5 and M23: a 401 while the open is out. When the open then
// fails, the expired session says the disk was not cleared; when it
// succeeds, the namespace leaves the disk and nothing of the kind is said.
func TestA401WhileTheOpenIsOut(t *testing.T) {
	for _, opens := range []bool{false, true} {
		t.Run(map[bool]string{false: "the open fails", true: "the open succeeds"}[opens], func(t *testing.T) {
			root := t.TempDir()
			// An earlier run left an entry of the identity.
			earlier := diskOf(t, root, t0ui())
			tok := recorded(t).me.Token.ID
			ns, _ := cache.Confirmed("default", "https://example.invalid", tok)
			old := ns.Key(api.NotificationsSpec.Tool)
			old.Organisation, old.Status, old.Page, old.PerPage = orgA, "unread", 2, 25
			if err := earlier.Apply([]cache.Change{{Kind: cache.ChangePut, Key: old, Entry: cache.Entry{Payload: []byte(`{"old":true}`), LoadedAt: t0ui()}}}, t0ui()); err != nil {
				t.Fatal(err)
			}
			_ = earlier.Close()
			gate, fail := make(chan struct{}), !opens
			h := buildHarness(t, nil, gatedOpener(root, gate, &fail))
			opened := h.startGated()
			a := h.m.app
			h.events.Observe(func(context.Context) transport.Answer { return failing(transport.Unauthenticated, 401) })(context.Background())
			h.feed(tea.WindowSizeMsg{Width: 120, Height: 24})
			if !a.expired || !a.disk.clearing || a.disk.clearFailed {
				t.Fatalf("expired %v, clearing %v, failed %v", a.expired, a.disk.clearing, a.disk.clearFailed)
			}
			close(gate)
			h.drive(h.feed(h.receive(opened))...)
			for a.disk.busy {
				h.drive(h.feed(nil)...)
			}
			status := h.m.screen().Status.String()
			d := diskOf(t, root, t0ui())
			if opens {
				if a.disk.clearing || a.disk.clearFailed || strings.Contains(status, "could not be cleared") {
					t.Fatalf("clearing %v, failed %v, status %q", a.disk.clearing, a.disk.clearFailed, status)
				}
				if onDisk(t, d, old, t0ui()) {
					t.Fatal("the earlier run's entry stayed")
				}
			} else {
				if !a.disk.clearFailed || !strings.Contains(status, "could not be cleared") {
					t.Fatalf("failed %v, status %q", a.disk.clearFailed, status)
				}
				if !onDisk(t, d, old, t0ui()) {
					t.Fatal("the entry went although the open failed")
				}
			}
		})
	}
}

// t0ui is the harness's clock.
func t0ui() time.Time { return time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC) }

// Probe M18: the disk went off earlier (a write failed); a 401 then cannot
// clear it, and the expired session says so.
func TestA401AfterTheDiskWentOffSaysSo(t *testing.T) {
	root := t.TempDir()
	o := &diskcache.Opener{Root: root, Faults: func(step, _ string) error {
		if step == "rename" {
			return errors.New("injected rename")
		}
		return nil
	}}
	h := buildHarness(t, nil, o)
	h.start()
	a := h.m.app
	if !a.disk.off {
		t.Fatal("the disk is still on")
	}
	h.f.set(func(q api.NotificationQuery) transport.Answer { return failing(transport.Unauthenticated, 401) })
	h.drive(h.press("R"))
	if !a.expired || !a.disk.clearFailed {
		t.Fatalf("expired %v, failed %v", a.expired, a.disk.clearFailed)
	}
	if got := h.m.screen().Status.String(); !strings.Contains(got, "could not be cleared") {
		t.Fatalf("status %q", got)
	}
}

// Probe M11: a disk miss gives its ticket back; nothing stays pending in
// the scope for the session.
func TestADiskMissLeavesNothingPending(t *testing.T) {
	h := newDiskHarness(t, t.TempDir(), nil)
	h.start()
	a := h.m.app
	h.drive(h.choose(orgA))
	for a.disk.busy {
		h.drive(h.feed(nil)...)
	}
	if n := a.scope.Pending(); n != 0 {
		t.Fatalf("%d tickets pending after a disk miss", n)
	}
	if len(a.disk.reads) != 0 {
		t.Fatalf("%d disk reads known", len(a.disk.reads))
	}
}

// Review round 2: the profile the session runs with names the directory;
// another profile is another directory (and another lock).
func TestTheProfileNamesTheDirectory(t *testing.T) {
	root := t.TempDir()
	h := buildHarnessFor(t, nil, &diskcache.Opener{Root: root}, "work")
	h.start()
	if h.m.app.disk.cache == nil {
		t.Fatal("no disk")
	}
	if !exists(t, filepath.Join(root, "work", "lock")) {
		t.Fatal("no directory for the profile work")
	}
	if exists(t, filepath.Join(root, "default")) {
		t.Fatal("a directory for a profile the session does not run with")
	}
	if got := h.m.app.disk.cache.Dir(); got != filepath.Join(root, "work") {
		t.Fatalf("dir %s", got)
	}
	// The other profile opens beside it, with a lock of its own.
	other := buildHarnessFor(t, nil, &diskcache.Opener{Root: root}, "default")
	other.start()
	if other.m.app.disk.cache == nil || other.m.app.disk.notice != 0 {
		t.Fatalf("the other profile: cache %v, notice %d", other.m.app.disk.cache != nil, other.m.app.disk.notice)
	}
}
