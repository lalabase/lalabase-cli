//go:build linux || darwin

package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/inboxfixture"
)

// K7a: a project's meetings through a real terminal, against the loopback
// fixture: the chooser's entry, the projects, the rows with date, title
// and duration, the pages, the search, Esc back, and a title that tries to
// reach the terminal reaching nothing, at 80 and 120 columns.
func TestPTYMeetingsFlow(t *testing.T) {
	t.Parallel()

	for _, width := range []uint16{80, 120} {
		t.Run(fmt.Sprintf("%d", width), func(t *testing.T) {
			c, f := startInbox(t, inboxfixture.Normal, width)
			c.expect(t, "the directory and the counters", width, "All organisations", "Nordlicht GmbH", "Südwind AG", "33", "30")
			c.moveTo(t, keyDown, width, "Nordlicht GmbH", "Meetings", 3)
			c.act(t, keyEnter, width, "Projects", "Nordlicht GmbH › Meetings")
			c.act(t, keyDown, width, "> Handbuch")
			c.act(t, keyDown, width, "> Intern")
			c.act(t, keyDown, width, "> Website-Relaunch")
			c.act(t, keyEnter, width, "Meeting 1", "0:31", "Nordlicht GmbH › Meetings › Website-Relaunch · page 1 of 2")
			c.expect(t, "the cleaned title", width, "Plan")
			c.act(t, "]", width, "page 2 of 2", "Meeting 26")
			c.act(t, "[", width, "page 1 of 2", "Meeting 1")
			c.act(t, "/", width, "Search: _")
			c.act(t, "Meeting 2", width, "Search: Meeting 2_")
			c.act(t, keyEnter, width, `search "Meeting 2"`, "page 1 of 1")
			c.act(t, keyEsc, width, "Projects", "esc organisations")
			c.act(t, keyEsc, width, "up/down highlight")
			if err := c.send("q"); err != nil {
				t.Fatal(err)
			}
			c.finish(t, 0)
			if err := terminalOutputError(c.bytes()); err != nil {
				t.Fatalf("%v\n%q", err, c.bytes())
			}
			if strings.Contains(c.bytes(), "owned") {
				t.Fatal("the title's sequence reached the terminal")
			}
			var reads []string
			for _, r := range f.Requests() {
				if r.Route == "meetings.list" {
					reads = append(reads, fmt.Sprintf("%v/%v/%v %d", r.Args["page"], r.Args["per_page"], r.Args["q"], r.Status))
				}
			}
			want := []string{"1/25/<nil> 200", "2/25/<nil> 200", "1/25/<nil> 200", "1/25/Meeting 2 200"}
			if strings.Join(reads, "\n") != strings.Join(want, "\n") {
				t.Fatalf("meeting reads:\n%s\nwant\n%s", strings.Join(reads, "\n"), strings.Join(want, "\n"))
			}
		})
	}
}
