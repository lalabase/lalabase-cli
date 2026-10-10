package ui

import (
	"context"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/cache"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/queue"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
	"github.com/gboyke/lalabase-cli/internal/uitext"
)

// issue asks the gateway for one read under a ticket of its own, of the
// person's priority or as background work (a preview, its reload). Two
// reads of the same data in flight share one request in the queue; the
// result names the request (its flight), and the flights order the
// requests of one epoch.
func (a *app) issue(area gateway.Area, key cache.Key, p purpose, prio queue.Priority) tea.Cmd {
	if a.expired || a.scope == nil || a.deps.Gateway == nil {
		return nil
	}
	t := a.scope.Begin(area, key, prio)
	ctx, cancel := context.WithCancel(a.base)
	a.waiters[t.Waiter] = &waiter{ticket: t, cancel: cancel, purpose: p}
	return a.deps.Gateway.Read(ctx, t)
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

// receive is the one way any result reaches the state: the waiter, the
// order of requests, the scope's acceptance (which files it), a 401. It
// returns the decision and whether the result is to be applied to the
// views; false means nothing of it is.
func (m *Model) receive(received gateway.Received) (cache.Decision, bool) {
	a := m.app
	r := received.Header()
	if a == nil || a.expired || a.scope == nil {
		return cache.Drop, false
	}
	w := a.waiters[r.Waiter]
	if w == nil {
		return cache.Drop, false
	}
	delete(a.waiters, r.Waiter)
	w.cancel()
	a.settleLoading()
	seq, key := uint64(r.Flight), r.Key
	removal := cache.Decide(r.Answer.Class, cache.Read) == cache.Remove
	if seq < a.newestFiled(key, removal) {
		// An older request than the newest filed for the key (for a
		// removal: for the resource): its answer is not filed over the
		// newer one. The waiter's ticket is used up. A read refused before
		// it was sent carries flight 0 and lands here too once the key was
		// filed: the model built a ticket the gateway does not read, which
		// no state of it does; nothing of that is shown.
		a.scope.Leave(r.Waiter)
		return cache.Drop, false
	}
	d, ok := a.scope.Receive(a.store, received, a.now())
	if ok && r.Answer.Class == transport.Unauthenticated {
		// Defence in depth: with a mailbox the 401 was observed on the
		// request and Update expired the session before this result
		// arrived; without one this is the path.
		m.expire()
		return d, false
	}
	if !ok {
		return cache.Drop, false
	}
	if d == cache.Replace || d == cache.Remove {
		a.filed[key] = max(a.filed[key], seq)
	}
	return d, true
}

// receiveInbox applies an inbox result: counters and view, for any
// consumer of the key.
func (m Model) receiveInbox(r gateway.InboxResult) (Model, tea.Cmd) {
	a := m.app
	d, ok := m.receive(r)
	if !ok {
		return m, nil
	}
	seq, key := uint64(r.Flight), r.Key
	switch d {
	case cache.Replace:
		a.applyCounters(seq, key, r.Page)
	case cache.Remove:
		a.forgetCounters(seq, key)
	}
	if key.Global {
		// Any consumer of a global page settles the counters step.
		a.countersResult(seq, d, r)
	}
	return m.inboxResult(key, r, d)
}

// settleLoading makes every view's loading state follow the waiters: a
// view loads while a read of its key is out for somebody, whatever became
// of the answer that just arrived.
func (a *app) settleLoading() {
	if v := a.active; v != nil {
		v.loading = a.pending(v.key)
	}
	if v := a.projects; v != nil {
		v.loading = a.pending(v.key)
	}
	if v := a.tickets; v != nil {
		v.loading = a.pending(v.key)
	}
	if v := a.detail; v != nil {
		v.loading = a.pending(v.key)
	}
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
// view's key, whoever asked for it, and a removal when it is the view's
// resource: the server took every page of it. The order of requests was
// kept before (filed): what arrives here is the newest answer with data, or
// a failure, or a copy of the same answer, which applies the same thing
// again.
func (m Model) inboxResult(key cache.Key, r gateway.InboxResult, d cache.Decision) (Model, tea.Cmd) {
	a := m.app
	v := a.active
	if v == nil {
		return m, nil
	}
	sameResource := d == cache.Remove && v.key.Resource() == key.Resource()
	if v.key != key && !sameResource {
		return m, nil
	}
	switch d {
	case cache.Replace:
		v.rows, v.total, v.perPage = r.Page.Data, r.Page.Meta.Total, r.Page.Meta.PerPage
		v.fresh(a.now())
		v.reselect()
		if m.focus == view.Object && v.opened == "" {
			m.focus = view.List
		}
		return m.correctPage()
	case cache.Remove:
		v.rows, v.total = nil, 0
		v.selected, v.opened = "", ""
		v.removed()
		if m.focus == view.Object {
			m.focus = view.List
		}
	default:
		v.failed(d, r.Answer)
	}
	return m, nil
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
	return m, a.issue(gateway.Inbox, v.key, forInbox, queue.User)
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
	if !a.canOpen(org, sectionInbox) {
		return m, nil
	}
	if a.active != nil && a.active.org == org {
		m.focus = view.List
		return m, nil
	}
	a.leaveTickets()
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
			v.fromCache(e)
			v.reselect()
		} else {
			a.store.Forget(v.key)
		}
	}
	v.loading = true
	return a.issue(gateway.Inbox, v.key, forInbox, queue.User)
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
	v.rows, v.status = nil, status{}
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
	if v == nil {
		return nil
	}
	v.corrected = false
	v.loading = true
	if a.pending(v.key) {
		// A read of the key is out for somebody else (the counters, say):
		// the view waits for that one and issues nothing of its own.
		return nil
	}
	return a.issue(gateway.Inbox, v.key, forInbox, queue.User)
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
		if m.focus != view.Navigation && a.active != nil {
			// The active page as well: a failed page of the global inbox
			// failed the counters step too, and R from the page must read
			// the page, not only the counters.
			cmds = append(cmds, a.refreshActive())
		}
		if m.focus != view.Navigation && a.projects != nil {
			cmds = append(cmds, m.refreshSection())
		}
		return m, tea.Batch(cmds...)
	}
	if m.focus != view.Navigation && a.active != nil {
		return m, a.refreshActive()
	}
	if m.focus != view.Navigation && a.projects != nil {
		return m, m.refreshSection()
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
	// Mirrors retryOrRefresh: R is offered only when it would issue a read
	// (a read already out for the same key is not issued again).
	countersFree := !a.pending(a.inboxKey("", 1))
	activeFree := m.focus != view.Navigation && a.active != nil && !a.pending(a.active.key)
	sectionFree := m.focus != view.Navigation && a.projects != nil && m.canRefreshSection()
	if a.anyFailed() {
		for i := stepInitialize; i < stepCounters; i++ {
			if a.steps[i].state == failed {
				return true
			}
		}
		return (a.steps[stepCounters].state == failed && countersFree) || activeFree || sectionFree
	}
	if m.focus != view.Navigation && a.active != nil {
		return activeFree
	}
	if m.focus != view.Navigation && a.projects != nil {
		return sectionFree
	}
	return a.steps[stepOrganisations].state == ready || (a.steps[stepCounters].state == ready && countersFree)
}
