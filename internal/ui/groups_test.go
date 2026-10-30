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
	if g.page() != 2 || len(g.rows) != 5 || first.page() != 1 || v.page != 1 {
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
			if got := rowOf(h, "new"); !got.Heading || got.Note.String() == "" {
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
