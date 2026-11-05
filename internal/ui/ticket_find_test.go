package ui

import (
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/agents"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/diskcache"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

// F2: the find in the ticket, as the meeting's (F1), over the description,
// the comments of every page and the files: the other pages of comments
// read one after the other with the pane's own key, n turning the pane to
// a hit there.

// The keys of findSix's texts beside the comments' numbers.
const (
	findDescription = 0  // the description as a page sends it
	findWhole       = -1 // the description read whole
)

// findSix answers ticket #6 of Website with n comments (numbered oldest
// first; a page holds 25, newest first) and texts in the comments numbered
// and the description; the rest as the harness has it.
func (h *harness) findSix(n int, texts map[int]string) {
	h.f.setTicket(func(project, id string, o api.TicketOptions) transport.Answer {
		if id != ticketID(website, 6) || o.PostID != "" {
			return h.d.defaultTicket(project, id, o)
		}
		d := h.d
		d.sixComments = n
		t := d.ticketRecord(project, 6, o)
		if text, ok := texts[findDescription]; ok {
			t.DescriptionText = text
		}
		if text, ok := texts[findWhole]; ok && o.FullText {
			t.DescriptionText = text
		}
		for i := range t.Posts {
			for c, text := range texts {
				if c > 0 && t.Posts[i].ID == sixPostID(c) {
					t.Posts[i].BodyText = text
				}
			}
		}
		return ticketAnswer(t)
	})
}

// findReads is the number of the find's reads out now.
func (h *harness) findReads() int {
	n := 0
	for _, w := range h.m.app.waiters {
		if w.purpose == forFindPage && !w.canceled {
			n++
		}
	}
	return n
}

// readsSince are the options of the ticket reads after the first n.
func (h *harness) readsSince(n int) []api.TicketOptions {
	var out []api.TicketOptions
	for _, r := range h.f.detailReads()[n:] {
		out = append(out, r.o)
	}
	return out
}

// /, Enter, n, N and Esc in the ticket as in the meeting: the line takes
// every key as text, the hits are marked and counted, n and N go round
// the ends, the first Esc ends the find where the pane stands.
func TestTheFindInTheTicketTakesTheMeetingsKeys(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.findSix(30, map[int]string{30: "Kiwi vorne", 28: "Kiwi und noch eine kiwi"})
	h.openSix()
	if !h.offers(Search) || !strings.Contains(h.helpText(), "/ find") || h.offers(NextHit) {
		t.Fatalf("help %q", h.helpText())
	}
	h.press("/")
	for _, k := range []string{"q", "e", "o", "]", "R", "k"} {
		if cmd := h.press(k); cmd != nil || h.m.dialog != nil || h.m.mode != FindEntry {
			t.Fatalf("%s acted", k)
		}
	}
	if got := h.paneNotice(); got != "Find: qeo]Rk_" {
		t.Fatalf("notice %q", got)
	}
	h.find("kiwi")
	if cur, others := h.allMarks(); h.paneNotice() != "1 of 3" || !slices.Equal(cur, []string{"Kiwi"}) || !slices.Equal(others, []string{"Kiwi", "kiwi"}) || !h.offers(NextHit) {
		t.Fatalf("notice %q, current %q, others %q", h.paneNotice(), cur, others)
	}
	for _, want := range []string{"2 of 3", "3 of 3", "1 of 3, from the top"} {
		h.press("n")
		if got := h.paneNotice(); got != want {
			t.Fatalf("n: %q, want %q", got, want)
		}
	}
	if h.press("N"); h.paneNotice() != "3 of 3, from the bottom" {
		t.Fatalf("N: %q", h.paneNotice())
	}
	if !strings.Contains(h.helpText(), "esc end find") {
		t.Fatalf("help %q", h.helpText())
	}
	top, _ := h.paneTop()
	h.press("esc")
	if cur, others := h.allMarks(); h.paneNotice() != "" || len(cur)+len(others) > 0 || h.m.focus != view.Object {
		t.Fatalf("after esc: notice %q, marks %q %q, focus %d", h.paneNotice(), cur, others, h.m.focus)
	}
	if now, _ := h.paneTop(); now != top {
		t.Fatalf("esc moved the pane from %d to %d", top, now)
	}
	if h.press("esc"); h.m.focus != view.List {
		t.Fatalf("the second esc: focus %d", h.m.focus)
	}
}

// A hit deep in a long comment is in sight, not only the comment's head;
// a hit behind the pane's cut of a comment (8,000 characters) on another
// page is not counted: the find takes the lines the pane would draw.
func TestAHitDeepInALongCommentIsInSight(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.findSix(30, map[int]string{
		29: strings.Repeat("Wort ", 1000) + "Kiwi am Ende.",
		3:  strings.Repeat("Wort ", 1700) + "Kiwi hinter dem Schnitt.",
	})
	h.openSix()
	h.find("kiwi")
	if cur, _ := h.inSight(); h.paneNotice() != "1 of 1" || !slices.Equal(cur, []string{"Kiwi"}) {
		t.Fatalf("notice %q, in sight %q", h.paneNotice(), cur)
	}
}

// From page 1 the hits of page 3 are found and counted, the other pages
// read one after the other with the pane's key (R while one is out starts
// no second), the pane left on page 1 (scrolled to its end: no hit below,
// the first is the current one); n turns it to page 3 from memory, the hit
// in sight, without a read.
func TestTheFindReadsTheOtherPagesOneByOne(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.findSix(60, map[int]string{50: "Kiwi auf Seite eins", 5: "Kiwi auf Seite drei", 2: "noch eine Kiwi"})
	h.openSix()
	h.scrollDown(-1)
	before := len(h.f.detailReads())
	h.typeFind("kiwi")
	for cmds := []tea.Cmd{h.press("enter"), h.press("R")}; len(cmds) > 0; {
		if n := h.findReads(); n > 1 {
			t.Fatalf("%d pages out at once", n)
		}
		var next []tea.Cmd
		for _, c := range cmds {
			next = append(next, h.feed(messages(h.t, c)...)...)
		}
		cmds = next
	}
	// R read the page shown again, beside the find's pages.
	if got := slices.DeleteFunc(h.readsSince(before), func(o api.TicketOptions) bool { return o == first }); !slices.Equal(got, []api.TicketOptions{paged(2, true, 1), paged(3, true, 1)}) {
		t.Fatalf("reads %+v", got)
	}
	d := h.m.app.detail
	if h.paneNotice() != "1 of 3, from the top" || d.key.PostsPage != 1 || d.shown.PostsPage != 1 || d.record.Posts[0].ID != sixPostID(60) {
		t.Fatalf("notice %q, the pane on page %d of %d", h.paneNotice(), d.shown.PostsPage, d.key.PostsPage)
	}
	for _, want := range []string{"2 of 3", "3 of 3"} {
		h.press("n")
		if cur, _ := h.inSight(); h.paneNotice() != want || d.shown.PostsPage != 3 || !slices.Equal(cur, []string{"Kiwi"}) {
			t.Fatalf("n: notice %q, page %d, in sight %q", h.paneNotice(), d.shown.PostsPage, cur)
		}
	}
	if h.press("n"); h.paneNotice() != "1 of 3, from the top" || d.shown.PostsPage != 1 {
		t.Fatalf("round the end: %q, page %d", h.paneNotice(), d.shown.PostsPage)
	}
	if n := len(h.f.detailReads()) - before; n != 3 {
		t.Fatalf("n read %d pages", n-3)
	}
}

// openSixOnDisk opens ticket #6 in a session on the disk cache, and with
// fill its other two pages of comments first, so the disk holds them.
func (h *harness) openSixOnDisk(fill bool) {
	h.t.Helper()
	h.openTickets(website)
	h.m.app.tickets.selected = ticketID(website, 6)
	h.drive(h.press("enter"))
	if fill {
		h.press("tab")
		for _, k := range []string{"]", "]", "[", "["} {
			h.drive(h.press(k))
		}
	}
}

// Pages the memory holds, or the disk after a restart, are not read.
func TestTheFindTakesPagesFromMemoryAndDisk(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	h := newDiskHarness(t, root, nil)
	h.findSix(60, map[int]string{5: "Kiwi"})
	h.openSixOnDisk(true)
	find := func(name string, h *harness) {
		before := len(h.f.detailReads())
		h.find("kiwi")
		if got := h.paneNotice(); got != "1 found" || len(h.f.detailReads()) != before {
			t.Fatalf("%s: notice %q after %d reads", name, got, len(h.f.detailReads())-before)
		}
	}
	find("memory", h)
	h = h.restart(root)
	h.openSixOnDisk(false)
	find("disk", h)
}

// A disk that fails while it reads a page of the find leaves no page
// waiting: the find goes on over the server.
func TestAFailingDiskLeavesNoPageOfTheFindWaiting(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	var fail atomic.Bool
	o := &diskcache.Opener{Root: root, Faults: func(step, _ string) error {
		if fail.Load() && step == "open" {
			return errors.New("injected read failure")
		}
		return nil
	}}
	h := buildHarness(t, nil, o)
	h.findSix(60, map[int]string{50: "Kiwi", 5: "Kiwi"})
	h.openSixOnDisk(true)
	h.press("q")
	h = buildHarness(t, h.f, o)
	h.findSix(60, map[int]string{50: "Kiwi", 5: "Kiwi"})
	h.openSixOnDisk(false)
	fail.Store(true)
	h.find("kiwi")
	if got := h.paneNotice(); got != "1 of 2" || !h.m.app.disk.off {
		t.Fatalf("notice %q, the disk off: %v", got, h.m.app.disk.off)
	}
}

// Leaving the ticket, a new find and Esc cancel the page out; its late
// answer changes nothing.
func TestThePageOutIsCanceledWhenTheFindEnds(t *testing.T) {
	t.Parallel()

	for name, end := range map[string]func(h *harness){
		"leaving the ticket": func(h *harness) { h.press("left"); h.press("esc") },
		"a new find":         func(h *harness) { h.typeFind("pflaume"); h.press("enter") },
		"esc":                func(h *harness) { h.press("esc") },
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.findSix(60, map[int]string{5: "Kiwi"})
			h.openSix()
			before := len(h.f.detailReads())
			gate := make(chan struct{})
			defer close(gate)
			h.f.setGate(gate, ticketID(website, 6), false)
			h.typeFind("kiwi")
			held := h.background(h.press("enter"))
			h.waitFor("the page on the wire", func() bool { return len(h.f.detailReads()) == before+1 })
			end(h)
			notice := h.paneNotice()
			r := h.receive(held).(gateway.TicketResult)
			if r.Answer.Class != transport.Canceled {
				t.Fatalf("the page out was not canceled: %v", r.Answer.Class)
			}
			if h.feed(r); h.paneNotice() != notice {
				t.Fatalf("the late page changed the notice from %q to %q", notice, h.paneNotice())
			}
		})
	}
}

// A page that does not load is named, the others are searched, and R
// reads it again.
func TestAPageThatDidNotLoadIsNamedAndRReadsItAgain(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.findSix(60, map[int]string{50: "Kiwi", 20: "Kiwi", 5: "Kiwi"})
	answer := h.f.ticket
	var fail atomic.Bool
	fail.Store(true)
	h.f.setTicket(func(project, id string, o api.TicketOptions) transport.Answer {
		if fail.Load() && o.PostsPage == 2 {
			return failing(transport.Temporary, 503)
		}
		return answer(project, id, o)
	})
	h.openSix()
	h.find("kiwi")
	if got := h.paneNotice(); got != "1 of 2, page 2 did not load" {
		t.Fatalf("notice %q", got)
	}
	fail.Store(false)
	if h.drive(h.press("R")); h.paneNotice() != "1 of 3" {
		t.Fatalf("after R: %q", h.paneNotice())
	}
}

// A find reads 40 pages at most, and says how many comments it searched.
func TestTheFindReadsFortyPagesAtMost(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.findSix(1030, map[int]string{1030: "Kiwi", 1: "Kiwi"})
	h.openSix()
	before := len(h.f.detailReads())
	h.find("kiwi")
	if got, n := h.paneNotice(), len(h.f.detailReads())-before; got != "1 of 1, 1000 of 1030 comments searched" || n != 39 {
		t.Fatalf("notice %q after %d reads", got, n)
	}
}

// [ and ] keep the find; o runs it again over the pages of the other
// order.
func TestOFindsAgainInTheOtherOrderAndTheBracketsKeepTheFind(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.findSix(60, map[int]string{50: "Kiwi", 5: "Kiwi"})
	h.openSix()
	h.press("tab")
	h.find("kiwi")
	if h.drive(h.press("]")); h.paneNotice() != "1 of 2" || h.m.app.detail.shown.PostsPage != 2 || h.m.app.detail.follow != anchorArea {
		t.Fatalf("]: %q, anchor %d", h.paneNotice(), h.m.app.detail.follow)
	}
	before := len(h.f.detailReads())
	h.drive(h.press("o"))
	reads := h.readsSince(before)
	if !slices.Contains(reads, paged(2, false, 1)) || !slices.Contains(reads, paged(3, false, 1)) || h.paneNotice() != "2 found" {
		t.Fatalf("o: reads %+v, notice %q", reads, h.paneNotice())
	}
}

// A new comment shifts the pages: a hit whose comment left the pages held
// drops out, the count right; read again on its new page, the comment is
// found by its id, its hit the current one again. A comment on two pages
// held, one of them older, counts once.
func TestANewCommentShiftsThePagesAndTheHitsFollowTheirComments(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	texts := map[int]string{36: "Kiwi am Ende von Seite eins", 20: "Kiwi"}
	h.findSix(60, texts)
	h.openSix()
	h.find("kiwi")
	if got := h.paneNotice(); got != "1 of 2" {
		t.Fatalf("notice %q", got)
	}
	h.findSix(61, texts)
	if h.drive(h.press("R")); h.paneNotice() != "1 of 1" {
		t.Fatalf("after the new comment: %q", h.paneNotice())
	}
	h.press("tab")
	h.drive(h.press("]"))
	if cur, _ := h.inSight(); h.paneNotice() != "1 of 2" || !slices.Equal(cur, []string{"Kiwi"}) {
		t.Fatalf("on page 2: %q, in sight %q", h.paneNotice(), cur)
	}
	// The new comment is gone again: page 1 holds comment 36 once more, as
	// the page 2 held does.
	h.findSix(60, texts)
	h.drive(h.press("["))
	if cur, _ := h.allMarks(); h.paneNotice() != "1 of 2" || len(cur) != 1 {
		t.Fatalf("on page 1 again: %q, current %q", h.paneNotice(), cur)
	}
}

// e on the description: the current hit stays in its text, the find then
// finds in the whole text. Esc ends the find first, then closes the
// expansion, then goes back.
func TestEKeepsTheHitInItsTextAndEscEndsTheFindFirst(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.findSix(30, map[int]string{findDescription: "Kiwi am Anfang" + api.TruncationMarker, findWhole: "Kiwi am Anfang und Kiwi am Ende.", 30: "eine Kiwi"})
	h.openSix()
	h.find("kiwi")
	if got := h.paneNotice(); got != "1 of 2" {
		t.Fatalf("notice %q", got)
	}
	h.drive(h.press("e"))
	d := h.m.app.detail
	if d.expansion == nil || !d.expansion.known || h.paneNotice() != "1 of 3" || d.found.at.area != areaDescription || !strings.Contains(h.helpText(), "esc end find") {
		t.Fatalf("after e: %q, at %+v, help %q", h.paneNotice(), d.found.at, h.helpText())
	}
	if h.press("n"); h.paneNotice() != "2 of 3" || d.found.at != (findPlace{area: areaDescription, k: 1}) {
		t.Fatalf("n in the whole text: %q, at %+v", h.paneNotice(), d.found.at)
	}
	h.press("esc")
	if d.find.query != "" || d.expansion == nil {
		t.Fatalf("the first esc: query %q, expansion %v", d.find.query, d.expansion != nil)
	}
	h.press("esc")
	if d.expansion != nil || h.m.focus != view.Object {
		t.Fatal("the second esc did not close the expansion alone")
	}
	if h.press("esc"); h.m.focus != view.List {
		t.Fatalf("the third esc: focus %d", h.m.focus)
	}
}

// The ticket's head and the headings are not searched.
func TestTheHeadAndTheHeadingsAreNotSearched(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.findSix(30, map[int]string{findDescription: "Kiwi"})
	h.openSix()
	for _, word := range []string{"ticket 6", "description", "readable", "in all"} {
		if h.find(word); h.paneNotice() != "no match" {
			t.Fatalf("%q: %q", word, h.paneNotice())
		}
	}
}

// A page that says the ticket is gone ends the find with the ticket.
func TestAPageOfAGoneTicketEndsTheFind(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.findSix(60, map[int]string{5: "Kiwi"})
	answer := h.f.ticket
	h.f.setTicket(func(project, id string, o api.TicketOptions) transport.Answer {
		if o.PostsPage == 2 {
			return transport.Answer{Class: transport.NotFound, Message: "No ticket found for " + id + ".", Status: 200, Sent: true}
		}
		return answer(project, id, o)
	})
	h.openSix()
	h.find("kiwi")
	if d := h.m.app.detail; d.find.query != "" || d.found.pages != nil || d.known {
		t.Fatalf("the find outlived its ticket: query %q, known %v", d.find.query, d.known)
	}
}

// A server without pages: the one answer is searched, nothing more read.
func TestAServerWithoutPagesFindsInItsOneAnswer(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	tools := withoutArgument(h.d.tools, "get_ticket", "posts_order")
	h.f.tools = func() ([]transport.Tool, transport.Answer) {
		return tools, transport.Answer{Class: transport.Object, Status: 200, Sent: true}
	}
	h.findSix(30, map[int]string{30: "Kiwi", 1: "Kiwi"})
	h.openSix()
	before := len(h.f.detailReads())
	h.find("kiwi")
	if got := h.paneNotice(); got != "1 of 2" || len(h.f.detailReads()) != before {
		t.Fatalf("notice %q after %d reads", got, len(h.f.detailReads())-before)
	}
}

// The ticket from a notification and an agent's ticket find as the
// tickets section's does; a notification's details have no find.
func TestTheFindWhereverATicketStands(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.m.width, h.m.height = 120, 24
	h.start()
	h.openDetails(orgA, 0)
	if h.press("/"); h.offers(Search) || h.m.mode != Navigate || strings.Contains(h.helpText(), "/ find") {
		t.Fatalf("the details: mode %d, help %q", h.m.mode, h.helpText())
	}
	h.drive(h.press("o"))
	h.find("zzz")
	if got := h.paneNotice(); h.m.app.origin == nil || got != "no match" {
		t.Fatalf("the notification's ticket: notice %q", got)
	}

	h = newHarness(t)
	h.findSix(30, map[int]string{30: "Kiwi"})
	h.withWatcher(agentIn(1, orgA, 6, agents.Working, h.now))
	h.start()
	h.m.width = 60
	h.drive(h.press("A"))
	h.drive(h.press("enter"))
	h.find("kiwi")
	if got := h.paneNotice(); !h.m.app.agentTicket() || got != "1 of 1" {
		t.Fatalf("the agent's ticket: notice %q", got)
	}
}
