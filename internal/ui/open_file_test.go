package ui

import (
	"strings"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/launch"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

// C10: Tab reaches the files of the ticket, the arrows select one, Enter
// opens it. The file comes from the record the pane shows, and nothing is
// read for it.

// toFiles opens ticket #6 and goes to its section of files.
func (h *harness) toFiles() *fakeLauncher {
	h.t.Helper()
	f := h.withLauncher()
	h.openSix()
	h.press("tab")
	h.press("tab")
	if h.m.app.detail.area != areaAttachments {
		h.t.Fatalf("area = %d", h.m.app.detail.area)
	}
	return f
}

func selectedLine(t *testing.T, h *harness) string {
	t.Helper()
	for _, line := range strings.Split(h.paneText(), "\n") {
		if strings.Contains(line, "> six-") {
			return line
		}
	}
	return ""
}

func TestArrowsSelectAFileAndEnterOpensIt(t *testing.T) {
	h := newHarness(t)
	f := h.toFiles()
	if !strings.Contains(selectedLine(t, h), "six-01.txt") {
		t.Fatalf("selected %q", selectedLine(t, h))
	}
	if !h.offers(OpenList) || !strings.Contains(helpText(h.m), "open file") {
		t.Fatalf("enter is not offered: %q", helpText(h.m))
	}
	h.press("down")
	h.press("down")
	h.press("up")
	if !strings.Contains(selectedLine(t, h), "six-02.txt") {
		t.Fatalf("selected %q", selectedLine(t, h))
	}
	cmd := h.press("enter")
	want := launch.Ticket{Org: orgA, Project: website, ID: ticketID(website, 6), Number: 6}
	calls := f.opened()
	if len(calls) != 1 || calls[0].ticket != want || calls[0].file != (launch.File{ID: fileID(2), Name: "six-02.txt"}) {
		t.Fatalf("opened %+v", calls)
	}
	if got := h.status(); got != "Opening six-02.txt…" {
		t.Fatalf("status %q", got)
	}
	h.drive(cmd)
	if got := h.status(); got != "Sent six-02.txt to the browser" {
		t.Fatalf("status %q", got)
	}
	if h.m.focus != view.Object {
		t.Fatalf("focus %v", h.m.focus)
	}
}

func TestEnterOpensNoFileInTheOtherSections(t *testing.T) {
	h := newHarness(t)
	f := h.withLauncher()
	h.openSix()
	for range 2 {
		if h.offers(OpenList) {
			t.Fatalf("enter offered in area %d", h.m.app.detail.area)
		}
		h.drive(h.press("enter"))
		h.press("tab")
	}
	if calls := f.opened(); len(calls) != 0 {
		t.Fatalf("opened %+v", calls)
	}
}

// The arrows act on the comments in their section, as before, and on the
// files in theirs; neither moves the other.
func TestArrowsActOnTheirOwnSection(t *testing.T) {
	h := newHarness(t)
	h.withLauncher()
	h.openSix()
	first := h.m.app.detail.post
	h.press("down")
	if h.m.app.detail.post != first {
		t.Fatal("down moved a comment in the description")
	}
	h.press("tab")
	h.press("down")
	moved := h.m.app.detail.post
	if moved == first {
		t.Fatal("down moved no comment in the comments")
	}
	if h.m.app.detail.file != "" {
		t.Fatalf("down in the comments moved the file %q", h.m.app.detail.file)
	}
	h.press("tab")
	h.press("down")
	if h.m.app.detail.post != moved {
		t.Fatal("down in the files moved the comment")
	}
	if h.m.app.detail.file != fileID(2) {
		t.Fatalf("file %q", h.m.app.detail.file)
	}
}

func TestTheFileIsTheRecordsNotTheRowsUnderTheCursor(t *testing.T) {
	h := newHarness(t)
	f := h.toFiles()
	h.m.app.tickets.selected = ticketID(website, 1)
	h.drive(h.press("enter"))
	if calls := f.opened(); len(calls) != 1 || calls[0].ticket.ID != ticketID(website, 6) {
		t.Fatalf("opened %+v", calls)
	}
}

func TestASecondEnterWhileOneRunsDoesNothing(t *testing.T) {
	h := newHarness(t)
	f := h.toFiles()
	cmd := h.press("enter")
	if h.offers(OpenList) {
		t.Fatal("enter offered while a file opens")
	}
	h.press("enter")
	if n := len(f.opened()); n != 1 {
		t.Fatalf("%d opens", n)
	}
	h.drive(cmd)
	if h.m.app.launch.busy || !h.offers(OpenList) {
		t.Fatal("enter stays blocked")
	}
}

func TestAnOpenFileAskedForReadsNothing(t *testing.T) {
	h := newHarness(t)
	h.toFiles()
	before := len(h.f.detailReads())
	h.drive(h.press("enter"))
	if after := len(h.f.detailReads()); after != before {
		t.Fatalf("%d reads, then %d", before, after)
	}
}

func TestTheFileNoticeStaysWithItsTicket(t *testing.T) {
	h := newHarness(t)
	h.toFiles()
	cmd := h.press("enter")
	h.press("esc")
	h.m.app.tickets.selected = ticketID(website, 1)
	h.drive(h.press("enter"))
	h.drive(cmd)
	if got := h.status(); strings.Contains(got, "six-01") {
		t.Fatalf("status on another ticket %q", got)
	}
}

func TestTurningThePageSelectsItsFirstFile(t *testing.T) {
	h := newHarness(t)
	h.toFiles()
	h.press("down")
	h.drive(h.press("]"))
	if !strings.Contains(selectedLine(t, h), "six-11.txt") {
		t.Fatalf("selected %q", selectedLine(t, h))
	}
}

func TestAFileGoneAfterARefreshSelectsTheFirst(t *testing.T) {
	h := newHarness(t)
	f := h.toFiles()
	h.press("down")
	d := h.m.app.detail
	d.record.Attachments = d.record.Attachments[2:]
	h.drive(h.press("enter"))
	if calls := f.opened(); len(calls) != 1 || calls[0].file.ID != fileID(3) {
		t.Fatalf("opened %+v", calls)
	}
}

func TestARefreshKeepsTheSelectionByItsID(t *testing.T) {
	h := newHarness(t)
	h.toFiles()
	h.press("down")
	d := h.m.app.detail
	d.record.Attachments = d.record.Attachments[1:]
	if !strings.Contains(selectedLine(t, h), "six-02.txt") {
		t.Fatalf("selected %q", selectedLine(t, h))
	}
}

func TestFilesWithoutAnIDAreSkipped(t *testing.T) {
	h := newHarness(t)
	f := h.toFiles()
	d := h.m.app.detail
	d.record.Attachments[0].ID, d.record.Attachments[1].ID = nil, nil
	if !strings.Contains(selectedLine(t, h), "six-03.txt") {
		t.Fatalf("selected %q", selectedLine(t, h))
	}
	h.press("up")
	h.drive(h.press("enter"))
	if calls := f.opened(); len(calls) != 1 || calls[0].file.ID != fileID(3) {
		t.Fatalf("opened %+v", calls)
	}
}

func TestNoFileThatCanBeOpened(t *testing.T) {
	h := newHarness(t)
	f := h.toFiles()
	h.m.app.detail.record.Attachments = nil
	if h.offers(OpenList) {
		t.Fatal("enter offered on an empty list")
	}
	h.press("down")
	h.drive(h.press("enter"))
	if calls := f.opened(); len(calls) != 0 {
		t.Fatalf("opened %+v", calls)
	}
}

func TestEveryOutcomeOfAFileHasItsNotice(t *testing.T) {
	for kind, want := range map[launch.Kind]struct {
		text string
		tone present.Style
		goes bool
	}{
		launch.FileOpened:    {"Sent six-01.txt to the browser", present.Plain, true},
		launch.NoOpener:      {"No browser can be opened here", present.Plain, true},
		launch.OpenFailed:    {"The browser could not be opened", present.NoticeProblem, false},
		launch.NotHandedOver: {"The file's address did not pass its check", present.NoticeProblem, false},
		launch.NoAnswer:      {"The browser did not answer in time; it may still open", present.NoticeProblem, false},
		launch.Canceled:      {"", present.Plain, false},
	} {
		h := newHarness(t)
		f := h.toFiles()
		f.openKind = kind
		h.drive(h.press("enter"))
		l := h.m.app.launch
		if got := h.status(); got != want.text || l.tone != want.tone || l.until.IsZero() == want.goes {
			t.Fatalf("kind %d: %q, tone %v, until %v", kind, got, l.tone, l.until)
		}
	}
}

func TestAnEmptyIDIsNoFile(t *testing.T) {
	h := newHarness(t)
	f := h.toFiles()
	empty := ""
	h.m.app.detail.record.Attachments[0].ID = &empty
	h.drive(h.press("enter"))
	if calls := f.opened(); len(calls) != 1 || calls[0].file.ID != fileID(2) {
		t.Fatalf("opened %+v", calls)
	}
}

func TestWithoutTheOrganisationNoFileIsOffered(t *testing.T) {
	h := newHarness(t)
	f := h.toFiles()
	h.m.app.tickets.project = "another"
	if h.m.app.detailOrg() != "" {
		t.Fatalf("organisation %q", h.m.app.detailOrg())
	}
	if h.offers(OpenList) {
		t.Fatal("enter offered without the organisation")
	}
	h.drive(h.press("enter"))
	if calls := f.opened(); len(calls) != 0 {
		t.Fatalf("opened %+v", calls)
	}
}

func TestAForeignFileNameIsCleanedInTheNotice(t *testing.T) {
	h := newHarness(t)
	f := h.toFiles()
	f.openKind = launch.FileOpened
	h.m.app.detail.record.Attachments[0].Filename = "a\x1b[2Jb.png"
	cmd := h.press("enter")
	if got := h.status(); strings.ContainsRune(got, 0x1b) {
		t.Fatalf("status %q", got)
	}
	h.drive(cmd)
	if got := h.status(); strings.ContainsRune(got, 0x1b) {
		t.Fatalf("status %q", got)
	}
}

func TestTheSelectedFileStaysInSight(t *testing.T) {
	h := newHarness(t)
	h.toFiles()
	top := h.m.objectColumn().Offset
	for range 9 {
		h.press("down")
	}
	if off := h.m.objectColumn().Offset; off <= top {
		t.Fatalf("offset %d, was %d", off, top)
	}
}
