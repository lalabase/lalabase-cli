package ui

import (
	"fmt"
	"slices"
	"strings"

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

// The ticket list grouped by status (C11). The chronological list stays
// what it is, one page under the view's key: it brings the project's
// statuses and the total, and g goes back to it without a read. Each status
// is a list of its own beside it, a page under a key with that status, so
// a group pages alone: groups do not fit one server page. All of them are
// read, a folded one too: only a row says what a status means.

// ticketGroup is one status of the project with the page of it shown.
type ticketGroup struct {
	// name is the status as the project's catalogue has it; foreign text.
	name           string
	key            cache.Key
	rows           []api.TicketRow
	total, perPage int
	flight         uint64
	status
	// functions are the status's meaning, from its rows; they outlive a
	// page turn, as the total does.
	functions []string
	corrected bool
}

func (g *ticketGroup) page() int { return g.key.Page }

// settled: the group's read is over, with data or without.
func (g *ticketGroup) settled() bool { return !g.loading && (g.known || g.err.String() != "") }

// shown: a group without tickets has no head, one that failed says so.
func (g *ticketGroup) shown() bool { return g.total > 0 || g.err.String() != "" }

// done: the status means the work is over.
func (g *ticketGroup) done() bool {
	return slices.Contains(g.functions, "is_completed") || slices.Contains(g.functions, "is_closed")
}

// groupLine is a line of the grouped list: a group's head, or a row of it.
type groupLine struct {
	group *ticketGroup
	row   int
}

const groupHead = -1

// isGrouped: the list on the screen is the grouped one.
func (v *ticketsView) isGrouped() bool { return v.grouped && len(v.groups) > 0 }

// groupable: the project has statuses to group by.
func (v *ticketsView) groupable() bool { return len(v.statuses) > 0 }

// group is the group a key reads, nil for none.
func (v *ticketsView) group(k cache.Key) *ticketGroup {
	for _, g := range v.groups {
		if g.key == k {
			return g
		}
	}
	return nil
}

func (v *ticketsView) folded(g *ticketGroup) bool {
	if f, ok := v.folds[g.name]; ok {
		return f
	}
	return g.done()
}

// owners says which group holds a ticket: groups are read apart, so one
// that moved stands in two until the older is read again, and the newer
// read knows.
func (v *ticketsView) owners() map[string]*ticketGroup {
	out := map[string]*ticketGroup{}
	for _, g := range v.groups {
		for _, r := range g.rows {
			if o := out[r.ID]; o == nil || g.flight > o.flight {
				out[r.ID] = g
			}
		}
	}
	return out
}

// entries are the lines the cursor can stand on, in order, each ticket once.
func (v *ticketsView) entries() []groupLine {
	var out []groupLine
	owners := v.owners()
	for _, g := range v.groups {
		if !g.shown() {
			continue
		}
		out = append(out, groupLine{g, groupHead})
		if v.folded(g) {
			continue
		}
		for i, r := range g.rows {
			if owners[r.ID] == g {
				out = append(out, groupLine{g, i})
			}
		}
	}
	return out
}

// cursorAt is the entry the cursor is on, -1 for none.
func (v *ticketsView) cursorAt(entries []groupLine) int {
	return slices.IndexFunc(entries, func(e groupLine) bool {
		if e.row == groupHead {
			return v.heading != "" && e.group.name == v.heading
		}
		return v.heading == "" && v.selected != "" && e.group.rows[e.row].ID == v.selected
	})
}

// put sets the cursor on an entry.
func (v *ticketsView) put(e groupLine) {
	if e.row == groupHead {
		v.selected, v.heading = "", e.group.name
		return
	}
	v.selected, v.heading = e.group.rows[e.row].ID, ""
}

func (v *ticketsView) moveInGroups(delta int) {
	entries := v.entries()
	if len(entries) == 0 {
		return
	}
	v.put(entries[max(0, min(len(entries)-1, max(0, v.cursorAt(entries))+delta))])
}

// allSettled: every group's read is over, so what is not there is gone.
func (v *ticketsView) allSettled() bool {
	return !slices.ContainsFunc(v.groups, func(g *ticketGroup) bool { return !g.settled() })
}

// reselectGroup keeps the cursor where it is. While groups load, a ticket
// not there yet may still come, so the cursor waits for it; once all have
// landed it takes the first line, and a ticket opened that is in no group
// closes, as one that left the page does.
func (v *ticketsView) reselectGroup() {
	lines := v.entries()
	settled := v.allSettled()
	if v.cursorAt(lines) < 0 {
		if settled {
			v.selected, v.heading = "", ""
		}
		if v.selected == "" && v.heading == "" && len(lines) > 0 {
			v.put(lines[0])
		}
	}
	if settled && v.opened != "" {
		if _, _, ok := v.findInGroups(v.opened); !ok {
			v.opened = ""
		}
	}
}

func (v *ticketsView) findInGroups(id string) (api.TicketRow, uint64, bool) {
	for _, g := range v.groups {
		for _, r := range g.rows {
			if r.ID == id {
				return r, g.flight, true
			}
		}
	}
	return api.TicketRow{}, 0, false
}

// find is a row of the list on the screen, with the read it came from.
func (v *ticketsView) find(id string) (api.TicketRow, uint64, bool) {
	if v.isGrouped() {
		return v.findInGroups(id)
	}
	if i := v.index(id); i >= 0 {
		return v.rows[i], v.flight, true
	}
	return api.TicketRow{}, 0, false
}

// shows: the cursor can stand on the ticket.
func (v *ticketsView) shows(id string) bool {
	if !v.isGrouped() {
		return v.index(id) >= 0
	}
	return slices.ContainsFunc(v.entries(), func(e groupLine) bool {
		return e.row != groupHead && e.group.rows[e.row].ID == id
	})
}

// cursorGroup is the group the cursor is in, nil for none.
func (v *ticketsView) cursorGroup() *ticketGroup {
	entries := v.entries()
	if at := v.cursorAt(entries); at >= 0 {
		return entries[at].group
	}
	return nil
}

// modeSwitch is what the frame says of the list's mode, nothing for a
// project without statuses, whose list has none.
func (v *ticketsView) modeSwitch() *present.ModeSwitch {
	if !v.groupable() {
		return nil
	}
	mark := func(id uitext.ID, on bool) string {
		if on {
			return uitext.Fill(uitext.ModeShown, uitext.Inline(id).String()).String()
		}
		return uitext.Inline(id).String()
	}
	grouped := v.isGrouped()
	active := uitext.ChronologicalMode
	if grouped {
		active = uitext.GroupedMode
	}
	return &present.ModeSwitch{
		Key:    uitext.Inline(uitext.GroupKey),
		Modes:  present.NewInline(mark(uitext.GroupedMode, grouped) + " " + mark(uitext.ChronologicalMode, !grouped)),
		Active: present.NewInline(mark(active, true)),
	}
}

// --- reads --------------------------------------------------------------------

// groupNames are the statuses to read, each once: the server compares
// statuses without their case, so two spellings are one list.
func groupNames(statuses []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range statuses {
		fold := strings.ToLower(s)
		if s == "" || seen[fold] {
			continue
		}
		seen[fold] = true
		out = append(out, s)
	}
	return out
}

// syncGroups makes the groups follow the list: nothing when it is not
// grouped, a notice when the project turns out to have no statuses, else
// the reads of the statuses without a group. A group already there stays,
// so g back and forth reads nothing.
func (a *app) syncGroups() tea.Cmd {
	v := a.tickets
	if v == nil || !v.grouped {
		return nil
	}
	names := groupNames(v.statuses)
	if len(names) == 0 {
		if !v.known {
			return nil // the page that names the statuses is still out
		}
		v.grouped, v.groups, v.heading = false, nil, ""
		v.reselect()
		return a.groupHint()
	}
	groups := make([]*ticketGroup, 0, len(names))
	var fresh []*ticketGroup
	for _, name := range names {
		i := slices.IndexFunc(v.groups, func(g *ticketGroup) bool { return g.name == name })
		if i >= 0 {
			groups = append(groups, v.groups[i])
			continue
		}
		k := a.ticketsKey(v.project, v.query, 1)
		k.Status = name
		g := &ticketGroup{name: name, key: k}
		groups, fresh = append(groups, g), append(fresh, g)
	}
	v.groups = groups
	cmds := make([]tea.Cmd, 0, len(fresh))
	for _, g := range fresh {
		cmds = append(cmds, a.loadGroup(g))
	}
	v.reselect()
	return tea.Batch(cmds...)
}

// loadGroup shows the group's cached page, if there is one that still
// reads, and starts one reload, as loadTickets does for the list.
func (a *app) loadGroup(g *ticketGroup) tea.Cmd {
	if e, ok := a.cachedGroup(g); ok {
		g.fromCache(e)
	} else {
		a.diskRead(gateway.Tickets, g.key, forGroup)
	}
	g.loading = true
	return a.issue(gateway.Tickets, g.key, forGroup, queue.User)
}

// cachedGroup puts the memory entry of the group's key into it, if there is
// one that still reads.
func (a *app) cachedGroup(g *ticketGroup) (cache.Entry, bool) {
	e, ok := a.store.Get(g.key)
	if !ok {
		return cache.Entry{}, false
	}
	page, ans := api.DecodeTickets(transport.Answer{Class: transport.Object, Payload: e.Payload, Status: 200, Sent: true})
	if !ans.OK() || gateway.CheckTicketsPage(g.key, page) != nil {
		a.forget(g.key)
		return cache.Entry{}, false
	}
	g.fill(page, a.filed[g.key])
	return e, true
}

// fill takes a page into the group.
func (g *ticketGroup) fill(page api.TicketPage, flight uint64) {
	g.rows, g.total, g.perPage, g.flight = page.Data, page.Meta.Total, page.Meta.PerPage, flight
	if len(page.Data) > 0 {
		g.functions = page.Data[0].StatusFunctions
	}
}

// reloadGroups reads every group again, each once.
func (a *app) reloadGroups() tea.Cmd {
	v := a.tickets
	if v == nil || !v.grouped {
		return nil
	}
	cmds := make([]tea.Cmd, 0, len(v.groups))
	for _, g := range v.groups {
		g.corrected, g.loading = false, true
		if !a.pending(g.key) {
			cmds = append(cmds, a.issue(gateway.Tickets, g.key, forGroup, queue.User))
		}
	}
	return tea.Batch(cmds...)
}

// groupToRead: a group has no read out, so R would issue one.
func (v *ticketsView) groupToRead(a *app) bool {
	return v.grouped && slices.ContainsFunc(v.groups, func(g *ticketGroup) bool { return !a.pending(g.key) })
}

// receiveGroup applies an accepted answer to its group. A removal of the
// project does not come here: it takes the whole list.
func (m Model) receiveGroup(g *ticketGroup, d cache.Decision, r gateway.TicketsResult) (Model, tea.Cmd) {
	a := m.app
	v := a.tickets
	if d != cache.Replace {
		g.failed(d, r.Answer)
		v.reselect()
		return m, m.selectionChanged()
	}
	g.fill(r.Page, uint64(r.Flight))
	for _, row := range r.Page.Data {
		a.learnTitle(row.ID, row.Title, uint64(r.Flight))
	}
	g.fresh(a.now())
	cmd := a.correctGroup(g)
	v.reselect()
	if m.focus == view.Object && v.opened == "" && a.detail == nil {
		m.focus = view.List
	}
	return m, tea.Batch(cmd, m.selectionChanged())
}

// correctGroup reacts to a group that shrank below the page shown, as
// correctTicketPage does for the list: once to the new last page, a second
// shrink stops and offers R. Only this page's entry goes: a removal of the
// resource would refuse the reads of the other groups.
func (a *app) correctGroup(g *ticketGroup) tea.Cmd {
	last := lastPage(g.total, g.perPage)
	if g.page() <= last {
		return nil
	}
	a.forget(g.key)
	g.rows = nil
	if g.total == 0 {
		g.key.Page = 1
		return nil
	}
	if g.corrected {
		g.err = uitext.Inline(uitext.ListShrank)
		return nil
	}
	g.corrected = true
	g.key.Page = last
	g.known, g.loading = false, true
	return a.issue(gateway.Tickets, g.key, forGroup, queue.User)
}

// groupHint says g found nothing to group by, for as long as a hint stands.
func (a *app) groupHint() tea.Cmd {
	now := a.now()
	a.groupNotice = bookNotice{text: uitext.Inline(uitext.GroupNone), tone: present.Plain, until: now.Add(hintFor)}
	if a.deps.Events == nil {
		return nil
	}
	return a.deps.Events.Expiry(a.groupNotice.until, now)
}

// --- keys ---------------------------------------------------------------------

// toggleGroups is g on the ticket list.
func (m Model) toggleGroups() (Model, tea.Cmd) {
	a := m.app
	v := a.tickets
	if v == nil || a.active != nil || m.focus != view.List {
		return m, nil
	}
	if v.grouped {
		v.grouped, v.heading = false, ""
		v.reselect()
		return m, m.selectionChanged()
	}
	if v.known && !v.groupable() {
		return m, a.groupHint()
	}
	v.grouped = true
	cmd := a.syncGroups()
	v.reselect()
	return m, tea.Batch(cmd, m.selectionChanged())
}

// onGroupHead: the cursor stands on a group's head in the list.
func (m Model) onGroupHead() bool {
	a := m.app
	return m.focus == view.List && a.active == nil && a.tickets != nil && a.tickets.isGrouped() && a.tickets.heading != ""
}

// foldGroup folds or opens the group whose head the cursor is on.
func (m Model) foldGroup() (Model, tea.Cmd) {
	v := m.app.tickets
	g := v.cursorGroup()
	if g == nil {
		return m, nil
	}
	if v.folds == nil {
		v.folds = map[string]bool{}
	}
	v.folds[g.name] = !v.folded(g)
	return m, nil
}

// turnGroup moves the group at the cursor to its previous or next page; the
// other groups keep theirs and no read of theirs is touched. The cursor
// goes to the head: the row it stood on goes with the page.
func (m Model) turnGroup(delta int) (Model, tea.Cmd) {
	a := m.app
	v := a.tickets
	g := v.cursorGroup()
	if g == nil || g.loading || v.folded(g) {
		return m, nil
	}
	target := g.page() + delta
	if target < 1 || (delta > 0 && (!g.known || target > lastPage(g.total, g.perPage))) {
		return m, nil
	}
	if slices.ContainsFunc(g.rows, func(r api.TicketRow) bool { return r.ID == v.opened }) {
		v.opened = ""
		a.dropDetail()
	}
	if m.focus == view.Object {
		m.focus = view.List
	}
	g.key.Page = target
	g.rows, g.status, g.corrected = nil, status{}, false
	v.selected, v.heading = "", g.name
	cmd := a.loadGroup(g)
	return m, tea.Batch(cmd, m.selectionChanged())
}

// groupBindings are the list's keys while it is grouped.
func (m Model) groupBindings(v *ticketsView) []binding {
	var out []binding
	if len(v.entries()) > 1 {
		out = append(out, binding{Up, uitext.UpDownKey, uitext.SelectHelp}, binding{PageUp, uitext.PageUpDownKey, uitext.MoveHelp})
	}
	switch {
	case v.heading != "":
		out = append(out, binding{OpenList, uitext.EnterKey, uitext.FoldHelp})
		out = append(out, binding{Expand, uitext.ExpandKey, uitext.FoldHelp})
	case v.selected != "" && m.app.ticketOn():
		out = append(out, binding{OpenList, uitext.EnterKey, uitext.OpenTicketHelp})
	}
	if g := v.cursorGroup(); g != nil && !g.loading && !v.folded(g) {
		if g.page() > 1 {
			out = append(out, binding{PrevPage, uitext.PrevPageKey, uitext.PrevPageHelp})
		}
		if g.known && g.page() < lastPage(g.total, g.perPage) {
			out = append(out, binding{NextPage, uitext.NextPageKey, uitext.NextPageHelp})
		}
	}
	return out
}

// --- drawing ------------------------------------------------------------------

// groupsNotice is the line under the title: the list's, loading as long as
// any group does.
func (v *ticketsView) groupsNotice(empty uitext.ID) present.Inline {
	s := v.status
	s.known = slices.ContainsFunc(v.groups, func(g *ticketGroup) bool { return g.known })
	s.loading = s.loading || slices.ContainsFunc(v.groups, func(g *ticketGroup) bool { return g.loading })
	return s.notice(empty, len(v.entries()) == 0 && v.rest() == 0)
}

// rest is the number of tickets no group holds: without a status, or with
// one the project no longer has, which no status filter finds. Known only
// when the list and every group are; a group still out would count as rest.
func (v *ticketsView) rest() int {
	if !v.known || !v.allSettled() || slices.ContainsFunc(v.groups, func(g *ticketGroup) bool { return !g.known }) {
		return 0
	}
	n := v.total
	for _, g := range v.groups {
		n -= g.total
	}
	return max(0, n)
}

// groupHeadRow is a group's head: mark, status in its tone, count.
func (v *ticketsView) groupHeadRow(g *ticketGroup) present.Row {
	mark := uitext.GroupOpenMark
	note := []string{fmt.Sprint(g.total)}
	if v.folded(g) {
		mark = uitext.GroupFoldedMark
		note = append(note, uitext.Inline(uitext.GroupFolded).String())
	} else if last := lastPage(g.total, g.perPage); last > 1 {
		note = append(note, uitext.Fill(uitext.PageOf, g.page(), last).String())
	}
	switch {
	case g.err.String() != "":
		note = append(note, g.err.String())
	case g.stale:
		note = append(note, uitext.Inline(uitext.PossiblyStale).String())
	}
	return present.Row{
		Heading:      true,
		Primary:      present.NewInline(uitext.Inline(mark).String() + " " + bounded(g.name, maxStatus)),
		HeadingStyle: statusStyle(g.functions, present.Strong),
		Note:         present.NewInline(strings.Join(note, " · ")),
	}
}

// groupRows are the lines of the grouped list: the numbers aligned over all
// groups, no status at the row's end, its head says it. The line of the
// rest is no entry, so it comes into sight with the cursor on the last one.
func (m Model) groupRows(c *present.Column) {
	v := m.app.tickets
	lines := v.entries()
	width := 0
	for _, l := range lines {
		if l.row != groupHead {
			width = max(width, len(uitext.Fill(uitext.TicketNumber, l.group.rows[l.row].Number).String()))
		}
	}
	at := v.cursorAt(lines)
	c.Cursor = at
	c.Rows = make([]present.Row, 0, len(lines)+1)
	for _, l := range lines {
		if l.row == groupHead {
			c.Rows = append(c.Rows, v.groupHeadRow(l.group))
			continue
		}
		r := l.group.rows[l.row]
		number := uitext.Fill(uitext.TicketNumber, r.Number).String()
		row := present.Row{
			Primary:   present.NewInline(fmt.Sprintf("%*s %s", width, number, textOr(r.Title, uitext.UntitledTicket))),
			Secondary: present.NewInline(strings.Repeat(" ", width+1) + assigneesOf(r.Assignees) + " · " + m.app.when(r.UpdatedAt)),
			Quiet:     statusStyle(r.StatusFunctions, present.Plain) == present.StatusDone,
		}
		m.app.markAgent(&row, r.ID)
		c.Rows = append(c.Rows, row)
	}
	c.Offset = v.offset
	if n := v.rest(); n > 0 {
		c.Rows = append(c.Rows, present.Row{Primary: uitext.Fill(uitext.GroupRest, n), Quiet: true})
		if at == len(lines)-1 {
			c.Offset = len(c.Rows)
		}
	}
}
