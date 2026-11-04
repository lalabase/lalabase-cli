//go:build linux || darwin

package main

import (
	"fmt"
	"regexp"
	"slices"
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

// K7b: a meeting in the object pane through a real terminal: Enter reads
// it, Tab walks its sections, Esc leads back to the list; a transcript
// that tries to reach the terminal reaches nothing; a meeting gone and
// one above the client's bound are an error line beside a list that
// stays, at 80 and 120 columns.
func TestPTYMeetingPaneFlow(t *testing.T) {
	t.Parallel()

	for _, width := range []uint16{80, 120} {
		t.Run(fmt.Sprintf("%d", width), func(t *testing.T) {
			c, f := startInbox(t, inboxfixture.Normal, width)
			c.expect(t, "the directory and the counters", width, "All organisations", "Nordlicht GmbH", "33")
			c.moveTo(t, keyDown, width, "Nordlicht GmbH", "Meetings", 3)
			c.act(t, keyEnter, width, "Projects", "Nordlicht GmbH › Meetings")
			c.act(t, keyDown, width, "> Handbuch")
			c.act(t, keyDown, width, "> Intern")
			c.act(t, keyDown, width, "> Website-Relaunch")
			c.act(t, keyEnter, width, "Meeting 1", "page 1 of 2")
			c.act(t, keyEnter, width, "» Summary   Notes   Transcript", "Ergebnis: Start im März.", "• Seiten-Vorlagen", "tab next section")
			c.act(t, "\t", width, "  Summary » Notes   Transcript", "Termin im März halten.")
			// K7c: the speakers as heads, their words indented below.
			c.act(t, "\t", width, "  Summary   Notes » Transcript", "│ Anna Albers", "│   Wir starten im März", "│ Erika Beispiel")
			if c.screenShows(width, "**Anna") {
				t.Fatal("a speaker's markers reached the screen")
			}
			c.act(t, keyEsc, width, "enter open")
			c.act(t, keyDown+keyDown+keyDown, width, "Title: Plan")
			c.act(t, keyEnter, width, "copied", "cleared")
			c.act(t, keyEsc, width, "enter open")
			c.act(t, keyDown, width, "Title: Meeting 5")
			c.act(t, keyEnter, width, "Not found or no longer accessible.")
			c.act(t, keyEsc, width, "enter open")
			c.act(t, keyDown+keyDown, width, "Title: Meeting 7")
			c.act(t, keyEnter, width, "Unexpected server response")
			c.act(t, keyEsc, width, "enter open", "page 1 of 2")
			if err := c.send("q"); err != nil {
				t.Fatal(err)
			}
			c.finish(t, 0)
			if err := terminalOutputError(c.bytes()); err != nil {
				t.Fatalf("%v\n%q", err, c.bytes())
			}
			// The transcript's own sequences, with the text they precede:
			// the program clears the screen itself.
			for _, bad := range []string{"\x1b]52", "\x1b[2Jcleared", "\u202egone"} {
				if strings.Contains(c.bytes(), bad) {
					t.Fatalf("%q from the transcript reached the terminal", bad)
				}
			}
			var shown []string
			for _, r := range f.Requests() {
				if r.Route == "meetings.show" {
					shown = append(shown, fmt.Sprintf("%v %d", r.Args["id"], r.Status))
				}
			}
			for _, n := range []int{1, 4, 5} {
				if !slices.Contains(shown, fmt.Sprintf("%s %d", inboxfixture.MeetingID(inboxfixture.ProjectWebsite, n), map[int]int{1: 200, 4: 200, 5: 404}[n])) {
					t.Fatalf("meeting %d was not read: %q", n, shown)
				}
			}
		})
	}
}

// F1: the find in the meeting's pane through a real terminal: / opens its
// line, Enter finds a speaker in the transcript, n moves on, Esc ends it.
// The current hit is reversed on the bytes and its place stands as text,
// with colour and without it.
func TestPTYMeetingFind(t *testing.T) {
	t.Parallel()

	const width = 120
	for _, noColor := range []bool{false, true} {
		t.Run(fmt.Sprintf("NO_COLOR=%v", noColor), func(t *testing.T) {
			_, url := fixtureServer(t, inboxfixture.Normal)
			opts := terminalOptions{url: url, marker: "Token can write: yes", limit: inboxLimit}
			if noColor {
				opts.env = []string{"NO_COLOR=1"}
			}
			c := startTerminalWith(t, "inbox-"+string(inboxfixture.Normal), width, opts)
			if err := c.ready(); err != nil {
				t.Fatalf("%v\n%s", err, c.bytes())
			}
			c.moveTo(t, keyDown, width, "Nordlicht GmbH", "Meetings", 3)
			c.act(t, keyEnter, width, "Projects", "Nordlicht GmbH › Meetings")
			c.act(t, keyDown, width, "> Handbuch")
			c.act(t, keyDown, width, "> Intern")
			c.act(t, keyDown, width, "> Website-Relaunch")
			c.act(t, keyEnter, width, "Meeting 1", "page 1 of 2")
			c.act(t, keyEnter, width, "» Summary   Notes   Transcript", "/ find")
			c.act(t, "\t", width, "  Summary » Notes   Transcript")
			c.act(t, "\t", width, "  Summary   Notes » Transcript", "│ Erika Beispiel")
			c.act(t, "/", width, "Find: _", "enter find")
			c.act(t, "erika", width, "Find: erika_")
			c.act(t, keyEnter, width, "1 of 35", "n/N next/prev hit", "esc end find")
			c.act(t, "n", width, "2 of 35")
			c.act(t, keyEsc, width, "esc list")
			if c.screenShows(width, " of 35") {
				t.Fatal("the place stayed after the find ended")
			}
			if err := c.send("q"); err != nil {
				t.Fatal(err)
			}
			c.finish(t, 0)
			out := c.bytes()
			if err := terminalOutputError(out); err != nil {
				t.Fatalf("%v\n%q", err, out)
			}
			// The current hit: an attribute run with reverse (7) right
			// before the name it found.
			if !regexp.MustCompile(`\x1b\[(?:\d+;)*7(?:;\d+)*mErika`).MatchString(out) {
				t.Fatalf("the current hit is not reversed:\n%q", out)
			}
			if seen := sgrParams(out); noColor && (seen[38] || seen[48]) {
				t.Fatalf("a colour despite NO_COLOR: %v", seen)
			}
		})
	}
}
