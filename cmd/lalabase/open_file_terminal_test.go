//go:build linux || darwin

package main

import (
	"testing"

	"github.com/gboyke/lalabase-cli/internal/inboxfixture"
)

// C10: through a real terminal, Tab and the arrows reach a file of the
// ticket and Enter asks to open it. SSH_CONNECTION keeps the client from
// starting a browser: the run shows the way to the launcher, and no test
// starts one.
func TestPTYOpenFileOverSSHStartsNothing(t *testing.T) {
	_, url := fixtureServer(t, inboxfixture.Normal)
	c := startTerminalWith(t, "inbox-normal", 120, terminalOptions{
		url: url, marker: "Token can write: yes", limit: inboxLimit,
		env: []string{"SSH_CONNECTION=192.168.0.1 22 192.168.0.2 22"},
	})
	if err := c.ready(); err != nil {
		t.Fatalf("%v\n%s", err, c.bytes())
	}
	openWebsite(t, c, 120)
	c.act(t, keyEnter, 120, "esc back")
	c.act(t, keyTab, 120, "Comments")
	c.act(t, keyTab, 120, "> design.pdf", "open file")
	c.act(t, keyDown, 120, "> screenshot.png")
	c.act(t, keyEnter, 120, "No browser can be opened here")
	// The interface goes on.
	c.act(t, keyEsc, 120, "esc projects")
	if err := c.send("q"); err != nil {
		t.Fatal(err)
	}
	c.finish(t, 0)
}

// C12: the same way to a comment's file: the arrows go from a comment to
// the file under it, Enter asks to open it, and on the comment it does
// nothing.
func TestPTYOpenACommentsFileOverSSHStartsNothing(t *testing.T) {
	t.Parallel()

	_, url := fixtureServer(t, inboxfixture.Normal)
	c := startTerminalWith(t, "inbox-normal", 120, terminalOptions{
		url: url, marker: "Token can write: yes", limit: inboxLimit,
		env: []string{"SSH_CONNECTION=192.168.0.1 22 192.168.0.2 22"},
	})
	if err := c.ready(); err != nil {
		t.Fatalf("%v\n%s", err, c.bytes())
	}
	openWebsite(t, c, 120)
	c.act(t, keyEnter, 120, "esc back")
	c.act(t, keyTab, 120, "> Anna Albers", "comment or file")
	c.act(t, keyDown, 120, "> Ben Berger")
	c.act(t, keyDown, 120, "> test_file.txt", "open file")
	c.act(t, keyEnter, 120, "No browser can be opened here")
	c.act(t, keyUp, 120, "> Ben Berger")
	if err := c.until("enter off the help", readyLimit, func() bool { return !c.screenShows(120, "open file") }); err != nil {
		t.Fatalf("enter offered on a comment: %v\n%s", err, c.bytes())
	}
	// The interface goes on.
	c.act(t, keyEsc, 120, "esc projects")
	if err := c.send("q"); err != nil {
		t.Fatal(err)
	}
	c.finish(t, 0)
}

// C12: a server before R7 sends a count alone: the comment says so, and
// there is nothing to select.
func TestPTYACountWithoutAListIsSaid(t *testing.T) {
	t.Parallel()

	_, url := fixtureServer(t, inboxfixture.Normal, inboxfixture.BeforeR1())
	c := startTerminalWith(t, "inbox-normal", 120, terminalOptions{url: url, marker: "Token can write: yes", limit: inboxLimit})
	if err := c.ready(); err != nil {
		t.Fatalf("%v\n%s", err, c.bytes())
	}
	openWebsite(t, c, 120)
	c.act(t, keyEnter, 120, "esc back")
	c.act(t, keyTab, 120, "> Anna Albers")
	c.act(t, keyDown, 120, "> Ben Berger", "1 file, not listed by this server")
	if c.screenShows(120, "open file") {
		t.Fatalf("enter offered without a list:\n%s", c.bytes())
	}
	if err := c.send("q"); err != nil {
		t.Fatal(err)
	}
	c.finish(t, 0)
}
