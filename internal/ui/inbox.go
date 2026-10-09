package ui

import (
	"context"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/cache"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/queue"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
	"github.com/gboyke/lalabase-cli/internal/uitext"
)

// issue asks the gateway for one read, of the person's priority, under a
// ticket of its own. Two reads of the same data in flight share one
// request in the queue; the result names the request (its flight), and the
// flights order the requests of one epoch.
func (a *app) issue(area gateway.Area, key cache.Key, p purpose) tea.Cmd {
	if a.expired || a.scope == nil || a.deps.Gateway == nil {
		return nil
	}
	t := a.scope.Begin(area, key, queue.User)
	ctx, cancel := context.WithCancel(a.base)
	a.waiters[t.Waiter] = &waiter{ticket: t, cancel: cancel, purpose: p}
	return a.deps.Gateway.Inbox(ctx, t)
}

// pending reports a read of key still out for somebody who waits for it.
func (a *app) pending(key cache.Key) bool {
	for _, w := range a.waiters {
		if !w.canceled && w.ticket.Key == key {
			return true
		}
	}
	return false
}

// moveInbox moves the inbox selection on and cancels the waiters it leaves
// behind; they stay known until their canceled result arrives.
func (a *app) moveInbox() {
	if a.scope == nil {
		return
	}
	for _, id := range a.scope.Move(gateway.Inbox) {
		if w := a.waiters[id]; w != nil && !w.canceled {
			w.canceled = true
			w.cancel()
		}
	}
}

// receiveInbox is the one way an inbox result reaches the state: the
// mailbox's revocation first, then the order of requests, then the scope's
// acceptance, then counters and view, for any consumer of the key.
func (m Model) receiveInbox(r gateway.InboxResult) (Model, tea.Cmd) {
	a := m.app
	if a == nil || a.expired || a.scope == nil {
		return m, nil
	}
	w := a.waiters[r.Waiter]
	if w == nil {
		return m, nil
	}
	delete(a.waiters, r.Waiter)
	w.cancel()
	if a.revoked() {
		m.expire()
		return m, nil
	}
	seq, key := uint64(r.Flight), r.Key
	removal := cache.Decide(r.Answer.Class, cache.Read) == cache.Remove
	if seq < a.newestFiled(key, removal) {
		// An older request than the newest filed for the key (for a
		// removal: for the resource): its answer is not filed over the
		// newer one. The waiter's ticket is used up.
		a.scope.Leave(r.Waiter)
		return m, nil
	}
	d, ok := a.scope.Receive(a.store, r, a.now())
	if ok && r.Answer.Class == transport.Unauthenticated {
		m.expire()
		return m, nil
	}
	if !ok {
		return m, nil
	}
	switch d {
	case cache.Replace:
		a.applyCounters(seq, key, r.Page)
		a.filed[key] = max(a.filed[key], seq)
	case cache.Remove:
		a.forgetCounters(seq, key)
		a.filed[key] = max(a.filed[key], seq)
	}
	if key.Global {
		// Any consumer of a global page settles the counters step.
		a.countersResult(seq, d, r)
	}
	return m.inboxResult(key, r, d)
}

// newestFiled is the newest request filed for the key or, for a removal,
// for any key of the key's resource: a removal takes the whole resource,
// and an older one must not take what a newer request brought.
func (a *app) newestFiled(key cache.Key, removal bool) uint64 {
	newest := a.filed[key]
	if !removal {
		return newest
	}
	res := key.Resource()
	for k, seq := range a.filed {
		if k.Resource() == res {
			newest = max(newest, seq)
		}
	}
	return newest
}

// inboxResult applies an accepted result to the active view when it is the
// view's key, whoever asked for it. The order of requests was kept before
// (filed, per key): what arrives here is the newest answer with data, or a
// failure, or a copy of the same answer, which applies the same thing again.
func (m Model) inboxResult(key cache.Key, r gateway.InboxResult, d cache.Decision) (Model, tea.Cmd) {
	a := m.app
	v := a.active
	if v == nil || v.key != key {
		return m, nil
	}
	v.loading = a.pending(v.key)
	switch d {
	case cache.Replace:
		v.rows, v.total, v.perPage = r.Page.Data, r.Page.Meta.Total, r.Page.Meta.PerPage
		v.known, v.cached, v.stale, v.err = true, false, false, present.Inline{}
		v.loadedAt = a.now()
		v.reselect()
		if m.focus == view.Object && v.opened == "" {
			m.focus = view.List
		}
		return m.correctPage()
	case cache.Remove:
		v.rows, v.total, v.known = nil, 0, false
		v.selected, v.opened = "", ""
		v.err = uitext.Inline(uitext.NotFoundRemoved)
		if m.focus == view.Object {
			m.focus = view.List
		}
	case cache.KeepStale:
		v.stale = true
		if !v.known {
			v.err = uitext.Inline(uitext.ServerUnreachable)
		}
	case cache.KeepShowMessage:
		v.err = messageText(r.Answer)
	}
	return m, nil
}

// messageText is the error shown for an answer that is neither an object
// nor temporary: the server's refusal, or an answer the client cannot place.
func messageText(ans transport.Answer) present.Inline {
	if ans.Class == transport.Protocol {
		return uitext.Fill(uitext.UnexpectedResponse, detail(ans))
	}
	return uitext.Fill(uitext.RefusedWith, detail(ans))
}

// correctPage reacts to a refresh that showed the current page beyond the
// last one: the resource's cached pages go (a root-initiated removal, so
// the scope is told explicitly), the view moves to the new last page and
// loads it once. A second shrink stops and offers R; a total of zero shows
// an empty page 1 without a request.
func (m Model) correctPage() (Model, tea.Cmd) {
	a := m.app
	v := a.active
	last := lastPage(v.total, v.perPage)
	if v.page <= last {
		return m, nil
	}
	a.store.Apply(v.key, cache.Remove, nil, a.now())
	a.scope.Invalidate(v.key)
	v.rows, v.selected, v.opened, v.offset = nil, "", "", 0
	if m.focus == view.Object {
		m.focus = view.List
	}
	if v.total == 0 {
		v.page, v.key.Page = 1, 1
		a.remember()
		return m, nil
	}
	if v.corrected {
		v.err = uitext.Inline(uitext.InboxShrank)
		return m, nil
	}
	v.corrected = true
	a.moveInbox()
	v.page, v.key.Page = last, last
	v.known, v.loading = false, true
	a.remember()
	return m, a.issue(gateway.Inbox, v.key, forInbox)
}

// remember keeps the page and the selected row of the active inbox, for
// the return to it.
func (a *app) remember() {
	if v := a.active; v != nil {
		a.remembered[v.org] = position{page: v.page, selected: v.selected}
	}
}

// activate opens the inbox of org ("" for all): the remembered page and
// row, from the cache at once when it holds the page, then one reload.
func (m Model) activate(org string) (Model, tea.Cmd) {
	a := m.app
	if !a.canOpen(org) {
		return m, nil
	}
	if a.active != nil && a.active.org == org {
		m.focus = view.List
		return m, nil
	}
	a.remember()
	pos := a.remembered[org]
	page := 1
	if pos.page > 0 && a.pagingOn() {
		page = pos.page
	}
	a.moveInbox()
	name := ""
	if o := a.organisation(org); o != nil {
		name = o.Name
	}
	a.active = &inboxView{org: org, name: name, page: page, key: a.inboxKey(org, page), selected: pos.selected}
	m.focus = view.List
	return m, a.load()
}

// load shows the cached page, if there is one that still reads, and starts
// exactly one reload of the person's priority. A cached page that does not
// read any more is forgotten.
func (a *app) load() tea.Cmd {
	v := a.active
	if e, ok := a.store.Get(v.key); ok {
		page, ans := api.DecodeNotifications(transport.Answer{Class: transport.Object, Payload: e.Payload, Status: 200, Sent: true})
		if ans.OK() && gateway.CheckInboxPage(v.key, page) == nil {
			v.rows, v.total, v.perPage = page.Data, page.Meta.Total, page.Meta.PerPage
			v.known, v.cached, v.stale, v.loadedAt = true, true, e.Stale, e.LoadedAt
			v.reselect()
		} else {
			a.store.Forget(v.key)
		}
	}
	v.loading = true
	return a.issue(gateway.Inbox, v.key, forInbox)
}

// turnPage moves to the previous or next server page, when there is one
// and no load is pending.
func (m Model) turnPage(delta int) (Model, tea.Cmd) {
	a := m.app
	v := a.active
	if v == nil || !a.pagingOn() || v.loading {
		return m, nil
	}
	target := v.page + delta
	if target < 1 || (delta > 0 && (!v.known || target > lastPage(v.total, v.perPage))) {
		return m, nil
	}
	a.moveInbox()
	v.page, v.key.Page = target, target
	v.rows, v.known, v.cached, v.stale, v.err = nil, false, false, false, present.Inline{}
	v.selected, v.opened, v.offset, v.detailOffset = "", "", 0, 0
	v.corrected = false
	a.remember()
	if m.focus == view.Object {
		m.focus = view.List
	}
	return m, a.load()
}

// refreshActive reloads the active page, unless a read of it is pending.
// An explicit refresh frees the page correction again.
func (a *app) refreshActive() tea.Cmd {
	v := a.active
	if v == nil || a.pending(v.key) {
		return nil
	}
	v.corrected = false
	v.loading = true
	return a.issue(gateway.Inbox, v.key, forInbox)
}

// anyFailed reports a startup step that failed: R retries those first.
func (a *app) anyFailed() bool {
	for _, s := range a.steps {
		if s.state == failed {
			return true
		}
	}
	return false
}

// retryOrRefresh is R: before /me, /me again; failed steps first, each
// alone (the tools after initialize come by themselves); then, healthy, the
// directory and the counters from the chooser, the active page from the
// inbox. Work still pending is not issued again.
func (m Model) retryOrRefresh() (Model, tea.Cmd) {
	a := m.app
	if a.expired {
		return m, nil
	}
	if a.steps[stepMe].state != ready {
		if a.steps[stepMe].state == failed {
			return m, a.startStep(stepMe)
		}
		return m, nil
	}
	if a.anyFailed() {
		var cmds []tea.Cmd
		for i := stepInitialize; i < stepCounters; i++ {
			if a.steps[i].state == failed {
				cmds = append(cmds, a.startStep(i))
			}
		}
		if a.steps[stepCounters].state == failed {
			cmds = append(cmds, a.issueCounters())
		}
		return m, tea.Batch(cmds...)
	}
	if m.focus != view.Navigation && a.active != nil {
		return m, a.refreshActive()
	}
	var cmds []tea.Cmd
	if a.steps[stepOrganisations].state == ready {
		cmds = append(cmds, a.startStep(stepOrganisations))
	}
	if a.steps[stepCounters].state == ready {
		cmds = append(cmds, a.issueCounters())
	}
	return m, tea.Batch(cmds...)
}

// canRefresh says whether R would do anything now.
func (m Model) canRefresh() bool {
	a := m.app
	if a.expired {
		return false
	}
	if a.steps[stepMe].state != ready {
		return a.steps[stepMe].state == failed
	}
	if a.anyFailed() {
		return true
	}
	if m.focus != view.Navigation && a.active != nil {
		return !a.pending(a.active.key)
	}
	return a.steps[stepOrganisations].state == ready || a.steps[stepCounters].state == ready
}
