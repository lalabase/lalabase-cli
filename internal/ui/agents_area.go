package ui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/agents"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/cache"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/queue"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
	"github.com/gboyke/lalabase-cli/internal/uitext"
)

// The Agents area (C9b): every agent of the profile, across the
// organisations, under the chooser. Of an agent's files only its number
// and its state are shown; the title of its ticket comes from what the
// session holds (the ticket in the pane, the list shown, the cache), and
// a title it lacks is read once through the gateway, as background work.

// title is what the session knows of one agent's ticket for its title:
// whether a read of it is out, failed (kept until R) or known, whether the
// server removed the ticket (no title then, whatever older data holds,
// until a newer read succeeds), and whether the disk cache was asked.
type title struct {
	asked, failed, known, removed, disk bool
	text                                string
}

// agentsColumn is the Agents area: per agent, in the order of urgency,
// its glyph, its number and its ticket's title (the number alone until
// the title is known), and under it its organisation when that is not the
// chosen one. A seen agent that ended is quiet. No rows without an agent:
// then there is no area.
func (m Model) agentsColumn() present.Column {
	a := m.app
	c := present.Column{Title: uitext.Inline(uitext.Agents), Cursor: -1}
	list := a.agentsInOrder()
	if len(list) == 0 {
		return c
	}
	chosen := a.chosenOrg()
	c.Rows = make([]present.Row, 0, len(list))
	current, _ := a.agentUnderCursor()
	for i, ag := range list {
		if m.focus == view.Agents && ag.ID == current.ID {
			c.Cursor = i
		}
		text := uitext.Fill(uitext.TicketNumber, ag.Number).String()
		if t, ok := a.agentTitle(ag); ok {
			text += " " + textOr(t, uitext.UntitledTicket)
		}
		row := present.Row{
			Primary: present.NewInline(text), Marked: true,
			Mark: present.NewInline(a.glyph(ag.State)), MarkStyle: agentStyles[ag.State],
			Quiet: ag.State == agents.Ended && a.seen(ag),
		}
		if o := a.organisation(ag.Organisation); o != nil && ag.Organisation != chosen {
			row.Secondary = present.NewInline(textOr(o.Name, uitext.UnnamedOrganisation))
		}
		c.Rows = append(c.Rows, row)
	}
	return c
}

// seen: the person opened the agent once, in this run or, recorded on
// disk, in an earlier one (C9b).
func (a *app) seen(ag agents.Agent) bool { return ag.Seen || a.agents.seen[ag.ID] }

// chosenOrg is the organisation the person is in: the one of the open
// inbox ("" for all) or tickets, else the one the chooser's cursor is on.
func (a *app) chosenOrg() string {
	switch {
	case a.projects != nil:
		return a.projects.org
	case a.active != nil:
		return a.active.org
	}
	return a.highlight
}

// agentTitle is the title of the agent's ticket from what the session
// holds: the ticket in the pane, a row of the list shown, a read of it
// for its title. ok is false while none of them has it.
func (a *app) agentTitle(ag agents.Agent) (string, bool) {
	if t := a.titles[ag.Ticket]; t != nil && t.removed {
		// A removal outweighs older data, a list's rows among them.
		return "", false
	}
	if d := a.detail; d != nil && d.known && d.record.ID == ag.Ticket {
		return d.record.Title, true
	}
	if v := a.tickets; v != nil {
		if i := v.index(ag.Ticket); i >= 0 {
			return v.rows[i].Title, true
		}
	}
	if t := a.titles[ag.Ticket]; t != nil && t.known {
		return t.text, true
	}
	return "", false
}

// requestTitles reads, once each, the titles of the agents' tickets the
// session does not hold: from the memory cache when it has the ticket,
// else from the server through the gateway as idle work, after every
// preview and within half the background budget (queue.Idle). The disk
// cache is asked beside (C1e), in batches that never push out a read
// waiting for it (diskRoom); a title it holds cancels the server's read
// while that still waits (titleFromDisk). The next batch follows with the
// next reading of the agents. A title that was asked for, failed or is
// known is not asked again; R frees the failed ones (retryTitles).
func (a *app) requestTitles() tea.Cmd {
	if a.expired || a.scope == nil || !a.ticketOn() {
		return nil
	}
	var cmds []tea.Cmd
	for _, ag := range a.agents.list {
		if _, ok := a.agentTitle(ag); ok {
			continue
		}
		key := a.detailKey(ag.Project, ag.Ticket)
		t := a.titles[ag.Ticket]
		if t == nil {
			t = &title{}
			a.titles[ag.Ticket] = t
			if a.titleFromCache(t, key) {
				continue
			}
		}
		if !t.asked && !t.failed {
			if cmd := a.issue(gateway.Titles, key, forTitle, queue.Idle); cmd != nil {
				t.asked = true
				cmds = append(cmds, cmd)
			}
		}
		if !t.disk && !t.removed {
			// Whatever became of the server's read (out, failed): the
			// disk may hold the title.
			switch room := a.diskRoom(); {
			case room < 0:
				t.disk = true // no disk cache in this session
			case room > 0:
				a.diskRead(gateway.Titles, key, forTitle)
				t.disk = true
			}
		}
	}
	return tea.Batch(cmds...)
}

// titleFromDisk takes a title the disk cache brought into memory, and
// cancels the server's read of it: one still waiting is never sent.
func (a *app) titleFromDisk(t *title, k cache.Key) bool {
	if !a.titleFromCache(t, k) {
		return false
	}
	for _, w := range a.waiters {
		if w.purpose == forTitle && w.ticket.Key == k && !w.canceled {
			w.canceled = true
			w.cancel()
		}
	}
	return true
}

// titleFromCache takes the title from the memory entry of the ticket, if
// there is one that still reads, and says whether it did.
func (a *app) titleFromCache(t *title, k cache.Key) bool {
	e, ok := a.store.Get(k)
	if !ok {
		return false
	}
	record, ans := api.DecodeTicket(transport.Answer{Class: transport.Object, Payload: e.Payload, Status: 200, Sent: true}, gateway.DetailOptions(k))
	if !ans.OK() || gateway.CheckDetail(k, record) != nil {
		return false
	}
	t.known, t.failed, t.removed, t.text = true, false, false, record.Title
	return true
}

// receiveTitle takes the answer to a title's read: filed through the
// scope like any read (a removal there takes the title, receive); an
// accepted one gives its title from the answer, whether the cache keeps it
// or not (a ticket too large for it). A read that brought none has failed
// and is not repeated before R. A title the session learned meanwhile
// stays: the late answer fills only one not known.
func (m Model) receiveTitle(r gateway.TicketResult) (Model, tea.Cmd) {
	a := m.app
	d, ok := m.receive(r)
	if a == nil {
		return m, nil
	}
	t := a.titles[r.Key.Ticket]
	if t == nil || !t.asked {
		return m, nil
	}
	t.asked = false
	switch {
	case t.known:
	case ok && d == cache.Replace:
		t.known, t.failed, t.removed, t.text = true, false, false, r.Record.Title
	case t.removed || !a.titleFromCache(t, r.Key):
		// After a removal only a read accepted since may bring the title
		// back, nothing the cache held from before it.
		t.failed = true
	}
	return m, nil
}

// learnTitle renews the remembered title of a ticket from an accepted read
// of it (the list, the ticket): a title read earlier gives way to the
// newer one, a failed read and a removal are settled by it. A title read
// still out keeps it: its late answer fills only a title not known
// (receiveTitle). Tickets without a remembered title need nothing: the
// area takes theirs from the session.
func (a *app) learnTitle(ticket, text string) {
	if t := a.titles[ticket]; t != nil {
		t.known, t.failed, t.removed, t.text = true, false, false, text
	}
}

// forgetTitle follows an accepted removal of a ticket (not found, no
// access) from any read of it: its agent's row says #n again, older data
// (a list shown) brings no title back, and R may read it. The disk holds
// none of it any more.
func (a *app) forgetTitle(ticket string) {
	t := a.titles[ticket]
	if t == nil {
		t = &title{}
		a.titles[ticket] = t
	}
	t.known, t.text, t.removed, t.disk = false, "", true, true
	t.failed = !t.asked
}

// titlesRemoved follows an accepted removal: of one ticket, or of a
// project's tickets with their list.
func (a *app) titlesRemoved(k cache.Key) {
	switch k.Operation {
	case api.TicketSpec.Tool:
		a.forgetTitle(k.Ticket)
	case api.TicketsSpec.Tool:
		for _, ag := range a.agents.list {
			if ag.Project == k.Project {
				a.forgetTitle(ag.Ticket)
				// Every read of the ticket begun before the removal is
				// refused from now on, a title read among them, as the
				// scope does for a ticket's own removal.
				a.scope.Invalidate(a.detailKey(ag.Project, ag.Ticket))
			}
		}
	}
}

// titlesFailed: the title of an agent's ticket is missing because its
// read failed, so R would read it again; one the session learned since (the
// ticket in the pane, the list shown) or whose agent is gone counts not.
func (a *app) titlesFailed() bool {
	for _, ag := range a.agents.list {
		if t := a.titles[ag.Ticket]; t != nil && t.failed {
			if _, ok := a.agentTitle(ag); !ok {
				return true
			}
		}
	}
	return false
}

// retryTitles is R for the titles: the failed reads are asked again; a
// removal stays until a read succeeds.
func (a *app) retryTitles() tea.Cmd {
	for _, t := range a.titles {
		t.failed = false
	}
	return a.requestTitles()
}

// agentUnderCursor is the agent the area's cursor is on: the one it was
// put on, or the first in order when that one is gone; ok is false
// without an agent.
func (a *app) agentUnderCursor() (agents.Agent, bool) {
	list := a.agentsInOrder()
	for _, ag := range list {
		if ag.ID == a.agents.cursor {
			return ag, true
		}
	}
	if len(list) == 0 {
		return agents.Agent{}, false
	}
	return list[0], true
}

// moveAgent moves the area's cursor by rows; nothing is loaded by it.
func (a *app) moveAgent(delta int) {
	list := a.agentsInOrder()
	current, ok := a.agentUnderCursor()
	if !ok {
		return
	}
	at := 0
	for i, ag := range list {
		if ag.ID == current.ID {
			at = i
		}
	}
	a.agents.cursor = list[max(0, min(len(list)-1, at+delta))].ID
}

// agentsBindings are the keys of the agents' list: the cursor, Enter on
// an agent whose ticket can open, R while a title's read failed, Esc
// back (and A, which the help does not name twice).
func (m Model) agentsBindings() []binding {
	a := m.app
	var out []binding
	if len(a.agents.list) > 1 {
		out = append(out, binding{Up, uitext.UpDownKey, uitext.SelectHelp})
	}
	if ag, ok := a.agentUnderCursor(); ok && a.canOpenAgent(ag) {
		out = append(out, binding{OpenList, uitext.EnterKey, uitext.OpenTicketHelp})
	}
	if a.titlesFailed() {
		out = append(out, binding{Refresh, uitext.RefreshKey, uitext.RetryHelp})
	}
	return append(out, binding{Back, uitext.EscapeKey, uitext.BackHelp})
}

// canOpenAgent: the agent's ticket can open in its organisation's tickets.
func (a *app) canOpenAgent(ag agents.Agent) bool {
	return a.canOpen(ag.Organisation, sectionTickets) && a.ticketOn()
}

// openAgents is A: the agents' list, its cursor where it was (the first
// agent when that one is gone); in three columns the area under the
// organisations, in two in the list's place, in one alone.
func (m Model) openAgents() (Model, tea.Cmd) {
	a := m.app
	ag, ok := a.agentUnderCursor()
	if !ok {
		return m, nil
	}
	a.agents.cursor = ag.ID
	m.agentsFrom, m.focus = m.focus, view.Agents
	return m, m.selectionChanged()
}

// agentsFit: the agents' list would show at least one agent in the
// current terminal, so A does not lead into a screen too small to use.
func (m Model) agentsFit() bool {
	next := m
	next.agentsFrom, next.focus = m.focus, view.Agents
	return !next.small()
}

// closeAgents goes back to where A was pressed; to the list instead of an
// object pane that shows nothing any more (its ticket was removed
// meanwhile).
func (m Model) closeAgents() Model {
	if m.focus != view.Agents {
		return m
	}
	m.focus = m.agentsFrom
	if a := m.app; m.focus == view.Object && a.detail == nil && (a.active == nil || a.active.opened == "") {
		m.focus = view.List
	}
	return m
}

// agentTicket: the pane shows a ticket an agent opened (openAgent): the
// projects of its organisation are in the list's place, no ticket list.
func (a *app) agentTicket() bool {
	return a.detail != nil && a.projects != nil && a.tickets == nil && a.origin == nil
}

// openAgent is Enter on an agent: its ticket opens in the pane, in its
// organisation, the way a ticket of a notification does (ensureDetail):
// the tickets of its organisation with its project selected, or the
// ticket list of its project when that is the one shown and holds it.
// An agent that ended or is lost is seen after it: drawn faint once
// ended, recorded for the next run.
func (m Model) openAgent() (Model, tea.Cmd) {
	a := m.app
	ag, ok := a.agentUnderCursor()
	if !ok || !a.canOpenAgent(ag) {
		return m, nil
	}
	var cmds []tea.Cmd
	a.highlight, a.highlightSection = ag.Organisation, sectionTickets
	if v := a.tickets; v != nil && v.org == ag.Organisation && v.project == ag.Project && v.index(ag.Ticket) >= 0 {
		v.selected = ag.Ticket
		var cmd tea.Cmd
		m, cmd = m.openTicket()
		cmds = append(cmds, cmd)
	} else {
		if p := a.projects; p == nil || p.org != ag.Organisation {
			a.leaveInbox()
			a.leaveTickets()
			name := ""
			if o := a.organisation(ag.Organisation); o != nil {
				name = o.Name
			}
			a.projects = &projectsView{org: ag.Organisation, name: name, key: a.projectsKey(ag.Organisation)}
			cmds = append(cmds, a.loadProjects())
		} else if a.tickets != nil {
			a.leaveList()
		}
		a.projects.selected = ag.Project
		shown := a.detail != nil && a.detail.id == ag.Ticket
		d := a.ensureDetail(ag.Project, ag.Ticket)
		if !shown {
			// A ticket new in the pane starts at its head; one already
			// shown keeps its place and its selected comment.
			d.offset, d.follow, d.area, d.post = 0, anchorNone, areaDescription, ""
			a.settle(d)
		}
		m.focus = view.Object
		if !d.holds() || d.loading {
			d.loading = true
			cmds = append(cmds, a.issue(gateway.Detail, d.key, forDetail, queue.User))
		}
	}
	if ag.State == agents.Ended || ag.State == agents.Lost {
		cmds = append(cmds, a.markSeen(ag))
	}
	return m, tea.Batch(cmds...)
}

// markSeen records an agent as seen: at once for this run, on disk for
// the next (the watcher's own command, under the supervisor).
func (a *app) markSeen(ag agents.Agent) tea.Cmd {
	if a.agents.seen == nil {
		a.agents.seen = map[string]bool{}
	}
	a.agents.seen[ag.ID] = true
	if a.deps.Agents == nil {
		return nil
	}
	return a.deps.Agents.See(ag.ID, a.now())
}
