package ui

import (
	"strings"
	"time"

	"github.com/gboyke/lalabase-cli/internal/agents"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/features"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
	"github.com/gboyke/lalabase-cli/internal/uitext"
)

// presentation is the one mapping from the shell's content to display
// records (C1a). The session's mapping below takes the same boundary: API
// and cache values become present values here and nowhere else.
func presentation(organisation, list, object string) present.Screen {
	return present.Screen{
		Context: present.NewInline(uitext.Inline(uitext.Organisation).String() + ": " + organisation),
		Crumbs:  [3]present.Inline{uitext.Inline(uitext.Organisation), uitext.Inline(uitext.List), uitext.Inline(uitext.Object)},
		Chooser: present.Column{Title: uitext.Inline(uitext.Chooser), Text: uitext.Body(uitext.NoData), Cursor: -1},
		List:    present.Column{Title: uitext.Inline(uitext.List), Text: present.NewBody(list), Cursor: -1},
		Object:  present.Column{Title: uitext.Inline(uitext.Object), Text: present.NewBody(object), Cursor: -1},
	}
}

func placeholder() [3]string {
	return [3]string{uitext.Inline(uitext.NotLoaded).String(), uitext.Body(uitext.NoData).String(), uitext.Body(uitext.NoItem).String()}
}

// textOr is a foreign one-line value, cleaned, or the fallback when
// nothing readable is left of it: a name of spaces or of invisible
// characters alone is no name.
func textOr(raw string, fallback uitext.ID) string {
	text := present.NewInline(raw).String()
	if strings.TrimSpace(text) == "" {
		return uitext.Inline(fallback).String()
	}
	return text
}

// entry is one selectable row of the chooser: a section of all
// organisations (the inbox alone) or of one.
type entry struct {
	org     string
	section section
	name    string // the organisation's name, for its heading row
}

// entries are the chooser's selectable rows: the inbox of all organisations
// first, then per organisation, as the server listed them, the inbox and,
// with the list tool, the tickets.
func (a *app) entries() []entry {
	out := []entry{{org: "", section: sectionInbox, name: uitext.Inline(uitext.AllOrganisations).String()}}
	for _, o := range a.directory {
		name := textOr(o.Name, uitext.UnnamedOrganisation)
		out = append(out, entry{org: o.ID, section: sectionInbox, name: name})
		if a.ticketsOn() {
			out = append(out, entry{org: o.ID, section: sectionTickets, name: name})
		}
	}
	return out
}

func sectionName(s section) uitext.ID {
	if s == sectionTickets {
		return uitext.Tickets
	}
	return uitext.Inbox
}

// screen is the whole display of the current state.
func (m Model) screen() present.Screen {
	a := m.app
	if a == nil {
		s := presentation(m.shell[0], m.shell[1], m.shell[2])
		s.Help = m.help()
		return s
	}
	s := present.Screen{
		Crumbs:  m.crumbs(),
		Chooser: m.chooserColumn(),
		List:    m.listColumn(),
		Object:  m.objectColumn(),
		Agents:  m.agentsColumn(),
		Help:    m.help(),
	}
	s.Identity = m.identity()
	s.AgentCounts = a.agentSummary()
	s.Context = m.context()
	s.Status, s.StatusStyle = m.status()
	return s
}

// status is the notice of the second line, the most urgent first: an
// expired session, what became of a booking, a rate-limit pause, the disk
// cache, then of what the agents told (a removal, a seen mark) and a
// launch's outcome the newer; with its tone, from the same state. A
// booking's stands above what lasts: it has a deadline and no second
// telling, and nothing else says what became of the time. A plain
// one is a hint, which the view draws in the path's place.
func (m Model) status() (present.Inline, present.Style) {
	a := m.app
	booking, bookingTone, booked := a.bookStatus()
	switch {
	case a.expired && a.disk != nil && a.disk.clearFailed:
		return uitext.Inline(uitext.SessionExpiredDiskKept), present.NoticeProblem
	case a.expired:
		return uitext.Inline(uitext.SessionExpired), present.NoticeProblem
	case booked:
		return booking, bookingTone
	case a.pause.After(a.now()):
		return uitext.Fill(uitext.RateLimited, a.pause.In(a.loc).Format("15:04:05")), present.NoticeWaiting
	case a.diskStatus() != 0:
		return uitext.Inline(a.diskStatus()), present.NoticeProblem
	case a.agents.tooMany:
		return uitext.Inline(uitext.AgentsTooMany), present.NoticeProblem
	case a.groupNotice.until.After(a.now()):
		return a.groupNotice.text, a.groupNotice.tone
	case a.agentsNoticeShown():
		// Of the agents' notice and the launch's the newer shows: a
		// launch's start and its answer clear the agents' notice.
		return a.agents.notice, a.agents.tone
	case a.launchShown(m.focus == view.Agents):
		return a.launch.notice, a.launch.tone
	}
	return present.Inline{}, present.Plain
}

func (m Model) identity() present.Inline {
	a := m.app
	switch {
	case a.expired:
		return uitext.Inline(uitext.SessionExpired)
	case a.steps[stepMe].state == ready:
		write := uitext.CanWriteUnknown
		if a.me.Token.ReadOnly != nil {
			write = uitext.CanWriteYes
			if *a.me.Token.ReadOnly {
				write = uitext.CanWriteNo
			}
		}
		return present.NewInline(textOr(a.me.User.DisplayName, uitext.UnknownUser) + " | " + uitext.Inline(write).String())
	case a.steps[stepMe].state == failed:
		return a.steps[stepMe].err
	}
	return uitext.Inline(uitext.VerifyingIdentity)
}

// crumbs are the names of the three panes for the breadcrumb of one
// column: the section and the kind of object shown.
func (m Model) crumbs() [3]present.Inline {
	a := m.app
	if a.projects != nil && !a.expired {
		return [3]present.Inline{uitext.Inline(uitext.Organisations), uitext.Inline(uitext.Tickets), uitext.Inline(uitext.Ticket)}
	}
	if a.origin != nil && !a.expired {
		return [3]present.Inline{uitext.Inline(uitext.Organisations), uitext.Inline(uitext.Inbox), uitext.Inline(uitext.Ticket)}
	}
	return [3]present.Inline{uitext.Inline(uitext.Organisations), uitext.Inline(uitext.Inbox), uitext.Inline(uitext.Details)}
}

// orgName is the name of an organisation for a heading: from the
// directory, else the name it had when its view was opened.
func (a *app) orgName(org, opened string) string {
	if org == "" {
		return uitext.Inline(uitext.AllOrganisations).String()
	}
	name := textOr(opened, uitext.UnnamedOrganisation)
	if o := a.organisation(org); o != nil {
		name = textOr(o.Name, uitext.UnnamedOrganisation)
	}
	return name
}

// context is the path to where the person is: the open view's
// organisation (by the name it had when it was opened, when the directory
// lost it), the section and the project as places, the search and the page
// as their details.
func (m Model) context() present.Inline {
	a := m.app
	if a.expired {
		return uitext.Inline(uitext.Organisations)
	}
	step, detail := uitext.Inline(uitext.PathStep).String(), uitext.Inline(uitext.PathDetail).String()
	if v := a.active; v != nil {
		text := a.orgName(v.org, v.name) + step + uitext.Inline(uitext.Inbox).String()
		if v.known {
			text += detail + uitext.Fill(uitext.PageOf, v.page, lastPage(v.total, v.perPage)).String()
		}
		return present.NewInline(text)
	}
	if p := a.projects; p != nil {
		text := a.orgName(p.org, p.name) + step + uitext.Inline(uitext.Tickets).String()
		if v := a.tickets; v != nil {
			text += step + textOr(v.projectName, uitext.UnnamedProject)
			if v.query != "" {
				text += detail + uitext.Fill(uitext.SearchFor, v.query).String()
			}
			// Grouped, every group names its own pages.
			if v.known && !v.isGrouped() {
				text += detail + uitext.Fill(uitext.PageOf, v.page, lastPage(v.total, v.perPage)).String()
			}
		}
		return present.NewInline(text)
	}
	return uitext.Inline(uitext.Organisations)
}

// chooserColumn is the directory with its counters; its notice is the
// start's state, the most important thing first.
func (m Model) chooserColumn() present.Column {
	a := m.app
	c := present.Column{Title: uitext.Inline(uitext.Organisations), Cursor: -1}
	if a.expired {
		c.Rows = []present.Row{}
		return c
	}
	c.Notice = m.chooserNotice()
	if a.steps[stepMe].state != ready {
		c.Rows = []present.Row{}
		return c
	}
	// Per organisation a heading row (not selectable) and its sections,
	// indented; the unread count sits on the inbox row.
	c.Rows = []present.Row{}
	heading := ""
	first := true
	// While an agent exists, every row reserves the two cells of an
	// agent's glyph (C9b), so the sections stay indented under their
	// heading; an organisation's heading holds its most urgent agent's,
	// "All organisations" and the sections hold none.
	marked := len(a.agents.list) > 0
	for _, e := range a.entries() {
		if first || e.org != heading {
			row := present.Row{Primary: present.NewInline(e.name), Marked: marked}
			if ag, ok := a.mostUrgent(func(ag agents.Agent) bool { return e.org != "" && ag.Organisation == e.org }); ok {
				l := a.look(ag.State)
				row.Mark, row.MarkStyle = present.NewInline(l.glyph), l.style
			}
			c.Rows = append(c.Rows, row)
			heading, first = e.org, false
		}
		if e.org == a.highlight && e.section == a.highlightSection {
			c.Cursor = len(c.Rows)
		}
		row := present.Row{Primary: present.NewInline("  " + uitext.Inline(sectionName(e.section)).String()), Marked: marked}
		if e.section == sectionInbox {
			cnt := a.countOf(e.org)
			row.Trailing = uitext.Inline(uitext.UnknownCount)
			if cnt.known {
				row.Trailing = uitext.Fill(uitext.CountValue, cnt.value)
				// Nothing unread is quiet; unread asks for attention.
				row.Quiet = cnt.value == 0
				if cnt.value > 0 {
					row.TrailingStyle = present.Strong
				}
			}
			row.Active = a.active != nil && a.active.org == e.org
		} else {
			row.Active = a.projects != nil && a.projects.org == e.org
		}
		c.Rows = append(c.Rows, row)
	}
	return c
}

func (m Model) chooserNotice() present.Inline {
	a := m.app
	for _, i := range []stepIndex{stepMe, stepInitialize, stepTools, stepOrganisations} {
		if a.steps[i].state == failed {
			return a.steps[i].err
		}
	}
	switch {
	case a.steps[stepMe].state != ready:
		return uitext.Inline(uitext.VerifyingIdentity)
	case a.steps[stepOrganisations].state == loading && a.report == nil:
		return uitext.Inline(uitext.LoadingOrganisations)
	case a.report == nil:
		return uitext.Inline(uitext.ReadingCapabilities)
	case !a.inboxOn():
		return uitext.Inline(uitext.InboxUnsupported)
	case !a.ticketsOn():
		return uitext.Inline(uitext.TicketsUnsupported)
	case !a.byOrgOn():
		return uitext.Inline(uitext.FilterUnavailable)
	case a.steps[stepCounters].state == failed:
		return a.steps[stepCounters].err
	case a.steps[stepOrganisations].state == loading:
		return uitext.Inline(uitext.LoadingOrganisations)
	case a.steps[stepOrganisations].state == ready && len(a.directory) == 0:
		return uitext.Inline(uitext.NoOrganisations)
	case a.report.BelowTested:
		return uitext.Fill(uitext.BelowTested, a.server.Version, features.MinTestedServerVersion)
	}
	return present.Inline{}
}

// listColumn is the list pane: the inbox page, the projects or the ticket
// page, whichever is open; the notice says what the data is (loading,
// cached, stale, empty) or what went wrong.
func (m Model) listColumn() present.Column {
	a := m.app
	if a.expired {
		return present.Column{Title: uitext.Inline(uitext.Inbox), Cursor: -1, Text: uitext.Body(uitext.NoData)}
	}
	switch {
	case a.active != nil:
		return m.inboxColumn()
	case a.tickets != nil:
		return m.ticketsColumn()
	case a.projects != nil:
		return m.projectsColumn()
	}
	return present.Column{Title: uitext.Inline(uitext.Inbox), Cursor: -1, Text: uitext.Body(uitext.NoData)}
}

// inboxColumn is the active inbox page: rows of two lines.
func (m Model) inboxColumn() present.Column {
	v := m.app.active
	c := present.Column{Title: uitext.Inline(uitext.Inbox), Cursor: -1}
	empty := uitext.NoneOnPage
	if v.total == 0 {
		empty = uitext.NoUnread
	}
	c.Notice = v.notice(empty, len(v.rows) == 0)
	c.Rows = make([]present.Row, 0, len(v.rows))
	for i, n := range v.rows {
		if n.ID == v.selected {
			c.Cursor = i
		}
		// The word at the end of a row names what became of marking it
		// (C1f), by the row's id: an answer for one row never sits on
		// another.
		c.Rows = append(c.Rows, present.Row{Primary: rowPrimary(n), Secondary: m.app.rowSecondary(n), Trailing: m.app.markShort(n.ID)})
	}
	c.Offset = v.offset
	return c
}

// rowPrimary: the ticket number, if any, and the message.
func rowPrimary(n api.Notification) present.Inline {
	text := messageOf(n)
	if n.Ticket != nil {
		text = uitext.Fill(uitext.TicketNumber, n.Ticket.Number).String() + " " + text
	}
	return present.NewInline(text)
}

// rowSecondary: the actor, the project and the time of the last event.
func (a *app) rowSecondary(n api.Notification) present.Inline {
	return present.NewInline(actorOf(n) + " · " + projectOf(n) + " · " + a.when(n.LastEventAt))
}

func messageOf(n api.Notification) string { return textOr(n.Message, uitext.NoMessage) }

// messageBody is the message of the details: a body, cleaned once as one,
// its lines kept; the row's one-line form is messageOf.
func messageBody(n api.Notification) string {
	text := present.NewBody(n.Message).String()
	if strings.TrimSpace(text) == "" {
		return uitext.Inline(uitext.NoMessage).String()
	}
	return text
}

func actorOf(n api.Notification) string {
	if n.Actor == nil {
		return uitext.Inline(uitext.UnknownActor).String()
	}
	return textOr(n.Actor.Name, uitext.UnknownActor)
}

func projectOf(n api.Notification) string {
	if n.Project == nil {
		return uitext.Inline(uitext.NoProject).String()
	}
	return textOr(n.Project.Name, uitext.UnnamedProject)
}

func ticketOf(n api.Notification) string {
	if n.Ticket == nil {
		return uitext.Inline(uitext.NoTicket).String()
	}
	return uitext.Fill(uitext.TicketNumber, n.Ticket.Number).String() + " " + textOr(n.Ticket.Title, uitext.UntitledTicket)
}

// timeOf shows a server time in the person's zone, to the minute: day and
// month, the year only when it is not the current one there. A time that
// does not read says so.
func timeOf(raw string, now time.Time, loc *time.Location) string {
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return uitext.Inline(uitext.TimeUnknown).String()
	}
	t = t.In(loc)
	if t.Year() == now.In(loc).Year() {
		return t.Format("2 Jan 15:04")
	}
	return t.Format("2 Jan 2006 15:04")
}

// when is a server time as timeOf shows it, now and here.
func (a *app) when(raw string) string { return timeOf(raw, a.now(), a.loc) }

// objectColumn is the object pane: the opened notification, or the ticket
// previewed or opened.
func (m Model) objectColumn() present.Column {
	a := m.app
	if (a.projects != nil || a.origin != nil) && !a.expired {
		return m.detailColumn()
	}
	c := present.Column{Title: uitext.Inline(uitext.Details), Cursor: -1}
	v := a.active
	if v == nil || a.expired || v.opened == "" {
		c.Text = uitext.Body(uitext.NoItem)
		return c
	}
	i := v.index(v.opened)
	if i < 0 {
		c.Text = uitext.Body(uitext.NoItem)
		return c
	}
	n := v.rows[i]
	label := func(id uitext.ID, value string) string { return uitext.Inline(id).String() + ": " + value }
	state := uitext.StateUnread
	if n.Read {
		state = uitext.StateRead
	}
	lines := []string{
		messageBody(n),
		"",
		label(uitext.LabelActor, actorOf(n)),
		label(uitext.LabelOrganisation, textOr(n.Organisation.Name, uitext.UnnamedOrganisation)),
		label(uitext.LabelProject, projectOf(n)),
		label(uitext.LabelTicket, ticketOf(n)),
		label(uitext.LabelEvent, n.EventType),
		label(uitext.LabelNotification, n.ID),
		label(uitext.LabelState, uitext.Inline(state).String()),
		label(uitext.LabelLastEvent, a.when(n.LastEventAt)),
	}
	if text := a.markText(n.ID, !n.Read); text != "" {
		lines = append(lines, label(uitext.LabelMarkRead, text))
	}
	c.Text = present.NewBody(strings.Join(lines, "\n"))
	c.Offset = v.detailOffset
	return c
}
