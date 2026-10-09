package ui

import (
	"strings"
	"time"

	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/features"
	"github.com/gboyke/lalabase-cli/internal/present"
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

// entry is one row of the chooser: all organisations, or one.
type entry struct {
	org  string
	name string
}

// entries are the chooser's rows: all organisations first, then the
// directory as the server listed it.
func (a *app) entries() []entry {
	out := []entry{{org: "", name: uitext.Inline(uitext.AllOrganisations).String()}}
	for _, o := range a.directory {
		out = append(out, entry{org: o.ID, name: textOr(o.Name, uitext.UnnamedOrganisation)})
	}
	return out
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
		Crumbs:  [3]present.Inline{uitext.Inline(uitext.Organisations), uitext.Inline(uitext.Inbox), uitext.Inline(uitext.Details)},
		Chooser: m.chooserColumn(),
		List:    m.listColumn(),
		Object:  m.objectColumn(),
		Help:    m.help(),
	}
	s.Identity = m.identity()
	s.Context = m.context()
	switch {
	case a.expired:
		s.Status = uitext.Inline(uitext.SessionExpired)
	case a.pause.After(a.now()):
		s.Status = uitext.Fill(uitext.RateLimited, a.pause.UTC().Format("15:04:05"))
	}
	return s
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

// context names where the person is: the active inbox's organisation (by
// the name it had when it was opened, when the directory lost it), the
// section and the page.
func (m Model) context() present.Inline {
	a := m.app
	v := a.active
	if v == nil || a.expired {
		return uitext.Inline(uitext.Organisations)
	}
	name := uitext.Inline(uitext.AllOrganisations).String()
	if v.org != "" {
		name = textOr(v.name, uitext.UnnamedOrganisation)
		if o := a.organisation(v.org); o != nil {
			name = textOr(o.Name, uitext.UnnamedOrganisation)
		}
	}
	text := name + " | " + uitext.Inline(uitext.Inbox).String()
	if v.known {
		text += " | " + uitext.Fill(uitext.PageOf, v.page, lastPage(v.total, v.perPage)).String()
	}
	return present.NewInline(text)
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
	entries := a.entries()
	c.Rows = make([]present.Row, 0, len(entries))
	for i, e := range entries {
		if e.org == a.highlight {
			c.Cursor = i
		}
		cnt := a.countOf(e.org)
		trailing := uitext.Inline(uitext.UnknownCount)
		if cnt.known {
			trailing = uitext.Fill(uitext.CountValue, cnt.value)
		}
		c.Rows = append(c.Rows, present.Row{
			Primary:  present.NewInline(e.name),
			Trailing: trailing,
			Active:   a.active != nil && a.active.org == e.org,
		})
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

// listColumn is the active inbox page: rows of two lines, the notice says
// what the page is (loading, cached, stale, empty) or what went wrong.
func (m Model) listColumn() present.Column {
	a := m.app
	c := present.Column{Title: uitext.Inline(uitext.Inbox), Cursor: -1}
	v := a.active
	if v == nil || a.expired {
		c.Text = uitext.Body(uitext.NoData)
		return c
	}
	c.Notice = listNotice(v)
	c.Rows = make([]present.Row, 0, len(v.rows))
	for i, n := range v.rows {
		if n.ID == v.selected {
			c.Cursor = i
		}
		c.Rows = append(c.Rows, present.Row{Primary: rowPrimary(n), Secondary: rowSecondary(n)})
	}
	c.Offset = v.offset
	return c
}

func listNotice(v *inboxView) present.Inline {
	switch {
	case v.err.String() != "":
		return v.err
	// A cached page says so while its one reload runs; "Refreshing" is for
	// a page the server gave.
	case v.cached && v.stale:
		return uitext.Inline(uitext.CachedStale)
	case v.cached:
		return uitext.Inline(uitext.Cached)
	case v.loading && !v.known:
		return uitext.Inline(uitext.Loading)
	case v.loading:
		return uitext.Inline(uitext.Refreshing)
	case v.stale:
		return uitext.Inline(uitext.PossiblyStale)
	case v.known && len(v.rows) == 0 && v.total == 0:
		return uitext.Inline(uitext.NoUnread)
	case v.known && len(v.rows) == 0:
		return uitext.Inline(uitext.NoneOnPage)
	}
	return present.Inline{}
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
func rowSecondary(n api.Notification) present.Inline {
	return present.NewInline(actorOf(n) + " | " + projectOf(n) + " | " + timeOf(n.LastEventAt, false))
}

func messageOf(n api.Notification) string { return textOr(n.Message, uitext.NoMessage) }

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

// timeOf shows a server time in UTC: minutes in a row, seconds in the
// details. A time that does not read says so.
func timeOf(raw string, seconds bool) string {
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return uitext.Inline(uitext.TimeUnknown).String()
	}
	layout := "2006-01-02 15:04"
	if seconds {
		layout = "2006-01-02 15:04:05"
	}
	return t.UTC().Format(layout) + " " + uitext.Inline(uitext.UTC).String()
}

// objectColumn is the opened notification, from the row's own data: the
// message as a body, the fields one per line.
func (m Model) objectColumn() present.Column {
	a := m.app
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
		messageOf(n),
		"",
		label(uitext.LabelActor, actorOf(n)),
		label(uitext.LabelOrganisation, textOr(n.Organisation.Name, uitext.UnnamedOrganisation)),
		label(uitext.LabelProject, projectOf(n)),
		label(uitext.LabelTicket, ticketOf(n)),
		label(uitext.LabelEvent, n.EventType),
		label(uitext.LabelNotification, n.ID),
		label(uitext.LabelState, uitext.Inline(state).String()),
		label(uitext.LabelLastEvent, timeOf(n.LastEventAt, true)),
	}
	c.Text = present.NewBody(strings.Join(lines, "\n"))
	c.Offset = v.detailOffset
	return c
}
