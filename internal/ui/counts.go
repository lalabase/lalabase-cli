package ui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/cache"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/present"
)

// Counters: organisation id → known, value, sequence. The sequence is the
// request's flight; a newer accepted observation wins, the order the
// answers arrived in does not. A global page is a snapshot of every
// organisation: one it omits is unknown, and the snapshot's sequence
// (snapshot) outranks every older filtered answer, also for an organisation
// the snapshot and the directory did not list at the time. Cache hits
// replay nothing here.

// inboxKey is the cache key of one unread page: of one organisation, or
// explicitly of all of them.
func (a *app) inboxKey(org string, page int) cache.Key {
	k := a.ns.Key(api.NotificationsSpec.Tool)
	if org == "" {
		k.Global = true
	} else {
		k.Organisation = org
	}
	k.Status, k.Page, k.PerPage = "unread", page, gateway.InboxPageSize
	return k
}

// issueCounters reads the global unread page 1 in the counters' area. A
// read already pending is not issued again.
func (a *app) issueCounters() tea.Cmd {
	if !a.inboxOn() || a.scope == nil {
		return nil
	}
	key := a.inboxKey("", 1)
	if a.pending(key) {
		return nil
	}
	a.steps[stepCounters].state = loading
	return a.issue(gateway.Organisations, key, forCounters)
}

// applyCounters takes the counts of an accepted page: a global page is a
// snapshot of every known organisation (absent means unknown), a filtered
// page speaks for its organisation alone.
func (a *app) applyCounters(seq uint64, key cache.Key, page api.NotificationPage) {
	observed := map[string]int{}
	for _, c := range page.Meta.ByOrganisation {
		observed[c.ID] = c.Unread
	}
	if !key.Global {
		v, known := observed[key.Organisation]
		a.setCount(key.Organisation, known, v, seq)
		return
	}
	if seq > a.global.seq {
		a.global = count{known: true, value: page.Meta.UnreadTotal, seq: seq}
	}
	ids := map[string]bool{}
	for _, o := range a.directory {
		ids[o.ID] = true
	}
	for id := range observed {
		ids[id] = true
	}
	for id := range a.counters {
		ids[id] = true
	}
	for id := range ids {
		v, known := observed[id]
		a.setCount(id, known, v, seq)
	}
	a.snapshot = max(a.snapshot, seq)
}

// forgetCounters makes the counter of a removed resource unknown. The
// removal is not older than any answer filed for its resource (checked
// before it is received), so the global count needs no order of its own;
// an organisation's count does, a global snapshot may have set it since.
func (a *app) forgetCounters(seq uint64, key cache.Key) {
	if key.Global {
		a.global = count{seq: seq}
		return
	}
	a.setCount(key.Organisation, false, 0, seq)
}

func (a *app) setCount(id string, known bool, v int, seq uint64) {
	if seq < a.snapshot {
		return
	}
	if c := a.counters[id]; seq > c.seq {
		a.counters[id] = count{known: known, value: v, seq: seq}
	}
}

// countOf is the counter shown for an organisation ("" for all).
func (a *app) countOf(org string) count {
	if org == "" {
		return a.global
	}
	return a.counters[org]
}

// countersResult settles the counters step after its result. A result of
// an older sequence than the one settled (a late copy of a shared
// removal) changes nothing.
func (a *app) countersResult(seq uint64, d cache.Decision, r gateway.InboxResult) {
	if seq <= a.countersSeq {
		return
	}
	a.countersSeq = seq
	s := &a.steps[stepCounters]
	if d == cache.Replace {
		s.state, s.err = ready, present.Inline{}
		return
	}
	s.state, s.err = failed, failureText(stepCounters, r.Answer)
}
