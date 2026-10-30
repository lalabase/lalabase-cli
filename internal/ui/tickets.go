package ui

import (
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/cache"
	"github.com/gboyke/lalabase-cli/internal/features"
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
	// flight is the request whose answer the rows are (queue.Flight); from
	// the cache, the newest filed for the key, which the memory entry is.
	// It ranks the rows' titles against other reads (newTitle).
	flight uint64
	status
	// selected is the row the cursor is on; opened the ticket opened with
	// Enter, "" for none (the pane then shows a preview, if any).
	selected, opened string
	offset           int
	corrected        bool
	// statuses are the project's, in its order, from the last page read;
	// grouped asks for the list by status (C11, groups.go), groups is that
	// list, nil while it is not built; heading the status whose head the
	// cursor is on, "" on a row; folds what the person folded or opened.
	statuses []string
	grouped  bool
	groups   []*ticketGroup
	heading  string
	folds    map[string]bool
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
	if v.isGrouped() {
		v.reselectGroup()
		return
	}
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
	if v.isGrouped() {
		v.moveInGroups(delta)
		return
	}
	if len(v.rows) == 0 {
		return
	}
	at := max(0, v.index(v.selected))
	v.selected = v.rows[max(0, min(len(v.rows)-1, at+delta))].ID
}

// detailView is the ticket in the object pane.
type detailView struct {
	id, project string
	// key is the page asked for; shown the page the record answers. They
	// differ while a page turn or a correction loads (C1d).
	key, shown cache.Key
	record     api.Ticket
	// uncut: the record came over REST (K1), which cuts no text; a text
	// that ends in the tools' marker was written so. A record from the
	// cache may be the tools' and is read as theirs until it is replaced.
	uncut bool
	// booked is the person's time on the ticket (S6, time.go).
	booked bookedTime
	// flight is the request whose answer the record is, as the list's;
	// a page corrected without a read keeps it (correctDetailPage).
	flight uint64
	status
	offset int
	// area is the active section, post the selected comment (by id),
	// follow what the pane keeps in sight (C1d).
	area ticketArea
	post string
	// postFile is the file of that comment the walk stands on (C12), by
	// id, "" on the comment itself. post stays the comment: e reads it.
	postFile string
	// file is the id of the selected file (C10), "" for the page's first.
	file   string
	follow anchor
	// corrected: the one page correction after a shrink was done; R and a
	// page turn free it again.
	corrected bool
	expansion *expansion
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
	a.leaveLinkedTicket()
	a.forgetSettledMarks()
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

// dropDetail takes the ticket out of the object pane and cancels its
// reads, the expansion's included.
func (a *app) dropDetail() {
	a.cancelArea(gateway.Detail)
	a.cancelArea(gateway.Expansion)
	a.cancelArea(gateway.Time)
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
		a.cancelDiskRead(id)
	}
}

// openProjects opens the tickets section of org: its projects, from the
// cache at once when they are there, then one reload. On the section already
// open it leads back to the projects from a ticket list; Esc in the chooser
// is the way back to that list.
func (m Model) openProjects(org string) (Model, tea.Cmd) {
	a := m.app
	if !a.canOpen(org, sectionTickets) {
		return m, nil
	}
	if a.projects != nil && a.projects.org == org {
		a.leaveList()
		m.focus = view.List
		return m, nil
	}
	cmd := a.enterProjects(org)
	m.focus = view.List
	return m, cmd
}

// enterProjects leaves what is open in the list's place (an inbox, another
// organisation's tickets), opens the projects of org there and loads
// them. The selected project is the one selected when the person last
// left them; openAgent selects the agent's project afterwards. The chooser
// and an agent open the projects this one way.
func (a *app) enterProjects(org string) tea.Cmd {
	a.leaveInbox()
	a.leaveTickets()
	name := ""
	if o := a.organisation(org); o != nil {
		name = o.Name
	}
	a.projects = &projectsView{org: org, name: name, key: a.projectsKey(org), selected: a.rememberedProject[org]}
	return a.loadProjects()
}

// loadProjects shows the cached list, if there is one that still reads,
// and starts exactly one reload of the person's priority; without one in
// memory the disk is asked too (C1e).
func (a *app) loadProjects() tea.Cmd {
	v := a.projects
	if e, ok := a.cachedProjects(v); ok {
		v.fromCache(e)
	} else {
		a.diskRead(gateway.Projects, v.key, forProjects)
	}
	v.loading = true
	return a.issue(gateway.Projects, v.key, forProjects, queue.User)
}

// cachedProjects puts the memory entry of the view's key into the view, if
// there is one that still reads.
func (a *app) cachedProjects(v *projectsView) (cache.Entry, bool) {
	e, ok := a.store.Get(v.key)
	if !ok {
		return cache.Entry{}, false
	}
	list, ans := api.DecodeProjects(transport.Answer{Class: transport.Object, Payload: e.Payload, Status: 200, Sent: true})
	if !ans.OK() || gateway.CheckProjectsList(v.key, list) != nil {
		a.forget(v.key)
		return cache.Entry{}, false
	}
	v.rows = list
	v.reselect()
	return e, true
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
// and starts exactly one reload of the person's priority; without one in
// memory the disk is asked too (C1e).
func (a *app) loadTickets() tea.Cmd {
	v := a.tickets
	if e, ok := a.cachedTickets(v); ok {
		v.fromCache(e)
	} else {
		a.diskRead(gateway.Tickets, v.key, forTickets)
	}
	v.loading = true
	return a.issue(gateway.Tickets, v.key, forTickets, queue.User)
}

// cachedTickets puts the memory entry of the view's key into the view, if
// there is one that still reads.
func (a *app) cachedTickets(v *ticketsView) (cache.Entry, bool) {
	e, ok := a.store.Get(v.key)
	if !ok {
		return cache.Entry{}, false
	}
	page, ans := api.DecodeTickets(transport.Answer{Class: transport.Object, Payload: e.Payload, Status: 200, Sent: true})
	if !ans.OK() || gateway.CheckTicketsPage(v.key, page) != nil {
		a.forget(v.key)
		return cache.Entry{}, false
	}
	v.rows, v.total, v.perPage = page.Data, page.Meta.Total, page.Meta.PerPage
	v.statuses = page.Meta.AvailableStatuses
	v.flight = a.filed[v.key]
	v.reselect()
	return e, true
}

// turnTicketPage moves to the previous or next server page, when there is
// one and no load is pending.
func (m Model) turnTicketPage(delta int) (Model, tea.Cmd) {
	a := m.app
	v := a.tickets
	if v != nil && v.isGrouped() {
		return m.turnGroup(delta)
	}
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
	v.groups, v.heading = nil, ""
	a.rememberTickets()
	if m.focus == view.Object {
		m.focus = view.List
	}
	return m, tea.Batch(a.loadTickets(), a.syncGroups(), m.selectionChanged())
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
	return m, a.issue(gateway.Detail, d.key, forDetail, queue.Preview)
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
	d.offset, d.follow = 0, anchorNone
	return m, a.readUnlessFresh(d)
}

// readUnlessFresh is the read of an opened ticket: as the person's, unless
// the pane holds it fresh and nothing loads; a pending preview is joined
// under the same key, without one this is a read of its own. Every way to
// open a ticket takes it (the list, an agent, a notification's link), so a
// ticket whose read or refresh failed is read again.
func (a *app) readUnlessFresh(d *detailView) tea.Cmd {
	// Every open reads the person's time on the ticket too (S6).
	booked := a.readTime(d, true)
	if d.holds() && !d.loading {
		return booked
	}
	d.loading = true
	return tea.Batch(a.issue(gateway.Detail, d.key, forDetail, queue.User), booked)
}

// ensureDetail makes the pane show the ticket: the one already there, or
// a new view, from the cache when it holds the ticket, else with the disk
// asked (C1e) beside the read the caller issues.
func (a *app) ensureDetail(project, id string) *detailView {
	if d := a.detail; d != nil && d.id == id {
		return d
	}
	a.dropDetail()
	if !a.showCachedDetail(project, id) {
		a.detail = &detailView{id: id, project: project, key: a.detailKey(project, id)}
		a.diskRead(gateway.Detail, a.detail.key, forDetail)
	}
	return a.detail
}

// showCachedDetail puts the ticket into the pane when the cache holds a
// record of it that still reads, and says whether it did.
func (a *app) showCachedDetail(project, id string) bool {
	d := &detailView{id: id, project: project, key: a.detailKey(project, id)}
	e, ok := a.cachedPage(d, d.key)
	if !ok {
		return false
	}
	d.fromCache(e)
	a.detail = d
	return true
}

// cachedPage puts the memory entry of a page of the ticket into the view,
// if there is one that still reads: the record and the page shown, the
// selection settled.
func (a *app) cachedPage(d *detailView, k cache.Key) (cache.Entry, bool) {
	e, ok := a.store.Get(k)
	if !ok {
		return cache.Entry{}, false
	}
	record, ans := api.DecodeTicket(transport.Answer{Class: transport.Object, Payload: e.Payload, Status: 200, Sent: true}, gateway.DetailOptions(k))
	if !ans.OK() || gateway.CheckDetail(k, record) != nil {
		a.forget(k)
		return cache.Entry{}, false
	}
	d.record, d.shown, d.uncut = record, k, false
	d.flight = a.filed[k]
	a.settle(d)
	return e, true
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
		d.loading, d.corrected = true, false
		booked := a.readTime(d, false) // R reads the time again too (S6)
		if a.pending(d.key) {
			return booked
		}
		return tea.Batch(a.issue(gateway.Detail, d.key, forDetail, queue.User), booked)
	}
	if v := a.tickets; v != nil {
		v.corrected = false
		v.loading = true
		groups := a.reloadGroups()
		if a.pending(v.key) {
			return groups
		}
		return tea.Batch(a.issue(gateway.Tickets, v.key, forTickets, queue.User), groups)
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
// would issue a read of what is shown. The ticket's booked time (S6),
// which R reads beside it, does not offer R alone: it is extra.
func (m Model) canRefreshSection() bool {
	a := m.app
	if m.focus == view.Object && a.detail != nil {
		return !a.pending(a.detail.key)
	}
	if v := a.tickets; v != nil {
		return !a.pending(v.key) || v.groupToRead(a)
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
		if m.focus != view.Agents {
			// The agents' list keeps the focus (C9b).
			m.focus = view.List
		}
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
	if g := v.group(r.Key); g != nil && d != cache.Remove {
		return m.receiveGroup(g, d, r)
	}
	sameResource := d == cache.Remove && v.key.Resource() == r.Key.Resource()
	if v.key != r.Key && !sameResource {
		return m, nil
	}
	switch d {
	case cache.Replace:
		v.rows, v.total, v.perPage = r.Page.Data, r.Page.Meta.Total, r.Page.Meta.PerPage
		v.statuses = r.Page.Meta.AvailableStatuses
		if !v.grouped {
			// Groups kept for the next g are now older than the total.
			v.groups = nil
		}
		v.flight = uint64(r.Flight)
		for _, row := range r.Page.Data {
			a.learnTitle(row.ID, row.Title, uint64(r.Flight))
		}
		v.fresh(a.now())
		v.reselect()
		if m.focus == view.Object && v.opened == "" && a.detail == nil {
			// A pane that holds nothing loses the focus; one that holds a
			// preview the person may be reading keeps it.
			m.focus = view.List
		}
		// After the correction: a removal there refuses what began before.
		m, cmd := m.correctTicketPage()
		synced := a.syncGroups()
		if synced != nil || v.isGrouped() {
			// The groups may have changed under the cursor, or have gone
			// with a hint whose end is in synced.
			cmd = tea.Batch(cmd, synced, m.selectionChanged())
		}
		return m, cmd
	case cache.Remove:
		// The project is gone: every page, the ticket shown with it and
		// the search line (a search of it would answer the same).
		m = m.closeSearch()
		a.dropDetail()
		v.rows, v.total, v.selected, v.opened = nil, 0, "", ""
		v.grouped, v.groups, v.heading = false, nil, ""
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
	if v.isGrouped() {
		// The page is out of sight and the ticket shown is a group's. The
		// groups' reads out were refused just now: reloadGroups starts anew.
		a.cancelArea(gateway.Tickets)
		v.rows = nil
	} else {
		a.dropDetail()
		v.rows, v.selected, v.opened, v.offset = nil, "", "", 0
		if m.focus == view.Object {
			m.focus = view.List
		}
	}
	if v.total == 0 {
		v.page, v.key.Page = 1, 1
		a.rememberTickets()
		return m, a.reloadGroups()
	}
	if v.corrected {
		v.err = uitext.Inline(uitext.ListShrank)
		return m, a.reloadGroups()
	}
	v.corrected = true
	a.cancelArea(gateway.Tickets)
	v.page, v.key.Page = last, last
	v.known, v.loading = false, true
	a.rememberTickets()
	// The removal took the groups' reads with it, the move those still out.
	return m, tea.Batch(a.issue(gateway.Tickets, v.key, forTickets, queue.User), a.reloadGroups())
}

func (m Model) receiveDetail(r gateway.TicketResult) (Model, tea.Cmd) {
	a := m.app
	d, ok := m.receive(r)
	if !ok {
		return m, nil
	}
	if d == cache.Replace {
		// An accepted read renews an agent's title, whatever the pane
		// shows by now (C9b); a removal takes it (receive).
		a.learnTitle(r.Record.ID, r.Record.Title, uint64(r.Flight))
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
		v.record, v.shown, v.uncut = r.Record, r.Key, r.Way == features.REST
		v.flight = uint64(r.Flight)
		v.fresh(a.now())
		a.settle(v)
		return m.correctDetailPage()
	case cache.Remove:
		// The ticket is gone: the pane says so, an open closes.
		return m.detailRemoved(), nil
	default:
		v.failed(d, r.Answer)
	}
	return m, nil
}
