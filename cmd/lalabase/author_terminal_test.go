//go:build linux || darwin

package main

import (
	"testing"

	"github.com/gboyke/lalabase-cli/internal/inboxfixture"
)

// L2: the token's own person among a ticket's comment authors keeps the
// mark "(you)" through a real terminal with NO_COLOR, where no colour is
// drawn at all: the mark, not the blue, carries the meaning.
func TestPTYOwnAuthorKeepsItsMarkWithoutColour(t *testing.T) {
	_, url := fixtureServer(t, inboxfixture.Normal)
	opts := terminalOptions{url: url, marker: "Token can write: yes", limit: inboxLimit, env: []string{"NO_COLOR=1"}}
	c := startTerminalWith(t, "inbox-"+string(inboxfixture.Normal), 120, opts)
	if err := c.ready(); err != nil {
		t.Fatalf("%v\n%s", err, c.bytes())
	}
	openWebsite(t, c, 120)
	c.expect(t, "the preview of #1 with its comments", 120, "Anna Albers (you)", "Ben Berger  ", "Tritt nur in Safari auf.")
	if c.screenShows(120, "Ben Berger (you)") {
		t.Fatal("another author is marked")
	}
	if err := c.send("q"); err != nil {
		t.Fatal(err)
	}
	c.finish(t, 0)
	out := c.bytes()
	if err := terminalOutputError(out); err != nil {
		t.Fatalf("%v\n%q", err, out)
	}
	seen := sgrParams(out)
	if !seen[1] {
		t.Fatal("no bold: the weights are gone")
	}
	for n := 30; n <= 37; n++ {
		if seen[n] {
			t.Fatalf("colour %d despite NO_COLOR", n)
		}
	}
}
