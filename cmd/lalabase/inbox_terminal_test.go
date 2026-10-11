//go:build linux || darwin

package main

import (
	"fmt"
	"net/http"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/gboyke/lalabase-cli/internal/contract"
	"github.com/gboyke/lalabase-cli/internal/inboxfixture"
)

// The production wiring through a real terminal: the child runs the
// program as a person would, against the loopback fixture started in this
// process. Nothing of the flow is injected into the interface.

// inboxLimit is the case limit of an inbox run: the start, two pages, a
// temporary failure with its retry, a recovery and the details.
const inboxLimit = 30 * time.Second

// keys as the terminal sends them
const (
	keyDown  = "\x1b[B"
	keyUp    = "\x1b[A"
	keyEnter = "\r"
	keyEsc   = "\x1b"
)

func fixtureServer(t *testing.T, scenario inboxfixture.Scenario) (*inboxfixture.Fixture, string) {
	t.Helper()
	f, err := inboxfixture.New(scenario, contract.Raw())
	if err != nil {
		t.Fatal(err)
	}
	ln, err := inboxfixture.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: f, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return f, "http://" + ln.Addr().String()
}

func startInbox(t *testing.T, scenario inboxfixture.Scenario, width uint16) (*terminalProcess, *inboxfixture.Fixture) {
	t.Helper()
	f, url := fixtureServer(t, scenario)
	c := startTerminalWith(t, "inbox-"+string(scenario), width, terminalOptions{url: url, marker: "Token can write: yes", limit: inboxLimit})
	if err := c.ready(); err != nil {
		t.Fatalf("%v\n%s", err, c.bytes())
	}
	return c, f
}

// expect waits until the screen, as a person sees it, shows every marker
// on one of its lines.
func (c *terminalProcess) expect(t *testing.T, label string, width uint16, markers ...string) {
	t.Helper()
	if err := c.until(label, readyLimit, func() bool {
		return screenHas(renderScreen(c.bytes(), 24, int(width)), markers...)
	}); err != nil {
		t.Fatalf("%v: waiting for %q\n%s", err, markers, strings.Join(renderScreen(c.bytes(), 24, int(width)), "\n"))
	}
}

// screenShows reports whether the screen shows every marker right now.
func (c *terminalProcess) screenShows(width uint16, markers ...string) bool {
	return screenHas(renderScreen(c.bytes(), 24, int(width)), markers...)
}

// act sends input and waits for the markers it should bring to the screen.
func (c *terminalProcess) act(t *testing.T, input string, width uint16, markers ...string) {
	t.Helper()
	if err := c.send(input); err != nil {
		t.Fatal(err)
	}
	c.expect(t, fmt.Sprintf("after %q", input), width, markers...)
}

func (c *terminalProcess) resize(t *testing.T, width uint16) {
	t.Helper()
	if err := pty.Setsize(c.master, &pty.Winsize{Rows: 24, Cols: width}); err != nil {
		t.Fatal(err)
	}
	if err := c.cmd.Process.Signal(syscall.SIGWINCH); err != nil {
		t.Fatal(err)
	}
}

// checkRequests fails on any read the plan excludes: another tool, the
// projects, a write; and returns the inbox reads in order.
func checkRequests(t *testing.T, f *inboxfixture.Fixture) []inboxfixture.Request {
	t.Helper()
	var inbox []inboxfixture.Request
	for _, r := range f.Requests() {
		switch {
		case r.Status == http.StatusUnauthorized && r.Path != "/api/mcp":
			t.Fatalf("a request without the token: %+v", r)
		case r.Path == "/api/v1/projects", r.Tool != "" && r.Tool != "list_notifications":
			t.Fatalf("an excluded request: %+v", r)
		case r.Tool == "list_notifications":
			inbox = append(inbox, r)
		}
	}
	return inbox
}

// The whole flow at 80 and 120 columns: the start, the choice of A, a page
// change that fails twice, R, the details, a resize, Esc, q, and the
// terminal as it was.
func TestPTYInboxFlow(t *testing.T) {
	for _, width := range []uint16{80, 120} {
		t.Run(fmt.Sprintf("%d", width), func(t *testing.T) {
			c, f := startInbox(t, inboxfixture.Temporary, width)
			c.expect(t, "the directory and the counters", width, "All organisations", "Nordlicht GmbH", "Südwind AG", "33", "30")
			c.moveTo(t, keyDown, width, "Nordlicht GmbH", "Inbox", 1)
			c.act(t, keyEnter, width, "#1 Benachrichtigung", "Nordlicht GmbH | Inbox | page 1 of 2")
			if width == 120 {
				// Highlighting another organisation relabels nothing: the
				// heading is the open inbox's, not the cursor's.
				c.act(t, keyEsc, width, "esc inbox")
				c.moveTo(t, keyDown, width, "Südwind AG", "Inbox", 2)
				if !c.screenShows(width, "Nordlicht GmbH | Inbox | page 1 of 2") {
					t.Fatalf("the heading followed the cursor:\n%s", strings.Join(renderScreen(c.bytes(), 24, int(width)), "\n"))
				}
				c.act(t, keyEsc, width, "esc organisations")
			}
			c.act(t, "]", width, "Could not reach the server") // clipped to the column at 80
			c.act(t, "R", width, "#26 Benachrichtigung", "page 2 of 2")
			c.act(t, keyEnter, width, "000000100026", "State: unread", "Last event: 5 Jan 09:25", "esc close")
			other := uint16(120)
			if width == 120 {
				other = 80
			}
			c.resize(t, other)
			c.expect(t, "the details after the resize", other, "000000100026", "State: unread", "page 2 of 2")
			c.resize(t, width)
			c.expect(t, "the details after the resize back", width, "000000100026", "State: unread")
			c.act(t, keyEsc, width, "esc organisations")
			if c.screenShows(width, "esc close") {
				t.Fatal("the details did not close")
			}
			if err := c.send("q"); err != nil {
				t.Fatal(err)
			}
			c.finish(t, 0)
			if err := terminalOutputError(c.bytes()); err != nil {
				t.Fatalf("%v\n%q", err, c.bytes())
			}
			inbox := checkRequests(t, f)
			var seen []string
			for _, r := range inbox {
				seen = append(seen, fmt.Sprintf("%v/%v/%d", r.Args["organisation_id"], r.Args["page"], r.Status))
			}
			want := []string{"<nil>/1/200", orgA + "/1/200", orgA + "/2/503", orgA + "/2/503", orgA + "/2/200"}
			if strings.Join(seen, " ") != strings.Join(want, " ") {
				t.Fatalf("inbox reads %v, want %v", seen, want)
			}
			startup := 0
			for _, r := range f.Requests() {
				if r.Path == "/api/v1/me" || r.Path == "/api/v1/organisations" || r.RPC == "initialize" || r.RPC == "tools/list" {
					startup++
				}
			}
			if startup != 4 {
				t.Fatalf("%d startup requests", startup)
			}
		})
	}
}

const orgA = inboxfixture.OrgA

// A 401 on a later read ends the session: everything cleared, the notice
// shown, keys do nothing but leave, and q still restores the terminal.
func TestPTYInboxExpiresOn401(t *testing.T) {
	c, _ := startInbox(t, inboxfixture.Unauthorized, 80)
	c.moveTo(t, keyDown, 80, "Nordlicht GmbH", "Inbox", 1)
	c.act(t, keyEnter, 80, "#1 Benachrichtigung", "Nordlicht GmbH | Inbox")
	c.act(t, "]", 80, "Session expired. Restart lalabase.")
	if c.screenShows(80, "Benachrichtigung") || c.screenShows(80, "Nordlicht") || c.screenShows(80, "Anna Albers") {
		t.Fatalf("the expiry left data on screen:\n%s", strings.Join(renderScreen(c.bytes(), 24, 80), "\n"))
	}
	for _, key := range []string{"R", keyEnter, keyDown, keyEsc, "["} {
		if err := c.send(key); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(300 * time.Millisecond)
	if c.screenShows(80, "Benachrichtigung") || c.screenShows(80, "Nordlicht") || !c.screenShows(80, "Session expired") {
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

// A 429 is shown with its deadline and the read goes on after the window.
func TestPTYInboxShowsTheRateLimitPause(t *testing.T) {
	c, f := startInbox(t, inboxfixture.RateLimited, 80)
	c.moveTo(t, keyDown, 80, "Nordlicht GmbH", "Inbox", 1)
	c.act(t, keyEnter, 80, "#1 Benachrichtigung", "Nordlicht GmbH | Inbox")
	c.act(t, "]", 80, "Rate limited until")
	c.expect(t, "the retry after the window", 80, "#26 Benachrichtigung")
	if err := c.send("q"); err != nil {
		t.Fatal(err)
	}
	c.finish(t, 0)
	if err := terminalOutputError(c.bytes()); err != nil {
		t.Fatal(err)
	}
	inbox := checkRequests(t, f)
	if len(inbox) != 4 || inbox[2].Status != http.StatusTooManyRequests || inbox[3].Status != http.StatusOK {
		t.Fatalf("inbox reads %+v", inbox)
	}
}

// Without the organisation filter only the entry for all organisations
// opens, page 1 only; no filtered or paged read leaves the client.
func TestPTYInboxWithoutTheOrganisationFilter(t *testing.T) {
	c, f := startInbox(t, inboxfixture.Older, 80)
	c.expect(t, "the limitation", 80, "first page only")
	c.moveTo(t, keyDown, 80, "Nordlicht GmbH", "Inbox", 1)
	before := len(f.Requests())
	if err := c.send(keyEnter); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if len(f.Requests()) != before {
		t.Fatal("an organisation opened without the filter")
	}
	c.moveTo(t, keyUp, 80, "All organisations", "Inbox", 1)
	c.act(t, keyEnter, 80, "#1 Benachrichtigung", "All organisations | Inbox")
	if err := c.send("]"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if err := c.send("q"); err != nil {
		t.Fatal(err)
	}
	c.finish(t, 0)
	for _, r := range checkRequests(t, f) {
		if _, ok := r.Args["organisation_id"]; ok {
			t.Fatalf("a filtered read: %+v", r)
		}
		if page, ok := r.Args["page"]; ok && page != float64(1) {
			t.Fatalf("a paged read: %+v", r)
		}
	}
}

// Ctrl-C in the inbox restores the terminal and exits with 130.
func TestPTYInboxInterrupt(t *testing.T) {
	c, _ := startInbox(t, inboxfixture.Normal, 80)
	c.moveTo(t, keyDown, 80, "Nordlicht GmbH", "Inbox", 1)
	c.act(t, keyEnter, 80, "#1 Benachrichtigung", "Nordlicht GmbH | Inbox")
	if err := c.send("\x03"); err != nil {
		t.Fatal(err)
	}
	c.finish(t, 130)
}
