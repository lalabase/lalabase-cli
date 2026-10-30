package ui

import (
	"strings"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

// line2 is the second line of the screen as written, its trailing spaces
// dropped.
func line2(h *harness) string {
	h.t.Helper()
	lines := strings.Split(h.m.View().Content, "\n")
	if len(lines) < 2 {
		h.t.Fatalf("no second line: %q", lines)
	}
	return strings.TrimRight(lines[1], " ")
}

// L2: the second line is a path, the places parted by " › ", the page and
// the search by " · ", in two and three columns alike; nothing else in it
// carries an attribute.
func TestTheSecondLineIsAPath(t *testing.T) {
	t.Parallel()

	for _, width := range []int{80, 120, 160} {
		h := newHarness(t)
		h.m.width = width
		h.start()
		h.drive(h.choose(orgA))
		h.m.focus = view.List
		if got, want := line2(h), "Nordlicht GmbH › Inbox · page 1 of 2"; got != want {
			t.Fatalf("width %d, inbox: %q, want %q", width, got, want)
		}
		h.drive(h.chooseTickets(orgA))
		h.m.focus = view.List
		if got, want := line2(h), "Nordlicht GmbH › Tickets"; got != want {
			t.Fatalf("width %d, projects: %q, want %q", width, got, want)
		}
		h.drive(h.openProject(website))
		if got, want := line2(h), "Nordlicht GmbH › Tickets › Website-Relaunch · page 1 of 2"; got != want {
			t.Fatalf("width %d, tickets: %q, want %q", width, got, want)
		}
		h.press("/")
		h.typeText("export")
		h.drive(h.press("enter"))
		if got, want := line2(h), `Nordlicht GmbH › Tickets › Website-Relaunch · search "export" · page 1 of 1`; got != want {
			t.Fatalf("width %d, search: %q, want %q", width, got, want)
		}
	}
}

// In one column the second line is the path of the panes, with the same
// separator.
func TestOneColumnShowsThePanesAsAPath(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.openTickets(website)
	h.m.width = 79
	for _, tc := range []struct {
		focus view.Pane
		want  string
	}{
		{view.Navigation, "Organisations"},
		{view.List, "Organisations › Tickets"},
		{view.Object, "Organisations › Tickets › Ticket"},
	} {
		h.m.focus = tc.focus
		if got := line2(h); got != tc.want {
			t.Fatalf("focus %d: %q, want %q", tc.focus, got, tc.want)
		}
	}
}
