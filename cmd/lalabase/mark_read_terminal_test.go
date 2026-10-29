//go:build linux || darwin

package main

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gboyke/lalabase-cli/internal/inboxfixture"
)

// Mark as read (C1f) through a real terminal, against the loopback
// fixture: the whole C1 run at 80 and 120 columns (start, organisation,
// inbox, details, the ticket the notification names, its pages and the
// expansion, r, back to the updated inbox), then read-only, the unknown
// outcome, the refusals, a 429, a 401, the slow answer with a moved
// selection, a resize, q and Ctrl-C with the terminal restored.

// markLimit is the case limit of a run with a write: the start, the
// ticket's pages and the reloads after the write.
const markLimit = 40 * time.Second

// row6 is the sixth unread row of A: it names ticket #6 of Website, the
// one with pages and shortened texts.
var row6 = fmt.Sprintf("00000000-0000-4000-8000-%012d", 100006)

func startMark(t *testing.T, scenario inboxfixture.Scenario, width uint16, marker string) (*terminalProcess, *inboxfixture.Fixture) {
	t.Helper()
	f, url := fixtureServer(t, scenario)
	c := startTerminalWith(t, "mark-"+string(scenario), width, terminalOptions{url: url, marker: marker, limit: markLimit})
	if err := c.ready(); err != nil {
		t.Fatalf("%v\n%s", err, c.bytes())
	}
	return c, f
}

// openInboxA goes from the chooser to A's inbox.
func openInboxA(t *testing.T, c *terminalProcess, width uint16) {
	t.Helper()
	c.expect(t, "the directory and the counters", width, "All organisations", "Nordlicht GmbH", "Südwind AG", "33", "30")
	c.moveTo(t, keyDown, width, "Nordlicht GmbH", "Inbox", 1)
	c.act(t, keyEnter, width, "#1 Benachrichtigung", "Nordlicht GmbH › Inbox · page 1 of 2")
}

// openRow6Details moves to the sixth row and opens its details. The fifth
// row names no ticket, so its first line has no number.
func openRow6Details(t *testing.T, c *terminalProcess, width uint16) {
	t.Helper()
	for n := 2; n <= 6; n++ {
		marker := fmt.Sprintf("> #%d Benachrichtigung", n)
		if n == 5 {
			marker = "> Benachrichtigung"
		}
		c.act(t, keyDown, width, marker)
	}
	// The id's tail: at 80 columns the whole id does not fit the pane.
	c.act(t, keyEnter, width, row6[len(row6)-12:], "State: unread", "o open ticket", "r mark read", "esc close")
}

// sortedReads sorts the reads of one batch: Bubble Tea runs the commands
// of a batch side by side, so the counters and the page come in either
// order.
func sortedReads(reads []string, n int) []string {
	out := slices.Clone(reads)
	if len(out) >= n {
		slices.Sort(out[:n])
	}
	return out
}

// markRequests are the write attempts, one line each: the notification,
// whether a key came along, and the status.
// requestLines are the fixture's requests, one line each, for a failure's
// message: the tool or path, the arguments that name a page, the status.
func requestLines(f *inboxfixture.Fixture) []string {
	var out []string
	for _, r := range f.Requests() {
		what := r.Path
		if r.Tool != "" {
			what = r.Tool
		}
		out = append(out, fmt.Sprintf("%s %s org=%v page=%v id=%v -> %d", r.Method, what, r.Args["organisation_id"], r.Args["page"], r.Args["notification_id"], r.Status))
	}
	return out
}

func markRequests(f *inboxfixture.Fixture) []string {
	var out []string
	for _, r := range f.Requests() {
		if r.Tool != "mark_notification_read" {
			continue
		}
		key, _ := r.Args["idempotency_key"].(string)
		out = append(out, fmt.Sprintf("%v key=%v %d", r.Args["notification_id"], key != "", r.Status))
	}
	return out
}

// rowGone waits until the screen no longer shows the marker: the row left
// with the reload after a write (at 80 columns the counters are not on
// the screen, the list is). The marker is short enough to be seen in a
// title cut for the row's "marking…", and no row may still say that: a
// row whose title is cut further must not pass for one that left.
func (c *terminalProcess) rowGone(t *testing.T, width uint16, marker string) {
	t.Helper()
	if err := c.until("the row leaving", readyLimit, func() bool {
		return !c.screenShows(width, marker) && !c.screenShows(width, "marking…")
	}); err != nil {
		t.Fatalf("%v: %q is still shown\n%s", err, marker, strings.Join(renderScreen(c.bytes(), 24, int(width)), "\n"))
	}
}

// inboxReads are the inbox reads after the start, one line each.
func inboxReads(f *inboxfixture.Fixture, from int) []string {
	var out []string
	for i, r := range f.Requests() {
		if i < from || r.Tool != "list_notifications" {
			continue
		}
		out = append(out, fmt.Sprintf("%v/%v/%d", r.Args["organisation_id"], r.Args["page"], r.Status))
	}
	return out
}

// The whole C1 run at 80 and 120 columns.
func TestPTYMarkReadFlow(t *testing.T) {
	for _, width := range []uint16{80, 120} {
		t.Run(fmt.Sprintf("%d", width), func(t *testing.T) {
			c, f := startMark(t, inboxfixture.Normal, width, "Token can write: yes")
			openInboxA(t, c, width)
			openRow6Details(t, c, width)
			// The ticket the notification names, with its origin in the
			// head; nothing is marked by opening it.
			c.act(t, "o", width, "#6 Ticket 6", "Notification: unread", "Description of six", "r mark read", "esc notification")
			c.act(t, keyTab, width, "» Comments · newest first · page 1 of 2", "Six comment 30 begins")
			c.act(t, "]", width, "page 2 of 2", "Six comment 5")
			c.act(t, keyTab, width, "» Attachments · page 1 of 2", "six-01.txt")
			c.act(t, "]", width, "Attachments · page 2 of 2", "six-12.txt")
			c.act(t, keyTab, width, "» Description")
			c.act(t, "e", width, "» Description · whole text", "esc collapse")
			c.act(t, keyEsc, width, "esc notification")
			if got := markRequests(f); len(got) != 0 {
				t.Fatalf("opening and paging wrote: %v", got)
			}
			before := len(f.Requests())
			other := uint16(120)
			if width == 120 {
				other = 80
			}
			// r: the head says so, the ticket stays open and usable.
			c.act(t, "r", width, "Marked as read")
			// The pane keeps the description in sight (the head may be
			// scrolled away at 80 columns after the Tabs).
			if !c.screenShows(width, "Description of six", "esc notification") {
				t.Fatalf("the ticket went with the mark:\n%s", strings.Join(renderScreen(c.bytes(), 24, int(width)), "\n"))
			}
			c.resize(t, other)
			c.expect(t, "the ticket after the resize", other, "Description of six", "Marked as read")
			c.resize(t, width)
			c.expect(t, "the ticket after the resize back", width, "Description of six", "Marked as read")
			// Esc: the row left with the reload, so the list is where the
			// person lands; the sixth row is now #7.
			c.act(t, keyEsc, width, "esc organisations", "page 1 of 2")
			if width == 120 {
				// Back in the list the navigation is on the screen again,
				// and its counters followed the server.
				c.expect(t, "the counters after the write", width, "29", "32")
			}
			if c.screenShows(width, "#6 Benachrichtigung") || c.screenShows(width, "esc close") {
				t.Fatalf("the marked row is still shown:\n%s", strings.Join(renderScreen(c.bytes(), 24, int(width)), "\n"))
			}
			c.act(t, keyEsc, width, "up/down highlight")
			if err := c.send("q"); err != nil {
				t.Fatal(err)
			}
			c.finish(t, 0)
			if err := terminalOutputError(c.bytes()); err != nil {
				t.Fatalf("%v\n%q", err, c.bytes())
			}
			if got := markRequests(f); len(got) != 1 || got[0] != row6+" key=true 200" {
				t.Fatalf("writes %v", got)
			}
			if got := f.Mutations(); len(got) != 1 || got[0].Notification != row6 || got[0].Key == "" {
				t.Fatalf("mutations %+v", got)
			}
			// After the write: the counters (global page 1) and the page
			// shown (A page 1), once each, in either order, and nothing
			// else.
			want := []string{orgA + "/1/200", "<nil>/1/200"}
			if got := sortedReads(inboxReads(f, before), 2); strings.Join(got, " ") != strings.Join(want, " ") {
				t.Fatalf("reads after the write %v, want %v", got, want)
			}
			for _, r := range f.Requests()[before:] {
				if r.Tool == "get_ticket" {
					t.Fatalf("the write reloaded the ticket: %+v", r)
				}
			}
			// The whole run: the start (/me, initialize, tools/list,
			// /organisations, the counters), A's page 1, four reads of
			// ticket #6 (the open, comments page 2, attachments page 2, the
			// description whole), its booked time (S6, read with the open),
			// the write, the two reloads.
			if n := len(f.Requests()); n != 14 {
				var lines []string
				for _, r := range f.Requests() {
					lines = append(lines, fmt.Sprintf("%s %s %s %v", r.Method, r.Path, r.Tool, r.Args))
				}
				t.Fatalf("%d requests in the run:\n%s", n, strings.Join(lines, "\n"))
			}
		})
	}
}

// Read-only: the identity says so, r is not offered and sends nothing,
// the details say why; the token's permission unknown likewise; a server
// without the tool likewise.
func TestPTYMarkReadIsHiddenWithoutThePermission(t *testing.T) {
	for _, tc := range []struct {
		scenario inboxfixture.Scenario
		identity string
		reason   string
	}{
		{inboxfixture.ReadOnly, "Token can write: no", "Mark as read: Read-only token"},
		{inboxfixture.UnknownWrite, "Token can write: unknown", "Mark as read: Write permission unknown"},
		{inboxfixture.NoMarkRead, "Token can write: yes", "Mark as read: Marking as read is not supported"},
	} {
		t.Run(string(tc.scenario), func(t *testing.T) {
			c, f := startMark(t, tc.scenario, 120, tc.identity)
			openInboxA(t, c, 120)
			if c.screenShows(120, "r mark read") {
				t.Fatal("r offered")
			}
			if err := c.send("r"); err != nil {
				t.Fatal(err)
			}
			c.act(t, keyEnter, 120, tc.reason, "esc close")
			if c.screenShows(120, "r mark read") {
				t.Fatal("r offered in the details")
			}
			if err := c.send("r"); err != nil {
				t.Fatal(err)
			}
			time.Sleep(300 * time.Millisecond)
			if err := c.send("q"); err != nil {
				t.Fatal(err)
			}
			c.finish(t, 0)
			if got := markRequests(f); len(got) != 0 {
				t.Fatalf("writes %v", got)
			}
		})
	}
}

// The answer is lost after the server acted: one write, no second one,
// the reads that clear it up, and the state stays unknown although the
// row left the unread page.
func TestPTYMarkReadWithALostAnswer(t *testing.T) {
	c, f := startMark(t, inboxfixture.Lost, 120, "Token can write: yes")
	openInboxA(t, c, 120)
	before := len(f.Requests())
	c.act(t, "r", 120, "29", "32")
	// Both reloads land in either order: wait for the row to leave too.
	c.rowGone(t, 120, "#1 Benachrichtigun")
	// Nothing claims a success.
	if c.screenShows(120, "marked") {
		t.Fatalf("the screen after the lost answer:\n%s", strings.Join(renderScreen(c.bytes(), 24, 120), "\n"))
	}
	// R reads again, writes nothing.
	c.act(t, "R", 120, "#2 Benachrichtigung")
	time.Sleep(300 * time.Millisecond)
	if err := c.send("q"); err != nil {
		t.Fatal(err)
	}
	c.finish(t, 0)
	first := fmt.Sprintf("00000000-0000-4000-8000-%012d", 100001)
	if got := markRequests(f); len(got) != 1 || got[0] != first+" key=true 0" {
		t.Fatalf("writes %v", got)
	}
	if got := f.Mutations(); len(got) != 1 || got[0].Notification != first {
		t.Fatalf("mutations %+v", got)
	}
	want := []string{orgA + "/1/200", "<nil>/1/200", orgA + "/1/200"}
	if got := sortedReads(inboxReads(f, before), 2); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("reads after the write %v, want %v", got, want)
	}
}

// The unknown outcome, seen from the ticket: the head says so while the
// reads are out and after them, and r is the person's again.
func TestPTYMarkReadUnknownOutcomeIsShownOnTheTicket(t *testing.T) {
	c, f := startMark(t, inboxfixture.Lost, 80, "Token can write: yes")
	openInboxA(t, c, 80)
	openRow6Details(t, c, 80)
	c.act(t, "o", 80, "#6 Ticket 6", "Notification: unread")
	c.act(t, "r", 80, "Result unknown")
	if c.screenShows(80, "Marked as read") {
		t.Fatal("a success was claimed")
	}
	c.expect(t, "the reads landed", 80, "r mark read")
	if c.screenShows(80, "refreshing") {
		t.Fatal("still refreshing")
	}
	if err := c.send("q"); err != nil {
		t.Fatal(err)
	}
	c.finish(t, 0)
	if got := markRequests(f); len(got) != 1 {
		t.Fatalf("writes %v", got)
	}
}

// The refusals keep the ticket: archived, not found, not allowed, each on
// its row, shown in the head and in the details; the ticket's cached
// pages stay (no second read of it).
func TestPTYRefusedMarksKeepTheTicket(t *testing.T) {
	c, f := startMark(t, inboxfixture.Refusals, 120, "Token can write: yes")
	openInboxA(t, c, 120)
	for _, tc := range []struct {
		row  int
		text string
	}{{2, "Not marked: refused"}, {3, "Not marked: not_found"}, {4, "Not marked: no_access"}} {
		c.act(t, keyDown, 120, fmt.Sprintf("> #%d Benachrichtigung", tc.row))
		c.act(t, keyEnter, 120, "esc close")
		c.act(t, "o", 120, fmt.Sprintf("#%d ", tc.row), "Notification: unread", "esc notification")
		ticketReads := len(ticketReadsOf(f))
		c.act(t, "r", 120, tc.text, "r mark read")
		if !c.screenShows(120, fmt.Sprintf("#%d ", tc.row), "Description") {
			t.Fatalf("row %d: the ticket went:\n%s", tc.row, strings.Join(renderScreen(c.bytes(), 24, 120), "\n"))
		}
		if len(ticketReadsOf(f)) != ticketReads {
			t.Fatalf("row %d: the refusal reloaded the ticket", tc.row)
		}
		c.act(t, keyEsc, 120, "Mark as read: "+tc.text, "esc close")
		// The list's own help, so the Esc was taken before the next key
		// (an Esc followed at once by a key reads as alt+key).
		c.act(t, keyEsc, 120, "esc organisations")
		if !c.screenShows(120, "not marked") {
			t.Fatalf("row %d: the row does not say so:\n%s", tc.row, strings.Join(renderScreen(c.bytes(), 24, 120), "\n"))
		}
	}
	if err := c.send("q"); err != nil {
		t.Fatal(err)
	}
	c.finish(t, 0)
	if err := terminalOutputError(c.bytes()); err != nil {
		t.Fatal(err)
	}
	if got := markRequests(f); len(got) != 3 {
		t.Fatalf("writes %v", got)
	}
	if got := f.Mutations(); len(got) != 0 {
		t.Fatalf("a refused write was carried out: %+v", got)
	}
	// The counters never moved: 30 and 33 still.
	if !c.screenShows(120, "30", "33") {
		t.Fatalf("counters:\n%s", strings.Join(renderScreen(c.bytes(), 24, 120), "\n"))
	}
}

func ticketReadsOf(f *inboxfixture.Fixture) []inboxfixture.Request {
	var out []inboxfixture.Request
	for _, r := range f.Requests() {
		if r.Tool == "get_ticket" {
			out = append(out, r)
		}
	}
	return out
}

// A 429 on the write: the pause is shown, the queue sends the same key
// once more after the window, and the write counts as done then.
func TestPTYMarkReadAfterARateLimit(t *testing.T) {
	c, f := startMark(t, inboxfixture.RateLimited, 80, "Token can write: yes")
	openInboxA(t, c, 80)
	c.act(t, "r", 80, "Rate limited until")
	c.rowGone(t, 80, "#1 Benachrichtigun")
	c.expect(t, "the retry after the window", 80, "> #2 Benachrichtigung")
	if err := c.send("q"); err != nil {
		t.Fatal(err)
	}
	c.finish(t, 0)
	first := fmt.Sprintf("00000000-0000-4000-8000-%012d", 100001)
	if got := markRequests(f); len(got) != 2 || got[0] != first+" key=true 429" || got[1] != first+" key=true 200" {
		t.Fatalf("writes %v", got)
	}
	if keys := f.Keys(); len(keys) != 2 || keys[0] != keys[1] || keys[0] == "" {
		t.Fatalf("keys %v", keys)
	}
	if got := f.Mutations(); len(got) != 1 {
		t.Fatalf("mutations %+v", got)
	}
}

// A 401 on the write ends the session.
func TestPTYMarkReadExpiresOn401(t *testing.T) {
	c, f := startMark(t, inboxfixture.Unauthorized, 80, "Token can write: yes")
	openInboxA(t, c, 80)
	c.act(t, "r", 80, "Session expired. Restart lalabase.")
	if c.screenShows(80, "Benachrichtigung") || c.screenShows(80, "Anna Albers") {
		t.Fatalf("the expiry left data on screen:\n%s", strings.Join(renderScreen(c.bytes(), 24, 80), "\n"))
	}
	if err := c.send("q"); err != nil {
		t.Fatal(err)
	}
	c.finish(t, 0)
	if got := markRequests(f); len(got) != 1 || !strings.HasSuffix(got[0], " 401") {
		t.Fatalf("writes %v", got)
	}
	if got := f.Mutations(); len(got) != 0 {
		t.Fatalf("mutations %+v", got)
	}
}

// A slow answer: the selection moves on while the write is out, and the
// answer marks the row it was for, not the one under the cursor.
func TestPTYASlowAnswerMarksTheRowItWasFor(t *testing.T) {
	c, f := startMark(t, inboxfixture.Slow, 120, "Token can write: yes")
	openInboxA(t, c, 120)
	c.act(t, "r", 120, "marking…")
	c.act(t, keyDown, 120, "> #2 Benachrichtigung")
	c.act(t, keyEnter, 120, "State: unread", "esc close")
	if !c.screenShows(120, "marking…") {
		t.Fatal("the running mark is not shown on its row")
	}
	// The answer: the first row leaves with the reload, the second stays
	// open and unmarked.
	c.rowGone(t, 120, "#1 Benachrichtigun")
	if c.screenShows(120, "Marked as read") || !c.screenShows(120, "State: unread") {
		t.Fatalf("after the slow answer:\n%s", strings.Join(renderScreen(c.bytes(), 24, 120), "\n"))
	}
	// Back in the list the navigation shows the counters the reload
	// brought.
	c.act(t, keyEsc, 120, "esc organisations")
	c.expect(t, "the answer's counters", 120, "29", "32")
	if err := c.send("q"); err != nil {
		t.Fatal(err)
	}
	c.finish(t, 0)
	first := fmt.Sprintf("00000000-0000-4000-8000-%012d", 100001)
	if got := f.Mutations(); len(got) != 1 || got[0].Notification != first {
		t.Fatalf("mutations %+v", got)
	}
}

// Ctrl-C while a write is out restores the terminal and exits with 130;
// q likewise with 0.
func TestPTYMarkReadInterruptWithAPendingWrite(t *testing.T) {
	for _, tc := range []struct {
		key  string
		code int
	}{{"\x03", 130}, {"q", 0}} {
		c, _ := startMark(t, inboxfixture.Slow, 80, "Token can write: yes")
		openInboxA(t, c, 80)
		c.act(t, "r", 80, "marking…")
		if err := c.send(tc.key); err != nil {
			t.Fatal(err)
		}
		c.finish(t, tc.code)
		if err := terminalOutputError(c.bytes()); err != nil {
			t.Fatal(err)
		}
	}
}

// A network error on the inbox after a write: the page says so and R
// reads it, as before C1f; the write itself was done once.
func TestPTYMarkReadThenANetworkError(t *testing.T) {
	c, f := startMark(t, inboxfixture.Temporary, 80, "Token can write: yes")
	openInboxA(t, c, 80)
	if err := c.send("r"); err != nil {
		t.Fatal(err)
	}
	c.rowGone(t, 80, "#1 Benachrichtigun")
	// On the CI this step failed now and then (2026-10-26 to 10-28), each
	// time with page 1 shown, no notice and "] next page" offered; it never
	// failed locally, also not 40 runs under -race on two cores, and a
	// slow reload after the write did not make it fail. A failure prints
	// what reached the server beside the screen, to tell whether ] sent
	// its read at all.
	if err := c.send("]"); err != nil {
		t.Fatal(err)
	}
	if err := c.until(`after "]"`, readyLimit, func() bool { return c.screenShows(80, "Could not reach the server") }); err != nil {
		t.Fatalf("%v: waiting for the failed page 2\n%s\nrequests:\n%s", err,
			strings.Join(renderScreen(c.bytes(), 24, 80), "\n"), strings.Join(requestLines(f), "\n"))
	}
	c.act(t, "R", 80, "#27 Benachrichtigung", "page 2 of 2")
	if err := c.send("q"); err != nil {
		t.Fatal(err)
	}
	c.finish(t, 0)
	if got := markRequests(f); len(got) != 1 || !strings.HasSuffix(got[0], fmt.Sprint(http.StatusOK)) {
		t.Fatalf("writes %v", got)
	}
}
