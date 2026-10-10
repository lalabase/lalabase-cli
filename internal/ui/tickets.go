package ui

import (
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/cache"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/queue"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
	"github.com/gboyke/lalabase-cli/internal/uitext"
)

// The tickets section (C1c): the projects of one organisation, the tickets
// of one project page by page with a search, and one ticket in the object
// pane, previewed after a pause on its row or opened with Enter.

// previewDelay is how long the cursor rests on a row before its ticket is
// read as a preview.
const previewDelay = 300 * time.Millisecond

// projectsView is the list of an organisation's projects.
type projectsView struct {
	org, name string
	key       cache.Key
	rows      []api.Project
	status
	selected string // project id
	offset   int
}

func (v *projectsView) index(id string) int {
	if id == "" {
		return -1
	}
	for i, p := range v.rows {
		if p.ID == id {
			return i
		}
	}
	return -1
}

func (v *projectsView) reselect() {
	if v.index(v.selected) < 0 {
		v.selected = ""
		if len(v.rows) > 0 {
			v.selected = v.rows[0].ID
		}
	}
}

func (v *projectsView) moveSelection(delta int) {
	if len(v.rows) == 0 {
		return
	}
	at := max(0, v.index(v.selected))
	v.selected = v.rows[max(0, min(len(v.rows)-1, at+delta))].ID
}

// ticketPosition is where the person was in a project's tickets.
type ticketPosition struct {
	page     int
	selected string
	query    string
}

// ticketsView is one page of one project's tickets, with its search.
type ticketsView struct {
	org, project, projectName string
	// query is the search sent; entry the search line while it is typed.
	query, entry   string
	page           int
	key            cache.Key
	rows           []api.TicketRow
	total, perPage int
	status
	// selected is the row the cursor is on; opened the ticket opened with
	// Enter, "" for none (the pane then shows a preview, if any).
	selected, opened string
	offset           int
	corrected        bool
}

func (v *ticketsView) index(id string) int {
	if id == "" {
		return -1
	}
	for i, r := range v.rows {
		if r.ID == id {
			return i
		}
	}
	return -1
}

func (v *ticketsView) reselect() {
	if v.index(v.selected) < 0 {
		v.selected = ""
		if len(v.rows) > 0 {
			v.selected = v.rows[0].ID
		}
	}
	if v.opened != "" && v.index(v.opened) < 0 {
		v.opened = ""
	}
}

func (v *ticketsView) moveSelection(delta int) {
	if len(v.rows) == 0 {
		return
	}
	at := max(0, v.index(v.selected))
	v.selected = v.rows[max(0, min(len(v.rows)-1, at+delta))].ID
}

// detailView is the ticket in the object pane.
type detailView struct {
	id, project string
	key         cache.Key
	record      api.Ticket
	status
	offset int
}

// The keys of the section's reads: every option of a request is part of
// its key.
func (a *app) projectsKey(org string) cache.Key {
	k := a.ns.Key(api.ProjectsSpec.Name)
	k.Organisation = org
	return k
}

func (a *app) ticketsKey(project, query string, page int) cache.Key {
	k := a.ns.Key(api.TicketsSpec.Tool)
	k.Project, k.Query, k.Page, k.PerPage = project, query, page, gateway.TicketPageSize
	return k
}

// detailKey is the read of one ticket: the first page of comments (newest
// first) and of attachments when the server pages, the plain read
// otherwise.
func (a *app) detailKey(project, id string) cache.Key {
	k := a.ns.Key(api.TicketSpec.Tool)
	k.Project, k.Ticket = project, id
	if a.pagedOn() {
		k.PostsPage, k.PostsPerPage, k.Newest, k.AttachmentsPage = 1, gateway.PostsPageSize, true, 1
	}
	return k
}

// --- leaving and entering ----------------------------------------------------

// leaveInbox closes the inbox view, remembering its position and canceling
// its reads.
func (a *app) leaveInbox() {
	if a.active == nil {
		return
	}
	a.remember()
	a.moveInbox()
	a.active = nil
}

// leaveTickets closes the tickets section: the ticket list and the ticket
// (leaveList), then the projects, every position remembered and every read
// of theirs canceled.
func (a *app) leaveTickets() {
	a.leaveList()
	if p := a.projects; p != nil {
		a.rememberedProject[p.org] = p.selected
		a.cancelArea(gateway.Projects)
		a.projects = nil
	}
}

// leaveList closes the ticket list and the ticket shown with it.
func (a *app) leaveList() {
	a.dropDetail()
	if v := a.tickets; v != nil {
		a.rememberTickets()
		a.cancelArea(gateway.Tickets)
		a.tickets = nil
	}
}

// dropDetail takes the ticket out of the object pane and cancels its read.
func (a *app) dropDetail() {
	a.cancelArea(gateway.Detail)
	a.detail = nil
}

func (a *app) rememberTickets() {
	if v := a.tickets; v != nil {
		a.rememberedTickets[v.project] = ticketPosition{page: v.page, selected: v.selected, query: v.query}
	}
}

// cancelArea moves an area on and cancels the waiters it leaves behind;
// they stay known until their canceled result arrives.
func (a *app) cancelArea(area gateway.Area) {
	if a.scope == nil {
		return
	}
	for _, id := range a.scope.Move(area) {
		if w := a.waiters[id]; w != nil && !w.canceled {
			w.canceled = true
			w.cancel()
		}
	}
}

// openProjects opens the tickets section of org: its projects, from the
// cache at once when they are there, then one reload.
func (m Model) openProjects(org string) (Model, tea.Cmd) {
	a := m.app
	if !a.canOpen(org, sectionTickets) {
		return m, nil
	}
	if a.projects != nil && a.projects.org == org {
		m.focus = view.List
		return m, nil
	}
	a.leaveInbox()
	a.leaveTickets()
	name := ""
	if o := a.organisation(org); o != nil {
		name = o.Name
	}
	a.projects = &projectsView{org: org, name: name, key: a.projectsKey(org), selected: a.rememberedProject[org]}
	m.focus = view.List
	return m, a.loadProjects()
}

// loadProjects shows the cached list, if there is one that still reads,
// and starts exactly one reload of the person's priority.
func (a *app) loadProjects() tea.Cmd {
	v := a.projects
	if e, ok := a.store.Get(v.key); ok {
		list, ans := api.DecodeProjects(transport.Answer{Class: transport.Object, Payload: e.Payload, Status: 200, Sent: true})
		if ans.OK() && gateway.CheckProjectsList(v.key, list) == nil {
			v.rows = list
			v.fromCache(e)
			v.reselect()
		} else {
			a.store.Forget(v.key)
		}
	}
	v.loading = true
	return a.issue(gateway.Projects, v.key, forProjects, queue.User)
}

// openTickets opens the tickets of the selected project: the remembered
// page, row and search, from the cache at once when the page is there,
// then one reload.
func (m Model) openTickets(project string) (Model, tea.Cmd) {
	a := m.app
	p := a.projects
	if p == nil || p.index(project) < 0 || !a.ticketsOn() {
		return m, nil
	}
	a.leaveList()
	pos := a.rememberedTickets[project]
	page := max(1, pos.page)
	v := &ticketsView{org: p.org, project: project, projectName: p.rows[p.index(project)].Name, query: pos.query, page: page, selected: pos.selected}
	v.key = a.ticketsKey(project, v.query, page)
	a.tickets = v
	m.focus = view.List
	return m, tea.Batch(a.loadTickets(), m.selectionChanged())
}

// loadTickets shows the cached page, if there is one that still reads,
// and starts exactly one reload of the person's priority.
func (a *app) loadTickets() tea.Cmd {
	v := a.tickets
	if e, ok := a.store.Get(v.key); ok {
		page, ans := api.DecodeTickets(transport.Answer{Class: transport.Object, Payload: e.Payload, Status: 200, Sent: true})
		if ans.OK() && gateway.CheckTicketsPage(v.key, page) == nil {
			v.rows, v.total, v.perPage = page.Data, page.Meta.Total, page.Meta.PerPage
			v.fromCache(e)
			v.reselect()
		} else {
			a.store.Forget(v.key)
		}
	}
	v.loading = true
	return a.issue(gateway.Tickets, v.key, forTickets, queue.User)
}

// turnTicketPage moves to the previous or next server page, when there is
// one and no load is pending.
func (m Model) turnTicketPage(delta int) (Model, tea.Cmd) {
	a := m.app
	v := a.tickets
	if v == nil || v.loading {
		return m, nil
	}
	target := v.page + delta
	if target < 1 || (delta > 0 && (!v.known || target > lastPage(v.total, v.perPage))) {
		return m, nil
	}
	return m.showTicketPage(target, v.query)
}

// showTicketPage replaces the page shown: another page, or page 1 of
// another search. The ticket shown goes with the rows.
func (m Model) showTicketPage(page int, query string) (Model, tea.Cmd) {
	a := m.app
	v := a.tickets
	a.dropDetail()
	a.cancelArea(gateway.Tickets)
	v.page, v.query, v.key = page, query, a.ticketsKey(v.project, query, page)
	v.rows, v.status = nil, status{}
	v.selected, v.opened, v.offset, v.corrected = "", "", 0, false
	a.rememberTickets()
	if m.focus == view.Object {
		m.focus = view.List
	}
	return m, tea.Batch(a.loadTickets(), m.selectionChanged())
}

// --- the search line ---------------------------------------------------------

// closeSearch discards the search line, if one is open: its list is gone
// (review round 2, finding 1). Every key is navigation again.
func (m Model) closeSearch() Model {
	if m.mode == TextEntry {
		m.mode = Navigate
		if v := m.app.tickets; v != nil {
			v.entry = ""
		}
	}
	return m
}

// startSearch opens the search line with the current search in it.
func (m Model) startSearch() Model {
	a := m.app
	if a.tickets == nil || !a.searchOn() || m.focus != view.List {
		return m
	}
	a.tickets.entry = a.tickets.query
	m.mode = TextEntry
	return m
}

// searchKey is a key while the search line is open: text, backspace, Enter
// sends the search once (a blank one clears it, the same one does
// nothing), Esc discards what was typed.
func (m Model) searchKey(kind Action, text string) (Model, tea.Cmd) {
	a := m.app
	v := a.tickets
	if v == nil {
		m.mode = Navigate
		return m, nil
	}
	switch kind {
	case InsertText:
		v.entry += text
	case DeleteBack:
		if v.entry != "" {
			runes := []rune(v.entry)
			v.entry = string(runes[:len(runes)-1])
		}
	case Cancel:
		v.entry = ""
		m.mode = Navigate
		// The line closed without a new search: a timer that fired while
		// it was open is set again (review round 1, finding 1).
		return m, m.selectionChanged()
	case Submit:
		query := strings.TrimSpace(v.entry)
		v.entry = ""
		m.mode = Navigate
		if query == v.query {
			return m, m.selectionChanged()
		}
		return m.showTicketPage(1, query)
	}
	return m, nil
}

// --- the preview and the open ------------------------------------------------

// selectionChanged follows a change of the ticket selection or of the
// panes shown: the pane shows the selected ticket from the cache at once,
// if it is there, and the timer for its preview starts. With a ticket
// opened the pane keeps that ticket.
func (m Model) selectionChanged() tea.Cmd {
	a := m.app
	v := a.tickets
	if v == nil || v.opened != "" {
		return nil
	}
	if d := a.detail; d != nil && d.id != v.selected {
		a.dropDetail()
	}
	if a.detail == nil && v.selected != "" && a.ticketOn() {
		a.showCachedDetail(v.project, v.selected)
	}
	return m.schedulePreview()
}

// schedulePreview starts the timer for the preview of the selected row:
// only with the object pane visible, the ticket tool on, no search line
// open, no ticket opened, and only when the pane holds nothing of the
// row yet or a cached record still awaiting its one reload. A record that
// is fresh, loading, stale or refused (a cached one whose reload failed
// included) is left as it is until the cursor leaves the row (no request
// per arrow key or resize) or R asks.
func (m Model) schedulePreview() tea.Cmd {
	a := m.app
	if !m.previewable() {
		return nil
	}
	if d := a.detail; d != nil && d.id == a.tickets.selected && (d.loading || !d.awaitingReload()) {
		return nil
	}
	if a.deps.Events == nil {
		return nil
	}
	a.previewToken++
	return a.deps.Events.Timer(previewDelay, a.previewToken)
}

// previewable: the state in which a preview may be read.
func (m Model) previewable() bool {
	a := m.app
	v := a.tickets
	return v != nil && !a.expired && m.mode == Navigate && v.opened == "" && v.selected != "" && a.ticketOn() && m.objectVisible()
}

// objectVisible: the object pane is on the screen in the current geometry;
// a terminal too small to show anything shows no pane.
func (m Model) objectVisible() bool {
	g := m.geometry()
	return !g.Small && slices.Contains(view.Visible(g, m.focus), view.Object)
}

// holds: the record came from the server and nothing failed since.
func (d *detailView) holds() bool { return d.known && !d.cached && !d.stale && d.err.String() == "" }

// due is the wake-up of a preview timer: with the current token and the
// state still previewable, the selected ticket is read as background work.
func (m Model) due(msg gateway.Due) (Model, tea.Cmd) {
	a := m.app
	if a == nil || a.expired || msg.Session != a.session || msg.Token != a.previewToken || !m.previewable() {
		return m, nil
	}
	v := a.tickets
	if d := a.detail; d != nil && d.id == v.selected && (d.loading || !d.awaitingReload()) {
		// The pane holds the row's ticket already, fresh, loading, stale
		// or refused: a wake-up reads nothing more (the same rule as the
		// timer's).
		return m, nil
	}
	d := a.ensureDetail(v.project, v.selected)
	d.loading = true
	return m, a.issue(gateway.Detail, d.key, forDetail, queue.Background)
}

// openTicket is Enter on a ticket row: the ticket is opened, the focus
// moves to the pane; a pending preview is joined as the person's read, a
// fresh ticket needs none, anything else is read as the person's.
func (m Model) openTicket() (Model, tea.Cmd) {
	a := m.app
	v := a.tickets
	if v == nil || v.selected == "" || !a.ticketOn() {
		return m, nil
	}
	v.opened = v.selected
	m.focus = view.Object
	d := a.ensureDetail(v.project, v.selected)
	d.offset = 0
	if d.holds() && !d.loading {
		return m, nil
	}
	// A pending preview is joined under the same key; without one this is
	// a read of its own.
	d.loading = true
	return m, a.issue(gateway.Detail, d.key, forDetail, queue.User)
}

// ensureDetail makes the pane show the ticket: the one already there, or
// a new view, from the cache when it holds the ticket.
func (a *app) ensureDetail(project, id string) *detailView {
	if d := a.detail; d != nil && d.id == id {
		return d
	}
	a.dropDetail()
	if !a.showCachedDetail(project, id) {
		a.detail = &detailView{id: id, project: project, key: a.detailKey(project, id)}
	}
	return a.detail
}

// showCachedDetail puts the ticket into the pane when the cache holds a
// record of it that still reads, and says whether it did.
func (a *app) showCachedDetail(project, id string) bool {
	d := &detailView{id: id, project: project, key: a.detailKey(project, id)}
	e, ok := a.store.Get(d.key)
	if !ok {
		return false
	}
	record, ans := api.DecodeTicket(transport.Answer{Class: transport.Object, Payload: e.Payload, Status: 200, Sent: true}, gateway.DetailOptions(d.key))
	if !ans.OK() || gateway.CheckDetail(d.key, record) != nil {
		a.store.Forget(d.key)
		return false
	}
	d.record = record
	d.fromCache(e)
	a.detail = d
	return true
}

// closeTicket is Esc on the opened ticket: back to the list; the pane
// keeps the ticket as a preview.
func (m Model) closeTicket() Model {
	a := m.app
	if v := a.tickets; v != nil {
		v.opened = ""
	}
	m.focus = view.List
	return m
}

// --- refresh -----------------------------------------------------------------

// refreshSection is R in the tickets section: the ticket when the pane is
// focused and shows one, else the page, else the projects; as the
// person's read, and not again while one is pending.
func (m Model) refreshSection() tea.Cmd {
	a := m.app
	if m.focus == view.Object && a.detail != nil {
		d := a.detail
		d.loading = true
		if a.pending(d.key) {
			return nil
		}
		return a.issue(gateway.Detail, d.key, forDetail, queue.User)
	}
	if v := a.tickets; v != nil {
		v.corrected = false
		v.loading = true
		if a.pending(v.key) {
			return nil
		}
		return a.issue(gateway.Tickets, v.key, forTickets, queue.User)
	}
	if p := a.projects; p != nil {
		p.loading = true
		if a.pending(p.key) {
			return nil
		}
		return a.issue(gateway.Projects, p.key, forProjects, queue.User)
	}
	return nil
}

// canRefreshSection mirrors refreshSection: R is offered only when it
// would issue a read.
func (m Model) canRefreshSection() bool {
	a := m.app
	if m.focus == view.Object && a.detail != nil {
		return !a.pending(a.detail.key)
	}
	if v := a.tickets; v != nil {
		return !a.pending(v.key)
	}
	if p := a.projects; p != nil {
		return !a.pending(p.key)
	}
	return false
}

// --- results -----------------------------------------------------------------

func (m Model) receiveProjects(r gateway.ProjectsResult) (Model, tea.Cmd) {
	a := m.app
	d, ok := m.receive(r)
	if !ok {
		return m, nil
	}
	v := a.projects
	if v == nil || v.key != r.Key {
		return m, nil
	}
	switch d {
	case cache.Replace:
		v.rows = r.Projects
		v.fresh(a.now())
		v.reselect()
	case cache.Remove:
		// The organisation is gone or out of reach: everything under it
		// goes, the ticket list, the ticket and the search line with it.
		m = m.closeSearch()
		a.leaveList()
		v.rows, v.selected = nil, ""
		v.removed()
		m.focus = view.List
	default:
		v.failed(d, r.Answer)
	}
	return m, nil
}

func (m Model) receiveTickets(r gateway.TicketsResult) (Model, tea.Cmd) {
	a := m.app
	d, ok := m.receive(r)
	if !ok {
		return m, nil
	}
	v := a.tickets
	if v == nil {
		return m, nil
	}
	sameResource := d == cache.Remove && v.key.Resource() == r.Key.Resource()
	if v.key != r.Key && !sameResource {
		return m, nil
	}
	switch d {
	case cache.Replace:
		v.rows, v.total, v.perPage = r.Page.Data, r.Page.Meta.Total, r.Page.Meta.PerPage
		v.fresh(a.now())
		v.reselect()
		if m.focus == view.Object && v.opened == "" && a.detail == nil {
			// A pane that holds nothing loses the focus; one that holds a
			// preview the person may be reading keeps it.
			m.focus = view.List
		}
		return m.correctTicketPage()
	case cache.Remove:
		// The project is gone: every page, the ticket shown with it and
		// the search line (a search of it would answer the same).
		m = m.closeSearch()
		a.dropDetail()
		v.rows, v.total, v.selected, v.opened = nil, 0, "", ""
		v.removed()
		if m.focus == view.Object {
			m.focus = view.List
		}
		return m, nil
	default:
		v.failed(d, r.Answer)
	}
	return m, nil
}

// correctTicketPage reacts to a refresh that showed the current page
// beyond the last one, as the inbox does: once to the new last page, a
// second shrink stops and offers R, a total of zero shows an empty page 1.
func (m Model) correctTicketPage() (Model, tea.Cmd) {
	a := m.app
	v := a.tickets
	last := lastPage(v.total, v.perPage)
	if v.page <= last {
		return m, m.selectionChanged()
	}
	a.store.Apply(v.key, cache.Remove, nil, a.now())
	a.scope.Invalidate(v.key)
	a.dropDetail()
	v.rows, v.selected, v.opened, v.offset = nil, "", "", 0
	if m.focus == view.Object {
		m.focus = view.List
	}
	if v.total == 0 {
		v.page, v.key.Page = 1, 1
		a.rememberTickets()
		return m, nil
	}
	if v.corrected {
		v.err = uitext.Inline(uitext.ListShrank)
		return m, nil
	}
	v.corrected = true
	a.cancelArea(gateway.Tickets)
	v.page, v.key.Page = last, last
	v.known, v.loading = false, true
	a.rememberTickets()
	return m, a.issue(gateway.Tickets, v.key, forTickets, queue.User)
}

func (m Model) receiveDetail(r gateway.TicketResult) (Model, tea.Cmd) {
	a := m.app
	d, ok := m.receive(r)
	if !ok {
		return m, nil
	}
	v := a.detail
	if v == nil {
		return m, nil
	}
	sameResource := d == cache.Remove && v.key.Resource() == r.Key.Resource()
	if v.key != r.Key && !sameResource {
		return m, nil
	}
	switch d {
	case cache.Replace:
		v.record = r.Record
		v.fresh(a.now())
	case cache.Remove:
		// The ticket is gone: the pane says so, an open closes.
		v.record = api.Ticket{}
		v.removed()
		if t := a.tickets; t != nil && t.opened == v.id {
			t.opened = ""
		}
		if m.focus == view.Object {
			m.focus = view.List
		}
	default:
		v.failed(d, r.Answer)
	}
	return m, nil
}
