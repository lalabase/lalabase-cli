package ui

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/cache"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/queue"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

// C1d: the ticket page by page. Comments newest first, 25 a page, the
// order switchable; attachments ten a page on their own; the description
// or one comment whole on an explicit e; the missing-comment rule.

// first is the read C1c sends for a ticket: the first page of comments,
// newest first, and of attachments.
var first = api.TicketOptions{PostsPage: 1, PostsPerPage: 25, Newest: true, AttachmentsPage: 1}

func paged(posts int, newest bool, files int) api.TicketOptions {
	return api.TicketOptions{PostsPage: posts, PostsPerPage: 25, Newest: newest, AttachmentsPage: files}
}

// openSix opens ticket #6 of Website: 30 comments, 12 attachments, a
// shortened description.
func (h *harness) openSix() {
	h.t.Helper()
	h.openTickets(website)
	h.m.app.tickets.selected = ticketID(website, 6)
	h.drive(h.press("enter"))
	if d := h.m.app.detail; d == nil || !d.known || h.m.focus != view.Object {
		h.t.Fatalf("ticket #6 is not open: %+v", d)
	}
}

// paneText is the whole text of the object pane, not the window of it.
func (h *harness) paneText() string { return h.m.objectColumn().Text.String() }

// lastRead is the options of the newest read of a ticket.
func (h *harness) lastRead() api.TicketOptions {
	h.t.Helper()
	reads := h.f.detailReads()
	if len(reads) == 0 {
		h.t.Fatal("no ticket was read")
	}
	return reads[len(reads)-1].o
}

func mustContain(t *testing.T, text string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Fatalf("%q is missing from:\n%s", want, text)
		}
	}
}

func mustNotContain(t *testing.T, text string, unwanted ...string) {
	t.Helper()
	for _, u := range unwanted {
		if strings.Contains(text, u) {
			t.Fatalf("%q is in:\n%s", u, text)
		}
	}
}

func TestCommentsArePagedNewestFirstAndAttachmentsOnTheirOwn(t *testing.T) {
	h := newHarness(t)
	h.openSix()
	if reads := h.f.detailReads(); len(reads) != 1 || reads[0].o != first {
		t.Fatalf("reads %+v", reads)
	}
	mustContain(t, h.paneText(), "Comments · newest first · page 1 of 2 · 30 readable comments", "Attachments · page 1 of 2 · 12 in all", "Six comment 30", "six-01.txt")
	mustNotContain(t, h.paneText(), "Six comment 5\n")
	// The description is the first section: the page keys do nothing there.
	if h.m.app.detail.area != areaDescription || h.m.allowed()[NextPage] {
		t.Fatalf("area %v, ] offered", h.m.app.detail.area)
	}
	if cmd := h.press("]"); cmd != nil || len(h.f.detailReads()) != 1 {
		t.Fatal("] acted in the description")
	}
	h.press("tab")
	h.drive(h.press("]"))
	if got := h.lastRead(); got != paged(2, true, 1) {
		t.Fatalf("comments page 2: %+v", got)
	}
	mustContain(t, h.paneText(), "Comments · newest first · page 2 of 2 · 30 readable comments", "Six comment 1", "Attachments · page 1 of 2")
	mustNotContain(t, h.paneText(), "Six comment 30")
	// The last page: ] is not offered and sends nothing.
	if h.m.allowed()[NextPage] || h.press("]") != nil {
		t.Fatal("] beyond the last page")
	}
	h.press("tab")
	h.drive(h.press("]"))
	if got := h.lastRead(); got != paged(2, true, 2) {
		t.Fatalf("attachments page 2: %+v", got)
	}
	mustContain(t, h.paneText(), "Attachments · page 2 of 2 · 12 in all", "six-11.txt", "Comments · newest first · page 2 of 2")
	mustNotContain(t, h.paneText(), "six-01.txt")
	// Tab goes round: description, comments, attachments, description.
	h.press("tab")
	if h.m.app.detail.area != areaDescription {
		t.Fatalf("area %v", h.m.app.detail.area)
	}
	if n := len(h.f.detailReads()); n != 3 {
		t.Fatalf("%d reads", n)
	}
}

// A page shown before comes from the cache at once and is read once again.
func TestAPageShownBeforeComesFromTheCacheAndIsReadOnce(t *testing.T) {
	h := newHarness(t)
	h.openSix()
	h.press("tab")
	h.drive(h.press("]"))
	cmd := h.press("[")
	d := h.m.app.detail
	if !d.cached || d.record.PostsPage != 1 {
		t.Fatalf("page 1 is not shown from the cache: %+v", d.status)
	}
	mustContain(t, h.paneText(), "Six comment 30")
	h.drive(cmd)
	if reads := h.f.detailReads(); len(reads) != 3 || reads[2].o != first {
		t.Fatalf("reads %+v", reads)
	}
	if d.cached || !d.known {
		t.Fatalf("after the reload: %+v", d.status)
	}
}

// The order resets the comments to page 1 and nothing else: the
// attachments page and the area stay.
func TestTheOrderResetsOnlyTheCommentPage(t *testing.T) {
	h := newHarness(t)
	h.openSix()
	h.press("tab")
	h.drive(h.press("]"))
	h.press("tab")
	h.drive(h.press("]"))
	h.press("tab")
	h.press("tab") // comments again
	if !h.m.allowed()[Order] {
		t.Fatal("the order is not offered")
	}
	h.drive(h.press("o"))
	if got := h.lastRead(); got != paged(1, false, 2) {
		t.Fatalf("oldest first: %+v", got)
	}
	mustContain(t, h.paneText(), "Comments · oldest first · page 1 of 2 · 30 readable comments", "Six comment 1", "Attachments · page 2 of 2")
	if h.m.app.detail.area != areaComments {
		t.Fatal("the area changed")
	}
	h.drive(h.press("o"))
	if got := h.lastRead(); got != paged(1, true, 2) {
		t.Fatalf("newest first again: %+v", got)
	}
	if !strings.Contains(helpText(h.m), "oldest first") {
		t.Fatalf("the help does not say what o does: %q", helpText(h.m))
	}
}

// A key pressed again while its read is out starts no second request.
func TestRepeatedKeysStartNoSecondRequest(t *testing.T) {
	h := newHarness(t)
	h.openSix()
	h.press("tab")
	gate := make(chan struct{})
	h.f.gate, h.f.gateTicket = gate, ticketID(website, 6)
	held := h.background(h.press("]"))
	h.waitFor("page 2 on the wire", func() bool { return len(h.f.detailReads()) == 2 })
	for _, key := range []string{"]", "]", "[", "o", "o"} {
		if cmd := h.press(key); cmd != nil {
			t.Fatalf("%s started a read while the page loads", key)
		}
	}
	mustContain(t, h.paneText(), "Loading")
	close(gate)
	h.feed(h.receive(held))
	if n := len(h.f.detailReads()); n != 2 {
		t.Fatalf("%d reads", n)
	}
	// The expansion: e twice sends once.
	gate = make(chan struct{})
	h.f.setGate(gate, ticketID(website, 6), false)
	h.press("tab")
	h.press("tab") // the description
	exp := h.background(h.press("e"))
	h.waitFor("the expansion on the wire", func() bool { return len(h.f.detailReads()) == 3 })
	if cmd := h.press("e"); cmd != nil || h.m.allowed()[Expand] {
		t.Fatal("e started a second expansion")
	}
	close(gate)
	h.feed(h.receive(exp))
	if n := len(h.f.detailReads()); n != 3 {
		t.Fatalf("%d reads", n)
	}
}

// The server's numbers bound the pages: a list that shrank under the page
// is corrected once to the last page, in one request for both areas; a
// second shrink stops and offers R; a total of zero asks nothing.
func TestAShrunkTicketPageIsCorrectedOnce(t *testing.T) {
	h := newHarness(t)
	sized := func(comments, files func(o api.TicketOptions) int) {
		h.f.setTicket(func(project, id string, o api.TicketOptions) transport.Answer {
			d := h.d
			d.sixComments, d.sixFiles = comments(o), files(o)
			return d.defaultTicket(project, id, o)
		})
	}
	fixed := func(n int) func(api.TicketOptions) int { return func(api.TicketOptions) int { return n } }
	sized(fixed(80), fixed(12))
	h.openSix()
	h.press("tab")
	for range 3 {
		h.drive(h.press("]"))
	}
	h.press("tab")
	h.drive(h.press("]"))
	if got := h.lastRead(); got != paged(4, true, 2) {
		t.Fatalf("%+v", got)
	}
	// Now 60 comments (3 pages) and 3 attachments (1 page): one correction
	// for both.
	sized(fixed(60), fixed(3))
	before := len(h.f.detailReads())
	h.drive(h.press("R"))
	reads := h.f.detailReads()[before:]
	if len(reads) != 2 || reads[0].o != paged(4, true, 2) || reads[1].o != paged(3, true, 1) {
		t.Fatalf("reads %+v", reads)
	}
	mustContain(t, h.paneText(), "page 3 of 3 · 60 readable comments", "Attachments · page 1 of 1 · 3 in all")
	// The list shrinks twice on the way: page 3 says 40 (two pages), the
	// correction to page 2 says 20 (one page). The second shrink stops.
	sized(func(o api.TicketOptions) int {
		if o.PostsPage == 3 {
			return 40
		}
		return 20
	}, fixed(3))
	before = len(h.f.detailReads())
	h.drive(h.press("R"))
	reads = h.f.detailReads()[before:]
	if len(reads) != 2 || reads[0].o != paged(3, true, 1) || reads[1].o != paged(2, true, 1) {
		t.Fatalf("reads %+v", reads)
	}
	if !strings.Contains(h.m.screen().Object.Notice.String(), "shrank") || !h.m.allowed()[Refresh] {
		t.Fatalf("the notice %q, R offered %v", h.m.screen().Object.Notice.String(), h.m.allowed()[Refresh])
	}
	// A total of zero: page 1 without a request (-1 makes no comments).
	sized(fixed(-1), fixed(3))
	before = len(h.f.detailReads())
	h.drive(h.press("R"))
	if n := len(h.f.detailReads()) - before; n != 1 {
		t.Fatalf("%d reads for a total of zero", n)
	}
	if k := h.m.app.detail.key; k.PostsPage != 1 {
		t.Fatalf("page %d", k.PostsPage)
	}
	mustContain(t, h.paneText(), "No comments")
}

// The selected comment is kept by its id across a reload; when it left
// the page the first comment is selected.
func TestTheSelectedCommentIsKeptByItsID(t *testing.T) {
	h := newHarness(t)
	h.openSix()
	h.press("tab")
	d := h.m.app.detail
	if d.post != sixPostID(30) {
		t.Fatalf("selected %q", d.post)
	}
	h.press("down")
	if d.post != sixPostID(29) {
		t.Fatalf("selected %q", d.post)
	}
	mustContain(t, h.paneText(), "> ")
	h.f.setTicket(func(project, id string, o api.TicketOptions) transport.Answer {
		dd := h.d
		dd.sixComments = 31
		return dd.defaultTicket(project, id, o)
	})
	h.drive(h.press("R"))
	if d.post != sixPostID(29) || d.record.Posts[0].ID != sixPostID(31) {
		t.Fatalf("selected %q, first %q", d.post, d.record.Posts[0].ID)
	}
	h.drive(h.press("o"))
	if d.post != sixPostID(1) {
		t.Fatalf("after the order: %q", d.post)
	}
}

// The description whole: only on e, with the pages' options and
// full_text, without touching the pages; Esc closes it without a request.
func TestTheDescriptionIsExpandedOnlyOnEAndKeepsThePages(t *testing.T) {
	h := newHarness(t)
	h.openSix()
	text := h.paneText()
	mustContain(t, text, "Description of six, the first part", "Text shortened by server")
	mustNotContain(t, text, "[truncated]", "the whole rest")
	h.press("tab")
	h.drive(h.press("]"))
	h.press("tab")
	h.drive(h.press("]"))
	h.press("tab") // the description
	before := len(h.f.detailReads())
	for _, o := range h.f.detailReads() {
		if o.o.FullText || o.o.PostID != "" {
			t.Fatalf("an expansion without e: %+v", o.o)
		}
	}
	h.drive(h.press("e"))
	want := paged(2, true, 2)
	want.FullText = true
	if got := h.f.detailReads(); len(got) != before+1 || got[before].o != want {
		t.Fatalf("reads %+v", got[before:])
	}
	d := h.m.app.detail
	if d.key.PostsPage != 2 || d.key.AttachmentsPage != 2 || d.record.PostsPage != 2 || d.record.AttachmentsPage != 2 {
		t.Fatalf("the pages changed: %+v", d.key)
	}
	mustContain(t, h.paneText(), "Description of six, the first part and the whole rest.", "Comments · newest first · page 2 of 2")
	if strings.Count(h.paneText(), "Text shortened by server") != 0 {
		t.Fatalf("still shortened:\n%s", h.paneText())
	}
	if !strings.Contains(helpText(h.m), "collapse") {
		t.Fatalf("help %q", helpText(h.m))
	}
	if cmd := h.press("esc"); cmd != nil || d.expansion != nil || h.m.app.tickets.opened == "" {
		t.Fatal("Esc did more than close the expansion")
	}
	mustContain(t, h.paneText(), "Text shortened by server")
	h.press("esc")
	if h.m.app.tickets.opened != "" {
		t.Fatal("the second Esc did not close the ticket")
	}
	if n := len(h.f.detailReads()); n != before+1 {
		t.Fatalf("%d reads", n-before)
	}
}

// One comment whole is no page: the page, its heading and the ticket stay;
// the answer is cached under its own key; shown again from there at once.
func TestOneCommentWholeIsNotAPage(t *testing.T) {
	h := newHarness(t)
	h.openSix()
	h.press("tab")
	d := h.m.app.detail
	pageKey := d.key
	h.drive(h.press("e"))
	if got := h.lastRead(); got != (api.TicketOptions{PostID: sixPostID(30)}) {
		t.Fatalf("read %+v", got)
	}
	if d.key != pageKey || len(d.record.Posts) != 25 || d.record.PostsPage != 1 {
		t.Fatalf("the page was replaced: %+v, %d comments", d.key, len(d.record.Posts))
	}
	mustContain(t, h.paneText(), "Six comment 30 begins and ends here.", "Comments · newest first · page 1 of 2 · 30 readable comments", "Six comment 29 begins")
	a := h.m.app
	expKey := pageKey
	expKey.PostsPage, expKey.PostsPerPage, expKey.Newest, expKey.AttachmentsPage, expKey.Post = 0, 0, false, 0, sixPostID(30)
	e, ok := a.store.Get(expKey)
	if !ok {
		t.Fatal("the comment is not cached under its own key")
	}
	if page, ok := a.store.Get(pageKey); !ok || len(page.Payload) <= len(e.Payload) {
		t.Fatal("the page entry was replaced by the comment")
	}
	h.press("esc")
	mustNotContain(t, h.paneText(), "begins and ends here")
	before := len(h.f.detailReads())
	cmd := h.press("e")
	mustContain(t, h.paneText(), "Six comment 30 begins and ends here.") // at once, from the cache
	h.drive(cmd)
	if n := len(h.f.detailReads()) - before; n != 1 {
		t.Fatalf("%d reads", n)
	}
	// A page turn takes the comment out of sight: its expansion closes.
	h.drive(h.press("]"))
	if d.expansion != nil {
		t.Fatal("the expansion of a comment that left the page stays")
	}
}

// Without the paging arguments: the server's first comments with the
// incompleteness shown, no page or order keys, the expansion still there.
// Without full_text and post_id: paging, but no expansion. Each capability
// on its own.
func TestPagingAndExpansionAreTwoCapabilities(t *testing.T) {
	t.Run("expansion without paging", func(t *testing.T) {
		h := newHarness(t)
		tools := withoutArgument(h.d.tools, "get_ticket", "posts_order")
		h.f.tools = func() ([]transport.Tool, transport.Answer) {
			return tools, transport.Answer{Class: transport.Object, Status: 200, Sent: true}
		}
		h.openSix()
		if got := h.lastRead(); got != (api.TicketOptions{}) {
			t.Fatalf("read %+v", got)
		}
		mustContain(t, h.paneText(), "Comments · oldest first · 30 comments", "Attachments · first 10 of 12")
		for range 3 {
			h.press("tab")
			for _, k := range []Action{PrevPage, NextPage, Order} {
				if h.m.allowed()[k] {
					t.Fatalf("area %v offers %v", h.m.app.detail.area, k)
				}
			}
			for _, key := range []string{"[", "]", "o"} {
				if cmd := h.press(key); cmd != nil {
					t.Fatalf("%s acted", key)
				}
			}
		}
		if h.m.app.detail.area != areaDescription {
			t.Fatal("not in the description")
		}
		h.drive(h.press("e"))
		if got := h.lastRead(); got != (api.TicketOptions{FullText: true}) {
			t.Fatalf("read %+v", got)
		}
		mustContain(t, h.paneText(), "the whole rest")
		h.press("tab")
		for range 28 {
			h.press("down")
		}
		if h.m.app.detail.post != sixPostID(29) {
			t.Fatalf("selected %q", h.m.app.detail.post)
		}
		h.drive(h.press("e"))
		if got := h.lastRead(); got != (api.TicketOptions{PostID: sixPostID(29)}) {
			t.Fatalf("read %+v", got)
		}
		if n := len(h.f.detailReads()); n != 3 {
			t.Fatalf("%d reads", n)
		}
	})
	t.Run("paging without expansion", func(t *testing.T) {
		h := newHarness(t)
		tools := withoutArgument(h.d.tools, "get_ticket", "full_text")
		h.f.tools = func() ([]transport.Tool, transport.Answer) {
			return tools, transport.Answer{Class: transport.Object, Status: 200, Sent: true}
		}
		h.openSix()
		mustContain(t, h.paneText(), "Text shortened by server")
		for range 3 {
			if h.m.allowed()[Expand] || h.press("e") != nil {
				t.Fatalf("e in area %v", h.m.app.detail.area)
			}
			h.press("tab")
		}
		h.press("tab")
		h.drive(h.press("]"))
		h.drive(h.press("o"))
		for _, r := range h.f.detailReads() {
			if r.o.FullText || r.o.PostID != "" {
				t.Fatalf("an expansion was sent: %+v", r.o)
			}
		}
		if n := len(h.f.detailReads()); n != 3 {
			t.Fatalf("%d reads", n)
		}
	})
}

// Attachments show what the server proved and say what it did not: a
// missing size is unknown, not zero bytes.
func TestAttachmentsShowOnlyTheMetadataTheServerSent(t *testing.T) {
	h := newHarness(t)
	h.openSix()
	text := h.paneText()
	mustContain(t, text,
		"six-01.txt · text/plain · 1000 B · text available",
		"six-02.txt · type unknown · size unknown · text status unknown",
		"six-03.txt · text/plain · 2.9 KiB · text available",
	)
	h.press("tab")
	h.press("tab")
	h.drive(h.press("]"))
	mustContain(t, h.paneText(), "six-12.txt · type unknown · 0 B · text status unknown")
	for status, want := range map[string]string{"available": "text available", "unsupported": "text not supported", "unavailable": "text unavailable", "empty": "no text", "other": "text status unknown"} {
		s := status
		if name, got := attachmentLine(api.Attachment{Filename: "f", TextStatus: &s}); name != "f" || !strings.HasSuffix(got, want) {
			t.Errorf("%s: %q", status, got)
		}
	}
	for size, want := range map[int64]string{0: "0 B", 1023: "1023 B", 1024: "1.0 KiB", 1536: "1.5 KiB", 5 << 20: "5.0 MiB", 3 << 30: "3.0 GiB"} {
		n := size
		if _, got := attachmentLine(api.Attachment{Filename: "f", ByteSize: &n}); !strings.Contains(got, " · "+want+" · ") {
			t.Errorf("%d: %q", size, got)
		}
	}
}

// A late answer for an expansion that was closed opens nothing and fills
// no cache.
func TestALateExpansionAnswerDoesNotOpenItAgain(t *testing.T) {
	h := newHarness(t)
	h.openSix()
	gate := make(chan struct{})
	h.f.setGate(gate, ticketID(website, 6), true)
	held := h.background(h.press("e"))
	h.waitFor("the expansion on the wire", func() bool { return len(h.f.detailReads()) == 2 })
	d := h.m.app.detail
	key := d.expansion.key
	h.press("esc")
	if d.expansion != nil {
		t.Fatal("Esc did not close the expansion")
	}
	close(gate)
	h.feed(h.receive(held))
	if d.expansion != nil || strings.Contains(h.paneText(), "the whole rest") {
		t.Fatal("the late answer opened the expansion again")
	}
	if _, ok := h.m.app.store.Get(key); ok {
		t.Fatal("the late answer was cached")
	}
	// Leaving the ticket closes it too.
	gate = make(chan struct{})
	h.f.setGate(gate, ticketID(website, 6), true)
	held = h.background(h.press("e"))
	h.waitFor("the expansion on the wire", func() bool { return len(h.f.detailReads()) == 3 })
	h.press("esc") // the expansion
	h.press("esc") // the ticket
	h.press("down")
	close(gate)
	h.feed(h.receive(held))
	if d := h.m.app.detail; d != nil && d.expansion != nil {
		t.Fatal("the expansion came back on another ticket")
	}
}

// The missing-comment rule: the cached expansion goes and its text is
// hidden at once, the ticket stays and says "Comment unavailable", and the
// ticket is read once without post_id; only that read may remove it.
func TestAMissingCommentHidesItsTextAndReadsTheTicketOnce(t *testing.T) {
	setup := func(t *testing.T) (*harness, cache.Key) {
		h := newHarness(t)
		h.openSix()
		h.press("tab")
		h.drive(h.press("e"))
		key := h.m.app.detail.expansion.key
		h.press("esc")
		h.f.setTicket(func(project, id string, o api.TicketOptions) transport.Answer {
			if o.PostID == sixPostID(30) {
				return transport.Answer{Class: transport.NotFound, Message: "No comment found for " + o.PostID + ".", Status: 200, Sent: true}
			}
			return h.d.defaultTicket(project, id, o)
		})
		return h, key
	}
	t.Run("the ticket stays", func(t *testing.T) {
		h, key := setup(t)
		d := h.m.app.detail
		before := len(h.f.detailReads())
		cmd := h.press("e")
		mustContain(t, h.paneText(), "begins and ends here") // the cached expansion, at once
		h.drive(cmd)
		reads := h.f.detailReads()[before:]
		if len(reads) != 2 || reads[0].o.PostID != sixPostID(30) || reads[1].o != first {
			t.Fatalf("reads %+v", reads)
		}
		if _, ok := h.m.app.store.Get(key); ok {
			t.Fatal("the cached expansion stayed")
		}
		if h.m.app.detail != d || !d.known || len(d.record.Posts) != 25 {
			t.Fatal("the ticket was dropped")
		}
		text := h.paneText()
		mustContain(t, text, "Comment unavailable")
		mustNotContain(t, text, "begins and ends here")
		if e := d.expansion; e == nil || !e.unavailable || e.text != "" || e.known {
			t.Fatalf("the expansion keeps its text: %+v", e)
		}
		if _, ok := h.m.app.store.Get(d.key); !ok {
			t.Fatal("the ticket's page is not cached")
		}
	})
	t.Run("the reload removes a ticket that is gone", func(t *testing.T) {
		h, _ := setup(t)
		h.f.setTicket(func(project, id string, o api.TicketOptions) transport.Answer {
			if o.PostID != "" {
				return transport.Answer{Class: transport.NotFound, Message: "No comment found for " + o.PostID + ".", Status: 200, Sent: true}
			}
			return transport.Answer{Class: transport.NotFound, Message: "No ticket found.", Status: 200, Sent: true}
		})
		before := len(h.f.detailReads())
		h.drive(h.press("e"))
		if n := len(h.f.detailReads()) - before; n != 2 {
			t.Fatalf("%d reads", n)
		}
		d := h.m.app.detail
		if d.known || d.expansion != nil || h.m.app.tickets.opened != "" {
			t.Fatalf("the ticket stayed: %+v", d.status)
		}
		mustContain(t, h.m.screen().Object.Notice.String(), "no longer")
	})
	t.Run("an older answer cannot bring the text back", func(t *testing.T) {
		h, key := setup(t)
		// Two reads of the expansion out at once (the model sends one; the
		// rule must not rest on that): the first is left unanswered, the
		// second says the comment is missing.
		older := h.press("e")
		var oldTicket gateway.Ticket
		for _, w := range h.m.app.waiters {
			if w.ticket.Key == key {
				oldTicket = w.ticket
			}
		}
		if oldTicket.Waiter == 0 {
			t.Fatal("no read of the expansion")
		}
		h.drive(h.m.app.issue(gateway.Expansion, key, forExpansion, queue.User))
		mustContain(t, h.paneText(), "Comment unavailable")
		// The older read's answer comes last, with the whole comment and a
		// later flight.
		whole := h.d.onePost(website, 6, sixPostID(30))
		record, _ := api.DecodeTicket(whole, api.TicketOptions{PostID: sixPostID(30)})
		if w := h.m.app.waiters[oldTicket.Waiter]; w == nil || !w.canceled {
			t.Fatal("the older read of the expansion was not canceled")
		}
		h.feed(gateway.TicketResult{Ticket: oldTicket, Record: record, Answer: whole, Flight: 1 << 40})
		if strings.Contains(h.paneText(), "begins and ends here") {
			t.Fatal("the older answer brought the text back")
		}
		if _, ok := h.m.app.store.Get(key); ok {
			t.Fatal("the older answer was cached")
		}
		_ = older
	})
}

// No access to one comment is no access to the ticket: every cached
// variant of it goes, and the answer is not "Comment unavailable".
func TestNoAccessOrAMissingTicketOnAnExpansionRemovesEveryVariant(t *testing.T) {
	for name, answer := range map[string]transport.Answer{
		"no access on a comment":      failing(transport.NoAccess, 403),
		"no ticket on the whole text": {Class: transport.NotFound, Message: "No ticket found.", Status: 200, Sent: true},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.openSix()
			h.press("tab")
			h.drive(h.press("]")) // page 2 cached beside page 1
			a := h.m.app
			pages := []cache.Key{a.detail.key}
			h.drive(h.press("["))
			pages = append(pages, a.detail.key)
			comment := name == "no access on a comment"
			if !comment {
				h.press("tab")
				h.press("tab")
			} else {
				h.drive(h.press("e"))
				pages = append(pages, a.detail.expansion.key)
				h.press("esc")
			}
			h.f.setTicket(func(project, id string, o api.TicketOptions) transport.Answer {
				if o.PostID != "" || o.FullText {
					return answer
				}
				return h.d.defaultTicket(project, id, o)
			})
			before := len(h.f.detailReads())
			h.drive(h.press("e"))
			if n := len(h.f.detailReads()) - before; n != 1 {
				t.Fatalf("%d reads", n)
			}
			for _, k := range pages {
				if _, ok := a.store.Get(k); ok {
					t.Fatalf("a variant stayed: %+v", k)
				}
			}
			if a.detail.known || a.detail.expansion != nil {
				t.Fatal("the ticket stayed")
			}
			if strings.Contains(h.paneText()+h.m.screen().Object.Notice.String(), "Comment unavailable") {
				t.Fatal("no access read as a missing comment")
			}
		})
	}
}

// A 401 on an expansion the person left ends the session; nothing comes
// back afterwards.
func TestA401FromALeftExpansionEndsTheSession(t *testing.T) {
	h := newHarness(t)
	h.openSix()
	a := h.m.app
	h.events = gateway.NewEvents(h.live)
	a.deps.Events = h.events
	a.session = h.events.Session()
	a.deps.Gateway = gateway.New(h.live, h.q, h.f, gateway.WithEvents(h.events))
	wake := h.background(h.events.Wait())
	gate := make(chan struct{})
	h.f.setGate(gate, ticketID(website, 6), true)
	h.f.setTicket(func(project, id string, o api.TicketOptions) transport.Answer {
		if o.FullText {
			return failing(transport.Unauthenticated, 401)
		}
		return h.d.defaultTicket(project, id, o)
	})
	held := h.background(h.press("e"))
	h.waitFor("the expansion on the wire", func() bool { return len(h.f.detailReads()) == 2 })
	h.press("esc") // the expansion is left
	close(gate)
	h.waitFor("the 401", func() bool { return h.events.State().Revoked })
	h.feed(h.receive(held), h.receive(wake))
	if !a.expired || a.detail != nil || a.store != nil {
		t.Fatal("the session stayed")
	}
	plain := ansi.Strip(h.m.View().Content)
	mustContain(t, plain, "Session expired")
	mustNotContain(t, plain, "Description of six")
	for _, key := range []string{"tab", "e", "o", "]", "[", "R"} {
		if cmd := h.press(key); cmd != nil {
			t.Fatalf("%s acted after the expiry", key)
		}
	}
}

// Paging, the order and the expansions are the person's: none of them
// waits for the background budget the previews used up.
func TestThePersonsReadsDoNotWaitForTheBackgroundBudget(t *testing.T) {
	h := newHarness(t)
	h.openTickets(website)
	a := h.m.app
	// Both budgets spent, as by a minute of browsing and reloads.
	h.spendBudgets()
	h.press("down") // #7
	a.tickets.selected = ticketID(website, 6)
	// Within the harness's wall: a read held by a budget would wait for
	// the minute to roll on.
	h.driveWithin(h.press("enter"))
	h.driveWithin(h.press("e"))
	h.press("esc")
	h.press("tab")
	h.driveWithin(h.press("]"))
	h.driveWithin(h.press("o"))
	if n := len(h.f.detailReads()); n != 4 {
		t.Fatalf("%d reads, want the person's 4", n)
	}
}

// A resize keeps the area, the pages, the selection and the expansion,
// and reads nothing.
func TestAResizeKeepsTheTicketState(t *testing.T) {
	h := newHarness(t)
	h.openSix()
	h.press("tab")
	h.drive(h.press("]"))
	h.drive(h.press("e")) // comment 5 is not shortened: nothing
	h.press("tab")
	h.press("tab")
	h.drive(h.press("e"))
	d := h.m.app.detail
	key, area, post, exp := d.key, d.area, d.post, d.expansion
	before := len(h.f.detailReads())
	for _, w := range []int{80, 79, 120, 200} {
		h.drive(h.feed(tea.WindowSizeMsg{Width: w, Height: 30})...)
		if d.key != key || d.area != area || d.post != post || d.expansion != exp || h.m.app.detail != d {
			t.Fatalf("width %d changed the state", w)
		}
	}
	if n := len(h.f.detailReads()) - before; n != 0 {
		t.Fatalf("%d reads", n)
	}
}

// Tab moves the pane to the heading of the area; the selected comment is
// kept in sight.
func TestTabAndTheSelectionKeepTheirPlaceInSight(t *testing.T) {
	h := newHarness(t)
	h.m.width, h.m.height = 120, 20
	h.openSix()
	for _, want := range []string{"Comments · newest first", "Attachments · page 1"} {
		h.press("tab")
		lines := view.Render(h.m.screen(), h.m.geometry(), h.m.focus, h.m.widthMethod)
		if !strings.Contains(ansi.Strip(lines), want) {
			t.Fatalf("after Tab %q is not in sight:\n%s", want, ansi.Strip(lines))
		}
	}
	h.press("tab")
	h.press("tab") // comments
	for range 20 {
		h.press("down")
	}
	if h.m.app.detail.post != sixPostID(10) {
		t.Fatalf("selected %q", h.m.app.detail.post)
	}
	if plain := ansi.Strip(view.Render(h.m.screen(), h.m.geometry(), h.m.focus, h.m.widthMethod)); !strings.Contains(plain, "Six comment 10") {
		t.Fatalf("the selected comment is not in sight:\n%s", plain)
	}
	// pgup/pgdown scroll the pane by themselves, the selection stays.
	h.press("pgup")
	if h.m.app.detail.post != sixPostID(10) {
		t.Fatal("the scroll moved the selection")
	}
}

// Hostile text in the new places (the attachments' metadata, a text read
// whole, an error of an expansion) reaches the screen cleaned, from the
// server and from the cache.
func TestHostileMetadataAndExpandedTextAreCleaned(t *testing.T) {
	h := newHarness(t)
	hostileKind, hostileStatus := "KIND_START"+hostileText, "STATUS_START"+hostileText
	h.f.setTicket(func(project, id string, o api.TicketOptions) transport.Answer {
		if o.PostID != "" {
			ans := h.d.onePost(project, 6, o.PostID)
			record, _ := api.DecodeTicket(ans, o)
			record.Posts[0].BodyText = "WHOLE_START" + hostileText
			return ticketAnswer(record)
		}
		r := h.d.ticketRecord(project, 6, o)
		r.Attachments[0].ContentType, r.Attachments[0].TextStatus = &hostileKind, &hostileStatus
		r.Attachments[1].Filename = "FILE_START" + hostileText
		if o.FullText {
			r.DescriptionText = "DESC_START" + hostileText
		}
		return ticketAnswer(r)
	})
	h.openSix()
	h.drive(h.press("e"))
	views := func(wants ...string) {
		t.Helper()
		for _, width := range []int{120, 80, 79} {
			h.feed(tea.WindowSizeMsg{Width: width, Height: 60})
			assertViewSafe(t, h.m.View().Content)
			mustContain(t, ansi.Strip(h.paneText()), wants...)
		}
	}
	views("KIND_START", "FILE_START", "DESC_START")
	// One expansion at a time: the comment's closes the description's.
	h.press("tab")
	h.drive(h.press("e"))
	views("KIND_START", "FILE_START", "WHOLE_START")
	// A hostile status word is unknown, not shown.
	mustNotContain(t, h.paneText(), "STATUS_START")
	// The cache path: the whole text filed directly, shown on e.
	h.press("esc")
	a := h.m.app
	k := a.detail.expansion
	if k != nil {
		t.Fatal("the expansion stayed")
	}
	key := a.detail.key
	key.PostsPage, key.PostsPerPage, key.Newest, key.AttachmentsPage, key.Post = 0, 0, false, 0, sixPostID(30)
	ans := h.d.onePost(website, 6, sixPostID(30))
	record, _ := api.DecodeTicket(ans, api.TicketOptions{PostID: sixPostID(30)})
	record.Posts[0].BodyText = "CACHED_START" + hostileText
	a.store.Apply(key, cache.Replace, ticketAnswer(record).Payload, h.now)
	h.press("e") // shown from the cache; its reload is not run
	text := h.m.View().Content
	assertViewSafe(t, text)
	mustContain(t, ansi.Strip(h.paneText()), "CACHED_START")
	// A hostile error of an expansion.
	h.press("esc")
	h.f.setTicket(func(project, id string, o api.TicketOptions) transport.Answer {
		if o.PostID != "" {
			return transport.Answer{Class: transport.Refused, Message: "ERR_START" + hostileText, Status: 200, Sent: true}
		}
		return h.d.defaultTicket(project, id, o)
	})
	a.store.Forget(key)
	h.drive(h.press("e"))
	text = h.m.View().Content
	assertViewSafe(t, text)
	mustContain(t, ansi.Strip(h.paneText()), "ERR_START")
}

// A page that failed does not say it loads: the section says it did not
// come, the heading keeps the page asked for, R reads it again.
func TestAFailedPageSaysSoInItsSection(t *testing.T) {
	h := newHarness(t)
	h.openSix()
	h.press("tab")
	h.f.setTicket(func(project, id string, o api.TicketOptions) transport.Answer {
		if o.PostsPage == 2 {
			return failing(transport.Temporary, 503)
		}
		return h.d.defaultTicket(project, id, o)
	})
	h.drive(h.press("]"))
	text := h.paneText()
	mustContain(t, text, "page 2 of 2", "This page did not load. R retry.")
	mustNotContain(t, text, "Loading this page")
	h.f.setTicket(h.d.defaultTicket)
	h.drive(h.press("R"))
	mustContain(t, h.paneText(), "page 2 of 2", "Six comment 5")
}

// An expansion that failed is offered again: e reads it once more; one
// that loads or is shown is not.
func TestAFailedExpansionIsOfferedAgain(t *testing.T) {
	h := newHarness(t)
	h.openSix()
	h.f.setTicket(func(project, id string, o api.TicketOptions) transport.Answer {
		if o.FullText {
			return failing(transport.Temporary, 503)
		}
		return h.d.defaultTicket(project, id, o)
	})
	h.drive(h.press("e"))
	mustContain(t, h.paneText(), "Could not reach the server")
	if !h.m.allowed()[Expand] {
		t.Fatal("a failed expansion is not offered again")
	}
	h.f.setTicket(h.d.defaultTicket)
	before := len(h.f.detailReads())
	h.drive(h.press("e"))
	if n := len(h.f.detailReads()) - before; n != 1 {
		t.Fatalf("%d reads", n)
	}
	mustContain(t, h.paneText(), "the whole rest")
	if h.m.allowed()[Expand] {
		t.Fatal("a shown expansion is offered again")
	}
}

// e is offered only for a text the server shortened: ticket #1's
// description and comments are whole.
func TestEIsOfferedOnlyForAShortenedText(t *testing.T) {
	h := newHarness(t)
	h.openTickets(website)
	h.drive(h.press("enter")) // #1
	for range 3 {
		if h.m.allowed()[Expand] || h.press("e") != nil {
			t.Fatalf("e offered in area %v", h.m.app.detail.area)
		}
		h.press("tab")
	}
	if n := len(h.f.detailReads()); n != 1 {
		t.Fatalf("%d reads", n)
	}
}

// Leaving a ticket while its expansion loads cancels that read: the
// waiter gets "canceled", nothing is cached or shown of it.
func TestLeavingATicketCancelsItsExpansion(t *testing.T) {
	h := newHarness(t)
	h.openSix()
	gate := make(chan struct{})
	h.f.setGate(gate, ticketID(website, 6), false)
	held := h.background(h.press("e"))
	h.waitFor("the expansion on the wire", func() bool { return len(h.f.detailReads()) == 2 })
	key := h.m.app.detail.expansion.key
	h.press("left") // the list, the ticket stays opened
	h.press("esc")  // the projects: the list and its ticket close
	if h.m.app.tickets != nil || h.m.app.detail != nil {
		t.Fatal("the list did not close")
	}
	r := h.receive(held).(gateway.TicketResult)
	close(gate)
	if r.Answer.Class != transport.Canceled {
		t.Fatalf("the expansion read was not canceled: %v", r.Answer.Class)
	}
	h.feed(r)
	if _, ok := h.m.app.store.Get(key); ok {
		t.Fatal("the expansion was cached")
	}
}

// Review round 1, finding 1: a missing comment answered for a newer read
// must not block the removal an older plain read of the ticket brings.
func TestR1AnOlderPlainRemovalAfterAMissingCommentStillRemoves(t *testing.T) {
	h := newHarness(t)
	h.openSix()
	h.press("tab")
	h.f.setTicket(func(project, id string, o api.TicketOptions) transport.Answer {
		return transport.Answer{Class: transport.NotFound, Message: "No ticket found.", Status: 200, Sent: true}
	})
	h.press("R") // its command is held back: the plain read stays out
	var plain gateway.Ticket
	for _, w := range h.m.app.waiters {
		if w.ticket.Area == gateway.Detail {
			plain = w.ticket
		}
	}
	if plain.Waiter == 0 {
		t.Fatal("no plain read out")
	}
	var flight queue.Flight
	for _, msg := range messages(t, h.press("e")) {
		if r, ok := msg.(gateway.TicketResult); ok {
			flight = r.Flight
		}
		h.drive(h.feed(msg)...)
	}
	if flight == 0 {
		t.Fatal("the expansion was not answered")
	}
	h.feed(gateway.TicketResult{Ticket: plain, Answer: transport.Answer{Class: transport.NotFound, Message: "No ticket found.", Status: 200, Sent: true}, Flight: flight - 1})
	if d := h.m.app.detail; d.known {
		t.Fatal("the missing ticket stays shown")
	}
}

// Review round 1, finding 2: a server that counts comments but no pages
// gets page 1 asked, never page 0.
func TestR1ZeroPagesBesideATotalCorrectsToPageOne(t *testing.T) {
	h := newHarness(t)
	h.openSix()
	h.press("tab")
	h.drive(h.press("]"))
	h.f.setTicket(func(project, id string, o api.TicketOptions) transport.Answer {
		r := h.d.ticketRecord(project, 6, o)
		r.PostsPages = 0
		return ticketAnswer(r)
	})
	before := len(h.f.detailReads())
	h.drive(h.press("R"))
	reads := h.f.detailReads()[before:]
	if len(reads) != 2 || reads[1].o.PostsPage != 1 {
		t.Fatalf("reads %+v", reads)
	}
	if k := h.m.app.detail.key; k.PostsPage != 1 || !pagedKey(k) {
		t.Fatalf("key %+v", k)
	}
}

// Review round 1, finding 3: pgup after Tab moves a screen from where the
// pane really is, not from an anchor beyond the last screen.
func TestR1PageUpAfterAnAnchorNearTheEndMovesAScreen(t *testing.T) {
	h := newHarness(t)
	h.m.width, h.m.height = 120, 20
	h.openSix()
	h.press("tab")
	h.press("tab")
	h.drive(h.press("]")) // attachments page 2: a short last section
	h.press("tab")
	h.press("tab")
	h.press("tab") // attachments again, its heading the anchor
	g := h.m.geometry()
	c := h.m.objectColumn()
	capacity := view.Capacity(c, g)
	top := view.Extent(c, g, view.Object, h.m.widthMethod) - capacity
	h.press("pgup")
	if got := h.m.app.detail.offset; got != max(0, top-capacity) {
		t.Fatalf("offset %d after pgup, want %d (top %d, capacity %d)", got, top-capacity, top, capacity)
	}
}

// Review round 1, backlog: while another page of comments loads, its
// comments are not shown, and up/down do not move a selection unseen.
func TestR1NoCommentSelectionWhileTheirPageLoads(t *testing.T) {
	h := newHarness(t)
	h.openSix()
	h.press("tab")
	h.press("]") // not run: the page is out
	if h.m.allowed()[Up] {
		t.Fatal("up/down offered while the comments load")
	}
	post := h.m.app.detail.post
	h.press("down")
	if h.m.app.detail.post != post {
		t.Fatal("the selection moved unseen")
	}
}

// Review round 2, finding 1: the read after a missing comment is sent
// after it, never a read that left before (whose answer may predate the
// deletion).
func TestR2TheReloadAfterAMissingCommentIsSentAfterIt(t *testing.T) {
	h := newHarness(t)
	h.openSix()
	h.press("tab")
	h.press("tab")
	older := messages(h.t, h.press("]")) // answered (the ticket exists), not delivered yet
	h.press("tab")
	h.press("tab")
	h.f.setTicket(func(p, id string, o api.TicketOptions) transport.Answer {
		if o.PostID != "" {
			return transport.Answer{Class: transport.NotFound, Message: "No comment found for " + o.PostID + ".", Status: 200, Sent: true}
		}
		return transport.Answer{Class: transport.NotFound, Message: "No ticket found.", Status: 200, Sent: true}
	})
	before := len(h.f.detailReads())
	h.drive(h.press("e"))
	h.drive(h.feed(older...)...)
	plain := 0
	for _, r := range h.f.detailReads()[before:] {
		if r.o.PostID == "" {
			plain++
		}
	}
	if plain != 1 {
		t.Fatalf("%d plain reads after the missing comment", plain)
	}
	if h.m.app.detail.known {
		t.Fatal("the deleted ticket stays shown")
	}
}

// driveWithin runs commands on supervised goroutines, each answer within
// the harness's wall time: a read held by the background budget (a
// minute) fails the test instead of passing late.
// spendBudgets fills the queue's windows: the previews' and the other
// background work's, with jobs that end at once. Since previews have a
// budget of their own, a few previews no longer spend anything a person's
// read could wait for (review of #44).
func (h *harness) spendBudgets() {
	h.t.Helper()
	quick := func(context.Context) transport.Answer {
		return transport.Answer{Class: transport.Object, Payload: []byte("{}"), Status: 200, Sent: true}
	}
	for range queue.PreviewBudget {
		h.q.Do(context.Background(), queue.Job{Priority: queue.Preview, Run: quick})
	}
	for range queue.BackgroundBudget {
		h.q.Do(context.Background(), queue.Job{Priority: queue.Background, Run: quick})
	}
}

func (h *harness) driveWithin(cmds ...tea.Cmd) {
	h.t.Helper()
	for len(cmds) > 0 {
		var next []tea.Cmd
		for _, c := range cmds {
			if c == nil {
				continue
			}
			switch msg := h.receive(h.background(c)).(type) {
			case nil:
			case tea.BatchMsg:
				next = append(next, msg...)
			default:
				next = append(next, h.feed(msg)...)
			}
		}
		cmds = next
	}
}

// Review round 2, probes: every read of C1d is the person's, also with the
// background budget spent: the page turn, the order, the expansion, the
// correction and the reload after a missing comment come at once.
func TestR2NoReadOfTheTicketWaitsForTheBudget(t *testing.T) {
	h := newHarness(t)
	h.openTickets(website)
	a := h.m.app
	// Both budgets spent, as by a minute of browsing and reloads.
	h.spendBudgets()
	h.press("down")
	a.tickets.selected = ticketID(website, 6)
	h.driveWithin(h.press("enter"))
	h.driveWithin(h.press("e"))
	mustContain(t, h.paneText(), "the whole rest")
	h.press("esc")
	h.press("tab")
	h.driveWithin(h.press("]"))
	h.driveWithin(h.press("o"))
	h.driveWithin(h.press("o"))
	mustContain(t, h.paneText(), "newest first · page 1 of 2")
	// A shrink: the correction.
	h.driveWithin(h.press("]"))
	h.f.setTicket(func(project, id string, o api.TicketOptions) transport.Answer {
		d := h.d
		d.sixComments = 20
		return d.defaultTicket(project, id, o)
	})
	h.driveWithin(h.press("R"))
	mustContain(t, h.paneText(), "page 1 of 1 · 20 readable comments")
	// A missing comment: the reload.
	h.f.setTicket(func(project, id string, o api.TicketOptions) transport.Answer {
		if o.PostID != "" {
			return transport.Answer{Class: transport.NotFound, Message: "No comment found.", Status: 200, Sent: true}
		}
		return h.d.defaultTicket(project, id, o)
	})
	h.driveWithin(h.press("R")) // 30 comments again
	for range 10 {
		h.press("up")
	}
	if a.detail.post != sixPostID(30) {
		t.Fatalf("selected %q", a.detail.post)
	}
	before := len(h.f.detailReads())
	h.driveWithin(h.press("e"))
	if reads := h.f.detailReads()[before:]; len(reads) != 2 || reads[1].o.PostID != "" {
		t.Fatalf("reads %+v", reads)
	}
}

// Review round 2, probes: the comments say they load as soon as another
// order or page is asked, and offer no expansion of the old page then.
func TestR2TheCommentsLoadOnAnOrderAndOfferNothingMeanwhile(t *testing.T) {
	h := newHarness(t)
	h.openSix()
	h.press("tab")
	if !h.m.allowed()[Expand] {
		t.Fatal("comment 30 is not offered for e")
	}
	h.press("o") // not run
	mustContain(t, h.paneText(), "Loading this page")
	mustNotContain(t, h.paneText(), "Six comment 30")
	if h.m.allowed()[Expand] {
		t.Fatal("e offered for a comment of the page being replaced")
	}
}

// Review round 2, probes: with one comment there is nothing to order or to
// select.
func TestR2OneCommentHasNoOrderAndNoSelection(t *testing.T) {
	h := newHarness(t)
	h.f.setTicket(func(project, id string, o api.TicketOptions) transport.Answer {
		d := h.d
		d.sixComments = 1
		return d.defaultTicket(project, id, o)
	})
	h.openSix()
	h.press("tab")
	if a := h.m.allowed(); a[Order] || a[Up] || a[Down] {
		t.Fatalf("offered: order %v, up %v", a[Order], a[Up])
	}
}

// Review round 2, probes: a page turn frees the correction (a new shrink
// found by the page read is corrected again), clears an error, and the
// corrected page leaves the cache.
func TestR2APageTurnFreesTheCorrectionAndClearsTheError(t *testing.T) {
	h := newHarness(t)
	comments := 80
	h.f.setTicket(func(project, id string, o api.TicketOptions) transport.Answer {
		d := h.d
		d.sixComments = comments
		return d.defaultTicket(project, id, o)
	})
	h.openSix()
	h.press("tab")
	for range 3 {
		h.drive(h.press("]"))
	}
	beyond := h.m.app.detail.key // page 4
	comments = 60
	h.drive(h.press("R")) // corrected to page 3
	if _, ok := h.m.app.store.Get(beyond); ok {
		t.Fatal("the page beyond the last stays cached")
	}
	comments = 20
	before := len(h.f.detailReads())
	h.drive(h.press("[")) // page 2, beyond the one page left
	reads := h.f.detailReads()[before:]
	if len(reads) != 2 || reads[1].o.PostsPage != 1 {
		t.Fatalf("reads %+v", reads)
	}
	mustContain(t, h.paneText(), "page 1 of 1 · 20 readable comments")
	// An error of a page goes with the next page turn.
	h.f.setTicket(func(project, id string, o api.TicketOptions) transport.Answer {
		return transport.Answer{Class: transport.Refused, Message: "no", Status: 200, Sent: true}
	})
	h.press("tab")
	h.drive(h.press("]")) // attachments page 2, refused
	if !strings.Contains(h.m.screen().Object.Notice.String(), "Refused") {
		t.Fatalf("notice %q", h.m.screen().Object.Notice.String())
	}
	h.press("[") // page 1 from the cache; its reload not run
	if n := h.m.screen().Object.Notice.String(); strings.Contains(n, "Refused") {
		t.Fatalf("the old error stays: %q", n)
	}
}

// Review round 2, probes: attachments shrink like comments: a section with
// none left goes to page 1 without a read, the second shrink stops with
// the page asked in the heading; ] never goes past the last page.
func TestR2AttachmentsShrinkLikeComments(t *testing.T) {
	h := newHarness(t)
	files := func(o api.TicketOptions) int { return 30 }
	h.f.setTicket(func(project, id string, o api.TicketOptions) transport.Answer {
		d := h.d
		d.sixFiles = files(o)
		return d.defaultTicket(project, id, o)
	})
	h.openSix()
	h.press("tab")
	h.press("tab")
	h.drive(h.press("]"))
	h.drive(h.press("]"))
	if h.m.allowed()[NextPage] || h.press("]") != nil {
		t.Fatal("] past the last attachments page")
	}
	files = func(o api.TicketOptions) int {
		if o.AttachmentsPage == 3 {
			return 20
		}
		return 5
	}
	before := len(h.f.detailReads())
	h.drive(h.press("R"))
	if reads := h.f.detailReads()[before:]; len(reads) != 2 || reads[1].o.AttachmentsPage != 2 {
		t.Fatalf("reads %+v", reads)
	}
	mustContain(t, h.paneText(), "Attachments · page 2 of 2 · 5 in all")
	// None left: page 1 without a read, and the section says so.
	files = func(api.TicketOptions) int { return -1 }
	before = len(h.f.detailReads())
	h.drive(h.press("R"))
	if n := len(h.f.detailReads()) - before; n != 1 || h.m.app.detail.key.AttachmentsPage != 1 {
		t.Fatalf("%d reads, page %d", n, h.m.app.detail.key.AttachmentsPage)
	}
	mustContain(t, h.paneText(), "No attachments")
	mustNotContain(t, h.paneText(), "Loading this page", "did not load")
}

// Review round 2, probes: the comments' second shrink keeps the page asked
// in the heading; a total of zero leaves no section loading.
func TestR2TheCommentHeadingNamesThePageAsked(t *testing.T) {
	h := newHarness(t)
	comments := func(o api.TicketOptions) int { return 80 }
	h.f.setTicket(func(project, id string, o api.TicketOptions) transport.Answer {
		d := h.d
		d.sixComments = comments(o)
		return d.defaultTicket(project, id, o)
	})
	h.openSix()
	h.press("tab")
	h.drive(h.press("]"))
	h.drive(h.press("]"))
	comments = func(o api.TicketOptions) int {
		if o.PostsPage == 3 {
			return 40
		}
		return 20
	}
	h.drive(h.press("R"))
	mustContain(t, h.paneText(), "page 2 of 2 · 20 readable comments")
	comments = func(api.TicketOptions) int { return -1 }
	h.drive(h.press("R"))
	mustContain(t, h.paneText(), "No comments")
	mustNotContain(t, h.paneText(), "Loading this page", "did not load")
}

// Review round 2, probes: a cached entry that is not the answer to its key
// is neither shown nor kept, for a page and for an expansion.
func TestR2ACachedEntryIsCheckedBeforeItIsShown(t *testing.T) {
	h := newHarness(t)
	h.openSix()
	a := h.m.app
	h.press("tab")
	page2 := a.detail.key
	page2.PostsPage = 2
	other := h.d.ticketRecord(website, 5, gateway.DetailOptions(page2))
	a.store.Apply(page2, cache.Replace, ticketAnswer(other).Payload, h.now)
	h.press("]") // not run
	if d := a.detail; d.cached || d.record.ID != ticketID(website, 6) {
		t.Fatalf("another ticket's page was shown: %s", d.record.ID)
	}
	if _, ok := a.store.Get(page2); ok {
		t.Fatal("the wrong entry stays")
	}
	h = newHarness(t)
	h.openSix()
	a = h.m.app
	h.press("tab")
	key := a.detail.key
	key.PostsPage, key.PostsPerPage, key.Newest, key.AttachmentsPage, key.Post = 0, 0, false, 0, sixPostID(30)
	wrong := h.d.onePost(website, 6, sixPostID(29))
	a.store.Apply(key, cache.Replace, wrong.Payload, h.now)
	h.press("e") // not run
	if strings.Contains(h.paneText(), "Six comment 29 begins and ends here.") {
		t.Fatal("another comment was shown as the expansion")
	}
	if _, ok := a.store.Get(key); ok {
		t.Fatal("the wrong entry stays")
	}
}

// Review round 2, probes: a reopened ticket starts at the top, whatever
// the pane followed before.
func TestR2AReopenedTicketStartsAtTheTopAfterTab(t *testing.T) {
	h := newHarness(t)
	h.m.width, h.m.height = 120, 20
	h.openSix()
	h.press("tab")
	h.press("tab")
	if h.m.objectColumn().Offset == 0 {
		t.Fatal("Tab did not move the pane")
	}
	h.press("esc")
	h.drive(h.press("enter"))
	if off := h.m.objectColumn().Offset; off != 0 {
		t.Fatalf("offset %d after reopening", off)
	}
}

// Review round 2, probes: the states of an expansion: loading says so and
// is not "whole" yet; a cached one reloading says nothing more; one whose
// reload failed says it may be stale.
func TestR2TheStatesOfAnExpansion(t *testing.T) {
	h := newHarness(t)
	h.openSix()
	cmd := h.press("e")
	text := h.paneText()
	mustContain(t, text, "Loading the whole text")
	mustNotContain(t, text, "whole text\n", "Description · whole text")
	h.drive(cmd)
	h.press("esc")
	cmd = h.press("e") // from the cache, its reload not run
	mustContain(t, h.paneText(), "Description · whole text")
	mustNotContain(t, h.paneText(), "Loading the whole text")
	h.drive(cmd)
	h.press("esc")
	h.f.setTicket(func(project, id string, o api.TicketOptions) transport.Answer {
		if o.FullText {
			return failing(transport.Temporary, 503)
		}
		return h.d.defaultTicket(project, id, o)
	})
	h.drive(h.press("e"))
	mustContain(t, h.paneText(), "Description · whole text", "Possibly stale")
}

// Review round 2, probes: metadata edge cases: a blank type is unknown, a
// long one is bounded, 1 MiB is named so.
func TestR2AttachmentMetadataEdges(t *testing.T) {
	blank, long := "   ", strings.Repeat("x", 200)
	if _, got := attachmentLine(api.Attachment{Filename: "f", ContentType: &blank}); !strings.Contains(got, "type unknown") {
		t.Fatalf("blank: %q", got)
	}
	if _, got := attachmentLine(api.Attachment{Filename: "f", ContentType: &long}); strings.Contains(got, long) || !strings.Contains(got, "…") {
		t.Fatalf("long: %q", got)
	}
	if got := sizeOf(1 << 20); got != "1.0 MiB" {
		t.Fatalf("1 MiB: %q", got)
	}
}

// Review round 2, probes: after a missing comment e is not offered for it
// again until Esc; a reload without the comment closes the marker (no
// state the person cannot see, Esc goes back at once).
func TestR2AMissingCommentIsNotOfferedAgainAndLeavesWithIt(t *testing.T) {
	h := newHarness(t)
	h.openSix()
	h.press("tab")
	gone := false
	h.f.setTicket(func(project, id string, o api.TicketOptions) transport.Answer {
		if o.PostID != "" {
			return transport.Answer{Class: transport.NotFound, Message: "No comment found.", Status: 200, Sent: true}
		}
		d := h.d
		if gone {
			d.sixComments = 29
		}
		return d.defaultTicket(project, id, o)
	})
	h.drive(h.press("e"))
	mustContain(t, h.paneText(), "Comment unavailable")
	if h.m.allowed()[Expand] {
		t.Fatal("e offered again for the missing comment")
	}
	gone = true
	h.drive(h.press("R"))
	if h.m.app.detail.expansion != nil {
		t.Fatal("the marker of a comment no longer on the page stays")
	}
	if !strings.Contains(helpText(h.m), "esc back") {
		t.Fatalf("help %q", helpText(h.m))
	}
}

// Review round 2, finding 1, in flight: a read of the page still out when
// the comment is found missing is canceled, and the reload is a request of
// its own (another share key), not a join of it.
func TestR2TheReloadDoesNotJoinAReadStillOut(t *testing.T) {
	h := newHarness(t)
	h.openSix()
	h.withoutBookedTime()
	h.press("tab")
	release := make(chan struct{})
	var held atomic.Bool
	h.f.setTicket(func(p, id string, o api.TicketOptions) transport.Answer {
		if o.PostID != "" {
			return transport.Answer{Class: transport.NotFound, Message: "No comment found.", Status: 200, Sent: true}
		}
		if held.CompareAndSwap(false, true) {
			<-release // the read that left before: the ticket still existed
			return h.d.defaultTicket(p, id, o)
		}
		return transport.Answer{Class: transport.NotFound, Message: "No ticket found.", Status: 200, Sent: true}
	})
	older := h.background(h.press("R"))
	h.waitFor("the older read on the wire", held.Load)
	var olderShare string
	for _, w := range h.m.app.waiters {
		if w.ticket.Area == gateway.Detail {
			olderShare = w.ticket.ShareKey()
		}
	}
	cmds := h.feed(messages(t, h.press("e"))...)
	for _, w := range h.m.app.waiters {
		if w.ticket.Area == gateway.Detail && !w.canceled && w.ticket.ShareKey() == olderShare {
			t.Fatal("the reload shares the request that left before")
		}
	}
	h.driveWithin(cmds...)
	close(release)
	h.feed(h.receive(older))
	if h.m.app.detail.known {
		t.Fatal("the deleted ticket stays shown")
	}
}
