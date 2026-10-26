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

// title is the read of one agent's ticket for its title: out, failed
// (kept until R), or known.
type title struct {
	asked, failed, known bool
	text                 string
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
	for i, ag := range list {
		if m.focus == view.Agents && ag.ID == a.agents.cursor {
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

// seen: the person opened the agent once, recorded on disk (C9b).
func (a *app) seen(ag agents.Agent) bool { return ag.Seen }

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
// else from the server through the gateway in the background (the disk
// asked beside, C1e). A title that was asked for, failed or is known is
// not asked again; R frees the failed ones (retryTitles).
func (a *app) requestTitles() tea.Cmd {
	if a.expired || a.scope == nil || !a.ticketOn() {
		return nil
	}
	var cmds []tea.Cmd
	for _, ag := range a.agents.list {
		if _, ok := a.agentTitle(ag); ok || a.titles[ag.Ticket] != nil {
			continue
		}
		t := &title{}
		a.titles[ag.Ticket] = t
		key := a.detailKey(ag.Project, ag.Ticket)
		if a.titleFromCache(t, key) {
			continue
		}
		cmd := a.issue(gateway.Titles, key, forTitle, queue.Background)
		if cmd == nil {
			// Nothing was asked: the next call tries again.
			delete(a.titles, ag.Ticket)
			continue
		}
		a.diskRead(gateway.Titles, key, forTitle)
		t.asked = true
		cmds = append(cmds, cmd)
	}
	return tea.Batch(cmds...)
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
	t.known, t.failed, t.text = true, false, record.Title
	return true
}

// receiveTitle takes the answer to a title's read: filed through the
// scope like any read, the title from the cache then; a read that brought
// none has failed and is not repeated before R.
func (m Model) receiveTitle(r gateway.TicketResult) (Model, tea.Cmd) {
	a := m.app
	m.receive(r)
	if a == nil {
		return m, nil
	}
	t := a.titles[r.Key.Ticket]
	if t == nil || !t.asked {
		return m, nil
	}
	t.asked = false
	if !t.known && !a.titleFromCache(t, r.Key) {
		t.failed = true
	}
	return m, nil
}

// titlesFailed: a title's read failed, and R would read it again.
func (a *app) titlesFailed() bool {
	for _, t := range a.titles {
		if t.failed {
			return true
		}
	}
	return false
}

// retryTitles is R for the titles: the failed reads are forgotten and
// asked again.
func (a *app) retryTitles() tea.Cmd {
	for id, t := range a.titles {
		if t.failed {
			delete(a.titles, id)
		}
	}
	return a.requestTitles()
}
