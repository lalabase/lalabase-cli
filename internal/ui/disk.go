package ui

import (
	"errors"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/cache"
	"github.com/gboyke/lalabase-cli/internal/diskcache"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/uitext"
)

// The disk cache (C1e), seen from the model.
//
// It is opened by a command after /me confirmed the identity, never before,
// and never on the start's path: the five requests of the start are what
// they were, and the interface waits for nothing of it. The memory store
// tells its journal every decision about data; the model hands the journal
// to the disk in batches, one batch out at a time, so the disk is a replay
// of the memory's decisions in their order and the work in flight is
// bounded. A read of the disk is issued where the memory is read and found
// empty (opening a page, a list, a ticket, an expansion; a preview falling
// due), under a ticket of the scope: its result passes the same checks as
// a network result (session, generation, epoch), files nothing over a
// network answer that landed since, and keeps its own load time.
//
// A failing file system switches the disk off for the session, once, with
// one notice; a second lalabase on the same profile gets memory alone. A
// 401 removes the namespace from the disk through the same journal; when
// that cannot be done the expired session says so.

// diskBatch is the most changes one batch applies; maxDiskReads the most
// reads waiting to be sent.
const (
	diskBatch    = 32
	maxDiskReads = 8
)

// diskState is the session's disk cache. It is not reset with the session:
// a session that ended still has its namespace to remove.
type diskState struct {
	opener  *diskcache.Opener
	cache   *diskcache.Cache
	opening bool
	journal *cache.Journal
	// busy: a batch is out.
	busy bool
	// off: the file system failed, or the open did; memory alone from now
	// on. notice is the one line that says so.
	off    bool
	notice uitext.ID
	// reads are the disk reads issued and not yet answered; order keeps
	// them oldest first.
	reads map[gateway.Waiter]*diskRead
	order []gateway.Waiter
	// clearing: a 401 ended the session and its namespace is to be removed
	// from the disk; cleared or failed say what became of it.
	clearing, clearFailed bool
}

// diskRead is one read of the disk: the ticket the scope issued, what the
// result is for, and the request filed for the key when the read was
// issued (a newer one filed since means a network answer came first).
type diskRead struct {
	ticket   gateway.Ticket
	purpose  purpose
	filed    uint64
	sent     bool
	canceled bool
}

// diskOpened is the message of the open command.
type diskOpened struct {
	session gateway.Session
	cache   *diskcache.Cache
	err     error
}

// diskDone is the message of one batch: what its reads found and the first
// failure of the file system, if any.
type diskDone struct {
	session gateway.Session
	reads   []diskReadResult
	cleared bool // a namespace removal was in the batch and applied
	err     error
}

type diskReadResult struct {
	waiter gateway.Waiter
	entry  cache.Entry
	hit    bool
}

func newDiskState(opener *diskcache.Opener) *diskState {
	return &diskState{opener: opener, journal: cache.NewJournal(), reads: map[gateway.Waiter]*diskRead{}}
}

// usable: the disk takes reads and changes now.
func (d *diskState) usable() bool { return d != nil && d.cache != nil && !d.off }

// openDisk is the command that opens the disk cache, issued once /me
// confirmed the identity.
func (a *app) openDisk() tea.Cmd {
	d := a.disk
	if d == nil || d.opener == nil || d.cache != nil || d.opening || d.off {
		return nil
	}
	d.opening = true
	opener, profile, session, now := *d.opener, a.deps.Profile, a.session, a.now()
	return func() tea.Msg {
		c, err := opener.Open(profile, now)
		return diskOpened{session: session, cache: c, err: err}
	}
}

// receiveDiskOpened takes the opened cache, or the reason there is none.
func (m Model) receiveDiskOpened(msg diskOpened) (Model, tea.Cmd) {
	a := m.app
	if a == nil || a.disk == nil || msg.session != a.session {
		if msg.cache != nil {
			_ = msg.cache.Close()
		}
		return m, nil
	}
	d := a.disk
	d.opening = false
	if msg.err != nil {
		d.off = true
		d.notice = uitext.DiskUnavailable
		if errors.Is(msg.err, diskcache.ErrBusy) {
			d.notice = uitext.DiskBusy
		}
		if d.clearing {
			// The session ended before the disk opened, and it will not
			// open: what an earlier run left of the namespace stays.
			d.clearFailed = true
		}
		return m, nil
	}
	d.cache = msg.cache
	// The journal holds what happened since /me; the flush at the end of
	// this update sends it.
	return m, nil
}

// diskRead asks the disk for key, for a view that found the memory empty.
// The read is sent with the next batch; its result is accepted only when
// the scope still accepts the ticket and no network answer was filed for
// the key meanwhile. More than maxDiskReads waiting drop the oldest.
func (a *app) diskRead(area gateway.Area, key cache.Key, p purpose) {
	d := a.disk
	if a.expired || a.scope == nil || !d.usable() {
		return
	}
	t := a.scope.Begin(area, key, 0)
	d.reads[t.Waiter] = &diskRead{ticket: t, purpose: p, filed: a.filed[key]}
	d.order = append(d.order, t.Waiter)
	waiting := 0
	for _, w := range d.order {
		if r := d.reads[w]; r != nil && !r.sent && !r.canceled {
			waiting++
		}
	}
	for _, w := range d.order {
		if waiting <= maxDiskReads {
			break
		}
		if r := d.reads[w]; r != nil && !r.sent && !r.canceled {
			a.cancelDiskRead(w)
			waiting--
		}
	}
}

// cancelDiskRead drops a read whose answer is no longer wanted; one not
// sent yet is settled at the next flush, one out is dropped on arrival.
func (a *app) cancelDiskRead(w gateway.Waiter) {
	if d := a.disk; d != nil {
		if r := d.reads[w]; r != nil {
			r.canceled = true
		}
	}
}

// cancelDiskReadsOf drops every read of a key: the entry was forgotten.
func (a *app) cancelDiskReadsOf(key cache.Key) {
	if d := a.disk; d != nil {
		for w, r := range d.reads {
			if r.ticket.Key == key {
				a.cancelDiskRead(w)
			}
		}
	}
}

// forget drops a memory entry the root cannot use any more, and with it
// every disk read of the key still out: the disk entry goes through the
// journal, and must not come back through a read issued before.
func (a *app) forget(key cache.Key) {
	a.store.Forget(key)
	a.cancelDiskReadsOf(key)
}

// flushDisk is called at the end of every update: it sends the next batch
// of changes and the reads waiting, when the disk is usable and no batch
// is out. Canceled reads are settled here without the disk. Without a
// usable disk the journal is emptied, so nothing piles up.
func (a *app) flushDisk() tea.Cmd {
	d := a.disk
	if d == nil {
		return nil
	}
	if a.scope != nil {
		for w, r := range d.reads {
			if r.canceled && !r.sent {
				a.scope.Leave(w)
				a.dropDiskRead(w)
			}
		}
	}
	if !d.usable() {
		if !d.opening {
			if a.scope == nil {
				// No session owns the pending reads any more.
				for w := range d.reads {
					a.dropDiskRead(w)
				}
			}
			d.journal.Take(cache.MaxPending)
		}
		return nil
	}
	if d.busy {
		return nil
	}
	changes := d.journal.Take(diskBatch)
	var reads []diskRead
	var waiters []gateway.Waiter
	for _, w := range d.order {
		r := d.reads[w]
		if r == nil || r.sent || r.canceled {
			continue
		}
		r.sent = true
		reads = append(reads, *r)
		waiters = append(waiters, w)
	}
	if len(changes) == 0 && len(reads) == 0 {
		if a.expired {
			// Nothing left to do for an ended session: the disk is let go.
			_ = d.cache.Close()
			d.cache = nil
		}
		return nil
	}
	d.busy = true
	c, session, now := d.cache, a.session, a.now()
	cleared := false
	for _, ch := range changes {
		if ch.Kind == cache.ChangeRemoveNamespace {
			cleared = true
		}
	}
	return func() tea.Msg {
		done := diskDone{session: session}
		if err := c.Apply(changes, now); err != nil {
			done.err = err
			return done
		}
		done.cleared = cleared
		for i, r := range reads {
			entry, hit, err := c.Read(r.ticket.Key, now)
			if err != nil {
				done.err = err
				return done
			}
			done.reads = append(done.reads, diskReadResult{waiter: waiters[i], entry: entry, hit: hit})
		}
		return done
	}
}

// dropDiskRead forgets a read, answered or not.
func (a *app) dropDiskRead(w gateway.Waiter) {
	d := a.disk
	delete(d.reads, w)
	for i, id := range d.order {
		if id == w {
			d.order = append(d.order[:i], d.order[i+1:]...)
			break
		}
	}
}

// receiveDiskDone takes a batch's outcome: a failure switches the disk off
// (and says so once); the reads' hits are promoted into memory through the
// scope and shown where their view still waits for them. A ticket list
// from disk starts the preview of its selected row, as one from memory
// does through the open.
func (m Model) receiveDiskDone(msg diskDone) (Model, tea.Cmd) {
	a := m.app
	if a == nil || a.disk == nil || msg.session != a.session {
		return m, nil
	}
	d := a.disk
	d.busy = false
	if msg.cleared {
		d.clearing = false
	}
	if msg.err != nil {
		d.off = true
		d.notice = uitext.DiskUnavailable
		if d.clearing {
			d.clearFailed = true
		}
		if d.cache != nil {
			_ = d.cache.Close()
			d.cache = nil
		}
		for w := range d.reads {
			if a.scope != nil {
				a.scope.Leave(w)
			}
			a.dropDiskRead(w)
		}
		return m, nil
	}
	var cmd tea.Cmd
	for _, r := range msg.reads {
		read := d.reads[r.waiter]
		a.dropDiskRead(r.waiter)
		if read == nil || a.expired || a.scope == nil {
			continue
		}
		if read.canceled || !r.hit || a.filed[read.ticket.Key] != read.filed {
			a.scope.Leave(r.waiter)
			continue
		}
		if !a.scope.Promote(a.store, read.ticket, r.entry) {
			continue
		}
		if a.showFromDisk(read.purpose, read.ticket.Key) && read.purpose == forTickets {
			cmd = m.selectionChanged()
		}
	}
	return m, cmd
}

// showFromDisk shows a promoted entry in the view that asked for it, when
// that view still asks for the key and holds no data of the server's. It
// reports whether it did.
func (a *app) showFromDisk(p purpose, key cache.Key) bool {
	switch p {
	case forInbox:
		if v := a.active; v != nil && v.key == key && !v.holdsServerData() {
			if e, ok := a.cachedInbox(v); ok {
				v.adoptCached(e)
				return true
			}
		}
	case forProjects:
		if v := a.projects; v != nil && v.key == key && !v.holdsServerData() {
			if e, ok := a.cachedProjects(v); ok {
				v.adoptCached(e)
				return true
			}
		}
	case forTickets:
		if v := a.tickets; v != nil && v.key == key && !v.holdsServerData() {
			if e, ok := a.cachedTickets(v); ok {
				v.adoptCached(e)
				return true
			}
		}
	case forDetail:
		if v := a.detail; v != nil && v.key == key && (v.shown != key || !v.holdsServerData()) {
			if e, ok := a.cachedPage(v, key); ok {
				v.adoptCached(e)
				return true
			}
		}
	case forExpansion:
		if v := a.detail; v != nil && v.expansion != nil && v.expansion.key == key && !v.expansion.holdsServerData() {
			if e, ok := a.cachedExpansion(v.expansion); ok {
				v.expansion.adoptCached(e)
				return true
			}
		}
	}
	return false
}

// closeDisk lets the disk go when the program ends: the lock is released
// at once, not when the process is gone.
func (a *app) closeDisk() {
	if a == nil || a.disk == nil || a.disk.cache == nil {
		return
	}
	_ = a.disk.cache.Close()
	a.disk.cache = nil
}

// diskStatus is the line the status shows for the disk: the fallback's
// notice, or nothing.
func (a *app) diskStatus() uitext.ID {
	if a.disk == nil {
		return 0
	}
	return a.disk.notice
}

// endDiskSession follows the session's end: pending reads are dropped (an
// answer of theirs fills nothing), and, when an identity was confirmed,
// the namespace's removal, journaled by the store, is what the disk does
// next. When the disk cannot do it, the expired session says so. A
// session that never confirmed an identity (a 401 on /me) opened no
// directory and has nothing to clear; nothing is claimed about it.
func (a *app) endDiskSession(confirmed bool) {
	d := a.disk
	if d == nil || d.opener == nil {
		return
	}
	for w := range d.reads {
		a.cancelDiskRead(w)
	}
	if !confirmed {
		return
	}
	d.clearing = true
	if d.off || (d.cache == nil && !d.opening) {
		d.clearFailed = true
	}
}
