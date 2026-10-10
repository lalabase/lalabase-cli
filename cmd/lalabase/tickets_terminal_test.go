//go:build linux || darwin

package main

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gboyke/lalabase-cli/internal/inboxfixture"
)

// The tickets section (C1c) through a real terminal, against the loopback
// fixture: projects, a paged ticket list, the search, the preview after
// the pause, the open and the way back, at 80 and 120 columns.

const (
	website = inboxfixture.ProjectWebsite
	ticket2 = "00000000-0000-4000-8000-000000500002"
)

// ticketRequests sorts the fixture's log: the projects reads, the ticket
// list reads and the ticket reads, in order, and the position of the first
// of them among all requests.
type ticketRequests struct {
	projects, lists, tickets []inboxfixture.Request
	firstAt, inboxAt         int
}

func sortTicketRequests(f *inboxfixture.Fixture) ticketRequests {
	out := ticketRequests{firstAt: -1, inboxAt: -1}
	for i, r := range f.Requests() {
		switch {
		case r.Path == "/api/v1/projects":
			out.projects = append(out.projects, r)
		case r.Tool == "list_tickets":
			out.lists = append(out.lists, r)
		case r.Tool == "get_ticket":
			out.tickets = append(out.tickets, r)
		case r.Tool == "list_notifications":
			if out.inboxAt < 0 {
				out.inboxAt = i
			}
			continue
		default:
			continue
		}
		if out.firstAt < 0 {
			out.firstAt = i
		}
	}
	return out
}

// cursorUnder reports whether the chooser's cursor is on the section row
// right under the organisation's heading: the headings carry no marker,
// and every organisation has an "Inbox" row. The chooser is the leftmost
// pane whenever it shows, so the lines start with it.
func cursorUnder(lines []string, heading, section string) bool {
	for i, line := range lines {
		if !strings.HasPrefix(line, "  "+heading) {
			continue
		}
		// The section rows of the heading follow it, indented, until the
		// next heading.
		for j := i + 1; j < len(lines) && (strings.HasPrefix(lines[j], "    ") || strings.HasPrefix(lines[j], ">   ")); j++ {
			if strings.HasPrefix(lines[j], ">   "+section) {
				return true
			}
		}
	}
	return false
}

// moveTo presses keys until the chooser's cursor is on the section of the
// organisation, at most n presses.
func (c *terminalProcess) moveTo(t *testing.T, key string, width uint16, heading, section string, n int) {
	t.Helper()
	for range n {
		if err := c.send(key); err != nil {
			t.Fatal(err)
		}
		if err := c.until("the cursor", readyLimit, func() bool {
			return cursorUnder(renderScreen(c.bytes(), 24, int(width)), heading, section)
		}); err == nil {
			return
		}
	}
	t.Fatalf("the cursor did not reach %s > %s:\n%s", heading, section, strings.Join(renderScreen(c.bytes(), 24, int(width)), "\n"))
}

// openWebsite goes from the chooser to the tickets of Website-Relaunch.
func openWebsite(t *testing.T, c *terminalProcess, width uint16) {
	t.Helper()
	c.moveTo(t, keyDown, width, "Nordlicht GmbH", "Tickets", 2)
	c.act(t, keyEnter, width, "Projects", "Website-Relaunch", "Nordlicht GmbH | Tickets")
	c.act(t, keyDown, width, "> Handbuch")
	c.act(t, keyDown, width, "> Intern")
	c.act(t, keyDown, width, "> Website-Relaunch")
	c.act(t, keyEnter, width, "#1 Login bricht ab", "Nordlicht GmbH | Tickets | Website-Relaunch | page 1 of 2")
}

func TestPTYTicketsFlow(t *testing.T) {
	for _, width := range []uint16{80, 120} {
		t.Run(fmt.Sprintf("%d", width), func(t *testing.T) {
			c, f := startInbox(t, inboxfixture.Normal, width)
			c.expect(t, "the directory and the counters", width, "All organisations", "Nordlicht GmbH", "Südwind AG", "33", "30")
			openWebsite(t, c, width)
			// The preview of the first row after the pause, without a key.
			c.expect(t, "the preview of #1", width, "Nach dem Absenden", "Tritt nur in Safari auf.")
			if c.screenShows(width, "esc back") {
				t.Fatal("a preview opened the ticket")
			}
			c.act(t, keyDown, width, "#2 Export als CSV", "No description", "No comments")
			c.act(t, keyEnter, width, "esc back")
			other := uint16(120)
			if width == 120 {
				other = 80
			}
			c.resize(t, other)
			c.expect(t, "the ticket after the resize", other, "#2 Export als CSV", "No description")
			c.resize(t, width)
			c.expect(t, "the ticket after the resize back", width, "#2 Export als CSV", "No description", "esc back")
			c.act(t, keyEsc, width, "esc projects")
			// The pane's own lines, not the list row with the same title.
			if !c.screenShows(width, "No description", "No comments") {
				t.Fatal("esc dropped the preview")
			}
			c.act(t, "/", width, "Search: _")
			c.act(t, "Export", width, "Search: Export_")
			c.act(t, keyEnter, width, `search "Export"`, "page 1 of 1", "#2 Export als CSV")
			c.act(t, keyEsc, width, "Projects", "esc organisations")
			// The chooser's own help, so the Esc was taken before the q is
			// sent (an Esc followed at once by a key reads as alt+key).
			c.act(t, keyEsc, width, "up/down highlight")
			if err := c.send("q"); err != nil {
				t.Fatal(err)
			}
			c.finish(t, 0)
			if err := terminalOutputError(c.bytes()); err != nil {
				t.Fatalf("%v\n%q", err, c.bytes())
			}
			reqs := sortTicketRequests(f)
			if reqs.firstAt < 0 || reqs.inboxAt < 0 || reqs.firstAt < reqs.inboxAt {
				t.Fatalf("the section was read before the start finished: first %d, inbox %d", reqs.firstAt, reqs.inboxAt)
			}
			if len(reqs.projects) != 1 || reqs.projects[0].Status != http.StatusOK {
				t.Fatalf("project reads %+v", reqs.projects)
			}
			var lists []string
			for _, r := range reqs.lists {
				lists = append(lists, fmt.Sprintf("%v/%v/%v", r.Args["project_id"], r.Args["page"], r.Args["q"]))
			}
			if want := []string{website + "/1/<nil>", website + "/1/Export"}; strings.Join(lists, " ") != strings.Join(want, " ") {
				t.Fatalf("ticket list reads %v, want %v", lists, want)
			}
			var reads []string
			for _, r := range reqs.tickets {
				reads = append(reads, fmt.Sprintf("%v/%v/%v/%v", r.Args["ticket_id"], r.Args["posts_page"], r.Args["posts_order"], r.Args["attachments_page"]))
			}
			// #1 previewed once; #2 previewed once and found fresh by the
			// Enter that came after the preview landed (the join of a
			// pending preview is held by the ui tests).
			if want := []string{inboxfixture.TicketLogin + "/1/newest/1", ticket2 + "/1/newest/1"}; strings.Join(reads, " ") != strings.Join(want, " ") {
				t.Fatalf("ticket reads %v, want %v", reads, want)
			}
		})
	}
}

// Page 2 of the tickets fails twice, R reads it.
func TestPTYTicketsPageRetry(t *testing.T) {
	c, f := startInbox(t, inboxfixture.Temporary, 80)
	openWebsite(t, c, 80)
	c.act(t, "]", 80, "Could not reach the server")
	c.act(t, "R", 80, "#26 Ticket 26", "page 2 of 2")
	c.act(t, "[", 80, "#1 Login bricht ab", "page 1 of 2")
	if err := c.send("q"); err != nil {
		t.Fatal(err)
	}
	c.finish(t, 0)
	reqs := sortTicketRequests(f)
	var seen []string
	for _, r := range reqs.lists {
		seen = append(seen, fmt.Sprintf("%v/%d", r.Args["page"], r.Status))
	}
	if want := []string{"1/200", "2/503", "2/503", "2/200", "1/200"}; strings.Join(seen, " ") != strings.Join(want, " ") {
		t.Fatalf("ticket list reads %v, want %v", seen, want)
	}
}

// A 401 on the preview of a ticket ends the session: nothing of the
// tickets stays on the screen, keys do nothing but leave.
func TestPTYTicketPreviewExpiresOn401(t *testing.T) {
	c, _ := startInbox(t, inboxfixture.Unauthorized, 80)
	openWebsite(t, c, 80)
	c.expect(t, "the preview of #1", 80, "Nach dem Absenden")
	c.act(t, keyDown, 80, "Session expired. Restart lalabase.")
	if c.screenShows(80, "Export als CSV") || c.screenShows(80, "Login bricht ab") || c.screenShows(80, "Website") {
		t.Fatalf("the expiry left data on screen:\n%s", strings.Join(renderScreen(c.bytes(), 24, 80), "\n"))
	}
	for _, key := range []string{"R", keyEnter, "/", keyEsc, "]"} {
		if err := c.send(key); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(300 * time.Millisecond)
	if c.screenShows(80, "Export als CSV") || !c.screenShows(80, "Session expired") {
		t.Fatalf("the session came back:\n%s", strings.Join(renderScreen(c.bytes(), 24, 80), "\n"))
	}
	if err := c.send("q"); err != nil {
		t.Fatal(err)
	}
	c.finish(t, 0)
	if err := terminalOutputError(c.bytes()); err != nil {
		t.Fatal(err)
	}
}

// Ctrl-C in the ticket list restores the terminal and exits with 130.
func TestPTYTicketsInterrupt(t *testing.T) {
	c, _ := startInbox(t, inboxfixture.Normal, 120)
	openWebsite(t, c, 120)
	if err := c.send("\x03"); err != nil {
		t.Fatal(err)
	}
	c.finish(t, 130)
}

// An older server: the ticket is read plain and its comments are named as
// the first ones, oldest first.
func TestPTYTicketsOnAnOlderServer(t *testing.T) {
	c, f := startInbox(t, inboxfixture.Older, 120)
	c.expect(t, "the limitation", 120, "Showing the first")
	openWebsite(t, c, 120)
	c.act(t, keyDown, 120, "#2 Export als CSV")
	c.act(t, keyDown, 120, "#3 Ticket 3", "Comments · oldest first · 30 comments", "Comment 1")
	if err := c.send("q"); err != nil {
		t.Fatal(err)
	}
	c.finish(t, 0)
	for _, r := range sortTicketRequests(f).tickets {
		for _, arg := range []string{"posts_page", "posts_per_page", "posts_order", "attachments_page"} {
			if _, ok := r.Args[arg]; ok {
				t.Fatalf("an older server was sent %s: %+v", arg, r)
			}
		}
	}
}
