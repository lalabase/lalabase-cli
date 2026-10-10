package ui

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/cache"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/queue"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

// C1c, the tickets section: lazy projects, paged ticket lists, a search
// sent on Enter, a preview after a pause, an explicit open and back, the
// ticket with its description and the first page of comments and
// attachments.

// Nothing of the projects or the tickets is read at the start or by moving
// the cursor; the five startup reads stay what they were.
func TestProjectsAndTicketsAreReadOnlyWhenOpened(t *testing.T) {
	h := newHarness(t)
	h.start()
	if h.f.meCalls != 1 || h.f.initCall != 1 || h.f.toolCall != 1 || h.f.orgCalls != 1 || len(h.f.inboxCalls()) != 1 {
		t.Fatal("the start changed")
	}
	if len(h.f.projectReads()) != 0 || len(h.f.ticketListReads()) != 0 || len(h.f.detailReads()) != 0 {
		t.Fatal("the start read projects or tickets")
	}
	h.m.focus = view.Navigation
	for range 6 {
		if cmd := h.press("down"); cmd != nil {
			t.Fatal("highlighting issued a command")
		}
	}
	a := h.m.app
	if a.highlight != orgB || a.highlightSection != sectionTickets {
		t.Fatalf("highlight %q section %d", a.highlight, a.highlightSection)
	}
	if len(h.f.projectReads()) != 0 || len(h.f.ticketListReads()) != 0 {
		t.Fatal("highlighting read something")
	}
	h.drive(h.press("enter"))
	if got := h.f.projectReads(); !slices.Equal(got, []string{orgB}) {
		t.Fatalf("project reads %v", got)
	}
	if a.projects == nil || a.projects.org != orgB || !a.projects.known || len(a.projects.rows) != 0 || a.active != nil {
		t.Fatalf("projects %+v, inbox %v", a.projects, a.active != nil)
	}
	if got := h.m.screen().List.Notice.String(); got != "No projects." {
		t.Fatalf("notice %q", got)
	}
	if got := h.m.screen().Context.String(); got != "Südwind AG | Tickets" {
		t.Fatalf("context %q", got)
	}
}

// The chooser lists, per organisation, its inbox (with the count) and its
// tickets; the cursor moves over those entries only. Enter opens the
// projects of the organisation, Enter on a project its tickets: page 1 of
// 25, without a search.
func TestTheChooserHasSectionsAndEnterOpensProjectsThenTickets(t *testing.T) {
	h := newHarness(t)
	h.start()
	rows := h.m.screen().Chooser.Rows
	var seen []string
	for _, r := range rows {
		seen = append(seen, r.Primary.String()+"|"+r.Trailing.String())
	}
	want := []string{"All organisations|", "  Inbox|33", "Nordlicht GmbH|", "  Inbox|30", "  Tickets|", "Südwind AG|", "  Inbox|3", "  Tickets|"}
	if !slices.Equal(seen, want) {
		t.Fatalf("chooser %v", seen)
	}
	if h.m.screen().Chooser.Cursor != 1 {
		t.Fatalf("cursor %d", h.m.screen().Chooser.Cursor)
	}
	h.m.focus = view.Navigation
	h.press("down")
	h.press("down")
	if h.m.screen().Chooser.Cursor != 4 || h.m.app.highlight != orgA || h.m.app.highlightSection != sectionTickets {
		t.Fatalf("cursor %d on %q/%d", h.m.screen().Chooser.Cursor, h.m.app.highlight, h.m.app.highlightSection)
	}
	h.drive(h.press("enter"))
	a := h.m.app
	s := h.m.screen()
	if a.projects == nil || len(a.projects.rows) != 4 || h.m.focus != view.List || s.List.Title.String() != "Projects" {
		t.Fatalf("projects %+v focus %d title %q", a.projects, h.m.focus, s.List.Title.String())
	}
	if s.List.Rows[3].Primary.String() != "Website-Relaunch" || s.List.Cursor != 0 || a.projects.selected != archiv {
		t.Fatalf("rows %+v cursor %d selected %q", s.List.Rows, s.List.Cursor, a.projects.selected)
	}
	if !s.Chooser.Rows[4].Active || s.Chooser.Rows[3].Active {
		t.Fatal("the open section is not the bold one")
	}
	if len(h.f.ticketListReads()) != 0 {
		t.Fatal("opening the projects read tickets")
	}
	h.press("down")
	h.press("down")
	h.press("down")
	if a.projects.selected != website {
		t.Fatalf("selected %q", a.projects.selected)
	}
	h.drive(h.press("enter"))
	if got := h.f.ticketListReads(); len(got) != 1 || got[0] != (api.TicketQuery{ProjectID: website, Page: 1, PerPage: 25}) {
		t.Fatalf("ticket reads %+v", got)
	}
	tv := a.tickets
	if tv == nil || len(tv.rows) != 25 || tv.total != 30 || tv.selected != login || h.m.focus != view.List {
		t.Fatalf("tickets %+v focus %d", tv, h.m.focus)
	}
	s = h.m.screen()
	if s.List.Title.String() != "Tickets" || s.Context.String() != "Nordlicht GmbH | Tickets | Website-Relaunch | page 1 of 2" {
		t.Fatalf("title %q context %q", s.List.Title.String(), s.Context.String())
	}
	if s.List.Rows[0].Primary.String() != "#1 Login bricht ab" || s.List.Rows[0].Trailing.String() != "in_progress" || s.List.Rows[0].Secondary.String() != "Anna Albers | 2026-01-05 09:26 UTC" {
		t.Fatalf("row 0 %+v", s.List.Rows[0])
	}
	if s.List.Rows[2].Secondary.String() != "Unassigned | 2026-01-05 09:26 UTC" || s.List.Rows[3].Trailing.String() != "No status" {
		t.Fatalf("rows 2, 3: %+v %+v", s.List.Rows[2], s.List.Rows[3])
	}
	if len(h.f.detailReads()) != 0 {
		t.Fatal("opening the list read a ticket")
	}
	// Esc: back to the projects, then to the chooser; the sections are
	// named in the crumbs of one column.
	h.press("esc")
	if a.tickets != nil || a.projects == nil || h.m.focus != view.List {
		t.Fatalf("after esc: tickets %v projects %v focus %d", a.tickets != nil, a.projects != nil, h.m.focus)
	}
	h.press("esc")
	if h.m.focus != view.Navigation || a.projects == nil {
		t.Fatalf("after the second esc: focus %d projects %v", h.m.focus, a.projects != nil)
	}
	h.m.width = 79
	if got := ansi.Strip(h.m.View().Content); !strings.Contains(got, "Organisations") {
		t.Fatalf("one column:\n%s", got)
	}
	h.m.focus = view.List
	if got := ansi.Strip(h.m.View().Content); !strings.Contains(got, "Organisations > Tickets") {
		t.Fatalf("one column crumbs:\n%s", got)
	}
}

// The search is one line: every printable key is text, nothing is sent
// while typing, Esc discards, Enter sends q once and shows page 1 of the
// result; an empty search sent clears the filter.
func TestTheSearchSendsOnEnterAndEscDiscards(t *testing.T) {
	h := newHarness(t)
	h.openTickets(website)
	a := h.m.app
	before := len(h.f.ticketListReads())
	if cmd := h.press("/"); cmd != nil || h.m.mode != TextEntry {
		t.Fatalf("/: cmd %v mode %d", cmd != nil, h.m.mode)
	}
	h.typeText("Exq")
	if h.m.mode != TextEntry || a.tickets.entry != "Exq" || len(h.f.ticketListReads()) != before {
		t.Fatalf("typing: mode %d entry %q reads %d", h.m.mode, a.tickets.entry, len(h.f.ticketListReads())-before)
	}
	if got := h.m.screen().List.Notice.String(); got != "Search: Exq_" {
		t.Fatalf("notice %q", got)
	}
	h.press("backspace")
	if a.tickets.entry != "Ex" {
		t.Fatalf("after backspace %q", a.tickets.entry)
	}
	help := helpText(h.m)
	for _, want := range []string{"enter search", "esc cancel", "backspace delete", "ctrl+c interrupt"} {
		if !strings.Contains(help, want) {
			t.Errorf("help lacks %q: %q", want, help)
		}
	}
	if strings.Contains(help, "quit") {
		t.Fatalf("q offered as quit while typing: %q", help)
	}
	// Esc closes the line and sends nothing; the command it returns is the
	// preview timer of the row, armed again.
	h.drive(h.press("esc"))
	if h.m.mode != Navigate || a.tickets.entry != "" || a.tickets.query != "" || len(h.f.ticketListReads()) != before || len(h.f.detailReads()) != 0 {
		t.Fatalf("esc: mode %d entry %q query %q reads %d", h.m.mode, a.tickets.entry, a.tickets.query, len(h.f.ticketListReads())-before)
	}
	if len(a.tickets.rows) != 25 {
		t.Fatal("esc changed the list")
	}
	h.press("/")
	h.typeText("export")
	h.drive(h.press("enter"))
	reads := h.f.ticketListReads()
	if len(reads) != before+1 || reads[before] != (api.TicketQuery{ProjectID: website, Q: "export", Page: 1, PerPage: 25}) {
		t.Fatalf("reads %+v", reads[before:])
	}
	tv := a.tickets
	if h.m.mode != Navigate || tv.query != "export" || tv.page != 1 || len(tv.rows) != 1 || tv.rows[0].Number != 2 || tv.selected != tv.rows[0].ID {
		t.Fatalf("result: mode %d query %q page %d rows %d", h.m.mode, tv.query, tv.page, len(tv.rows))
	}
	if got := h.m.screen().Context.String(); got != `Nordlicht GmbH | Tickets | Website-Relaunch | search "export" | page 1 of 1` {
		t.Fatalf("context %q", got)
	}
	// The search line starts with the current search; Enter with the same
	// text sends nothing new. An emptied line clears the filter.
	h.press("/")
	if tv.entry != "export" {
		t.Fatalf("the line does not start with the search: %q", tv.entry)
	}
	h.drive(h.press("enter"))
	if h.m.mode != Navigate || len(h.f.ticketListReads()) != before+1 {
		t.Fatal("the same search was sent again")
	}
	h.press("/")
	for range 6 {
		h.press("backspace")
	}
	h.drive(h.press("enter"))
	reads = h.f.ticketListReads()
	if len(reads) != before+2 || reads[before+1] != (api.TicketQuery{ProjectID: website, Page: 1, PerPage: 25}) || tv.query != "" || len(tv.rows) != 25 {
		t.Fatalf("clearing: reads %+v query %q rows %d", reads[before:], tv.query, len(tv.rows))
	}
	// A search of blanks is no search. The entry is cleaned on screen.
	h.press("/")
	h.typeText("  ")
	h.drive(h.press("enter"))
	if len(h.f.ticketListReads()) != before+2 {
		t.Fatal("blanks were sent")
	}
	// Keys that navigate elsewhere are text or nothing here: q, R, [ and
	// the arrows change nothing of the list.
	h.press("/")
	for _, key := range []string{"q", "R", "[", "]", "left", "right", "up", "down", "pgup", "pgdown"} {
		if cmd := h.press(key); cmd != nil || h.m.mode != TextEntry || h.m.focus != view.List || tv.page != 1 {
			t.Fatalf("%s acted while typing", key)
		}
	}
	if tv.entry != "qR[]" {
		t.Fatalf("entry %q", tv.entry)
	}
	h.press("esc")
}

// A preview is read after the pause on the row the cursor rests on, as
// background work, and only when the object pane is visible; moving on
// before the pause cancels it; the result shows in the object pane.
func TestAPreviewIsReadAfterThePauseWhenTheObjectPaneIsVisible(t *testing.T) {
	h := newHarness(t)
	h.openTickets(website)
	a := h.m.app
	if a.previewToken == 0 || a.detail != nil {
		t.Fatalf("no timer after the list opened: token %d detail %v", a.previewToken, a.detail != nil)
	}
	if len(h.f.detailReads()) != 0 {
		t.Fatal("a ticket was read before the pause")
	}
	token := a.previewToken
	h.press("down") // the cursor moves on before the pause: a new token
	if a.previewToken == token {
		t.Fatal("moving the cursor kept the token")
	}
	h.feed(gateway.Due{Session: a.session, Token: token})
	if len(h.f.detailReads()) != 0 || a.detail != nil {
		t.Fatal("an old timer read a ticket")
	}
	cmds := h.feed(gateway.Due{Session: a.session, Token: a.previewToken})
	second := a.tickets.rows[1].ID
	if a.detail == nil || a.detail.id != second || !a.detail.loading {
		t.Fatalf("detail %+v", a.detail)
	}
	for _, w := range a.waiters {
		if w.ticket.Area == gateway.Detail && w.ticket.Priority != queue.Background {
			t.Fatalf("the preview is not background work: %+v", w.ticket)
		}
	}
	if got := h.m.screen().Object.Notice.String(); got != "Loading…" {
		t.Fatalf("notice %q", got)
	}
	h.drive(cmds...)
	reads := h.f.detailReads()
	if len(reads) != 1 || reads[0] != (ticketRead{project: website, id: second, o: api.TicketOptions{PostsPage: 1, PostsPerPage: 25, Newest: true, AttachmentsPage: 1}}) {
		t.Fatalf("reads %+v", reads)
	}
	d := a.detail
	if !d.known || d.loading || d.record.Number != 2 || h.m.focus != view.List || a.tickets.opened != "" {
		t.Fatalf("after the preview: %+v focus %d opened %q", d, h.m.focus, a.tickets.opened)
	}
	s := h.m.screen()
	if s.Object.Title.String() != "Ticket" || s.Object.Notice.String() != "" {
		t.Fatalf("title %q notice %q", s.Object.Title.String(), s.Object.Notice.String())
	}
	text := s.Object.Text.String()
	for _, want := range []string{"#2 Export als CSV", "Status: No status", "Assignees: Unassigned", "Creator: Unknown user", "Project: Website-Relaunch", "No description", "No comments", "No attachments"} {
		if !strings.Contains(text, want) {
			t.Errorf("the pane lacks %q:\n%s", want, text)
		}
	}
	// Resting on the same row again asks nothing: the ticket is fresh.
	if cmd := h.feed(gateway.Due{Session: a.session, Token: a.previewToken}); len(cmd) != 0 || len(h.f.detailReads()) != 1 {
		t.Fatal("a fresh ticket was read again")
	}
	// The object pane hidden (one column, the list focused): no timer, no
	// read; it comes back when the pane shows.
	h.feed(tea.WindowSizeMsg{Width: 79, Height: 24})
	token = a.previewToken
	h.press("down")
	if a.previewToken != token || a.detail != nil {
		t.Fatalf("a timer without the object pane: token %d detail %v", a.previewToken, a.detail != nil)
	}
	h.press("up")
	h.press("down") // the same row: still nothing
	h.feed(gateway.Due{Session: a.session, Token: a.previewToken})
	if len(h.f.detailReads()) != 1 {
		t.Fatal("a read without the object pane")
	}
	cmds = h.feed(tea.WindowSizeMsg{Width: 80, Height: 24})
	if a.previewToken == token {
		t.Fatal("the pane showing started no timer")
	}
	h.drive(cmds...)
	h.drive(h.feed(gateway.Due{Session: a.session, Token: a.previewToken})...)
	if reads := h.f.detailReads(); len(reads) != 2 || reads[1].id != a.tickets.rows[2].ID {
		t.Fatalf("reads %+v", reads)
	}
}

// Enter opens the ticket: a pending preview is joined as the person's
// read (one request, both results whole), a fresh one is shown without a
// request, and Esc goes back to the list keeping the pane.
func TestOpenJoinsAPendingPreviewAndEscGoesBack(t *testing.T) {
	h := newHarness(t)
	h.openTickets(website)
	a := h.m.app
	gate := make(chan struct{})
	h.f.gate, h.f.gateTicket = gate, login
	preview := h.feed(gateway.Due{Session: a.session, Token: a.previewToken})
	if len(preview) != 1 {
		t.Fatalf("%d preview commands", len(preview))
	}
	previewResult := h.background(preview[0])
	var share string
	for _, w := range a.waiters {
		if w.ticket.Area == gateway.Detail {
			share = w.ticket.ShareKey()
		}
	}
	h.waitFor("the preview on the wire", func() bool { return h.q.Sharers(share) == 1 })
	open := h.press("enter")
	if a.tickets.opened != login || h.m.focus != view.Object || !a.detail.loading {
		t.Fatalf("open: opened %q focus %d loading %v", a.tickets.opened, h.m.focus, a.detail.loading)
	}
	users := 0
	for _, w := range a.waiters {
		if w.ticket.Area == gateway.Detail && w.ticket.Priority == queue.User {
			users++
		}
	}
	if users != 1 {
		t.Fatalf("%d waiters of the person", users)
	}
	openResult := h.background(open)
	h.waitFor("the open joining the preview", func() bool { return h.q.Sharers(share) == 2 })
	close(gate)
	r1, r2 := h.receive(previewResult).(gateway.TicketResult), h.receive(openResult).(gateway.TicketResult)
	if r1.Flight == 0 || r1.Flight != r2.Flight || len(h.f.detailReads()) != 1 {
		t.Fatalf("flights %d %d, %d reads", r1.Flight, r2.Flight, len(h.f.detailReads()))
	}
	if r1.Record.ID != login || r2.Record.ID != login || len(r1.Record.Posts) != 2 || len(r2.Record.Posts) != 2 {
		t.Fatalf("results %+v / %+v", r1.Record, r2.Record)
	}
	h.feed(r1, r2)
	d := a.detail
	if !d.known || d.loading || d.record.ID != login || h.m.focus != view.Object {
		t.Fatalf("after the results: %+v focus %d", d, h.m.focus)
	}
	text := h.m.screen().Object.Text.String()
	for _, want := range []string{"#1 Login bricht ab", "Status: in_progress", "Nach dem Absenden bleibt die Seite weiß.", "Comments · newest first · page 1 of 1 · 2 readable comments", "Anna Albers · 2026-01-05 09:26:00 UTC", "Ich schaue es mir an.", "Ben Berger · 2026-01-05 09:25:00 UTC", "Tritt nur in Safari auf.", "Attachments · page 1 of 1 · 2 in all", "- design.pdf", "- screenshot.png"} {
		if !strings.Contains(text, want) {
			t.Errorf("the pane lacks %q:\n%s", want, text)
		}
	}
	if strings.Index(text, "Ich schaue") > strings.Index(text, "Tritt nur") {
		t.Fatal("the comments are not newest first")
	}
	help := helpText(h.m)
	if !strings.Contains(help, "esc back") {
		t.Fatalf("help %q", help)
	}
	// Esc: back to the list; the pane keeps the ticket as a preview.
	if cmd := h.press("esc"); cmd != nil || a.tickets.opened != "" || h.m.focus != view.List || a.detail == nil || a.detail.id != login {
		t.Fatalf("esc: opened %q focus %d detail %v", a.tickets.opened, h.m.focus, a.detail != nil)
	}
	// Enter on a fresh ticket asks nothing.
	if cmd := h.press("enter"); cmd != nil || len(h.f.detailReads()) != 1 || h.m.focus != view.Object || a.tickets.opened != login {
		t.Fatal("opening a fresh ticket asked the server")
	}
	h.press("esc")
	// Enter before any preview reads as the person, once.
	h.f.gate = nil
	h.press("down")
	cmd := h.press("enter")
	if a.detail == nil || a.detail.id != a.tickets.rows[1].ID || !a.detail.loading {
		t.Fatalf("open without a preview: %+v", a.detail)
	}
	h.drive(cmd)
	if reads := h.f.detailReads(); len(reads) != 2 || reads[1].id != a.tickets.rows[1].ID {
		t.Fatalf("reads %+v", reads)
	}
	// The timer that was set by the move does nothing now: the ticket is
	// fresh and opened.
	if cmds := h.feed(gateway.Due{Session: a.session, Token: a.previewToken}); len(cmds) != 0 || len(h.f.detailReads()) != 2 {
		t.Fatal("the preview read an opened ticket again")
	}
	// Moving the cursor with a ticket opened previews nothing; the opened
	// ticket stays.
	h.m.focus = view.List
	token := a.previewToken
	h.press("down")
	if a.previewToken != token || a.detail.id != a.tickets.rows[1].ID {
		t.Fatal("the cursor moved the opened ticket")
	}
	h.m.focus = view.Object
	h.press("esc")
	if a.tickets.opened != "" || h.m.focus != view.List {
		t.Fatal("esc did not close")
	}
}

// A ticket seen before is shown from the cache at once and reloaded once
// after the pause; the reload is background work.
func TestACachedTicketShowsAtOnceAndReloadsOnce(t *testing.T) {
	h := newHarness(t)
	h.openTickets(website)
	a := h.m.app
	h.drive(h.feed(gateway.Due{Session: a.session, Token: a.previewToken})...)
	first := a.detail.id
	h.press("down")
	h.drive(h.feed(gateway.Due{Session: a.session, Token: a.previewToken})...)
	if len(h.f.detailReads()) != 2 {
		t.Fatalf("%d reads", len(h.f.detailReads()))
	}
	h.press("up")
	d := a.detail
	if d == nil || d.id != first || !d.known || !d.cached || d.loading {
		t.Fatalf("return to the first: %+v", d)
	}
	if got := h.m.screen().Object.Notice.String(); got != "Cached" {
		t.Fatalf("notice %q", got)
	}
	if !strings.Contains(h.m.screen().Object.Text.String(), "#1 Login bricht ab") {
		t.Fatal("the cached ticket is not shown")
	}
	cmds := h.feed(gateway.Due{Session: a.session, Token: a.previewToken})
	if !d.loading || !d.cached || len(cmds) != 1 {
		t.Fatalf("the reload: loading %v cached %v cmds %d", d.loading, d.cached, len(cmds))
	}
	if got := h.m.screen().Object.Notice.String(); got != "Cached" {
		t.Fatalf("notice while reloading %q", got)
	}
	h.drive(cmds...)
	if len(h.f.detailReads()) != 3 || d.cached || d.loading || h.m.screen().Object.Notice.String() != "" {
		t.Fatalf("after the reload: %d reads, cached %v, notice %q", len(h.f.detailReads()), d.cached, h.m.screen().Object.Notice.String())
	}
	// Enter on a cached ticket (not yet reloaded) reloads as the person.
	h.press("down")
	if !a.detail.cached {
		t.Fatal("the second ticket is not cached")
	}
	cmd := h.press("enter")
	if cmd == nil || !a.detail.loading {
		t.Fatal("opening a cached ticket did not reload it")
	}
	for _, w := range a.waiters {
		if w.ticket.Area == gateway.Detail && w.ticket.Priority != queue.User {
			t.Fatal("the open is not the person's")
		}
	}
	h.drive(cmd)
	if len(h.f.detailReads()) != 4 {
		t.Fatalf("%d reads", len(h.f.detailReads()))
	}
}

// Server pages of the ticket list: [ and ] turn them, the selection is an
// id, and a return to the project remembers page, row and search, showing
// the cached page at once with one reload.
func TestTicketPagesAndTheReturnToAProject(t *testing.T) {
	h := newHarness(t)
	h.openTickets(website)
	a := h.m.app
	if !h.m.allowed()[NextPage] || h.m.allowed()[PrevPage] {
		t.Fatal("page keys")
	}
	h.drive(h.press("]"))
	tv := a.tickets
	if tv.page != 2 || len(tv.rows) != 5 || tv.rows[0].Number != 26 || tv.selected != tv.rows[0].ID {
		t.Fatalf("page 2: %+v", tv)
	}
	if got := h.m.screen().Context.String(); got != "Nordlicht GmbH | Tickets | Website-Relaunch | page 2 of 2" {
		t.Fatalf("context %q", got)
	}
	if reads := h.f.ticketListReads(); reads[len(reads)-1].Page != 2 {
		t.Fatalf("reads %+v", reads)
	}
	h.press("down")
	h.press("down")
	third := tv.selected
	// A reorder keeps the selected id.
	h.f.setTickets(func(q api.TicketQuery) transport.Answer {
		ans := h.d.defaultTickets(q)
		var p api.TicketPage
		_ = json.Unmarshal(ans.Payload, &p)
		slices.Reverse(p.Data)
		return objectAnswer(p)
	})
	h.drive(h.press("R"))
	if tv.selected != third || tv.index(third) != 2 {
		t.Fatalf("after the reorder: selected %q at %d", tv.selected, tv.index(third))
	}
	h.f.setTickets(h.d.defaultTickets)
	// Back to the projects and in again: the remembered page and row.
	h.press("esc")
	before := len(h.f.ticketListReads())
	cmd := h.openProject(website)
	tv = a.tickets
	if tv.page != 2 || tv.selected != third || !tv.cached || !tv.loading {
		t.Fatalf("return: page %d selected %q cached %v loading %v", tv.page, tv.selected, tv.cached, tv.loading)
	}
	if got := h.m.screen().List.Notice.String(); got != "Cached" {
		t.Fatalf("notice %q", got)
	}
	h.drive(cmd)
	if len(h.f.ticketListReads()) != before+1 || tv.cached || tv.loading {
		t.Fatalf("after the reload: %d reads, cached %v", len(h.f.ticketListReads())-before, tv.cached)
	}
	h.drive(h.press("["))
	if tv.page != 1 || len(tv.rows) != 25 || !h.m.allowed()[NextPage] || h.m.allowed()[PrevPage] {
		t.Fatalf("page 1: page %d rows %d", tv.page, len(tv.rows))
	}
	// Another project keeps its own position; the first keeps its search.
	h.press("/")
	h.typeText("Ticket 2")
	h.drive(h.press("enter"))
	if len(tv.rows) != 10 { // Ticket 2, 20 to 29
		t.Fatalf("search: %d rows", len(tv.rows))
	}
	h.press("esc")
	h.drive(h.openProject(handbuch))
	if a.tickets.project != handbuch || a.tickets.query != "" || len(a.tickets.rows) != 3 || a.tickets.page != 1 {
		t.Fatalf("handbuch: %+v", a.tickets)
	}
	h.press("esc")
	h.drive(h.openProject(website))
	if a.tickets.query != "Ticket 2" || len(a.tickets.rows) != 10 {
		t.Fatalf("website again: query %q rows %d", a.tickets.query, len(a.tickets.rows))
	}
	// An empty project.
	h.press("esc")
	h.drive(h.openProject(intern))
	if got := h.m.screen().List.Notice.String(); got != "No tickets." || h.m.allowed()[NextPage] {
		t.Fatalf("empty: notice %q", got)
	}
	if got := h.m.screen().Context.String(); got != "Nordlicht GmbH | Tickets | Intern | page 1 of 1" {
		t.Fatalf("context %q", got)
	}
}

// Every search, page and option is its own entry: nothing of one shows for
// another, and the options of a ticket read follow the server's schemas.
func TestCacheEntriesKeepSearchesProjectsAndOptionsApart(t *testing.T) {
	h := newHarness(t)
	h.openTickets(website)
	a := h.m.app
	h.press("/")
	h.typeText("export")
	h.drive(h.press("enter"))
	plain, search := a.ticketsKey(website, "", 1), a.ticketsKey(website, "export", 1)
	if plain == search || plain.String() == search.String() {
		t.Fatal("the search is not part of the key")
	}
	e1, ok1 := a.store.Get(plain)
	e2, ok2 := a.store.Get(search)
	if !ok1 || !ok2 || string(e1.Payload) == string(e2.Payload) {
		t.Fatal("the two lists are not two entries")
	}
	if _, ok := a.store.Get(a.ticketsKey(handbuch, "export", 1)); ok {
		t.Fatal("another project sees the entry")
	}
	paged := a.detailKey(website, login)
	if paged.PostsPage != 1 || paged.PostsPerPage != 25 || !paged.Newest || paged.AttachmentsPage != 1 || paged.FullText || paged.Post != "" {
		t.Fatalf("detail key %+v", paged)
	}
	var unpaged cache.Key
	{
		h2 := newHarness(t)
		tools := withoutArgument(h2.d.tools, "get_ticket", "posts_order")
		h2.f.tools = func() ([]transport.Tool, transport.Answer) {
			return tools, transport.Answer{Class: transport.Object, Status: 200, Sent: true}
		}
		h2.openTickets(website)
		unpaged = h2.m.app.detailKey(website, login)
		if unpaged.PostsPage != 0 || unpaged.PostsPerPage != 0 || unpaged.Newest || unpaged.AttachmentsPage != 0 {
			t.Fatalf("an older server's key carries options: %+v", unpaged)
		}
		h2.drive(h2.feed(gateway.Due{Session: h2.m.app.session, Token: h2.m.app.previewToken})...)
		if reads := h2.f.detailReads(); len(reads) != 1 || reads[0].o != (api.TicketOptions{}) {
			t.Fatalf("an older server was sent options: %+v", reads)
		}
		text := h2.m.screen().Object.Text.String()
		if !strings.Contains(text, "Comments · oldest first · 2 comments") || !strings.Contains(text, "Attachments · 2") {
			t.Fatalf("unpaged headings:\n%s", text)
		}
	}
	if paged.Resource() != unpaged.Resource() {
		t.Fatal("two reads of one ticket are two resources")
	}
	// Two namespaces never see each other: a second session with another
	// token id shares nothing.
	other, _ := cache.Confirmed("default", "https://example.invalid", "another-token")
	k := other.Key(api.TicketsSpec.Tool)
	k.Project, k.Page, k.PerPage = website, 1, 25
	if _, ok := a.store.Get(k); ok {
		t.Fatal("another token sees the entry")
	}
}

// An answer for a list the person left is not painted over the one shown
// and not filed; a ticket read for a selection left is dropped.
func TestLateAnswersForALeftSelectionAreDropped(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.drive(h.chooseTickets(orgA))
	gate := make(chan struct{})
	h.f.gate, h.f.gateTickets, h.f.ignoreCancel = gate, true, true
	late := h.background(h.openProject(website))
	h.waitFor("the website read on the wire", func() bool { return len(h.f.ticketListReads()) == 1 })
	a := h.m.app
	h.press("esc")
	h.f.gateTickets = false
	h.drive(h.openProject(handbuch))
	if a.tickets.project != handbuch || len(a.tickets.rows) != 3 {
		t.Fatalf("handbuch: %+v", a.tickets)
	}
	close(gate)
	r := h.receive(late)
	h.feed(r)
	if a.tickets.project != handbuch || len(a.tickets.rows) != 3 || a.tickets.loading {
		t.Fatalf("the late answer was painted: %+v", a.tickets)
	}
	if _, ok := a.store.Get(a.ticketsKey(website, "", 1)); ok {
		t.Fatal("the late answer was filed")
	}
	// A preview left behind: its late answer shows nothing.
	h.f.gateTicket = handbuch
	gate = make(chan struct{})
	h.f.gate = gate
	first := a.tickets.rows[0].ID
	h.f.gateTicket = first
	preview := h.background(h.feed(gateway.Due{Session: a.session, Token: a.previewToken})[0])
	h.waitFor("the preview on the wire", func() bool { return len(h.f.detailReads()) == 1 })
	h.press("down")
	if a.detail != nil {
		t.Fatal("moving on kept the old detail")
	}
	close(gate)
	h.feed(h.receive(preview))
	if a.detail != nil {
		t.Fatalf("the late preview was shown: %+v", a.detail)
	}
	if _, ok := a.store.Get(a.detailKey(handbuch, first)); ok {
		t.Fatal("the late preview was filed")
	}
}

// A 401 from a preview the person left ends the session: nothing of it is
// shown, read or asked again, and a late answer does not fill it.
func TestA401FromALeftPreviewEndsTheSession(t *testing.T) {
	h := newHarness(t)
	h.openTickets(website)
	a := h.m.app
	h.events = gateway.NewEvents(h.live)
	a.deps.Events = h.events
	a.session = h.events.Session()
	a.deps.Gateway = gateway.New(h.live, h.q, h.f, gateway.WithEvents(h.events))
	wake := h.background(h.events.Wait())
	gate := make(chan struct{})
	h.f.gate, h.f.gateTicket, h.f.ignoreCancel = gate, login, true
	h.f.setTicket(func(project, id string, o api.TicketOptions) transport.Answer {
		if id == login {
			return failing(transport.Unauthenticated, 401)
		}
		return h.d.defaultTicket(project, id, o)
	})
	preview := h.background(h.feed(gateway.Due{Session: a.session, Token: a.previewToken})[0])
	h.waitFor("the preview on the wire", func() bool { return len(h.f.detailReads()) == 1 })
	h.press("down") // the preview is left behind
	close(gate)
	h.waitFor("the 401", func() bool { return h.events.State().Revoked })
	late := h.receive(preview)
	// Any message now ends the session; the late result refills nothing.
	before := len(h.f.detailReads())
	cmds := h.feed(gateway.Due{Session: a.session, Token: a.previewToken})
	if !a.expired || len(cmds) != 0 || len(h.f.detailReads()) != before {
		t.Fatalf("expired %v, %d commands, %d reads", a.expired, len(cmds), len(h.f.detailReads())-before)
	}
	h.feed(late, h.receive(wake))
	if a.tickets != nil || a.projects != nil || a.detail != nil || a.store != nil {
		t.Fatal("the session came back")
	}
	plain := ansi.Strip(h.m.View().Content)
	if strings.Contains(plain, "Login bricht ab") || strings.Contains(plain, "Website") || !strings.Contains(plain, "Session expired") {
		t.Fatalf("the screen:\n%s", plain)
	}
	for _, key := range []string{"enter", "/", "R", "]", "esc", "down"} {
		if cmd := h.press(key); cmd != nil {
			t.Fatalf("%s acted after the expiry", key)
		}
	}
	if len(h.f.detailReads()) != before || len(h.f.ticketListReads()) != 1 || len(h.f.projectReads()) != 1 {
		t.Fatal("something was read after the expiry")
	}
}

// What the server supports comes from its schemas: without list_tickets no
// tickets entry; without get_ticket a list without preview or open;
// without q no search; without the paging arguments a plain read.
func TestTicketCapabilitiesComeFromTheSchemas(t *testing.T) {
	t.Run("without list_tickets", func(t *testing.T) {
		h := newHarness(t)
		tools := withoutTool(h.d.tools, "list_tickets")
		h.f.tools = func() ([]transport.Tool, transport.Answer) {
			return tools, transport.Answer{Class: transport.Object, Status: 200, Sent: true}
		}
		h.start()
		for _, r := range h.m.screen().Chooser.Rows {
			if strings.TrimSpace(r.Primary.String()) == "Tickets" {
				t.Fatal("a tickets entry without the tool")
			}
		}
		if got := h.m.screen().Chooser.Notice.String(); got != "Tickets are not supported by this server." {
			t.Fatalf("notice %q", got)
		}
		if cmd := h.chooseTickets(orgA); cmd != nil || h.m.app.projects != nil || len(h.f.projectReads()) != 0 {
			t.Fatal("the tickets opened without the tool")
		}
	})
	t.Run("without get_ticket", func(t *testing.T) {
		h := newHarness(t)
		tools := withoutTool(h.d.tools, "get_ticket")
		h.f.tools = func() ([]transport.Tool, transport.Answer) {
			return tools, transport.Answer{Class: transport.Object, Status: 200, Sent: true}
		}
		h.openTickets(website)
		a := h.m.app
		if len(a.tickets.rows) != 25 || a.previewToken != 0 {
			t.Fatalf("rows %d token %d", len(a.tickets.rows), a.previewToken)
		}
		if h.m.allowed()[OpenList] {
			t.Fatal("enter offered without get_ticket")
		}
		if cmd := h.press("enter"); cmd != nil || a.tickets.opened != "" || len(h.f.detailReads()) != 0 {
			t.Fatal("enter read a ticket without the tool")
		}
		if got := h.m.screen().Object.Notice.String(); got != "Ticket details are not supported by this server." {
			t.Fatalf("notice %q", got)
		}
	})
	t.Run("without q", func(t *testing.T) {
		h := newHarness(t)
		tools := withoutArgument(h.d.tools, "list_tickets", "q")
		h.f.tools = func() ([]transport.Tool, transport.Answer) {
			return tools, transport.Answer{Class: transport.Object, Status: 200, Sent: true}
		}
		h.openTickets(website)
		if h.m.allowed()[Search] || strings.Contains(helpText(h.m), "search") {
			t.Fatal("search offered without q")
		}
		if cmd := h.press("/"); cmd != nil || h.m.mode != Navigate {
			t.Fatal("/ acted without q")
		}
		if got := h.m.screen().List.Notice.String(); got != "" {
			t.Fatalf("notice %q", got)
		}
	})
	t.Run("without the paging arguments", func(t *testing.T) {
		h := newHarness(t)
		tools := withoutArgument(h.d.tools, "get_ticket", "attachments_page")
		h.f.tools = func() ([]transport.Tool, transport.Answer) {
			return tools, transport.Answer{Class: transport.Object, Status: 200, Sent: true}
		}
		h.openTickets(website)
		a := h.m.app
		h.press("down")
		h.press("down") // #3: 30 comments
		h.drive(h.feed(gateway.Due{Session: a.session, Token: a.previewToken})...)
		if reads := h.f.detailReads(); len(reads) != 1 || reads[0].o != (api.TicketOptions{}) {
			t.Fatalf("reads %+v", reads)
		}
		text := h.m.screen().Object.Text.String()
		if !strings.Contains(text, "Comments · oldest first · 2 comments") && !strings.Contains(text, "Comments · oldest first · first 50 of 30 comments") {
			// 30 comments fit in the first 50: not truncated.
			if !strings.Contains(text, "Comments · oldest first · 30 comments") {
				t.Fatalf("heading:\n%s", text)
			}
		}
		if strings.Index(text, "Comment 1\n") > strings.Index(text, "Comment 30") {
			t.Fatal("not oldest first")
		}
		// #4: 12 attachments, the first ten shown as incomplete.
		h.press("down")
		h.drive(h.feed(gateway.Due{Session: a.session, Token: a.previewToken})...)
		text = h.m.screen().Object.Text.String()
		if !strings.Contains(text, "Attachments · first 10 of 12") || !strings.Contains(text, "- file-10.txt") || strings.Contains(text, "file-11") {
			t.Fatalf("attachments:\n%s", text)
		}
	})
}

// The pages of comments and attachments say where they are; incompleteness
// is named.
func TestTheTicketPaneNamesItsPages(t *testing.T) {
	h := newHarness(t)
	h.openTickets(website)
	a := h.m.app
	h.press("down")
	h.press("down") // #3
	h.drive(h.feed(gateway.Due{Session: a.session, Token: a.previewToken})...)
	text := h.m.screen().Object.Text.String()
	if !strings.Contains(text, "Comments · newest first · page 1 of 2 · 30 readable comments") || !strings.Contains(text, "Comment 30") || strings.Contains(text, "Comment 5\n") {
		t.Fatalf("page 1 of the comments:\n%s", text)
	}
	h.press("down") // #4
	h.drive(h.feed(gateway.Due{Session: a.session, Token: a.previewToken})...)
	text = h.m.screen().Object.Text.String()
	if !strings.Contains(text, "Attachments · page 1 of 2 · 12 in all") || !strings.Contains(text, "- file-10.txt") || strings.Contains(text, "file-11") {
		t.Fatalf("page 1 of the attachments:\n%s", text)
	}
	// A private comment is marked; a deleted author and an unreadable time
	// fall back.
	h.f.setTicket(func(project, id string, o api.TicketOptions) transport.Answer {
		r := h.d.ticketRecord(project, 1, o)
		r.Posts[0].IsPrivate = true
		r.Posts[0].Author = &api.Ref{ID: "u", Name: ""}
		r.Posts[1].Author = nil
		r.Posts[1].CreatedAt = "never"
		r.Posts[1].BodyText = "  "
		r.Status = nil
		r.Creator = &api.Ref{ID: "c", Name: " "}
		r.UpdatedAt = "no"
		return ticketAnswer(r)
	})
	h.press("up")
	h.press("up")
	h.press("up")
	h.drive(h.feed(gateway.Due{Session: a.session, Token: a.previewToken})...)
	text = h.m.screen().Object.Text.String()
	for _, want := range []string{"Status: No status", "Creator: Unknown user", "Updated: Time unknown", "Unknown user · 2026-01-05 09:26:00 UTC · private", "Unknown user · Time unknown", "No text"} {
		if !strings.Contains(text, want) {
			t.Errorf("the pane lacks %q:\n%s", want, text)
		}
	}
}

// R in the tickets section reloads what is shown: the projects, the page,
// or the opened ticket, as the person's read.
func TestRInTheTicketsSectionReloadsWhatIsShown(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.drive(h.chooseTickets(orgA))
	h.drive(h.press("R"))
	if got := h.f.projectReads(); len(got) != 2 {
		t.Fatalf("project reads %v", got)
	}
	h.drive(h.openProject(website))
	lists := len(h.f.ticketListReads())
	h.drive(h.press("R"))
	if len(h.f.ticketListReads()) != lists+1 || len(h.f.projectReads()) != 2 {
		t.Fatal("R on the list did not reload the page alone")
	}
	a := h.m.app
	h.drive(h.press("enter"))
	details := len(h.f.detailReads())
	cmd := h.press("R")
	for _, w := range a.waiters {
		if w.ticket.Area == gateway.Detail && w.ticket.Priority != queue.User {
			t.Fatal("R is not the person's read")
		}
	}
	h.drive(cmd)
	if len(h.f.detailReads()) != details+1 || len(h.f.ticketListReads()) != lists+1 {
		t.Fatal("R on the ticket did not reload the ticket alone")
	}
	// A removed ticket: the pane says so, the open closes.
	h.f.setTicket(func(string, string, api.TicketOptions) transport.Answer { return failing(transport.NotFound, 200) })
	h.drive(h.press("R"))
	if a.tickets.opened != "" || h.m.focus != view.List || a.detail == nil || a.detail.known {
		t.Fatalf("removed: opened %q focus %d detail %+v", a.tickets.opened, h.m.focus, a.detail)
	}
	if got := h.m.screen().Object.Notice.String(); got != "Not found or no longer accessible." {
		t.Fatalf("notice %q", got)
	}
	if _, ok := a.store.Get(a.detailKey(website, login)); ok {
		t.Fatal("the removed ticket stayed cached")
	}
	// A temporary failure keeps the ticket, possibly stale; a refusal keeps
	// it with the message.
	h.f.setTicket(h.d.defaultTicket)
	h.drive(h.feed(gateway.Due{Session: a.session, Token: a.previewToken})...)
	h.f.setTicket(func(string, string, api.TicketOptions) transport.Answer { return failing(transport.Temporary, 503) })
	h.drive(h.press("enter"))
	h.drive(h.press("R"))
	if !a.detail.known || !a.detail.stale || h.m.screen().Object.Notice.String() != "Possibly stale" {
		t.Fatalf("temporary: %+v notice %q", a.detail, h.m.screen().Object.Notice.String())
	}
	h.f.setTicket(func(string, string, api.TicketOptions) transport.Answer {
		return transport.Answer{Class: transport.Refused, Reason: "archived", Message: "archived", Status: 200, Sent: true}
	})
	h.drive(h.press("R"))
	if !a.detail.known || h.m.screen().Object.Notice.String() != "Refused: refused: archived" {
		t.Fatalf("refused: notice %q", h.m.screen().Object.Notice.String())
	}
	// A removed project: the list and the ticket go.
	h.f.setTickets(func(api.TicketQuery) transport.Answer { return failing(transport.NotFound, 200) })
	h.m.focus = view.List
	h.drive(h.press("R"))
	if len(a.tickets.rows) != 0 || a.tickets.known || a.detail != nil || h.m.screen().List.Notice.String() != "Not found or no longer accessible." {
		t.Fatalf("removed project: %+v detail %v notice %q", a.tickets, a.detail != nil, h.m.screen().List.Notice.String())
	}
}

// Leaving a section remembers it and cancels its pending reads; coming
// back shows what was cached.
func TestSwitchingSectionsRemembersAndCancels(t *testing.T) {
	h := newHarness(t)
	h.openTickets(website)
	a := h.m.app
	h.press("down")
	gate := make(chan struct{})
	second := a.tickets.rows[1].ID
	h.f.gate, h.f.gateTicket = gate, second
	preview := h.background(h.feed(gateway.Due{Session: a.session, Token: a.previewToken})[0])
	h.waitFor("the preview", func() bool { return len(h.f.detailReads()) == 1 })
	h.drive(h.choose(orgA)) // the inbox of A
	if a.active == nil || a.projects != nil || a.tickets != nil || a.detail != nil {
		t.Fatal("the tickets section was not left")
	}
	if r := h.receive(preview).(gateway.TicketResult); r.Answer.Class != transport.Canceled {
		t.Fatalf("the preview was not canceled: %v", r.Answer.Class)
	}
	before := len(h.f.projectReads())
	cmd := h.chooseTickets(orgA)
	if a.active != nil || a.projects == nil || !a.projects.cached || a.projects.selected != website || a.tickets != nil {
		t.Fatalf("back: inbox %v projects %+v", a.active != nil, a.projects)
	}
	h.drive(cmd)
	if len(h.f.projectReads()) != before+1 {
		t.Fatal("no single reload of the projects")
	}
	// The ticket list of the project is remembered with its row, shown
	// from the cache.
	cmd = h.openProject(website)
	if a.tickets.selected != second || !a.tickets.cached {
		t.Fatalf("tickets: selected %q cached %v", a.tickets.selected, a.tickets.cached)
	}
	h.drive(cmd)
	// Esc in the chooser goes to the list of the section.
	h.m.focus = view.Navigation
	h.press("esc")
	if h.m.focus != view.List {
		t.Fatal("esc in the chooser did not return to the list")
	}
	// Opening the same section again from the chooser only focuses.
	reads := len(h.f.projectReads())
	if cmd := h.chooseTickets(orgA); cmd != nil || a.tickets == nil || h.m.focus != view.List || len(h.f.projectReads()) != reads {
		t.Fatal("re-opening the open section reloaded")
	}
}

// The seventh preview in a minute waits for the budget; moving on cancels
// it without a request.
func TestTheSeventhPreviewWaitsForTheBudgetAndMovingOnCancelsIt(t *testing.T) {
	h := newHarness(t)
	h.openTickets(website)
	a := h.m.app
	for i := range 6 {
		if i > 0 {
			h.press("down")
		}
		h.drive(h.feed(gateway.Due{Session: a.session, Token: a.previewToken})...)
	}
	if len(h.f.detailReads()) != 6 {
		t.Fatalf("%d reads", len(h.f.detailReads()))
	}
	h.press("down")
	held := h.background(h.feed(gateway.Due{Session: a.session, Token: a.previewToken})[0])
	time.Sleep(100 * time.Millisecond)
	if len(h.f.detailReads()) != 6 {
		t.Fatal("the seventh preview was sent")
	}
	if !a.detail.loading || h.m.screen().Object.Notice.String() != "Loading…" {
		t.Fatalf("the held preview: %+v", a.detail)
	}
	h.press("down")
	r := h.receive(held).(gateway.TicketResult)
	if r.Answer.Class != transport.Canceled {
		t.Fatalf("%v", r.Answer.Class)
	}
	h.feed(r)
	if len(h.f.detailReads()) != 6 {
		t.Fatal("the canceled preview was sent")
	}
	// The person is not held: Enter reads at once.
	h.drive(h.press("enter"))
	if len(h.f.detailReads()) != 7 || a.detail == nil || !a.detail.known {
		t.Fatalf("%d reads, detail %+v", len(h.f.detailReads()), a.detail)
	}
}

// Hostile text from the server and from the cache reaches the screen
// cleaned, in every pane of the tickets section.
func TestHostileTicketTextFromServerAndCacheIsCleaned(t *testing.T) {
	h := newHarness(t)
	h.f.setProjects(func(org string) transport.Answer {
		list := slices.Clone(h.d.projects)
		list[3].Name = "PROJECT_START" + hostileText
		return objectAnswer(map[string]any{"data": list})
	})
	h.f.setTickets(func(q api.TicketQuery) transport.Answer {
		rows := h.d.ticketRows(website, 2)
		rows[0].Title = "TITLE_START" + hostileText
		status := "STATUS_START" + strings.ReplaceAll(hostileText, "\n", "")
		rows[0].Status = &status
		rows[0].Assignees = []api.Ref{{ID: "u", Name: "ASSIGNEE_START" + hostileText}}
		return ticketPageAnswer(1, 25, 2, rows)
	})
	h.f.setTicket(func(project, id string, o api.TicketOptions) transport.Answer {
		r := h.d.ticketRecord(project, 1, o)
		r.Title = "TITLE_START" + hostileText
		r.DescriptionText = "DESC_START" + hostileText
		r.Posts[0].BodyText = "BODY_START" + hostileText
		r.Posts[0].Author.Name = "AUTHOR_START" + hostileText
		r.Attachments[0].Filename = "FILE_START" + hostileText
		r.Project.Name = "PNAME_START" + hostileText
		return ticketAnswer(r)
	})
	h.openTickets(website)
	a := h.m.app
	h.drive(h.feed(gateway.Due{Session: a.session, Token: a.previewToken})...)
	h.drive(h.press("enter"))
	for _, width := range []int{120, 80, 79} {
		h.feed(tea.WindowSizeMsg{Width: width, Height: 40})
		for _, focus := range []view.Pane{view.List, view.Object} {
			h.m.focus = focus
			text := h.m.View().Content
			assertViewSafe(t, text)
			plain := ansi.Strip(text)
			if focus == view.List && !strings.Contains(plain, "TITLE_START") {
				t.Fatalf("width %d: the title is missing:\n%s", width, plain)
			}
			if focus == view.Object {
				for _, want := range []string{"DESC_START", "BODY_START", "AUTHOR_START", "FILE_START"} {
					if !strings.Contains(plain, want) {
						t.Fatalf("width %d: %s is missing:\n%s", width, want, plain)
					}
				}
			}
		}
	}
	h.m.focus = view.List
	h.press("esc") // the projects
	text := h.m.View().Content
	assertViewSafe(t, text)
	if !strings.Contains(ansi.Strip(text), "PROJECT_START") {
		t.Fatal("the project name is missing")
	}
	// The search entry is the person's own text, cleaned all the same.
	h.drive(h.openProject(website))
	h.press("/")
	h.typeText("abc")
	a.tickets.entry = "ENTRY_START" + hostileText
	text = h.m.View().Content
	assertViewSafe(t, text)
	if !strings.Contains(ansi.Strip(text), "ENTRY_START") {
		t.Fatal("the entry is missing")
	}
	h.press("esc")
	h.feed(tea.WindowSizeMsg{Width: 120, Height: 40})
	// The cache path: a hostile ticket filed directly, shown on the return.
	r := h.d.ticketRecord(website, 2, api.TicketOptions{PostsPage: 1, PostsPerPage: 25, Newest: true, AttachmentsPage: 1})
	r.DescriptionText = "CACHED_START" + hostileText
	a.store.Apply(a.detailKey(website, r.ID), cache.Replace, ticketAnswer(r).Payload, h.now)
	h.press("down")
	text = h.m.View().Content
	assertViewSafe(t, text)
	if plain := ansi.Strip(text); !strings.Contains(plain, "CACHED_START") || !strings.Contains(plain, "Cached") {
		t.Fatalf("the cached ticket is missing:\n%s", plain)
	}
	// A hostile error text.
	h.f.setTickets(func(api.TicketQuery) transport.Answer {
		return transport.Answer{Class: transport.Refused, Message: "ERR_START" + hostileText, Status: 200, Sent: true}
	})
	h.drive(h.press("R"))
	text = h.m.View().Content
	assertViewSafe(t, text)
	if !strings.Contains(ansi.Strip(text), "ERR_START") {
		t.Fatal("the error text is missing")
	}
}

// The help names exactly the keys that act, in every state of the tickets
// section, the search line included.
func TestHelpNamesExactlyTheKeysThatActInTheTicketsSection(t *testing.T) {
	keys := map[string]Action{"up": Up, "down": Down, "pgup": PageUp, "pgdown": PageDown, "[": PrevPage, "]": NextPage, "R": Refresh, "enter": OpenList, "esc": Back, "left": Left, "right": Right, "/": Search}
	entryKeys := map[string]Action{"enter": Submit, "esc": Cancel, "backspace": DeleteBack}
	acted := func(h *harness, key string) bool {
		before := ansi.Strip(h.m.View().Content)
		focus, mode := h.m.focus, h.m.mode
		m, cmd := step(t, h.m, press(key))
		h.m = m
		return cmd != nil || ansi.Strip(h.m.View().Content) != before || h.m.focus != focus || h.m.mode != mode
	}
	check := func(t *testing.T, state string, build func() *harness) {
		t.Helper()
		h := build()
		allowed := h.m.allowed()
		help := helpText(h.m)
		set := keys
		if h.m.mode == TextEntry {
			set = entryKeys
		}
		for key, action := range set {
			if acted(build(), key) {
				if !allowed[action] {
					t.Errorf("%s: %s acted but is not offered (%q)", state, key, help)
				}
				if !strings.Contains(help, keyLabel(key)) {
					t.Errorf("%s: %s acted but the help does not name it: %q", state, key, help)
				}
			}
		}
		for _, b := range h.m.actions() {
			if b.kind == Quit || b.kind == Interrupt {
				continue
			}
			acts := false
			for key, action := range set {
				if action != b.kind && pairs[b.kind] != action {
					continue
				}
				if acted(build(), key) {
					acts = true
				}
			}
			if !acts {
				t.Errorf("%s: the help offers %v but no key of it acts", state, b.kind)
			}
		}
	}
	projects := func() *harness {
		h := newHarness(t)
		h.start()
		h.drive(h.chooseTickets(orgA))
		return h
	}
	tickets := func() *harness { h := newHarness(t); h.openTickets(website); return h }
	previewed := func() *harness {
		h := tickets()
		h.drive(h.feed(gateway.Due{Session: h.m.app.session, Token: h.m.app.previewToken})...)
		return h
	}
	check(t, "chooser with sections", func() *harness { h := newHarness(t); h.start(); h.m.focus = view.Navigation; return h })
	check(t, "projects", projects)
	check(t, "projects, chooser focused", func() *harness { h := projects(); h.m.focus = view.Navigation; return h })
	check(t, "no projects", func() *harness { h := newHarness(t); h.start(); h.drive(h.chooseTickets(orgB)); return h })
	check(t, "tickets page 1", tickets)
	check(t, "tickets page 2", func() *harness { h := tickets(); h.drive(h.press("]")); return h })
	check(t, "tickets, object focused, nothing shown", func() *harness { h := tickets(); h.m.focus = view.Object; return h })
	check(t, "tickets, preview shown", previewed)
	check(t, "tickets, preview, object focused", func() *harness { h := previewed(); h.m.focus = view.Object; return h })
	check(t, "ticket opened", func() *harness { h := previewed(); h.drive(h.press("enter")); return h })
	check(t, "long ticket opened", func() *harness {
		h := tickets()
		h.press("down")
		h.press("down")
		h.drive(h.feed(gateway.Due{Session: h.m.app.session, Token: h.m.app.previewToken})...)
		h.drive(h.press("enter"))
		return h
	})
	check(t, "search line", func() *harness { h := tickets(); h.press("/"); return h })
	check(t, "search line with text", func() *harness { h := tickets(); h.press("/"); h.typeText("ab"); return h })
	check(t, "search result", func() *harness {
		h := tickets()
		h.press("/")
		h.typeText("export")
		h.drive(h.press("enter"))
		return h
	})
	check(t, "empty project", func() *harness { h := newHarness(t); h.openTickets(intern); return h })
	check(t, "two columns", func() *harness { h := previewed(); h.m.width = 80; return h })
	check(t, "one column", func() *harness { h := previewed(); h.m.width = 79; return h })
	check(t, "one column, object", func() *harness { h := previewed(); h.m.width, h.m.focus = 79, view.Object; return h })
	check(t, "without get_ticket", func() *harness {
		h := newHarness(t)
		tools := withoutTool(h.d.tools, "get_ticket")
		h.f.tools = func() ([]transport.Tool, transport.Answer) {
			return tools, transport.Answer{Class: transport.Object, Status: 200, Sent: true}
		}
		h.openTickets(website)
		return h
	})
	check(t, "small while typing", func() *harness { h := tickets(); h.press("/"); h.m.width, h.m.height = 20, 5; return h })
}

// A 401 on the projects read ends the session like any other.
func TestA401OnTheProjectsEndsTheSession(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.f.setProjects(func(string) transport.Answer { return failing(transport.Unauthenticated, 401) })
	h.events.Observe(func(context.Context) transport.Answer { return failing(transport.Unauthenticated, 401) })(context.Background())
	h.drive(h.chooseTickets(orgA))
	if !h.m.app.expired || h.m.app.projects != nil {
		t.Fatal("the session did not end")
	}
}

// A timer's wake-up reaching the shell (no session) changes nothing and
// does not crash.
func TestADueWithoutASessionIsIgnored(t *testing.T) {
	m := New()
	m.width, m.height = 120, 24
	next, cmd := step(t, m, gateway.Due{Session: 1, Token: 1})
	if next != m || cmd != nil {
		t.Fatal("a wake-up without a session acted")
	}
}

// A preview timer set before the person opened a ticket and moved the
// cursor on reads nothing when it wakes up: the opened ticket stays in the
// pane, its read is not canceled. (A mutation probe survived without it.)
func TestAWakeUpWhileATicketIsOpenedReadsNothing(t *testing.T) {
	h := newHarness(t)
	h.openTickets(website)
	a := h.m.app
	token := a.previewToken // the timer of row 0, not yet due
	gate := make(chan struct{})
	h.f.gate, h.f.gateTicket = gate, login
	open := h.background(h.press("enter")) // the person's read of #1 is pending
	h.waitFor("the open on the wire", func() bool { return len(h.f.detailReads()) == 1 })
	h.m.focus = view.List
	h.press("down") // the cursor moves on; #1 stays opened
	cmds := h.feed(gateway.Due{Session: a.session, Token: token})
	if len(cmds) != 0 || a.detail == nil || a.detail.id != login || !a.detail.loading || a.tickets.opened != login {
		t.Fatalf("the wake-up acted: %d commands, detail %+v, opened %q", len(cmds), a.detail, a.tickets.opened)
	}
	close(gate)
	r := h.receive(open).(gateway.TicketResult)
	if r.Answer.Class != transport.Object {
		t.Fatalf("the open was canceled: %v", r.Answer.Class)
	}
	h.feed(r)
	if a.detail.record.ID != login || len(h.f.detailReads()) != 1 {
		t.Fatalf("after the open: %+v, %d reads", a.detail.record.ID, len(h.f.detailReads()))
	}
}

// Leaving the tickets section cancels a pending read of its projects, and a
// 401 while the search line is open ends the session in navigation mode,
// so q quits at once. (Mutation probes survived without these.)
func TestLeavingCancelsTheProjectsReadAndA401WhileTypingLeavesTheLine(t *testing.T) {
	h := newHarness(t)
	h.start()
	gate := make(chan struct{})
	h.f.gate, h.f.gateProjects = gate, true
	pending := h.background(h.chooseTickets(orgA))
	h.waitFor("the projects on the wire", func() bool { return len(h.f.projectReads()) == 1 })
	h.drive(h.choose(orgA)) // the inbox: the section is left
	r := h.receive(pending).(gateway.ProjectsResult)
	if r.Answer.Class != transport.Canceled {
		t.Fatalf("the projects read was not canceled: %v", r.Answer.Class)
	}
	h.feed(r)
	if h.m.app.active == nil || h.m.app.projects != nil {
		t.Fatal("the late result changed the section")
	}

	h2 := newHarness(t)
	h2.openTickets(website)
	h2.press("/")
	h2.typeText("ab")
	h2.events.Observe(func(context.Context) transport.Answer { return failing(transport.Unauthenticated, 401) })(context.Background())
	h2.feed(gateway.Wake{Session: h2.events.Session()})
	if !h2.m.app.expired || h2.m.mode != Navigate {
		t.Fatalf("expired %v, mode %d", h2.m.app.expired, h2.m.mode)
	}
	cmd := h2.press("q")
	if cmd == nil {
		t.Fatal("q did not quit after the expiry")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("q did not quit after the expiry")
	}
}

// An organisation that leaves the directory while its tickets entry is
// highlighted: the cursor goes to the inbox of all organisations, an entry
// that exists, so it is shown and Enter opens it.
func TestAVanishedOrganisationResetsTheHighlightToAnEntryThatExists(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.m.focus = view.Navigation
	h.m.app.highlight, h.m.app.highlightSection = orgA, sectionTickets
	h.f.orgs = func() ([]api.Ref, transport.Answer) {
		return []api.Ref{{ID: orgB, Name: "B"}}, transport.Answer{Class: transport.Object, Status: 200, Sent: true}
	}
	h.drive(h.press("R"))
	a := h.m.app
	if a.highlight != "" || a.highlightSection != sectionInbox {
		t.Fatalf("highlight %q section %d", a.highlight, a.highlightSection)
	}
	if h.m.screen().Chooser.Cursor != 1 || !h.m.allowed()[OpenList] {
		t.Fatalf("cursor %d, enter offered %v", h.m.screen().Chooser.Cursor, h.m.allowed()[OpenList])
	}
}

// A 401 in a result ends the session through the result itself, also
// without a mailbox to observe it (the model's own check, not only the
// mailbox's). (A mutation probe of the reviewer survived without it.)
func TestA401InAResultEndsTheSessionWithoutAMailbox(t *testing.T) {
	h := newHarness(t)
	h.start()
	a := h.m.app
	a.deps.Events = nil
	a.deps.Gateway = gateway.New(h.live, h.q, h.f)
	h.f.setProjects(func(string) transport.Answer { return failing(transport.Unauthenticated, 401) })
	h.drive(h.chooseTickets(orgA))
	if !a.expired || a.projects != nil || a.store != nil {
		t.Fatalf("expired %v, projects %v, store %v", a.expired, a.projects != nil, a.store != nil)
	}
}

// Review round 1, finding 1: a preview timer that fired while the search
// line was open is armed again when the line closes without a new search
// (Esc, or Enter with the search unchanged); otherwise no wake-up ever
// comes for the row the cursor rests on.
func TestThePreviewIsArmedAgainWhenTheSearchLineCloses(t *testing.T) {
	for _, close := range []string{"esc", "enter"} {
		h := newHarness(t)
		h.openTickets(website)
		a := h.m.app
		token := a.previewToken
		if cmd := h.press("/"); cmd != nil || h.m.mode != TextEntry {
			t.Fatal("/ did not open the line")
		}
		if cmds := h.feed(gateway.Due{Session: a.session, Token: token}); len(cmds) != 0 || len(h.f.detailReads()) != 0 || a.detail != nil {
			t.Fatalf("%s: a preview was read while typing", close)
		}
		cmd := h.press(close)
		if h.m.mode != Navigate || !h.m.previewable() || a.detail != nil || len(h.f.ticketListReads()) != 1 {
			t.Fatalf("%s: mode %d previewable %v detail %v reads %d", close, h.m.mode, h.m.previewable(), a.detail != nil, len(h.f.ticketListReads()))
		}
		// In the program the timer of token fired once already; without a
		// new timer no Due ever comes for this row.
		if cmd == nil || a.previewToken == token {
			t.Fatalf("%s: no new timer after the search line closed: cmd %v, token %d (was %d)", close, cmd != nil, a.previewToken, token)
		}
		h.drive(h.feed(gateway.Due{Session: a.session, Token: a.previewToken})...)
		if len(h.f.detailReads()) != 1 || a.detail == nil || !a.detail.known {
			t.Fatalf("%s: the preview did not come: %d reads", close, len(h.f.detailReads()))
		}
	}
}

// Review round 1, question 1: the projects of an organisation answered
// "not found" while a project's tickets are open. Everything under the
// organisation goes: the list, the ticket shown, the pending reads.
func TestARemovedOrganisationClosesItsTicketList(t *testing.T) {
	h := newHarness(t)
	h.openTickets(website)
	a := h.m.app
	h.drive(h.feed(gateway.Due{Session: a.session, Token: a.previewToken})...)
	h.drive(h.press("enter"))
	// The projects reload lands late, as "not found".
	h.f.setProjects(func(string) transport.Answer { return failing(transport.NotFound, 404) })
	h.m.focus = view.List
	h.drive(a.issue(gateway.Projects, a.projects.key, forProjects, queue.User))
	if a.tickets != nil || a.detail != nil || a.projects == nil || len(a.projects.rows) != 0 || h.m.focus != view.List {
		t.Fatalf("tickets %v detail %v projects %+v focus %d", a.tickets != nil, a.detail != nil, a.projects, h.m.focus)
	}
	if got := h.m.screen().List.Notice.String(); got != "Not found or no longer accessible." {
		t.Fatalf("notice %q", got)
	}
	if got := h.m.screen().Context.String(); got != "Nordlicht GmbH | Tickets" {
		t.Fatalf("context %q", got)
	}
	if _, ok := a.store.Get(a.projectsKey(orgA)); ok {
		t.Fatal("the projects stayed cached")
	}
}

// The pause before a preview is the documented one.
func TestThePreviewPauseIsTheDocumentedOne(t *testing.T) {
	if previewDelay != 300*time.Millisecond {
		t.Fatalf("preview delay %s", previewDelay)
	}
}

// Review round 1, finding 3 (the reviewer's R8): a result produced before
// the selection moved on and delivered after it is neither shown nor
// filed. The generation of the area refuses it, not the cancellation.
func TestAResultProducedBeforeTheMoveIsRefusedAfterIt(t *testing.T) {
	h := newHarness(t)
	h.openTickets(website)
	a := h.m.app
	first := a.tickets.selected
	cmds := h.feed(gateway.Due{Session: a.session, Token: a.previewToken})
	if len(cmds) != 1 {
		t.Fatalf("%d commands", len(cmds))
	}
	late := messages(t, cmds[0]) // the preview's result, with data, produced now
	if r, ok := late[0].(gateway.TicketResult); !ok || !r.Answer.OK() || r.Record.ID != first {
		t.Fatalf("not a result with data: %#v", late[0])
	}
	h.press("down") // the selection moves on before the message is delivered
	h.feed(late...)
	if d := a.detail; d != nil && (d.id == first || d.known) {
		t.Fatalf("the late result was shown: %+v", d)
	}
	if _, ok := a.store.Get(a.detailKey(website, first)); ok {
		t.Fatal("the late result was filed")
	}
}

// Review round 1, question 6 (the reviewer's R3): a one-line field of the
// ticket (title, project, creator, author, file name) cannot open a line of
// its own in the pane, whatever it holds.
func TestOneLineFieldsCannotOpenALineInThePane(t *testing.T) {
	h := newHarness(t)
	forged := "x\nFORGED_LINE\r\nmore"
	h.f.setTicket(func(project, id string, o api.TicketOptions) transport.Answer {
		r := h.d.ticketRecord(project, 1, o)
		r.Title = "T" + forged
		r.Project.Name = "P" + forged
		r.Creator = &api.Ref{ID: "c", Name: "C" + forged}
		r.Posts[0].Author.Name = "A" + forged
		r.Attachments[0].Filename = "F" + forged
		status := "S" + forged
		r.Status = &status
		return ticketAnswer(r)
	})
	h.openTickets(website)
	a := h.m.app
	h.drive(h.feed(gateway.Due{Session: a.session, Token: a.previewToken})...)
	text := h.m.screen().Object.Text.String()
	if !strings.Contains(text, "FORGED_LINE") {
		t.Fatal("the fields are missing")
	}
	for i, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "FORGED_LINE") || strings.HasPrefix(line, "more") {
			t.Fatalf("line %d opened by a one-line field: %q", i, line)
		}
	}
}
