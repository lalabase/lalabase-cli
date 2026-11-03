package ui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

// K7a: a project's meetings in the harness. Website has 30 (two pages),
// newest first, #2 without a duration and #3 by a creator who is gone;
// Intern none; Handbuch three, one with a title that tries to reach the
// terminal; Archiv the empty list of a reader without the meetings area.
// Another project is out of reach.

func (f *fakes) MeetingsRESTAnswer(ctx context.Context, q api.MeetingQuery) transport.Answer {
	f.mu.Lock()
	f.meetingQueries = append(f.meetingQueries, q)
	answer, held := f.meetings, f.gateMeetings
	f.mu.Unlock()
	if !f.hold(ctx, held) {
		return transport.Answer{Class: transport.Canceled, Message: "canceled"}
	}
	return answer(q)
}

func (f *fakes) meetingReads() []api.MeetingQuery {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.meetingQueries)
}

func (f *fakes) setMeetings(fn func(q api.MeetingQuery) transport.Answer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.meetings = fn
}

// hostileTitle is a meeting title with a terminal sequence in it.
const hostileTitle = "Plan \x1b[31mred\x1b[0m \u202eevil"

// meetingID is the id of meeting n of a project.
func meetingID(project string, n int) string {
	return idOf(700000 + 1000*slices.Index([]string{website, intern, handbuch, archiv}, project) + n)
}

// meetingRows are a project's meetings, newest first: meeting n took
// place n days before the 31st of January 2026.
func meetingRows(project string) []api.MeetingRow {
	count := map[string]int{website: 30, handbuch: 3}[project]
	rows := make([]api.MeetingRow, 0, count)
	for n := 1; n <= count; n++ {
		r := api.MeetingRow{
			ID: meetingID(project, n), ProjectID: project, Title: fmt.Sprintf("Meeting %d", n),
			OccurredAt: time.Date(2026, 1, 31-n, 9, 0, 0, 0, time.UTC).Format(time.RFC3339),
		}
		minutes, name := 30+n, "Anna Albers"
		r.DurationMinutes, r.Creator.ID, r.Creator.Name = &minutes, idOf(11), &name
		switch n {
		case 2:
			r.DurationMinutes = nil
		case 3:
			r.Creator.Name = nil
		}
		if project == handbuch && n == 1 {
			r.Title = hostileTitle
		}
		rows = append(rows, r)
	}
	return rows
}

// defaultMeetings answers a page of a project's meetings, with its search
// on the titles; a project the harness does not know is out of reach.
func defaultMeetings(q api.MeetingQuery) transport.Answer {
	if !slices.Contains([]string{website, intern, handbuch, archiv}, q.ProjectID) {
		return restNotFound()
	}
	rows := meetingRows(q.ProjectID)
	if q.Q != "" {
		rows = slices.DeleteFunc(rows, func(r api.MeetingRow) bool {
			return !strings.Contains(strings.ToLower(r.Title), strings.ToLower(q.Q))
		})
	}
	page, per := max(1, q.Page), q.PerPage
	if per == 0 {
		per = 10
	}
	from := min(len(rows), (page-1)*per)
	return objectAnswer(map[string]any{
		"data": rows[from:min(len(rows), from+per)],
		"meta": map[string]any{"page": page, "per_page": per, "total": len(rows)},
	})
}

// chooseMeetings highlights the meetings entry of an organisation and
// opens it: the projects list.
func (h *harness) chooseMeetings(org string) tea.Cmd {
	h.t.Helper()
	h.m.focus = view.Navigation
	h.m.app.highlight, h.m.app.highlightSection = org, sectionMeetings
	return h.press("enter")
}

// openMeetings runs the start of a session whose server lists the
// meetings and opens the meetings of a project of A.
func (h *harness) openMeetings(project string) {
	h.t.Helper()
	h.start()
	h.drive(h.chooseMeetings(orgA))
	h.drive(h.openProject(project))
}

// meetingLines are the rows of the meetings list as text: primary,
// trailing and secondary, one line each.
func (h *harness) meetingLines() []string {
	var out []string
	for _, r := range h.m.listColumn().Rows {
		out = append(out, r.Primary.String()+" | "+r.Trailing.String()+" | "+r.Secondary.String())
	}
	return out
}
