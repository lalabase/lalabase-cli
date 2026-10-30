//go:build linux || darwin

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gboyke/lalabase-cli/internal/agents"
	"github.com/gboyke/lalabase-cli/internal/inboxfixture"
)

// C9a through a real terminal: an agent at ticket #6, waiting for the
// person, recorded as a launch and a hook would (this test's process
// stands in for Claude Code, so its pid lives). The list shows its glyph
// before the number, the ticket's meta bar its line (the head below the
// bar's width). With NO_COLOR the glyph
// and the word stay and no colour is drawn; with colour the glyph is
// magenta.
func TestPTYAgentsInTheListAndTheHead(t *testing.T) {
	t.Parallel()

	for _, noColor := range []bool{false, true} {
		t.Run(fmt.Sprintf("NO_COLOR=%v", noColor), func(t *testing.T) {
			_, url := fixtureServer(t, inboxfixture.Normal)
			home, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			dir := testAgentsDir(home, url)
			if err := agents.Prepare(dir); err != nil {
				t.Fatal(err)
			}
			const agent = "0f8fad5b-d9cb-469f-a165-70867728950e"
			if err := agents.WriteLaunch(dir, agents.Launch{
				Agent: agent, Organisation: inboxfixture.OrgA, Project: inboxfixture.ProjectWebsite, Ticket: ticket6,
				Number: 6, Session: "7c9e6679-7425-40de-944b-e07fc1f90ae7", At: time.Now().Add(-5 * time.Minute),
			}); err != nil {
				t.Fatal(err)
			}
			if err := agents.Hook(agents.HookCall{
				Dir: dir, Event: agents.Stop, Agent: agent, Input: strings.NewReader(`{"background_tasks":[]}`),
				PID: strconv.Itoa(os.Getpid()), Now: time.Now().Add(-4*time.Minute - 10*time.Second), Procs: agents.System,
			}); err != nil {
				t.Fatal(err)
			}
			opts := terminalOptions{url: url, marker: "Token can write: yes", limit: inboxLimit, home: home}
			if noColor {
				opts.env = []string{"NO_COLOR=1"}
			}
			c := startTerminalWith(t, "inbox-"+string(inboxfixture.Normal), 120, opts)
			if err := c.ready(); err != nil {
				t.Fatalf("%v\n%s", err, c.bytes())
			}
			openWebsite(t, c, 120)
			c.expect(t, "the glyph before #6", 120, "⣿  #6 Ticket 6", "     #7 Ticket 7")
			for n := 2; n <= 5; n++ {
				title := fmt.Sprintf(">    #%d Ticket %d", n, n)
				if n == 2 {
					title = ">    #2 Export als CSV"
				}
				c.act(t, keyDown, 120, title)
			}
			c.act(t, keyDown, 120, "> ⣿  #6 Ticket 6")
			// The ticket in focus is wide enough for the meta bar (C9b):
			// the agent's line stands there, under its label.
			c.act(t, keyEnter, 120, "│ Agent", "│ ⣿ waiting for you", "│   · 4 min")
			if err := c.send("q"); err != nil {
				t.Fatal(err)
			}
			c.finish(t, 0)
			out := c.bytes()
			if err := terminalOutputError(out); err != nil {
				t.Fatalf("%v\n%q", err, out)
			}
			seen := sgrParams(out)
			if noColor {
				if seen[35] || seen[31] || seen[32] || seen[33] || seen[36] {
					t.Fatalf("a colour despite NO_COLOR: %v", seen)
				}
				return
			}
			if !strings.Contains(out, "\x1b[35m⣿") {
				t.Fatalf("the glyph is not magenta: %q", out)
			}
		})
	}
}
