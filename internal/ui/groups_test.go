package ui

import (
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

// C11, the ticket list grouped by status. The fake is a server's list: the
// rows of a project with their statuses, the status filter as the core has
// it, paged, the catalogue in the project's order.

type groupServer struct {
	h        *harness
	statuses []string
	rows     []api.TicketRow
}

// add appends n rows of a status with its functions; nil is no status.
func (s *groupServer) add(status *string, functions []string, n int) {
	for range n {
		r := s.h.d.ticketTmpl
		i := len(s.rows) + 1
		r.ID, r.Number, r.Title = ticketID(website, i), i, "Row "+strconv.Itoa(i)
		r.Status, r.StatusFunctions = status, functions
		s.rows = append(s.rows, r)
	}
}

func (s *groupServer) answer(q api.TicketQuery) transport.Answer {
	rows := slices.DeleteFunc(slices.Clone(s.rows), func(r api.TicketRow) bool {
		if q.Status != "" && (r.Status == nil || !strings.EqualFold(*r.Status, q.Status)) {
			return true
		}
		return q.Q != "" && !strings.Contains(r.Title, q.Q)
	})
	from := (q.Page - 1) * q.PerPage
	data := []api.TicketRow{}
	if from < len(rows) {
		data = rows[from:min(len(rows), from+q.PerPage)]
	}
	var p api.TicketPage
	p.Data = data
	p.Meta.Page, p.Meta.PerPage, p.Meta.Total, p.Meta.AvailableStatuses = q.Page, q.PerPage, len(rows), s.statuses
	return objectAnswer(p)
}

// groupHarness opens Website on a server with in_progress (2 rows), new
// (30), resolved (2, done) and hold, which holds nothing.
func groupHarness(t *testing.T) (*harness, *groupServer) {
	t.Helper()
	h := newHarness(t)
	s := &groupServer{h: h, statuses: []string{"in_progress", "new", "resolved", "hold"}}
	s.add(strPtr("new"), []string{"is_new"}, 30)
	s.add(strPtr("in_progress"), []string{"is_in_progress"}, 2)
	s.add(strPtr("resolved"), []string{"is_completed"}, 2)
	h.f.setTickets(s.answer)
	h.openTickets(website)
	return h, s
}

func heads(c present.Column) []string {
	var out []string
	for _, r := range c.Rows {
		if r.Heading {
			out = append(out, r.Primary.String())
		}
	}
	return out
}

func statusReads(h *harness) []string {
	var out []string
	for _, q := range h.f.ticketListReads() {
		if q.Status != "" {
			out = append(out, q.Status+":"+strconv.Itoa(q.Page))
		}
	}
	return out
}

// toHead takes the cursor to a group's head.
func toHead(h *harness, name string) {
	h.t.Helper()
	v := h.m.app.tickets
	for range 200 {
		h.drive(h.press("up"))
	}
	for range 200 {
		if v.heading == name {
			return
		}
		h.drive(h.press("down"))
	}
	h.t.Fatalf("no head %q", name)
}

func cursorText(h *harness) string {
	c := h.m.ticketsColumn()
	if c.Cursor < 0 {
		return ""
	}
	return c.Rows[c.Cursor].Primary.String()
}

func TestGShowsTheStatusesInTheProjectsOrderAndGoesBackWithoutARead(t *testing.T) {
	t.Parallel()
	h, _ := groupHarness(t)
	chronological := h.m.ticketsColumn().Rows
	if len(statusReads(h)) != 0 || heads(h.m.ticketsColumn()) != nil {
		t.Fatalf("a status was read before g: %v", statusReads(h))
	}
	h.drive(h.press("g"))
	want := []string{"▾ in_progress", "▾ new", "▸ resolved"}
	if got := heads(h.m.ticketsColumn()); !slices.Equal(got, want) {
		t.Fatalf("heads %q", got)
	}
	if got := statusReads(h); len(got) != 4 {
		t.Fatalf("one read per status: %v", got)
	}
	if !strings.Contains(h.m.screen().Context.String(), "Website") || strings.Contains(h.m.screen().Context.String(), "page") {
		t.Fatalf("the path: %q", h.m.screen().Context.String())
	}
	reads := len(h.f.ticketListReads())
	h.drive(h.press("g"))
	if h.m.app.tickets.isGrouped() || len(h.f.ticketListReads()) != reads {
		t.Fatalf("back: grouped %v, %d reads", h.m.app.tickets.isGrouped(), len(h.f.ticketListReads())-reads)
	}
	if !slices.EqualFunc(h.m.ticketsColumn().Rows, chronological, func(a, b present.Row) bool { return a.Primary == b.Primary && a.Trailing == b.Trailing }) {
		t.Fatal("the chronological list changed")
	}
	h.drive(h.press("g"))
	if len(h.f.ticketListReads()) != reads || !h.m.app.tickets.isGrouped() {
		t.Fatalf("a third g read: %d", len(h.f.ticketListReads())-reads)
	}
}

func TestFoldingFollowsTheFunctionsNeverTheName(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	s := &groupServer{h: h, statuses: []string{"Done", "fertig"}}
	s.add(strPtr("Done"), []string{"is_new"}, 1)
	s.add(strPtr("fertig"), []string{"is_closed"}, 1)
	h.f.setTickets(s.answer)
	h.openTickets(website)
	h.drive(h.press("g"))
	if got := heads(h.m.ticketsColumn()); !slices.Equal(got, []string{"▾ Done", "▸ fertig"}) {
		t.Fatalf("heads %q", got)
	}
}

func TestEnterAndEFoldAHeadAndTheCursorStaysOnIt(t *testing.T) {
	t.Parallel()
	h, _ := groupHarness(t)
	h.drive(h.press("g"))
	v := h.m.app.tickets
	first := v.selected
	if first == "" || v.heading != "" || !v.shows(first) {
		t.Fatalf("g moved the cursor off its ticket: %q %q", v.heading, first)
	}
	toHead(h, "in_progress")
	h.drive(h.press("enter"))
	if !v.folded(v.groups[0]) || v.heading != "in_progress" || h.m.focus != view.List {
		t.Fatalf("enter: folded %v, on %q", v.folded(v.groups[0]), v.heading)
	}
	h.drive(h.press("e"))
	if v.folded(v.groups[0]) {
		t.Fatal("e did not open it")
	}
	// A folded group's rows are not stepped on.
	h.drive(h.press("enter"))
	h.drive(h.press("down"))
	if v.heading != "new" {
		t.Fatalf("down from a folded head: %q %q", v.heading, v.selected)
	}
	toHead(h, "new")
	h.drive(h.press("down"))
	if cursorText(h) == "" || v.selected == "" {
		t.Fatalf("down into the group: %q", cursorText(h))
	}
	h.drive(h.press("enter"))
	if h.m.focus != view.Object || v.opened != v.selected {
		t.Fatalf("enter on a row: focus %v, opened %q", h.m.focus, v.opened)
	}
}

func TestACursorOnAHeadShowsNoTicket(t *testing.T) {
	t.Parallel()
	h, _ := groupHarness(t)
	h.drive(h.press("g"))
	v := h.m.app.tickets
	if v.selected == "" || !h.m.previewable() {
		t.Fatalf("on a row: %q", v.selected)
	}
	toHead(h, "new")
	if v.selected != "" || h.m.previewable() {
		t.Fatal("a head offers a preview")
	}
}

func TestAGroupPagesAloneAndTheOtherGroupsKeepTheirPage(t *testing.T) {
	t.Parallel()
	h, _ := groupHarness(t)
	h.drive(h.press("g"))
	v := h.m.app.tickets
	toHead(h, "new")
	first := v.group(v.groups[0].key)
	h.drive(h.press("]"))
	g := v.groups[1]
	if g.page() != 2 || len(g.rows) != 5 || first.page() != 1 || len(first.rows) != 2 || v.page != 1 {
		t.Fatalf("group page %d with %d rows, the first %d, the list %d", g.page(), len(g.rows), first.page(), v.page)
	}
	if got := statusReads(h); got[len(got)-1] != "new:2" || len(got) != 5 {
		t.Fatalf("reads %v", got)
	}
	if v.heading != "new" || !strings.Contains(rowOf(h, "new").Note.String(), "page 2 of 2") {
		t.Fatalf("the head: on %q, %q", v.heading, rowOf(h, "new").Note.String())
	}
	h.drive(h.press("]"))
	if g.page() != 2 {
		t.Fatal("beyond the last page")
	}
	h.drive(h.press("["))
	if g.page() != 1 {
		t.Fatalf("back: page %d", g.page())
	}
}

func rowOf(h *harness, status string) present.Row {
	for _, r := range h.m.ticketsColumn().Rows {
		if r.Heading && strings.HasSuffix(r.Primary.String(), " "+status) {
			return r
		}
	}
	return present.Row{}
}

func TestAnswersOfTheGroupsMayComeInAnyOrder(t *testing.T) {
	t.Parallel()
	h, _ := groupHarness(t)
	var msgs []tea.Msg
	msgs = append(msgs, messages(h.t, h.press("g"))...)
	slices.Reverse(msgs)
	h.drive(h.feed(msgs...)...)
	v := h.m.app.tickets
	for _, g := range v.groups {
		if !g.known {
			t.Fatalf("%s was lost", g.name)
		}
	}
	if len(v.groups[0].rows) != 2 || len(v.groups[1].rows) != 25 || v.groups[1].total != 30 {
		t.Fatalf("rows %d %d of %d", len(v.groups[0].rows), len(v.groups[1].rows), v.groups[1].total)
	}
}

func TestTheSearchGoesToEveryGroupAndFoldedStaysFolded(t *testing.T) {
	t.Parallel()
	h, _ := groupHarness(t)
	h.drive(h.press("g"))
	h.drive(h.press("/"))
	h.typeText("Row 3")
	h.drive(h.press("enter"))
	v := h.m.app.tickets
	if !v.isGrouped() || v.query != "Row 3" {
		t.Fatalf("grouped %v, query %q", v.isGrouped(), v.query)
	}
	for _, g := range v.groups {
		if g.key.Query != "Row 3" {
			t.Fatalf("%s without the search", g.name)
		}
	}
	// Row 3, 30, 31, 32 are in new and in_progress; 33 and 34 are resolved.
	if got := heads(h.m.ticketsColumn()); !slices.Equal(got, []string{"▾ in_progress", "▾ new", "▸ resolved"}) {
		t.Fatalf("heads %q", got)
	}
}

func TestRReadsEveryGroupAgain(t *testing.T) {
	t.Parallel()
	h, _ := groupHarness(t)
	h.drive(h.press("g"))
	before := len(statusReads(h))
	if !h.m.canRefreshSection() {
		t.Fatal("R is not offered")
	}
	h.drive(h.press("R"))
	if got := len(statusReads(h)) - before; got != 4 {
		t.Fatalf("%d groups read again", got)
	}
}

func TestAFailedGroupSaysSoAndTheOthersStayUsable(t *testing.T) {
	t.Parallel()
	for name, fail := range map[string]transport.Answer{
		"temporary": failing(transport.Temporary, 503),
		"refused":   failing(transport.Refused, 422),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h, s := groupHarness(t)
			h.f.setTickets(func(q api.TicketQuery) transport.Answer {
				if q.Status == "new" {
					return fail
				}
				return s.answer(q)
			})
			h.drive(h.press("g"))
			v := h.m.app.tickets
			if got := rowOf(h, "new"); !got.Heading || !strings.Contains(got.Note.String(), " · ") {
				t.Fatalf("the failed group: %+v", got)
			}
			if len(v.groups[0].rows) != 2 {
				t.Fatal("the other groups")
			}
			if r := h.m.ticketsColumn().Rows; strings.Contains(r[len(r)-1].Primary.String(), "without a listed") {
				t.Fatal("a failed group counts as rest")
			}
			h.drive(h.press("down"))
			if v.selected == "" {
				t.Fatal("the cursor does not move")
			}
		})
	}
}

func TestAShrunkGroupIsCorrectedOnceAndLeavesTheOthersAlone(t *testing.T) {
	t.Parallel()
	h, s := groupHarness(t)
	h.drive(h.press("g"))
	v := h.m.app.tickets
	toHead(h, "new")
	h.drive(h.press("]"))
	g := v.groups[1]
	s.rows = slices.DeleteFunc(s.rows, func(r api.TicketRow) bool { return r.Number > 10 && r.Number <= 30 })
	h.drive(h.press("R"))
	if g.page() != 1 || len(g.rows) != 10 || g.err.String() != "" {
		t.Fatalf("page %d, %d rows, %q", g.page(), len(g.rows), g.err.String())
	}
	if !v.groups[0].known || len(v.groups[0].rows) != 2 {
		t.Fatal("the other group went with it")
	}
	if !v.known {
		t.Fatal("the list was lost")
	}
}

func TestARemovedProjectTakesEveryGroup(t *testing.T) {
	t.Parallel()
	h, s := groupHarness(t)
	h.drive(h.press("g"))
	v := h.m.app.tickets
	key := v.groups[0].key
	h.f.setTickets(func(q api.TicketQuery) transport.Answer {
		if q.Status == "new" {
			return failing(transport.NotFound, 404)
		}
		return s.answer(q)
	})
	h.drive(h.press("R"))
	if v.grouped || v.groups != nil || v.err.String() == "" {
		t.Fatalf("grouped %v, %d groups, %q", v.grouped, len(v.groups), v.err.String())
	}
	if _, ok := h.m.app.store.Get(key); ok {
		t.Fatal("another group's page stayed cached")
	}
}

func TestTheRestStandsAtTheEndOnceAllHaveLandedAndIsNoEntry(t *testing.T) {
	t.Parallel()
	h, s := groupHarness(t)
	s.add(nil, nil, 3)
	s.add(strPtr("old_status"), nil, 1)
	h.drive(h.press("R"))
	msgs := messages(h.t, h.press("g"))
	var last tea.Msg
	last, msgs = msgs[len(msgs)-1], msgs[:len(msgs)-1]
	h.drive(h.feed(msgs...)...)
	rows := h.m.ticketsColumn().Rows
	if len(rows) > 0 && strings.Contains(rows[len(rows)-1].Primary.String(), "without a listed") {
		t.Fatal("a rest before all groups landed")
	}
	h.drive(h.feed(last)...)
	c := h.m.ticketsColumn()
	end := c.Rows[len(c.Rows)-1].Primary.String()
	if end != "4 more without a listed status · g chronological" {
		t.Fatalf("the rest %q", end)
	}
	for range 80 {
		h.drive(h.press("down"))
	}
	c = h.m.ticketsColumn()
	if c.Cursor != len(c.Rows)-2 {
		t.Fatalf("the cursor %d of %d rows", c.Cursor, len(c.Rows))
	}
	if !strings.Contains(ansi.Strip(h.m.View().Content), "without a listed status") {
		t.Fatal("the rest does not come into sight at the end")
	}
}

func TestAProjectWithoutStatusesStaysChronologicalWithAHint(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	s := &groupServer{h: h, statuses: []string{}}
	s.add(strPtr("new"), nil, 2)
	h.f.setTickets(s.answer)
	h.openTickets(website)
	if h.m.ticketsColumn().Switch != nil {
		t.Fatal("a mode without modes")
	}
	h.drive(h.press("g"))
	v := h.m.app.tickets
	text, tone := h.m.status()
	if v.grouped || tone != present.Plain || !strings.Contains(text.String(), "no statuses") {
		t.Fatalf("grouped %v, %q", v.grouped, text.String())
	}
	h.now = h.now.Add(hintFor + time.Second)
	if text, _ := h.m.status(); text.String() != "" {
		t.Fatalf("the hint stayed: %q", text.String())
	}
}

func TestGActsOnlyOnTheList(t *testing.T) {
	t.Parallel()
	h, _ := groupHarness(t)
	h.m.focus = view.Navigation
	h.press("g")
	if h.m.app.tickets.grouped {
		t.Fatal("g in the chooser")
	}
	h.m.focus = view.Object
	h.press("g")
	if h.m.app.tickets.grouped {
		t.Fatal("g in the ticket")
	}
}

func TestTheFrameNamesTheMode(t *testing.T) {
	t.Parallel()
	h, _ := groupHarness(t)
	h.m.width = 170
	frame := func() string { return ansi.Strip(h.m.View().Content) }
	if !strings.Contains(frame(), "Tickets  g grouped [chronological]") {
		t.Fatalf("chronological:\n%s", frame())
	}
	h.drive(h.press("g"))
	if !strings.Contains(frame(), "Tickets  g [grouped] chronological") {
		t.Fatalf("grouped:\n%s", frame())
	}
}

func TestEveryLineIsTheTerminalsWidthInEveryLayout(t *testing.T) {
	t.Parallel()
	h, _ := groupHarness(t)
	h.drive(h.press("g"))
	for _, width := range []int{60, 80, 120, 170} {
		h.m.width = width
		for _, focus := range []view.Pane{view.List, view.Object} {
			h.m.focus = focus
			for i, line := range strings.Split(h.m.View().Content, "\n") {
				if !strings.ContainsAny(line, "\u2502\u256d\u2570") {
					continue
				}
				if got := ansi.StringWidth(line); got != width {
					t.Fatalf("width %d, focus %v, line %d is %d: %q", width, focus, i, got, ansi.Strip(line))
				}
			}
		}
	}
}

func TestAHostileStatusIsCleaned(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	evil := "ne\u202ew\x07"
	s := &groupServer{h: h, statuses: []string{evil}}
	s.add(strPtr(evil), nil, 1)
	h.f.setTickets(s.answer)
	h.openTickets(website)
	h.f.setTickets(func(q api.TicketQuery) transport.Answer { q.Status = ""; return s.answer(q) })
	h.drive(h.press("g"))
	screen := h.m.View().Content
	if strings.ContainsAny(screen, "\u202e\x07") {
		t.Fatalf("not cleaned: %q", screen)
	}
}

// baseFirst feeds the list's own answer before the groups', the order that
// makes a shrink of the list land while their reads are out.
func baseFirst(msgs []tea.Msg) []tea.Msg {
	slices.SortStableFunc(msgs, func(a, b tea.Msg) int {
		rank := func(m tea.Msg) int {
			if r, ok := m.(gateway.TicketsResult); ok && r.Key.Status == "" {
				return 0
			}
			return 1
		}
		return rank(a) - rank(b)
	})
	return msgs
}

// A ticket that moved stands in two groups while one of them is old. It is
// drawn once, where the newer read has it, and the cursor passes it.
func TestATicketInTwoGroupsIsDrawnOnceAndTheCursorPassesIt(t *testing.T) {
	t.Parallel()
	h, s := groupHarness(t)
	h.drive(h.press("g"))
	moved := s.rows[30] // the first in_progress
	done := "new"
	s.rows[30].Status, s.rows[30].StatusFunctions = &done, []string{"is_new"}
	s.rows = append(s.rows[:30:30], append([]api.TicketRow{}, s.rows[30:]...)...)
	slices.SortStableFunc(s.rows, func(a, b api.TicketRow) int { return b.Number - a.Number })
	answer := s.answer
	h.f.setTickets(func(q api.TicketQuery) transport.Answer {
		if q.Status == "in_progress" {
			return failing(transport.Temporary, 503)
		}
		return answer(q)
	})
	h.drive(h.press("R"))
	lines, count := h.m.app.tickets.entries(), 0
	for _, l := range lines {
		if l.row != groupHead && l.group.rows[l.row].ID == moved.ID {
			count++
			if l.group.name != "new" {
				t.Fatalf("drawn in %q", l.group.name)
			}
		}
	}
	if count != 1 {
		t.Fatalf("the ticket stands %d times", count)
	}
	seen := map[int]bool{}
	for range 60 {
		h.drive(h.press("down"))
		seen[h.m.ticketsColumn().Cursor] = true
	}
	if len(seen) < len(lines)-3 {
		t.Fatalf("the cursor reached %d of %d lines", len(seen), len(lines))
	}
}

func TestAGroupWhoseReloadFailedSaysItMayBeOld(t *testing.T) {
	t.Parallel()
	h, s := groupHarness(t)
	h.drive(h.press("g"))
	h.f.setTickets(func(q api.TicketQuery) transport.Answer {
		if q.Status == "new" {
			return failing(transport.Temporary, 503)
		}
		return s.answer(q)
	})
	h.drive(h.press("R"))
	if note := rowOf(h, "new").Note.String(); !strings.Contains(note, "stale") {
		t.Fatalf("the head says %q", note)
	}
	if note := rowOf(h, "in_progress").Note.String(); strings.Contains(note, "stale") {
		t.Fatalf("the other head says %q", note)
	}
}

// The chronological list was read again meanwhile: the groups are older
// than its total, and g reads them.
func TestGAfterAChronologicalReloadReadsTheGroupsAgain(t *testing.T) {
	t.Parallel()
	h, s := groupHarness(t)
	h.drive(h.press("g"))
	h.drive(h.press("g"))
	s.add(strPtr("new"), []string{"is_new"}, 5)
	h.drive(h.press("R"))
	h.drive(h.press("g"))
	c := h.m.ticketsColumn()
	if end := c.Rows[len(c.Rows)-1].Primary.String(); strings.Contains(end, "without a listed") {
		t.Fatalf("a rest of old groups: %q", end)
	}
	if !strings.Contains(rowOf(h, "new").Note.String(), "35") {
		t.Fatalf("the head: %q", rowOf(h, "new").Note.String())
	}
}

func TestAShrinkOfTheListReadsTheGroupsAgain(t *testing.T) {
	t.Parallel()
	shrink := func(t *testing.T, keep int) (*harness, []tea.Cmd) {
		t.Helper()
		h, s := groupHarness(t)
		h.drive(h.press("]"))
		h.drive(h.press("g"))
		s.rows = s.rows[:keep]
		return h, h.feed(baseFirst(messages(h.t, h.press("R")))...)
	}
	t.Run("to nothing", func(t *testing.T) {
		t.Parallel()
		h, cmds := shrink(t, 0)
		h.drive(cmds...)
		if v := h.m.app.tickets; v.groups[1].total != 0 || v.groups[1].loading {
			t.Fatalf("the group kept %d", v.groups[1].total)
		}
	})
	t.Run("to one page", func(t *testing.T) {
		t.Parallel()
		h, cmds := shrink(t, 10)
		h.drive(cmds...)
		if v := h.m.app.tickets; v.page != 1 || v.groups[1].total != 10 {
			t.Fatalf("page %d, the group kept %d", v.page, v.groups[1].total)
		}
	})
	t.Run("a second time", func(t *testing.T) {
		t.Parallel()
		h, s := groupHarness(t)
		s.add(strPtr("new"), []string{"is_new"}, 40) // 74 rows, three pages
		h.drive(h.press("R"))
		h.drive(h.press("]"))
		h.drive(h.press("]"))
		h.drive(h.press("g"))
		s.rows = s.rows[:30]
		correction := h.feed(baseFirst(messages(h.t, h.press("R")))...)
		s.rows = s.rows[:10]
		h.drive(correction...)
		v := h.m.app.tickets
		if v.err.String() == "" || v.groups[1].total != 10 {
			t.Fatalf("%q, the group kept %d", v.err.String(), v.groups[1].total)
		}
	})
}

// The list's page is out of sight while grouped: its shrink closes nothing.
func TestAShrinkOfTheHiddenPageLeavesTheOpenTicket(t *testing.T) {
	t.Parallel()
	h, s := groupHarness(t)
	h.drive(h.press("]"))
	h.drive(h.press("g"))
	v := h.m.app.tickets
	toHead(h, "new")
	h.drive(h.press("down"))
	h.drive(h.press("enter"))
	opened := v.opened
	if opened == "" {
		t.Fatal("no ticket opened")
	}
	h.m.focus = view.List
	s.rows = slices.DeleteFunc(s.rows, func(r api.TicketRow) bool { return r.Number > 2 && r.Number <= 30 })
	h.drive(h.press("R"))
	if v.opened != opened || v.page != 1 {
		t.Fatalf("opened %q, page %d", v.opened, v.page)
	}
}

func TestTurningAGroupKeepsATicketOfAnotherGroupOpen(t *testing.T) {
	t.Parallel()
	h, s := groupHarness(t)
	s.add(strPtr("in_progress"), []string{"is_in_progress"}, 0)
	h.drive(h.press("g"))
	v := h.m.app.tickets
	id := v.groups[1].rows[0].ID
	other := v.groups[0].rows[0].ID
	v.opened, v.selected = other, other
	_ = id
	toHead(h, "new")
	v.opened = other
	h.drive(h.press("]"))
	if v.opened != other {
		t.Fatal("the other group's ticket closed")
	}
	v.opened = v.groups[1].rows[0].ID
	h.drive(h.press("["))
	if v.opened != "" {
		t.Fatal("the ticket of the page that left stayed open")
	}
}

func TestARestOfEverythingDoesNotSayThereAreNoTickets(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	s := &groupServer{h: h, statuses: []string{"new"}}
	s.add(nil, nil, 3)
	h.f.setTickets(s.answer)
	h.openTickets(website)
	h.drive(h.press("g"))
	c := h.m.ticketsColumn()
	if c.Notice.String() != "" || !strings.Contains(c.Rows[len(c.Rows)-1].Primary.String(), "3 more") {
		t.Fatalf("notice %q, %d rows", c.Notice.String(), len(c.Rows))
	}
}

func TestASecondShrinkOfAGroupStopsAndROffersAgain(t *testing.T) {
	t.Parallel()
	h, s := groupHarness(t)
	s.add(strPtr("new"), []string{"is_new"}, 40) // new: 70, three pages
	h.drive(h.press("R"))
	h.drive(h.press("g"))
	v := h.m.app.tickets
	toHead(h, "new")
	h.drive(h.press("]"))
	h.drive(h.press("]"))
	g := v.groups[1]
	keep := func(n int) {
		kept := 0
		s.rows = slices.DeleteFunc(s.rows, func(r api.TicketRow) bool {
			if *r.Status != "new" {
				return false
			}
			kept++
			return kept > n
		})
	}
	keep(30)
	correction := h.feed(messages(h.t, h.press("R"))...)
	keep(10)
	h.drive(correction...)
	if g.page() != 2 || g.err.String() == "" || len(g.rows) != 0 {
		t.Fatalf("page %d, %q, %d rows", g.page(), g.err.String(), len(g.rows))
	}
	h.drive(h.press("R"))
	if g.page() != 1 || g.err.String() != "" || len(g.rows) != 10 {
		t.Fatalf("after R: page %d, %q, %d rows", g.page(), g.err.String(), len(g.rows))
	}
}

func TestTwoSpellingsOfAStatusAreOneGroup(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	s := &groupServer{h: h, statuses: []string{"New", "new", ""}}
	s.add(strPtr("new"), nil, 2)
	h.f.setTickets(s.answer)
	h.openTickets(website)
	h.drive(h.press("g"))
	if got := statusReads(h); len(got) != 1 {
		t.Fatalf("reads %v", got)
	}
	if c := h.m.ticketsColumn(); strings.Contains(c.Rows[len(c.Rows)-1].Primary.String(), "without a listed") {
		t.Fatal("a rest of none")
	}
}

func TestAGroupInMemoryShowsAtOnce(t *testing.T) {
	t.Parallel()
	h, _ := groupHarness(t)
	h.drive(h.press("g"))
	h.drive(h.press("esc"))
	h.drive(h.openProject(website))
	cmd := h.press("g")
	v := h.m.app.tickets
	if len(v.groups) == 0 || !v.groups[0].known || !v.groups[0].cached {
		t.Fatalf("not at once: %+v", v.groups)
	}
	h.drive(cmd)
}

func TestReloadingDoesNotReadAGroupTwice(t *testing.T) {
	t.Parallel()
	h, _ := groupHarness(t)
	h.drive(h.press("g"))
	before := len(statusReads(h))
	first := h.press("R")
	second := h.press("R")
	h.drive(first, second)
	if got := len(statusReads(h)) - before; got != 4 {
		t.Fatalf("%d reads for two R", got)
	}
}

func TestAnOpenTicketThatLeftEveryGroupCloses(t *testing.T) {
	t.Parallel()
	h, s := groupHarness(t)
	h.drive(h.press("g"))
	v := h.m.app.tickets
	id := v.selected
	v.opened = id
	s.rows = slices.DeleteFunc(s.rows, func(r api.TicketRow) bool { return r.ID == id })
	h.drive(h.press("R"))
	if v.opened != "" || v.selected == id {
		t.Fatalf("opened %q", v.opened)
	}
}

func TestGBackLeavesTheHead(t *testing.T) {
	t.Parallel()
	h, _ := groupHarness(t)
	h.drive(h.press("g"))
	toHead(h, "new")
	h.drive(h.press("g"))
	if v := h.m.app.tickets; v.heading != "" || v.selected == "" {
		t.Fatalf("heading %q, selected %q", v.heading, v.selected)
	}
}

func TestAGroupsTitlesAreKnownToTheAgentsLine(t *testing.T) {
	t.Parallel()
	h, _ := groupHarness(t)
	h.drive(h.press("]"))
	h.drive(h.press("g"))
	v := h.m.app.tickets
	id := v.groups[0].rows[0].ID // on page 1 of the list at no time
	text, _, ok := h.m.app.viewTitle(id)
	if !ok || text != v.groups[0].rows[0].Title {
		t.Fatalf("%q %v", text, ok)
	}
}

func TestGroupKeysAreOfferedExactlyWhereTheyAct(t *testing.T) {
	t.Parallel()
	h, _ := groupHarness(t)
	h.drive(h.press("g"))
	has := func(kind Action) bool { return h.m.allowed()[kind] }
	toHead(h, "new")
	if !has(OpenList) || !has(Expand) || !has(NextPage) || has(PrevPage) {
		t.Fatalf("on a head: %v", h.m.allowed())
	}
	h.drive(h.press("]"))
	if !has(PrevPage) || has(NextPage) {
		t.Fatalf("on the last page: %v", h.m.allowed())
	}
	h.drive(h.press("enter"))
	if has(NextPage) || has(PrevPage) {
		t.Fatalf("folded: %v", h.m.allowed())
	}
	h.drive(h.press("enter"))
	h.drive(h.press("down"))
	if has(Expand) && h.m.app.detail == nil {
		t.Fatalf("on a row: %v", h.m.allowed())
	}
}

// The rest is a difference of two counts, so both must come from one round
// of reads: not while the list's read is out, not after a read failed.
func TestTheRestWaitsForCountsOfOneRound(t *testing.T) {
	t.Parallel()
	endsInRest := func(h *harness) bool {
		c := h.m.ticketsColumn()
		return strings.Contains(c.Rows[len(c.Rows)-1].Primary.String(), "without a listed")
	}
	t.Run("the list's read is out", func(t *testing.T) {
		t.Parallel()
		h, s := groupHarness(t)
		h.drive(h.press("g"))
		s.rows = s.rows[:len(s.rows)-1]
		msgs := baseFirst(messages(h.t, h.press("R")))
		h.drive(h.feed(msgs[1:]...)...) // every group, the list still out
		if endsInRest(h) {
			t.Fatal("a rest while the list's read is out")
		}
		h.drive(h.feed(msgs[0])...)
		if endsInRest(h) {
			t.Fatal("a rest of none")
		}
	})
	t.Run("a group's read failed", func(t *testing.T) {
		t.Parallel()
		h, s := groupHarness(t)
		h.drive(h.press("g"))
		s.add(strPtr("new"), []string{"is_new"}, 1)
		h.f.setTickets(func(q api.TicketQuery) transport.Answer {
			if q.Status == "new" {
				return failing(transport.Temporary, 503)
			}
			return s.answer(q)
		})
		h.drive(h.press("R"))
		if endsInRest(h) {
			t.Fatal("a rest beside a group that may be old")
		}
	})
	t.Run("the groups' reads are out", func(t *testing.T) {
		t.Parallel()
		h, s := groupHarness(t)
		h.drive(h.press("g"))
		s.add(strPtr("new"), []string{"is_new"}, 1)
		msgs := baseFirst(messages(h.t, h.press("R")))
		h.drive(h.feed(msgs[0])...) // the list alone
		if endsInRest(h) {
			t.Fatal("a rest of a new total and old groups")
		}
	})
	t.Run("the list was refused", func(t *testing.T) {
		t.Parallel()
		h, s := groupHarness(t)
		h.drive(h.press("g"))
		s.rows = s.rows[:len(s.rows)-1]
		h.f.setTickets(func(q api.TicketQuery) transport.Answer {
			if q.Status == "" {
				return failing(transport.Refused, 422)
			}
			return s.answer(q)
		})
		h.drive(h.press("R"))
		if endsInRest(h) {
			t.Fatal("a rest from a total the server refused to renew")
		}
	})
	t.Run("a group was refused", func(t *testing.T) {
		t.Parallel()
		h, s := groupHarness(t)
		h.drive(h.press("g"))
		s.add(strPtr("new"), []string{"is_new"}, 1)
		h.f.setTickets(func(q api.TicketQuery) transport.Answer {
			if q.Status == "new" {
				return failing(transport.Refused, 422)
			}
			return s.answer(q)
		})
		h.drive(h.press("R"))
		if endsInRest(h) {
			t.Fatal("a rest beside a refused group")
		}
	})
	t.Run("the list's read failed", func(t *testing.T) {
		t.Parallel()
		h, s := groupHarness(t)
		h.drive(h.press("g"))
		s.rows = s.rows[:len(s.rows)-1]
		h.f.setTickets(func(q api.TicketQuery) transport.Answer {
			if q.Status == "" {
				return failing(transport.Temporary, 503)
			}
			return s.answer(q)
		})
		h.drive(h.press("R"))
		if endsInRest(h) {
			t.Fatal("a rest from an old total")
		}
	})
}

// A status that left the project takes its group, with the cursor on it:
// the pane follows the cursor, whichever answer lands last.
func TestAGroupThatGoesTakesThePreviewWithIt(t *testing.T) {
	t.Parallel()
	h, s := groupHarness(t)
	h.drive(h.press("g"))
	v := h.m.app.tickets
	toHead(h, "in_progress")
	h.drive(h.press("down"))
	h.m.app.ensureDetail(website, v.selected)
	s.statuses = []string{"new", "resolved", "hold"}
	msgs := baseFirst(messages(h.t, h.press("R")))
	h.drive(h.feed(append(msgs[1:], msgs[0])...)...) // the list last
	if d := h.m.app.detail; d != nil && d.id != v.selected {
		t.Fatalf("the pane shows %q, the cursor %q %q", d.id, v.selected, v.heading)
	}
}

// A shrink of the list and a new status at once: the new group is read once,
// after the correction, whose cancelling would lose a read begun before it.
func TestANewStatusWithAShrinkIsReadOnce(t *testing.T) {
	t.Parallel()
	h, s := groupHarness(t)
	h.drive(h.press("]"))
	h.drive(h.press("g"))
	s.rows = s.rows[:10]
	s.statuses = append(s.statuses, "fresh")
	s.add(strPtr("fresh"), []string{"is_new"}, 1)
	h.drive(h.feed(baseFirst(messages(h.t, h.press("R")))...)...)
	n := 0
	for _, r := range statusReads(h) {
		if strings.HasPrefix(r, "fresh:") {
			n++
		}
	}
	v := h.m.app.tickets
	if n != 1 || v.page != 1 || v.groups[len(v.groups)-1].total != 1 {
		t.Fatalf("%d reads, page %d, %d groups", n, v.page, len(v.groups))
	}
}

// The last status left the project: the list falls back to chronological,
// the pane follows the cursor, and the hint has its end.
func TestWhenTheLastStatusGoesThePaneFollowsAndTheHintEnds(t *testing.T) {
	t.Parallel()
	h, s := groupHarness(t)
	h.drive(h.press("g"))
	v := h.m.app.tickets
	toHead(h, "in_progress")
	h.drive(h.press("down"))
	id := v.selected
	h.m.app.ensureDetail(website, id)
	s.statuses = []string{}
	h.drive(h.press("R"))
	if v.grouped || v.selected == id {
		t.Fatalf("grouped %v, selected %q", v.grouped, v.selected)
	}
	if d := h.m.app.detail; d != nil && d.id != v.selected {
		t.Fatalf("the pane shows %q, the cursor %q", d.id, v.selected)
	}
}

// Every ticket lacks a status and a group's read failed: there is no rest to
// tell, and no claim of an empty list either.
func TestAFailedEmptyGroupDoesNotSayThereAreNoTickets(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	s := &groupServer{h: h, statuses: []string{"new"}}
	s.add(nil, nil, 3)
	h.f.setTickets(s.answer)
	h.openTickets(website)
	h.drive(h.press("g"))
	h.f.setTickets(func(q api.TicketQuery) transport.Answer {
		if q.Status == "new" {
			return failing(transport.Temporary, 503)
		}
		return s.answer(q)
	})
	h.drive(h.press("R"))
	if got := h.m.ticketsColumn().Notice.String(); strings.Contains(got, "No tickets") {
		t.Fatalf("the notice %q", got)
	}
}

func strPtr(s string) *string { return &s }

// A group's page kept on the disk is shown while its reload is out, like
// the list's.
func TestAGroupComesBackFromTheDisk(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	h := newDiskHarness(t, root, nil)
	s := &groupServer{h: h, statuses: []string{"new"}}
	s.add(strPtr("new"), []string{"is_new"}, 2)
	h.f.setTickets(s.answer)
	h.openTickets(website)
	h.drive(h.press("g"))
	h2 := h.restart(root)
	h2.f.setTickets(func(q api.TicketQuery) transport.Answer {
		if q.Status != "" {
			return failing(transport.Temporary, 503)
		}
		return s.answer(q)
	})
	h2.openTickets(website)
	h2.drive(h2.press("g"))
	g := h2.m.app.tickets.groups
	if len(g) != 1 || !g[0].known || !g[0].cached || len(g[0].rows) != 2 {
		t.Fatalf("the group did not come from the disk: %+v", g)
	}
}
