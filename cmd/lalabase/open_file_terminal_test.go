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
