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

// C9b through a real terminal, in three columns and in two: two agents
// of Nordlicht, #6 waiting for the person and #7 ended. The header sums
// them up; from Südwind's inbox A opens the agents (the area under the
// organisations, or the list's place), Enter on #6 opens it in Nordlicht's
// tickets; Enter on #7 records it as seen, on disk. Two columns run with
// NO_COLOR: the glyphs and counts stay, no colour is drawn.
func TestPTYAgentsOverview(t *testing.T) {
	for _, width := range []uint16{120, 100} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			_, url := fixtureServer(t, inboxfixture.Normal)
			home, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			dir := testAgentsDir(home, url)
			if err := agents.Prepare(dir); err != nil {
				t.Fatal(err)
			}
			const waiting, ended = "0f8fad5b-d9cb-469f-a165-70867728950e", "1f8fad5b-d9cb-469f-a165-70867728950e"
			for _, a := range []struct {
				id, event, input string
				n                int
			}{
				{waiting, agents.Stop, `{"background_tasks":[]}`, 6},
				{ended, agents.SessionEnd, `{"reason":"prompt_input_exit"}`, 7},
			} {
				if err := agents.WriteLaunch(dir, agents.Launch{
					Agent: a.id, Organisation: inboxfixture.OrgA, Project: inboxfixture.ProjectWebsite,
					Ticket: inboxfixture.TicketID(inboxfixture.ProjectWebsite, a.n), Number: a.n,
					Session: "7c9e6679-7425-40de-944b-e07fc1f90ae7", At: time.Now().Add(-5 * time.Minute),
				}); err != nil {
					t.Fatal(err)
				}
				if err := agents.Hook(agents.HookCall{
					Dir: dir, Event: a.event, Agent: a.id, Input: strings.NewReader(a.input),
					PID: strconv.Itoa(os.Getpid()), Now: time.Now().Add(-4 * time.Minute), Procs: agents.System,
				}); err != nil {
					t.Fatal(err)
				}
			}
			opts := terminalOptions{url: url, marker: "Token can write: yes", limit: inboxLimit, home: home}
			summary := "Agents ⣿ 1 · ✓ 1"
			if width < 120 {
				opts.env = []string{"NO_COLOR=1"}
				summary = " ⣿ 1 · ✓ 1"
			}
			c := startTerminalWith(t, "inbox-"+string(inboxfixture.Normal), width, opts)
			if err := c.ready(); err != nil {
				t.Fatalf("%v\n%s", err, c.bytes())
			}
			c.expect(t, "the summary and the area", width, summary, "╭─ Agents", "⣿ #6 Ticket 6", "✓ #7 Ticket 7", "Nordlicht GmbH")
			c.moveTo(t, keyDown, width, "Südwind AG", "Inbox", 6)
			c.act(t, keyEnter, width, "Südwind AG › Inbox")
			c.act(t, "A", width, "> ⣿ #6 Ticket 6", "enter open", "esc back")
			if width < 120 && !c.screenShows(width, "╭─ Agents") {
				t.Fatal("two columns: the agents are not in the list's place")
			}
			c.act(t, keyEnter, width, "Nordlicht GmbH › Tickets", "#6 Ticket 6", "Agent waiting for you")
			// Esc first, alone: an A right behind it would be read as Alt-A.
			c.act(t, keyEsc, width, "Projects", "enter open tickets")
			c.act(t, "A", width, "> ⣿ #6 Ticket 6")
			c.act(t, keyDown, width, "> ✓ #7 Ticket 7")
			c.act(t, keyEnter, width, "#7 Ticket 7", "Agent ended")
			if err := c.send("q"); err != nil {
				t.Fatal(err)
			}
			c.finish(t, 0)
			out := c.bytes()
			if err := terminalOutputError(out); err != nil {
				t.Fatalf("%v\n%q", err, out)
			}
			for _, id := range []string{waiting, ended} {
				_, err := os.Lstat(filepath.Join(dir, id+".seen"))
				if seen := err == nil; seen != (id == ended) {
					t.Fatalf("%s seen on disk: %v (%v)", id, seen, err)
				}
			}
			seen := sgrParams(out)
			if width < 120 {
				if seen[35] || seen[31] || seen[32] || seen[33] || seen[36] {
					t.Fatalf("a colour despite NO_COLOR: %v", seen)
				}
			}
		})
	}
}
