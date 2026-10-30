//go:build linux || darwin

package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/inboxfixture"
)

// wheelAt is one turn of the wheel as an SGR mouse report: 64 up, 65 down,
// at the column and line given from 1.
func wheelAt(down bool, x, y int) string {
	button := 64
	if down {
		button = 65
	}
	return fmt.Sprintf("\x1b[<%d;%d;%dM", button, x, y)
}

// The wheel through a real terminal: the terminal reports it once the
// interface asked for it, the ticket's head scrolls out of the pane under
// the pointer and back, and the terminal's mouse modes are switched off on
// the way out.
func TestPTYTheWheelScrollsTheTicket(t *testing.T) {
	t.Parallel()

	const width = 120
	c, _ := startInbox(t, inboxfixture.Normal, width)
	openSix(t, c, width)
	// The ticket has the focus: the list and the ticket, the ticket from
	// column 40 on. Line 10 is in the body.
	down, up := strings.Repeat(wheelAt(true, 80, 10), 5), strings.Repeat(wheelAt(false, 80, 10), 5)
	if err := c.send(down); err != nil {
		t.Fatal(err)
	}
	if err := c.until("scroll down", caseLimit, func() bool {
		return !c.screenShows(width, "Description of six") && c.screenShows(width, "esc back")
	}); err != nil {
		t.Fatalf("%v\n%s", err, renderScreen(c.bytes(), 24, width))
	}
	c.act(t, up, width, "Description of six")
	if err := c.send("q"); err != nil {
		t.Fatal(err)
	}
	c.finish(t, 0)
	if err := terminalOutputError(c.bytes()); err != nil {
		t.Fatalf("%v\n%q", err, c.bytes())
	}
	for _, mode := range []int{1002, 1006} {
		if events := terminalModes(c.bytes())[mode]; len(events) < 2 || !events[0] || events[len(events)-1] {
			t.Fatalf("mode %d: %v", mode, events)
		}
	}
}
