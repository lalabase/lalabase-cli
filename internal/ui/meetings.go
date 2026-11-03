package ui

import (
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

// The meetings section (K7a): the projects of one organisation, as the
// tickets section has them (projectsView, opened for this section), and
// the meetings of one project page by page with a search. Read only: no
// meeting is opened yet, the object pane shows the selected row's facts.
// The memory keeps the pages for this session alone: the store never
// journals them (cache.WithMemoryOnly in startup.go), and nothing here
// asks the disk.
//
// It copies the shape of the ticket list on purpose instead of sharing an
// abstraction with it: the ticket's state is too specific to generalise
// from one second user (the plan's decision 6).

// meetingPosition is where the person was in a project's meetings.
type meetingPosition struct {
	page            int
	selected, query string
}

// meetingsView is one page of one project's meetings, with its search.
type meetingsView struct {
	org, project, projectName string
	// query is the search sent; entry the search line while it is typed.
	query, entry   string
	page           int
	key            cache.Key
	rows           []api.MeetingRow
	total, perPage int
	status
	selected  string
	offset    int
	corrected bool
}

func (v *meetingsView) index(id string) int {
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

func (v *meetingsView) reselect() {
	if v.index(v.selected) < 0 {
		v.selected = ""
		if len(v.rows) > 0 {
			v.selected = v.rows[0].ID
		}
	}
}

func (v *meetingsView) moveSelection(delta int) {
	if len(v.rows) == 0 {
		return
	}
	at := max(0, v.index(v.selected))
	v.selected = v.rows[max(0, min(len(v.rows)-1, at+delta))].ID
}

func (a *app) meetingsOn() bool { return a.on("meetings") }

// meetingsKey is the read of a page of a project's meetings.
func (a *app) meetingsKey(project, query string, page int) cache.Key {
	k := a.ns.Key(api.MeetingsRESTSpec.Name)
	k.Project, k.Query, k.Page, k.PerPage = project, query, page, gateway.MeetingPageSize
	return k
}

// meetingsSection: the projects in the list's place are the meetings'.
func (a *app) meetingsSection() bool {
	return a.projects != nil && a.projects.section == sectionMeetings
}

// --- leaving and entering ----------------------------------------------------

// leaveMeetings closes the meeting list, remembering its position and
// canceling its reads.
func (a *app) leaveMeetings() {
	if v := a.meetings; v != nil {
		a.rememberMeetings()
		a.cancelArea(gateway.Meetings)
		a.meetings = nil
	}
}

func (a *app) rememberMeetings() {
	if v := a.meetings; v != nil {
		a.rememberedMeetings[v.project] = meetingPosition{page: v.page, selected: v.selected, query: v.query}
	}
}

// openMeetings opens the meetings of the selected project: the remembered
// page, row and search, from memory at once when the page is there, then
// one reload.
func (m Model) openMeetings(project string) (Model, tea.Cmd) {
	a := m.app
	p := a.projects
	if p == nil || p.index(project) < 0 || !a.meetingsOn() {
		return m, nil
	}
	a.leaveMeetings()
	pos := a.rememberedMeetings[project]
	v := &meetingsView{org: p.org, project: project, projectName: p.rows[p.index(project)].Name, query: pos.query, page: max(1, pos.page), selected: pos.selected}
	v.key = a.meetingsKey(project, v.query, v.page)
	a.meetings = v
	m.focus = view.List
	return m, a.loadMeetings()
}

// loadMeetings shows the page from memory, if it is there and still
// reads, and starts exactly one reload of the person's priority. The disk
// is never asked.
func (a *app) loadMeetings() tea.Cmd {
	v := a.meetings
	if e, ok := a.store.Get(v.key); ok {
		page, ans := api.DecodeMeetingsREST(transport.Answer{Class: transport.Object, Payload: e.Payload, Status: 200, Sent: true})
		if ans.OK() && gateway.CheckMeetingsPage(v.key, page) == nil {
			v.rows, v.total, v.perPage = page.Data, page.Meta.Total, page.Meta.PerPage
			v.fromCache(e)
			v.reselect()
		} else {
			a.forget(v.key)
		}
	}
	v.loading = true
	return a.issue(gateway.Meetings, v.key, forMeetings, queue.User)
}

// turnMeetingPage moves to the previous or next server page, when there
// is one and no load is pending.
func (m Model) turnMeetingPage(delta int) (Model, tea.Cmd) {
	v := m.app.meetings
	if v == nil || v.loading {
		return m, nil
	}
	target := v.page + delta
	if target < 1 || (delta > 0 && (!v.known || target > lastPage(v.total, v.perPage))) {
		return m, nil
	}
	return m.showMeetingPage(target, v.query)
}

// showMeetingPage replaces the page shown: another page, or page 1 of
// another search.
func (m Model) showMeetingPage(page int, query string) (Model, tea.Cmd) {
	a := m.app
	v := a.meetings
	a.cancelArea(gateway.Meetings)
	v.page, v.query, v.key = page, query, a.meetingsKey(v.project, query, page)
	v.rows, v.status = nil, status{}
	v.selected, v.offset, v.corrected = "", 0, false
	a.rememberMeetings()
	m.focus = view.List
	return m, a.loadMeetings()
}

// --- the search line ---------------------------------------------------------

// startMeetingSearch opens the search line with the current search in it.
func (m Model) startMeetingSearch() Model {
	if v := m.app.meetings; v != nil && m.focus == view.List {
		v.entry = v.query
		m.mode = TextEntry
	}
	return m
}

// meetingSearchKey is a key while the search line is open, as searchKey is
// for tickets: Enter sends the search once (a blank one clears it, the
// same one does nothing), Esc discards what was typed.
func (m Model) meetingSearchKey(kind Action, text string) (Model, tea.Cmd) {
	v := m.app.meetings
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
	case Submit:
		query := strings.TrimSpace(v.entry)
		v.entry = ""
		m.mode = Navigate
		if query != v.query {
			return m.showMeetingPage(1, query)
		}
	}
	return m, nil
}

// --- refresh -----------------------------------------------------------------

// refreshMeetings is R in the meetings section: the page, else the
// projects; not again while a read of it is out.
func (m Model) refreshMeetings() tea.Cmd {
	a := m.app
	if v := a.meetings; v != nil {
		v.corrected = false
		v.loading = true
		if a.pending(v.key) {
			return nil
		}
		return a.issue(gateway.Meetings, v.key, forMeetings, queue.User)
	}
	return m.refreshSection()
}

// canRefreshMeetings mirrors refreshMeetings.
func (m Model) canRefreshMeetings() bool {
	a := m.app
	if v := a.meetings; v != nil {
		return !a.pending(v.key)
	}
	return m.canRefreshSection()
}

// --- results -----------------------------------------------------------------

// receiveMeetings applies a page through the one way any result takes
// (receive: the waiter, the scope, the filing).
func (m Model) receiveMeetings(r gateway.MeetingsResult) (Model, tea.Cmd) {
	a := m.app
	d, ok := m.receive(r)
	if !ok {
		return m, nil
	}
	v := a.meetings
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
		return m.correctMeetingPage()
	case cache.Remove:
		// The project is gone or out of reach: every page goes, and the
		// search line with it.
		if m.mode == TextEntry {
			m.mode, v.entry = Navigate, ""
		}
		v.rows, v.total, v.selected = nil, 0, ""
		v.removed()
	default:
		v.failed(d, r.Answer)
	}
	return m, nil
}

// correctMeetingPage follows a refresh that showed the page beyond the
// last one, as the ticket list does: once to the new last page, a second
// shrink stops and offers R, a total of zero shows an empty page 1.
func (m Model) correctMeetingPage() (Model, tea.Cmd) {
	a := m.app
	v := a.meetings
	last := lastPage(v.total, v.perPage)
	if v.page <= last {
		return m, nil
	}
	a.store.Apply(v.key, cache.Remove, nil, a.now())
	a.scope.Invalidate(v.key)
	v.rows, v.selected, v.offset = nil, "", 0
	if v.total == 0 {
		v.page, v.key.Page = 1, 1
		a.rememberMeetings()
		return m, nil
	}
	if v.corrected {
		v.err = uitext.Inline(uitext.ListShrank)
		return m, nil
	}
	v.corrected = true
	a.cancelArea(gateway.Meetings)
	v.page, v.key.Page = last, last
	v.known, v.loading = false, true
	a.rememberMeetings()
	return m, a.issue(gateway.Meetings, v.key, forMeetings, queue.User)
}

// --- the view ----------------------------------------------------------------

// meetingsColumn is the page of meetings: the date and the title, the
// duration at the end, the creator below. Every text is foreign and
// cleaned here (present).
func (m Model) meetingsColumn() present.Column {
	a := m.app
	v := a.meetings
	c := present.Column{Title: uitext.Inline(uitext.Meetings), Cursor: -1}
	if m.mode == TextEntry {
		c.Notice = present.NewInline(uitext.Inline(uitext.SearchLine).String() + v.entry + uitext.Inline(uitext.Caret).String())
	} else {
		empty := uitext.NoMeetings
		switch {
		case v.total > 0:
			empty = uitext.NoneOnMeetingPage
		case v.query != "":
			empty = uitext.NoMeetingMatches
		}
		c.Notice = v.notice(empty, len(v.rows) == 0)
	}
	c.Rows = make([]present.Row, 0, len(v.rows))
	for i, r := range v.rows {
		if r.ID == v.selected {
			c.Cursor = i
		}
		c.Rows = append(c.Rows, present.Row{
			Primary:   present.NewInline(a.when(r.OccurredAt) + "  " + textOr(r.Title, uitext.UntitledMeeting)),
			Trailing:  present.NewInline(durationOf(r)),
			Secondary: present.NewInline(creatorOf(r)),
		})
	}
	c.Offset = v.offset
	return c
}

// meetingFactsColumn is the object pane in the meetings section: the
// facts of the selected row, from the row alone (K7b reads the meeting).
func (m Model) meetingFactsColumn() present.Column {
	a := m.app
	c := present.Column{Title: uitext.Inline(uitext.Meeting), Cursor: -1}
	v := a.meetings
	if v == nil || v.index(v.selected) < 0 {
		c.Text = uitext.Body(uitext.NoItem)
		return c
	}
	r := v.rows[v.index(v.selected)]
	label := func(id uitext.ID, value string) string { return uitext.Inline(id).String() + ": " + value }
	duration := durationOf(r)
	if duration == "" {
		duration = uitext.Inline(uitext.NoDuration).String()
	}
	c.Text = present.NewBody(strings.Join([]string{
		label(uitext.LabelTitle, textOr(r.Title, uitext.UntitledMeeting)),
		label(uitext.LabelDate, a.when(r.OccurredAt)),
		label(uitext.LabelDuration, duration),
		label(uitext.LabelCreator, creatorOf(r)),
		label(uitext.LabelProject, textOr(v.projectName, uitext.UnnamedProject)),
	}, "\n"))
	return c
}

// durationOf is a meeting's duration as hours and minutes, "" for none.
func durationOf(r api.MeetingRow) string {
	if r.DurationMinutes == nil {
		return ""
	}
	return hoursMinutes(*r.DurationMinutes)
}

// creatorOf is the name of the meeting's creator, cleaned; a creator who
// is gone has none.
func creatorOf(r api.MeetingRow) string {
	if r.Creator.Name == nil {
		return uitext.Inline(uitext.UnknownCreator).String()
	}
	return textOr(*r.Creator.Name, uitext.UnknownCreator)
}
