package ui

import (
	"slices"
	"strings"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/launch"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

// w opens the ticket or the meeting the pane shows in the browser, its
// page in the core. It shares the one launch at a time and the status
// line with x and the files, and stands under Actions in the bar on the
// right and in the help, where it acts.

// actionsOf are the bar's key lines under Actions: key and help.
func (h *harness) actionsOf() []string {
	var out []string
	for _, l := range h.m.objectColumn().Meta {
		if l.Lead.String() != "" && l.LeadStyle == present.Strong {
			out = append(out, l.Lead.String()+" "+l.Text.String())
		}
	}
	return out
}

func TestWOpensTheTicketShownInTheBrowser(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	f := h.withLauncher()
	h.openSix()
	if !h.offers(OpenPage) || !strings.Contains(helpText(h.m), "open in browser") {
		t.Fatalf("w is not offered: %q", helpText(h.m))
	}
	if !slices.Contains(h.actionsOf(), "w open in browser") {
		t.Fatalf("actions %q", h.actionsOf())
	}
	// The pane's ticket, not the row under the list's cursor.
	h.m.app.tickets.selected = ticketID(website, 1)
	cmd := h.press("w")
	want := launch.Page{Kind: launch.TicketPage, Org: orgA, Project: website, ID: ticketID(website, 6), Number: 6}
	if calls := f.pages(); len(calls) != 1 || calls[0] != want {
		t.Fatalf("pages %+v", calls)
	}
	if got := h.status(); got != "Opening ticket #6 in the browser\u2026" {
		t.Fatalf("status %q", got)
	}
	if h.offers(OpenPage) {
		t.Fatal("w offered while it runs")
	}
	h.press("w")
	if n := len(f.pages()); n != 1 {
		t.Fatalf("%d opens", n)
	}
	h.drive(cmd)
	if got := h.status(); got != "Sent ticket #6 to the browser" {
		t.Fatalf("status %q", got)
	}
	if !h.offers(OpenPage) || h.m.focus != view.Object {
		t.Fatalf("after: offered %v, focus %v", h.offers(OpenPage), h.m.focus)
	}
}

func TestWOpensTheMeetingShownInTheBrowser(t *testing.T) {
	t.Parallel()

	h := newRESTHarness(t)
	f := h.withLauncher()
	h.openMeetings(website)
	h.drive(h.press("enter"))
	if !h.offers(OpenPage) || !strings.Contains(helpText(h.m), "open in browser") {
		t.Fatalf("w is not offered: %q", helpText(h.m))
	}
	if got := h.actionsOf(); !slices.Equal(got, []string{"w open in browser"}) {
		t.Fatalf("actions %q", got)
	}
	if !strings.Contains(h.metaText(), "Actions") {
		t.Fatalf("bar %q", h.metaText())
	}
	cmd := h.press("w")
	want := launch.Page{Kind: launch.MeetingPage, Org: orgA, Project: website, ID: meetingID(website, 1)}
	if calls := f.pages(); len(calls) != 1 || calls[0] != want {
		t.Fatalf("pages %+v", calls)
	}
	if got := h.status(); got != "Opening the meeting in the browser\u2026" {
		t.Fatalf("status %q", got)
	}
	h.drive(cmd)
	if got := h.status(); got != "Sent the meeting to the browser" {
		t.Fatalf("status %q", got)
	}
}

// Beside the list the keys of the pane do not act: no w, no Actions.
func TestWDoesNothingBesideTheList(t *testing.T) {
	t.Parallel()

	h := newRESTHarness(t)
	f := h.withLauncher()
	h.openMeetings(website)
	h.drive(h.press("enter"))
	h.press("esc")
	if h.offers(OpenPage) || len(h.actionsOf()) != 0 {
		t.Fatalf("offered %v, actions %q", h.offers(OpenPage), h.actionsOf())
	}
	h.drive(h.press("w"))
	if calls := f.pages(); len(calls) != 0 {
		t.Fatalf("pages %+v", calls)
	}
}

// Without a launcher (the tests' default, a session that cannot start one)
// w is neither offered nor listed.
func TestWNeedsALauncher(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.openSix()
	if h.offers(OpenPage) || slices.Contains(h.actionsOf(), "w open in browser") {
		t.Fatalf("offered %v, actions %q", h.offers(OpenPage), h.actionsOf())
	}
}

// A problem of the opener stands in the meeting's status line too.
func TestTheBrowsersAnswerStandsBesideTheMeeting(t *testing.T) {
	t.Parallel()

	h := newRESTHarness(t)
	f := h.withLauncher()
	f.openKind = launch.NoOpener
	h.openMeetings(website)
	h.drive(h.press("enter"))
	h.drive(h.press("w"))
	if got := h.status(); got != "No browser can be opened here" {
		t.Fatalf("status %q", got)
	}
}

// A page whose address fails its check says so in its own words, not a
// file's.
func TestAPageAddressThatFailsItsCheckSaysSo(t *testing.T) {
	t.Parallel()

	h := newRESTHarness(t)
	f := h.withLauncher()
	f.openKind = launch.NotHandedOver
	h.openMeetings(website)
	h.drive(h.press("enter"))
	h.drive(h.press("w"))
	if got := h.status(); got != "The page's address did not pass its check" {
		t.Fatalf("status %q", got)
	}
}

// A second w while the meeting's page opens does nothing, and the first
// one's end frees it again.
func TestASecondWOnTheMeetingWhileOneRunsDoesNothing(t *testing.T) {
	t.Parallel()

	h := newRESTHarness(t)
	f := h.withLauncher()
	h.openMeetings(website)
	h.drive(h.press("enter"))
	cmd := h.press("w")
	if h.offers(OpenPage) || slices.Contains(h.actionsOf(), "w open in browser") {
		t.Fatalf("while it runs: offered %v, actions %q", h.offers(OpenPage), h.actionsOf())
	}
	h.drive(h.press("w"))
	if n := len(f.pages()); n != 1 {
		t.Fatalf("%d opens", n)
	}
	h.drive(cmd)
	if !h.offers(OpenPage) {
		t.Fatal("w not offered after the first one ended")
	}
}

// Without its organisation a page has no address: no w on a ticket or a
// meeting whose organisation is not known.
func TestWNeedsTheOrganisation(t *testing.T) {
	t.Parallel()

	ticket := newHarness(t)
	ticket.withLauncher()
	ticket.openSix()
	ticket.m.app.tickets.org = ""
	if _, ok := ticket.m.launchTarget(); !ok {
		t.Fatal("x needs no organisation, so w alone is tested here")
	}
	if ticket.offers(OpenPage) {
		t.Fatal("w on a ticket without its organisation")
	}

	meeting := newRESTHarness(t)
	meeting.withLauncher()
	meeting.openMeetings(website)
	meeting.drive(meeting.press("enter"))
	meeting.m.app.meetings.org = ""
	if meeting.offers(OpenPage) {
		t.Fatal("w on a meeting without its organisation")
	}
}

// w opens the meeting the pane's record is: a record of another meeting
// (a late answer the pane holds for a moment) opens nothing.
func TestWNeedsTheMeetingsOwnRecord(t *testing.T) {
	t.Parallel()

	h := newRESTHarness(t)
	h.withLauncher()
	h.openMeetings(website)
	h.drive(h.press("enter"))
	h.m.app.meeting.record.ID = meetingID(website, 2)
	if h.offers(OpenPage) {
		t.Fatal("w on a record of another meeting")
	}
}

// The page's notice belongs to its meeting, as a launch's belongs to its
// ticket (TestTheNoticeStaysWithItsTicket): with another meeting in the
// pane, neither the waiting notice nor the answer stands there.
func TestThePageNoticeStaysWithItsMeeting(t *testing.T) {
	t.Parallel()

	h := newRESTHarness(t)
	h.withLauncher()
	h.openMeetings(website)
	h.drive(h.press("enter"))
	cmd := h.press("w")
	h.press("esc")
	h.m.app.meetings.selected = meetingID(website, 2)
	h.drive(h.press("enter"))
	if d := h.m.app.meeting; d == nil || d.id != meetingID(website, 2) || !d.known {
		t.Fatal("meeting 2 is not open")
	}
	if got := h.status(); got != "" {
		t.Fatalf("meeting 2 shows %q while the page opens", got)
	}
	h.drive(cmd)
	if got := h.status(); got != "" {
		t.Fatalf("meeting 2 shows %q", got)
	}
}

// Every outcome of w says what came of it, in its tone: a hint goes after
// its time, a problem stays, and w is free again. Beside a file's
// (TestEveryOutcomeOfAFileHasItsNotice): what a page borrows of a file's
// texts stays a page's.
func TestEveryOutcomeOfAPageHasItsNotice(t *testing.T) {
	t.Parallel()

	for kind, want := range map[launch.Kind]struct {
		text string
		tone present.Style
		goes bool
	}{
		launch.FileOpened:    {"Sent the meeting to the browser", present.Plain, true},
		launch.NoOpener:      {"No browser can be opened here", present.Plain, true},
		launch.OpenFailed:    {"The browser could not be opened", present.NoticeProblem, false},
		launch.NotHandedOver: {"The page's address did not pass its check", present.NoticeProblem, false},
		launch.NoAnswer:      {"The browser did not answer in time; it may still open", present.NoticeProblem, false},
		launch.Canceled:      {"", present.Plain, false},
	} {
		h := newRESTHarness(t)
		f := h.withLauncher()
		f.openKind = kind
		h.openMeetings(website)
		h.drive(h.press("enter"))
		h.drive(h.press("w"))
		l := h.m.app.launch
		if got := h.status(); got != want.text || l.tone != want.tone || l.until.IsZero() == want.goes {
			t.Fatalf("kind %d: %q, tone %v, until %v", kind, got, l.tone, l.until)
		}
		if !h.offers(OpenPage) {
			t.Fatalf("kind %d: w is not offered again", kind)
		}
	}
}
