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
	for n := 2; n <= 6; n++ {
		// The numbers stand right-aligned as wide as the page's widest,
		// after the agents' column of two cells (C9a).
		title := fmt.Sprintf(">    #%d Ticket %d", n, n)
		if n == 2 {
			title = ">    #2 Export als CSV"
		}
		c.act(t, keyDown, width, title)
	}
	c.act(t, keyEnter, width, "Description of six", "Text shortened by server", "esc back")
}

// sixReads are the reads of ticket #6, one line each: comments page,
// order, attachments page, and the expansion's argument when there is one.
func sixReads(f *inboxfixture.Fixture) []string {
	var out []string
	for _, r := range f.Requests() {
		if r.Tool != "get_ticket" || r.Args["ticket_id"] != ticket6 {
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

func TestPTYTicketPagingFlow(t *testing.T) {
	for _, width := range []uint16{80, 120} {
		t.Run(fmt.Sprintf("%d", width), func(t *testing.T) {
			c, f := startInbox(t, inboxfixture.Normal, width)
			openSix(t, c, width)
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
		})
	}
}

// Page 2 of #6's comments fails twice (the queue's retry included): the
// section says so, R reads it.
func TestPTYTicketPageFailsAndRReadsIt(t *testing.T) {
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
// the ticket is read once without post_id.
func TestPTYAVanishedComment(t *testing.T) {
	c, f := startInbox(t, inboxfixture.Vanished, 120)
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

// Ctrl-C with an expansion open restores the terminal and exits with 130.
func TestPTYTicketExpansionInterrupt(t *testing.T) {
	c, _ := startInbox(t, inboxfixture.Normal, 120)
	openSix(t, c, 120)
	c.act(t, "e", 120, "Description · whole text")
	if err := c.send("\x03"); err != nil {
		t.Fatal(err)
	}
	c.finish(t, 130)
}
