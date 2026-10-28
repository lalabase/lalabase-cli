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
// session holds (the newest accepted read of it, the ticket in the pane,
// the list shown, the cache), and a title it lacks is read once through
// the gateway, as idle work (queue.Idle): after every preview, so the
// area never slows what the person reads.

// title is what the session knows of one agent's ticket for its title.
//
// The one rule: a known title or a removal is the newest fact an accepted
// read brought, ordered by the request's flight (queue.Flight, numbered
// when it is sent, across all reads), and it outranks what the ticket in
// the pane or the list shown still holds. A fact from an older request
// never replaces a newer one; the cache's and the disk's (flight 0) only
// fill a title nothing is known of.
//
// The order comes from the flight and not from the order answers arrive
// in: a list read sent before a rename can answer after the rename's
// ticket read, and must not bring the old title back.
//
// The rule holds for every ticket an agent has, whether a title read of it
// was made or not: an accepted read of such a ticket makes its entry
// (learnTitle, forgetTitle). Only three functions change the fact:
// learnTitle and forgetTitle by flight, titleFromCache while there is none.
type title struct {
	// fact is what the newest accepted read said of the ticket, and text
	// its title when the fact is one.
	fact titleFact
	text string
	// flight is the request that brought the fact; 0 for the cache and
	// the disk, and while there is none.
	flight uint64
	// asked: a read of the title is out; there is at most one.
	asked bool
	// failed: the last read brought no title; it is not asked again
	// before R (retryTitles), so a failing server is not read in a loop.
	failed bool
	// disk: the disk cache was asked for the title, once per entry.
	disk bool
	// project is the ticket's project, so that a removal of the
	// project's list finds the titles of its tickets (titlesRemoved).
	project string
}

// titleFact is what an accepted read said of a ticket.
type titleFact int

const (
	// noFact: nothing yet; the views' data shows, else a read is made.
	noFact titleFact = iota
	// titled: the ticket has the title text.
	titled
	// removed: the server removed the ticket (not found, no access); its
	// agent's row shows the number alone.
	removed
)

// learn takes a title as the newest fact, brought by flight.
func (t *title) learn(text string, flight uint64) {
	t.fact = titled
	t.text = text
	t.failed = false
	t.flight = flight
}

// forget takes a removal of the ticket as the newest fact, brought by
// flight.
func (t *title) forget(flight uint64) {
	t.fact = removed
	t.text = ""
	t.flight = flight
}

// outranks says whether the fact remembered came from a request sent
// after flight: then a fact flight brought changes nothing.
func (t *title) outranks(flight uint64) bool { return flight < t.flight }

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
		l := a.look(ag.State)
		row := present.Row{
			Primary: present.NewInline(text), Marked: true,
			Mark: present.NewInline(l.glyph), MarkStyle: l.style,
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
// holds: the newest accepted fact remembered of it (title), else the
// ticket in the pane, else a row of the list shown. ok is false while
// none of them has it, and after a removal.
func (a *app) agentTitle(ag agents.Agent) (string, bool) {
	if t := a.titles[ag.Ticket]; t != nil {
		// The newest accepted fact outranks the views' data (title).
		switch t.fact {
		case removed:
			return "", false
		case titled:
			return t.text, true
		}
	}
	if d := a.detail; d != nil && d.known && d.record.ID == ag.Ticket {
		return d.record.Title, true
	}
	if v := a.tickets; v != nil {
		if i := v.index(ag.Ticket); i >= 0 {
			return v.rows[i].Title, true
		}
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
			t = &title{project: ag.Project}
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
		if !t.disk && t.fact != removed {
			// Whatever became of the server's read (out, failed): the
			// disk may hold the title.
			room, ok := a.diskRoom()
			switch {
			case !ok:
				t.disk = true // this session has no disk cache
			case room > 0:
				a.diskRead(gateway.Titles, key, forTitle)
				t.disk = true
			}
			// No room: the disk is not open yet, or other reads wait
			// for it; the next reading of the agents asks again.
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
// there is one that still reads, and says whether it did. It only fills a
// title nothing is known of: the cache has no flight, so a title or a
// removal an accepted read brought always outranks it.
func (a *app) titleFromCache(t *title, k cache.Key) bool {
	if t.fact != noFact {
		return false
	}
	e, ok := a.store.Get(k)
	if !ok {
		return false
	}
	record, ans := api.DecodeTicket(transport.Answer{Class: transport.Object, Payload: e.Payload, Status: 200, Sent: true}, gateway.DetailOptions(k))
	if !ans.OK() || gateway.CheckDetail(k, record) != nil {
		return false
	}
	t.learn(record.Title, 0)
	return true
}

// receiveTitle takes the answer to a title's read: filed through the
// scope like any read (a removal there takes the title, receive); an
// accepted one gives its title from the answer, whether the cache keeps it
// or not (a ticket too large for it), unless the session learned a newer
// fact meanwhile (learnTitle). A read that brought none has failed and is
// not repeated before R; after a removal nothing from the cache counts.
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
	if ok && d == cache.Replace {
		a.learnTitle(r.Key.Ticket, r.Record.Title, uint64(r.Flight))
		return m, nil
	}
	// Not accepted (a failure, a refusal, a removal, kept stale): the
	// cache may still hold the title; a title known meanwhile stays.
	if t.fact != titled && !a.titleFromCache(t, r.Key) {
		t.failed = true
	}
	return m, nil
}

// learnTitle takes the title of an accepted read of a ticket (its title
// read, the ticket, its full text, its list), sent as flight: it renews a
// remembered title, settles a failed read and lifts a removal, unless the
// fact it holds came from a request sent later. A ticket an agent has gets
// its entry here if it had none: else the views' order would decide (the
// ticket in the pane before the list), not the newest read. A ticket no
// agent has needs nothing.
func (a *app) learnTitle(ticket, text string, flight uint64) {
	t := a.titles[ticket]
	if t == nil {
		ag, ok := a.agentAt(ticket)
		if !ok {
			return
		}
		t = &title{project: ag.Project}
		a.titles[ticket] = t
	}
	if t.outranks(flight) {
		return
	}
	t.learn(text, flight)
}

// forgetTitle follows an accepted removal of a ticket (not found, no
// access), sent as flight: its agent's row says #n again, whatever the
// views still hold, until a read sent later succeeds; R may read it once
// no read of it is out. A removal from a request older than the fact
// remembered changes nothing.
func (a *app) forgetTitle(ticket string, flight uint64, project string) {
	t := a.titles[ticket]
	if t == nil {
		t = &title{project: project}
		a.titles[ticket] = t
	}
	if t.outranks(flight) {
		return
	}
	t.forget(flight)
	// Failed, so that R offers to read it again; while a read of it is
	// out, that read's answer settles it (receiveTitle).
	t.failed = !t.asked
}

// titlesRemoved follows an accepted removal, sent as flight: of one ticket,
// or of a project's list, which takes the titles of its tickets the agents
// show and of those remembered (an agent missing from one read of the
// agents comes back), and refuses every read of them begun before, as the
// scope does for a ticket's own removal. A ticket whose remembered fact
// came from a request sent after the list's is left alone, its reads too:
// the newer read saw it, and a title read begun since must not be refused.
func (a *app) titlesRemoved(k cache.Key, flight uint64) {
	switch k.Operation {
	case api.TicketSpec.Tool:
		a.forgetTitle(k.Ticket, flight, k.Project)
	case api.TicketsSpec.Tool:
		tickets := map[string]bool{}
		for _, ag := range a.agents.list {
			if ag.Project == k.Project {
				tickets[ag.Ticket] = true
			}
		}
		for id, t := range a.titles {
			if t.project == k.Project {
				tickets[id] = true
			}
		}
		for id := range tickets {
			if t := a.titles[id]; t != nil && t.outranks(flight) {
				continue
			}
			a.forgetTitle(id, flight, k.Project)
			a.scope.Invalidate(a.detailKey(k.Project, id))
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
	if m.canRefresh() {
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
// current terminal, so A does not lead into a screen too small to use. It
// asks small of the model as A would leave it, with the help that model
// shows, since the help's lines take from the list's room (bindings).
func (m Model) agentsFit() bool {
	next := m
	next.agentsFrom, next.focus = m.focus, view.Agents
	return !next.small()
}

// closeAgents goes back to where A was pressed; to the list instead of an
// object pane that shows nothing any more (its ticket was removed
// meanwhile). The focus it returns to gets its preview again, which the
// agents' list held back (selectionChanged): every way out of the list
// needs both, so they are one step.
func (m Model) closeAgents() (Model, tea.Cmd) {
	if m.focus != view.Agents {
		return m, nil
	}
	m.focus = m.agentsFrom
	if a := m.app; m.focus == view.Object && a.detail == nil && (a.active == nil || a.active.opened == "") {
		m.focus = view.List
	}
	return m, m.selectionChanged()
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
		if d := a.detail; d != nil && d.id == ag.Ticket && v.opened == ag.Ticket {
			// Already open: it keeps its place and selection, and is
			// read again as on any open when it is not fresh.
			m.focus = view.Object
			cmds = append(cmds, a.readUnlessFresh(d))
		} else {
			var cmd tea.Cmd
			m, cmd = m.openTicket()
			cmds = append(cmds, cmd)
		}
	} else {
		if p := a.projects; p == nil || p.org != ag.Organisation {
			cmds = append(cmds, a.enterProjects(ag.Organisation))
		} else if a.tickets != nil {
			a.leaveList()
		}
		a.projects.selected = ag.Project
		shown := a.detail != nil && a.detail.id == ag.Ticket
		d := a.ensureDetail(ag.Project, ag.Ticket)
		if !shown {
			// A ticket new in the pane starts at its head; one already
			// shown keeps its place and its selected comment.
			d.startAtHead()
			a.settle(d)
		}
		m.focus = view.Object
		cmds = append(cmds, a.readUnlessFresh(d))
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
