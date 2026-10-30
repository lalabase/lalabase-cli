//go:build linux || darwin

package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/inboxfixture"
)

// The ticket page by page (C1d) through a real terminal, against the
// loopback fixture: Tab between the sections, the comments' pages and
// order, the attachments' pages, a comment and the description whole, a
// failed page and R, the vanished comment, q and Ctrl-C, at 80 and 120
// columns.

const keyTab = "\t"

var ticket6 = inboxfixture.TicketID(inboxfixture.ProjectWebsite, 6)

// openSix goes from the chooser to ticket #6 of Website and opens it.
func openSix(t *testing.T, c *terminalProcess, width uint16) {
	t.Helper()
	openWebsite(t, c, width)
	openTicketBelow(t, c, width, 1, 6, "Description of six", "esc back")
}

// openTicketBelow moves the list's cursor from ticket from down to ticket
// to and opens it, waiting for markers.
func openTicketBelow(t *testing.T, c *terminalProcess, width uint16, from, to int, markers ...string) {
	t.Helper()
	for n := from + 1; n <= to; n++ {
		// The numbers stand right-aligned as wide as the page's widest,
		// after the agents' column of two cells (C9a).
		title := fmt.Sprintf(">    #%d Ticket %d", n, n)
		if n == 2 {
			title = ">    #2 Export als CSV"
		}
		c.act(t, keyDown, width, title)
	}
	c.act(t, keyEnter, width, markers...)
}

// sixReads are the reads of ticket #6, one line each: comments page,
// order, attachments page, and the expansion's argument when there is one.
// Either way (K1): REST's log names the same arguments, and its reads
// always carry a page; its one comment is a route of its own, logged with
// the comment's id as post_id.
func sixReads(f *inboxfixture.Fixture) []string {
	return readsOf(f, ticket6)
}

func readsOf(f *inboxfixture.Fixture, ticket string) []string {
	var out []string
	for _, r := range f.Requests() {
		if !isTicketRead(r) || r.Args["ticket_id"] != ticket {
			continue
		}
		line := fmt.Sprintf("%v/%v/%v", r.Args["posts_page"], r.Args["posts_order"], r.Args["attachments_page"])
		if v, ok := r.Args["full_text"]; ok {
			line += fmt.Sprintf(" full_text=%v", v)
		}
		if v, ok := r.Args["post_id"]; ok {
			line += fmt.Sprintf(" post_id=%v", v)
		}
		out = append(out, fmt.Sprintf("%s %d", line, r.Status))
	}
	return out
}

// Over the tools (a server before R1): the server cuts #6's description
// and newest comments, e reads them whole.
func TestPTYTicketPagingFlow(t *testing.T) {
	t.Parallel()

	for _, width := range []uint16{80, 120} {
		t.Run(fmt.Sprintf("%d", width), func(t *testing.T) {
			c, f := startInbox(t, inboxfixture.Normal, width, inboxfixture.BeforeR1())
			openSix(t, c, width)
			c.expect(t, "the server's cut", width, "Text shortened by server")
			c.act(t, keyTab, width, "» Comments · newest first", "Six comment 30 begins", "] next page")
			c.act(t, "]", width, "page 2 of 2", "Six comment 5")
			// "o newest first" in the help: the page has come and o is
			// offered again (while a page loads, o is not, and a press then
			// does nothing; the CI of #22 sent the second o into that gap).
			c.act(t, "o", width, "oldest first · page 1 of 2", "Six comment 1", "o newest first")
			c.act(t, "o", width, "newest first · page 1 of 2", "Six comment 30 begins")
			c.act(t, "e", width, "Six comment 30 begins and ends here.", "esc collapse")
			c.act(t, keyEsc, width, "esc back")
			c.act(t, keyTab, width, "» Attachments · page 1 of 2", "six-01.txt", "1000 B")
			c.act(t, "]", width, "Attachments · page 2 of 2", "six-12.txt")
			c.act(t, keyTab, width, "» Description")
			c.act(t, "e", width, "» Description · whole text", "esc collapse")
			c.act(t, keyEsc, width, "esc back")
			c.act(t, keyEsc, width, "esc projects")
			if err := c.send("q"); err != nil {
				t.Fatal(err)
			}
			c.finish(t, 0)
			if err := terminalOutputError(c.bytes()); err != nil {
				t.Fatalf("%v\n%q", err, c.bytes())
			}
			comment := "post_id=" + inboxfixture.SixPostID(30)
			want := []string{
				"1/newest/1 200", // the open (or the preview it found fresh)
				"2/newest/1 200", // ]
				"1/<nil>/1 200",  // o: oldest first is the server's default, not sent
				"1/newest/1 200", // o: page 1 newest again, from the cache and read once
				"<nil>/<nil>/<nil> " + comment + " 200",
				"1/newest/2 200", // ] in the attachments
				"1/newest/2 full_text=true 200",
			}
			if got := sixReads(f); strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Fatalf("reads of #6:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
			}
			if way := viewWay(f); way != "mcp" {
				t.Fatalf("the ticket view went over %q", way)
			}
		})
	}
}

// Over REST (K1): #6 comes whole, nothing to expand; its pages and order
// as over the tools, every read of a ticket with a page. #8 is longer than
// the pane shows: cut by the view, e reads the description again and the
// comment on its own route.
func TestPTYTicketPagingFlowOverREST(t *testing.T) {
	t.Parallel()

	for _, width := range []uint16{80, 120} {
		t.Run(fmt.Sprintf("%d", width), func(t *testing.T) {
			c, f := startInbox(t, inboxfixture.Normal, width)
			openSix(t, c, width)
			// The description's end ("and the whole rest."), wherever the
			// pane wraps it.
			c.expect(t, "the whole description", width, "rest.")
			if c.screenShows(width, "Text shortened") || c.screenShows(width, "e expand") {
				t.Fatalf("a whole text offers e:\n%s", strings.Join(renderScreen(c.bytes(), 24, int(width)), "\n"))
			}
			c.act(t, keyTab, width, "» Comments · newest first", "begins and ends here.", "] next page")
			c.act(t, "]", width, "page 2 of 2", "Six comment 5")
			c.act(t, "o", width, "oldest first · page 1 of 2", "Six comment 1", "o newest first")
			c.act(t, "o", width, "newest first · page 1 of 2", "begins and ends here.")
			c.act(t, keyTab, width, "» Attachments · page 1 of 2", "six-01.txt", "1000 B")
			c.act(t, "]", width, "Attachments · page 2 of 2", "six-12.txt")
			c.act(t, keyEsc, width, "esc projects")
			// The view's note stands under the cut text, far below; e is
			// offered on the description.
			openTicketBelow(t, c, width, 6, 8, "Description of eight", "e expand")
			c.act(t, "e", width, "» Description · whole text", "esc collapse")
			c.act(t, keyEsc, width, "esc back")
			c.act(t, keyTab, width, "» Comments · newest first", "Comment of eight", "e expand")
			c.act(t, "e", width, "esc collapse")
			c.act(t, keyEsc, width, "esc back")
			if err := c.send("q"); err != nil {
				t.Fatal(err)
			}
			c.finish(t, 0)
			if err := terminalOutputError(c.bytes()); err != nil {
				t.Fatalf("%v\n%q", err, c.bytes())
			}
			want := []string{"1/newest/1 200", "2/newest/1 200", "1/<nil>/1 200", "1/newest/1 200", "1/newest/2 200"}
			if got := sixReads(f); strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Fatalf("reads of #6:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
			}
			// #8: the open, the description whole (a page again: REST has
			// no full_text), the comment on its route.
			eight := inboxfixture.TicketID(inboxfixture.ProjectWebsite, 8)
			want = []string{"1/newest/1 200", "1/newest/1 200", "<nil>/<nil>/<nil> post_id=" + inboxfixture.EightPostID + " 200"}
			if got := readsOf(f, eight); strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Fatalf("reads of #8:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
			}
			if way := viewWay(f); way != "rest" {
				t.Fatalf("the ticket view went over %q", way)
			}
			for _, r := range f.Requests() {
				if r.Route == "tickets.show" && r.Args["posts_page"] == nil {
					t.Fatalf("a ticket read without a page: %+v", r)
				}
			}
		})
	}
}

// Page 2 of #6's comments fails twice (the queue's retry included): the
// section says so, R reads it.
func TestPTYTicketPageFailsAndRReadsIt(t *testing.T) {
	t.Parallel()

	c, f := startInbox(t, inboxfixture.Temporary, 80)
	openSix(t, c, 80)
	c.act(t, keyTab, 80, "» Comments · newest first")
	c.act(t, "]", 80, "This page did not load. R retry.")
	c.act(t, "R", 80, "page 2 of 2", "Six comment 5")
	if err := c.send("q"); err != nil {
		t.Fatal(err)
	}
	c.finish(t, 0)
	want := []string{"1/newest/1 200", "2/newest/1 503", "2/newest/1 503", "2/newest/1 200"}
	if got := sixReads(f); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("reads of #6 %v, want %v", got, want)
	}
}

// The newest comment vanished: its text is hidden, the ticket stays, and
// the ticket is read once without post_id. Over the tools: #6's comment
// is cut there.
func TestPTYAVanishedComment(t *testing.T) {
	t.Parallel()

	c, f := startInbox(t, inboxfixture.Vanished, 120, inboxfixture.BeforeR1())
	openSix(t, c, 120)
	c.act(t, keyTab, 120, "» Comments · newest first", "Six comment 30 begins")
	c.act(t, "e", 120, "Comment unavailable", "Six comment 29 begins")
	if c.screenShows(120, "Six comment 30 begins") {
		t.Fatal("the text of the vanished comment is still shown")
	}
	c.act(t, keyEsc, 120, "Six comment 30 begins", "esc back")
	if err := c.send("q"); err != nil {
		t.Fatal(err)
	}
	c.finish(t, 0)
	want := []string{"1/newest/1 200", "<nil>/<nil>/<nil> post_id=" + inboxfixture.SixPostID(30) + " 200", "1/newest/1 200"}
	if got := sixReads(f); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("reads of #6 %v, want %v", got, want)
	}
}

// Over REST the same: #8's newest comment, cut by the view, vanished on
// its route.
func TestPTYAVanishedCommentOverREST(t *testing.T) {
	t.Parallel()

	c, f := startInbox(t, inboxfixture.Vanished, 120)
	openWebsite(t, c, 120)
	openTicketBelow(t, c, 120, 1, 8, "Description of eight", "esc back")
	c.act(t, keyTab, 120, "» Comments · newest first", "Comment of eight")
	c.act(t, "e", 120, "Comment unavailable")
	if c.screenShows(120, "Comment of eight") {
		t.Fatal("the text of the vanished comment is still shown")
	}
	c.act(t, keyEsc, 120, "Comment of eight", "esc back")
	if err := c.send("q"); err != nil {
		t.Fatal(err)
	}
	c.finish(t, 0)
	eight := inboxfixture.TicketID(inboxfixture.ProjectWebsite, 8)
	want := []string{"1/newest/1 200", "<nil>/<nil>/<nil> post_id=" + inboxfixture.EightPostID + " 404", "1/newest/1 200"}
	if got := readsOf(f, eight); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("reads of #8 %v, want %v", got, want)
	}
}

// Ctrl-C with an expansion open restores the terminal and exits with 130.
func TestPTYTicketExpansionInterrupt(t *testing.T) {
	t.Parallel()

	c, _ := startInbox(t, inboxfixture.Normal, 120, inboxfixture.BeforeR1())
	openSix(t, c, 120)
	c.act(t, "e", 120, "Description · whole text")
	if err := c.send("\x03"); err != nil {
		t.Fatal(err)
	}
	c.finish(t, 130)
}
