package ui

// Review round 3 of C1d: no finding; these tests close the probes that
// survived the suite (the reviewer's scratch tests, kept as guards).

import (
	"testing"

	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/transport"
)

func r3MissingComments(h *harness) {
	h.f.setTicket(func(project, id string, o api.TicketOptions) transport.Answer {
		if o.PostID != "" {
			return transport.Answer{Class: transport.NotFound, Message: "No comment found for " + o.PostID + ".", Status: 200, Sent: true}
		}
		return h.d.defaultTicket(project, id, o)
	})
}

// A missing comment while a page turn is out: the reload reads the page
// asked, and that page lands.
func TestR3ReloadAfterMissingCommentReadsThePageAsked(t *testing.T) {
	h := newHarness(t)
	h.openSix()
	h.press("tab")
	r3MissingComments(h)
	cnf := messages(t, h.press("e"))
	h.press("]") // page 2 asked, not run
	before := len(h.f.detailReads())
	h.drive(h.feed(cnf...)...)
	reads := h.f.detailReads()[before:]
	if len(reads) != 1 || reads[0].o.PostID != "" || reads[0].o.PostsPage != 2 {
		t.Fatalf("reads %+v", reads)
	}
	d := h.m.app.detail
	if d.shown != d.key || d.key.PostsPage != 2 || d.loading {
		t.Fatalf("shown %+v key %+v loading %v", d.shown, d.key, d.loading)
	}
	mustContain(t, h.paneText(), "page 2 of 2")
	if d.expansion != nil {
		t.Fatal("the marker of a comment not on the page stays")
	}
}

// Between the missing comment and its reload the ticket loads: no page key
// is offered, the marker shows.
func TestR3TheTicketLoadsUntilTheReloadLands(t *testing.T) {
	h := newHarness(t)
	h.openSix()
	h.press("tab")
	r3MissingComments(h)
	cmds := h.feed(messages(t, h.press("e"))...)
	if len(cmds) == 0 {
		t.Fatal("no reload")
	}
	mustContain(t, h.paneText(), "Comment unavailable")
	if !h.m.app.detail.loading || h.m.allowed()[NextPage] || h.m.allowed()[Order] {
		t.Fatal("the ticket does not load while its reload is out")
	}
}

// A page shown from the cache keeps a selection on that page.
func TestR3ACachedPageKeepsASelectionOnIt(t *testing.T) {
	h := newHarness(t)
	h.openSix()
	h.press("tab")
	h.drive(h.press("]"))
	h.press("down")
	h.press("[") // page 1 from the cache, its reload not run
	if p := h.m.app.detail.selectedPost(); p == nil {
		t.Fatalf("no comment of the page is selected (%q)", h.m.app.detail.post)
	}
}

// A second expansion closes the first: its read is canceled.
func TestR3ASecondExpansionCancelsTheFirst(t *testing.T) {
	h := newHarness(t)
	h.openSix()
	h.press("e") // the description, not run
	first := h.m.app.detail.expansion.key
	h.press("tab")
	h.press("e") // comment 30
	for _, w := range h.m.app.waiters {
		if w.ticket.Area == gateway.Expansion && w.ticket.Key == first && !w.canceled {
			t.Fatal("the first expansion's read is still out")
		}
	}
}

// A ticket shown again from the cache has a comment selected at once.
func TestR3ACachedTicketHasACommentSelected(t *testing.T) {
	h := newHarness(t)
	h.openSix()
	h.press("esc")
	h.press("down")
	h.press("up")
	d := h.m.app.detail
	if d == nil || !d.cached || d.id != ticketID(website, 6) {
		t.Fatalf("ticket #6 is not shown from the cache: %+v", d)
	}
	if d.selectedPost() == nil {
		t.Fatal("no comment is selected")
	}
}

// The description is read whole with the options of the page asked, also
// while that page is on its way (round 3 backlog: the wording and the
// behaviour agree).
func TestR3TheDescriptionIsReadWithThePageAsked(t *testing.T) {
	h := newHarness(t)
	h.openSix()
	h.press("tab")
	h.press("]") // page 2 asked, not run
	h.press("tab")
	h.press("tab") // the description
	h.drive(h.press("e"))
	want := api.TicketOptions{PostsPage: 2, PostsPerPage: 25, Newest: true, AttachmentsPage: 1, FullText: true}
	if got := h.lastRead(); got != want {
		t.Fatalf("read %+v", got)
	}
}
