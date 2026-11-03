package ui

import (
	"strings"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/cache"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/queue"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

// K7a: a project's meetings, reached as tickets are: the chooser's entry,
// the projects, the list page by page with a search. Read only, memory
// only.

// With meetings.list every organisation offers Meetings after its
// tickets; without it there is no entry, and the rest stays as it was.
func TestMeetingsStandUnderEveryOrganisationWithTheirCapability(t *testing.T) {
	t.Parallel()

	h := newRESTHarness(t)
	h.start()
	var sections []section
	for _, e := range h.m.app.entries() {
		if e.org == orgA || e.org == orgB {
			sections = append(sections, e.section)
		}
	}
	want := []section{sectionInbox, sectionTickets, sectionMeetings, sectionInbox, sectionTickets, sectionMeetings}
	if len(sections) != len(want) {
		t.Fatalf("sections %v", sections)
	}
	for i := range want {
		if sections[i] != want[i] {
			t.Fatalf("sections %v, want %v", sections, want)
		}
	}
	if row := chooserRow(h.m, orgB, sectionMeetings); !strings.Contains(row.Primary.String(), "Meetings") {
		t.Fatalf("row %q", row.Primary.String())
	}
	if !h.m.app.canOpen(orgA, sectionMeetings) || h.m.app.canOpen("", sectionMeetings) {
		t.Fatal("Meetings opens without an organisation, or not with one")
	}

	// An older server: no entry, nothing opens, the tickets stay.
	old := newHarness(t)
	old.start()
	for _, e := range old.m.app.entries() {
		if e.section == sectionMeetings {
			t.Fatalf("an entry without the capability: %+v", e)
		}
	}
	if cmd := old.chooseMeetings(orgA); cmd != nil || old.m.app.projects != nil || len(old.f.meetingReads()) != 0 {
		t.Fatal("the meetings opened without the capability")
	}
	old.drive(old.chooseTickets(orgA))
	if old.m.app.projects == nil {
		t.Fatal("the tickets no longer open")
	}
}

// Picking a project shows its meetings newest first: date, title and
// duration on the row, the creator below; a meeting without a duration
// and one whose creator is gone are rows like the others.
func TestAProjectsMeetingsShowNewestFirstWithDateTitleAndDuration(t *testing.T) {
	t.Parallel()

	h := newRESTHarness(t)
	h.openMeetings(website)
	if reads := h.f.meetingReads(); len(reads) != 1 || reads[0] != (api.MeetingQuery{ProjectID: website, Page: 1, PerPage: gateway.MeetingPageSize}) {
		t.Fatalf("reads %+v", reads)
	}
	lines := h.meetingLines()
	if len(lines) != gateway.MeetingPageSize {
		t.Fatalf("%d rows", len(lines))
	}
	for i, want := range []string{
		"30 Jan 09:00  Meeting 1 | 0:31 | Anna Albers",
		"29 Jan 09:00  Meeting 2 |  | Anna Albers",
		"28 Jan 09:00  Meeting 3 | 0:33 | Creator unknown",
	} {
		if lines[i] != want {
			t.Errorf("row %d %q, want %q", i, lines[i], want)
		}
	}
	if got := h.m.context().String(); got != "Nordlicht GmbH › Meetings › Website-Relaunch · page 1 of 2" {
		t.Fatalf("path %q", got)
	}
	if got := h.m.listColumn().Title.String(); got != "Meetings" {
		t.Fatalf("title %q", got)
	}
	// The pane shows the selected row's facts, from the row alone.
	h.press("down")
	pane := h.paneText()
	for _, want := range []string{"Meeting 2", "29 Jan 09:00", "No duration", "Anna Albers"} {
		if !strings.Contains(pane, want) {
			t.Errorf("the pane lacks %q:\n%s", want, pane)
		}
	}
	if len(h.f.meetingReads()) != 1 {
		t.Fatal("moving the cursor read something")
	}
}

// Pages, search, R, Esc and the remembered position behave as they do
// for tickets.
func TestMeetingPagesSearchRefreshAndEscBehaveAsTickets(t *testing.T) {
	t.Parallel()

	h := newRESTHarness(t)
	h.openMeetings(website)
	if !h.offers(NextPage) || h.offers(PrevPage) || !h.offers(Search) || !h.offers(Refresh) {
		t.Fatal("page 1 does not offer ], / and R alone")
	}
	h.drive(h.press("]"))
	if lines := h.meetingLines(); len(lines) != 5 || !strings.Contains(lines[0], "Meeting 26") {
		t.Fatalf("page 2: %q", lines)
	}
	if h.offers(NextPage) || !h.offers(PrevPage) {
		t.Fatal("page 2 of 2 offers the wrong page keys")
	}
	h.press("down")
	h.press("down")
	if h.m.app.meetings.selected != meetingID(website, 28) {
		t.Fatalf("selected %q", h.m.app.meetings.selected)
	}
	// Esc to the projects, Enter again: the page and the row are where
	// they were, shown from memory at once, then read again.
	h.press("esc")
	if h.m.app.meetings != nil || h.m.app.projects == nil {
		t.Fatal("esc did not lead back to the projects")
	}
	reads := len(h.f.meetingReads())
	reload := h.press("enter")
	v := h.m.app.meetings
	if v == nil || v.page != 2 || v.selected != meetingID(website, 28) || !v.known || !v.cached {
		t.Fatalf("the position was not kept, or not shown from memory: %+v", v)
	}
	h.drive(reload)
	if got := len(h.f.meetingReads()); got <= reads {
		t.Fatal("the reopened page was not read again")
	}
	// R reads the page shown again.
	reads = len(h.f.meetingReads())
	h.drive(h.press("R"))
	if got := h.f.meetingReads(); len(got) != reads+1 || got[len(got)-1].Page != 2 {
		t.Fatalf("R read %+v", got[reads:])
	}
	// The search: page 1 of the matches; Esc in the line discards it.
	h.press("/")
	h.typeText("xyz")
	h.press("esc")
	if h.m.mode != Navigate || h.m.app.meetings.query != "" {
		t.Fatal("esc in the search line kept a search")
	}
	h.press("/")
	h.typeText("Meeting 2")
	h.drive(h.press("enter"))
	last := h.f.meetingReads()[len(h.f.meetingReads())-1]
	if last != (api.MeetingQuery{ProjectID: website, Q: "Meeting 2", Page: 1, PerPage: gateway.MeetingPageSize}) {
		t.Fatalf("the search sent %+v", last)
	}
	if lines := h.meetingLines(); len(lines) != 11 {
		t.Fatalf("the search shows %d rows", len(lines))
	}
	if got := h.m.context().String(); !strings.Contains(got, "Meeting 2") {
		t.Fatalf("the path does not name the search: %q", got)
	}
	// The search is remembered with the project, as a ticket search is.
	h.press("esc")
	h.press("enter")
	if h.m.app.meetings.query != "Meeting 2" {
		t.Fatal("the search was not remembered")
	}
	// Esc twice: the projects, then the chooser.
	h.press("esc")
	h.press("esc")
	if h.m.focus != view.Navigation {
		t.Fatalf("focus %v", h.m.focus)
	}
}

// An empty project, a reader without the meetings area and a search
// without a match each say so in text; none of them is an error.
func TestEmptyMeetingListsSayItInText(t *testing.T) {
	t.Parallel()

	for project, want := range map[string]string{intern: "No meetings.", archiv: "No meetings."} {
		h := newRESTHarness(t)
		h.openMeetings(project)
		c := h.m.listColumn()
		if c.Notice.String() != want || len(c.Rows) != 0 || h.m.app.meetings.err.String() != "" {
			t.Errorf("%s: notice %q, %d rows, error %q", project, c.Notice.String(), len(c.Rows), h.m.app.meetings.err.String())
		}
	}
	h := newRESTHarness(t)
	h.openMeetings(website)
	h.press("/")
	h.typeText("nothing like it")
	h.drive(h.press("enter"))
	if got := h.m.listColumn().Notice.String(); got != "No meetings match the search." {
		t.Fatalf("notice %q", got)
	}
}

// A project out of reach removes its meetings; the list says so and keeps
// working (Esc).
func TestAProjectOutOfReachRemovesItsMeetings(t *testing.T) {
	t.Parallel()

	h := newRESTHarness(t)
	h.openMeetings(website)
	h.f.setMeetings(func(api.MeetingQuery) transport.Answer { return restNotFound() })
	h.drive(h.press("R"))
	v := h.m.app.meetings
	if len(v.rows) != 0 || v.selected != "" || h.m.listColumn().Notice.String() == "" {
		t.Fatalf("%+v", v)
	}
	if _, ok := h.m.app.store.Get(v.key); ok {
		t.Fatal("the removed page stayed in memory")
	}
	h.press("esc")
	if h.m.app.projects == nil {
		t.Fatal("esc did not lead back")
	}
}

// A title is foreign: what it would do to a terminal is cleaned away
// where it is shown, on the row and in the pane.
func TestAMeetingTitleIsCleanedWhereItIsShown(t *testing.T) {
	t.Parallel()

	h := newRESTHarness(t)
	h.openMeetings(handbuch)
	row, pane := h.meetingLines()[0], h.paneText()
	for _, text := range []string{row, pane} {
		if strings.ContainsAny(text, "\x1b\u202e") || !strings.Contains(text, "Plan") || !strings.Contains(text, "red") {
			t.Errorf("not cleaned: %q", text)
		}
	}
}

// A result reaches the list only through the scope: a page no waiter of
// this view asked for (a late answer, a forged one) is not shown and not
// filed.
func TestAMeetingPageIsShownOnlyAfterTheScopeAcceptedIt(t *testing.T) {
	t.Parallel()

	h := newRESTHarness(t)
	h.openMeetings(website)
	v := h.m.app.meetings
	rows := v.rows
	forged := gateway.MeetingsResult{Ticket: h.m.app.scope.Begin(gateway.Meetings, v.key, queue.User)}
	forged.Page = api.MeetingPage{Data: []api.MeetingRow{{ID: "forged", ProjectID: website, Title: "Forged", OccurredAt: "2026-01-01T09:00:00Z"}}}
	forged.Page.Meta.Page, forged.Page.Meta.PerPage, forged.Page.Meta.Total = 1, gateway.MeetingPageSize, 1
	forged.Answer = objectAnswer(forged.Page)
	h.feed(forged)
	if len(v.rows) != len(rows) || v.rows[0].ID != rows[0].ID {
		t.Fatal("a page no waiter asked for was shown")
	}
	e, ok := h.m.app.store.Get(v.key)
	if !ok || strings.Contains(string(e.Payload), "forged") {
		t.Fatal("a page no waiter asked for was filed")
	}
}

// Meetings stay off the disk: a session that showed pages, a search and a
// removal leaves none of them there, while the tickets beside them are
// written; the next session reads them from the server again.
func TestNoMeetingReachesTheDisk(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	h := newDiskHarness(t, root, restFakes(recorded(t)))
	h.openMeetings(website)
	h.drive(h.press("]"))
	h.press("/")
	h.typeText("Meeting 1")
	h.drive(h.press("enter"))
	shown := []cache.Key{h.m.app.meetingsKey(website, "", 1), h.m.app.meetingsKey(website, "", 2), h.m.app.meetings.key}
	// The tickets of the same project, for a disk that is written to.
	h.press("esc")
	h.press("esc")
	h.drive(h.chooseTickets(orgA))
	h.drive(h.openProject(website))
	tickets := h.m.app.tickets.key
	h.drive(h.m.app.flushDisk())
	// The next session on the same disk shows nothing before the server
	// answers.
	next := h.restart(root)
	next.start()
	next.drive(next.chooseMeetings(orgA))
	next.m.focus = view.List
	next.m.app.projects.selected = website
	next.press("enter")
	if v := next.m.app.meetings; v == nil || v.known || len(v.rows) != 0 {
		t.Fatalf("a new session showed meetings before reading them: %+v", v)
	}
	next.press("q")
	c := diskOf(t, root, h.now)
	if !onDisk(t, c, tickets, h.now) {
		t.Fatal("the tickets were not written: this test proves nothing then")
	}
	for _, k := range shown {
		if onDisk(t, c, k, h.now) {
			t.Errorf("a meeting page is on disk: %+v", k)
		}
	}
}
