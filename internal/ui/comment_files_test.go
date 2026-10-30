package ui

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/launch"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

// C12: a comment's files stand under it. In the comments the arrows walk one
// list, comment, its files, the next comment; Enter opens the file the mark
// stands on, nothing is read for it, and e and b keep acting on the comment
// and the ticket. The comments are those of ticket #7 (sevenPosts).

// openSeven opens ticket #7 of Website.
func (h *harness) openSeven() {
	h.t.Helper()
	h.openTickets(website)
	h.m.app.tickets.selected = ticketID(website, 7)
	h.drive(h.press("enter"))
	if d := h.m.app.detail; d == nil || !d.known || h.m.focus != view.Object {
		h.t.Fatalf("ticket #7 is not open: %+v", d)
	}
}

// toComments opens ticket #7 and goes to its comments.
func (h *harness) toComments() *fakeLauncher {
	h.t.Helper()
	f := h.withLauncher()
	h.openSeven()
	h.press("tab")
	if h.m.app.detail.area != areaComments {
		h.t.Fatalf("area = %d", h.m.app.detail.area)
	}
	return f
}

// onSevenA walks from the first comment to the first file of comment 29.
func (h *harness) onSevenA() *fakeLauncher {
	h.t.Helper()
	f := h.toComments()
	h.press("down")
	h.press("down")
	h.at(29, "seven-a.png")
	return f
}

// editSeven serves ticket #7 with its comments changed by edit.
func (h *harness) editSeven(edit func(posts []api.Post) []api.Post) {
	h.f.setTicket(func(project, id string, o api.TicketOptions) transport.Answer {
		d := h.d
		d.sevenEdit = edit
		return d.defaultTicket(project, id, o)
	})
}

// postAt is comment i of ticket #7 among posts, oldest first.
func postAt(posts []api.Post, i int) *api.Post {
	return &posts[slices.IndexFunc(posts, func(p api.Post) bool { return p.ID == sevenPostID(i) })]
}

// markedLines are the lines of the pane with the mark, and the text under
// each.
func (h *harness) markedLines() (marked, under []string) {
	lines := strings.Split(h.paneText(), "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "> ") {
			marked = append(marked, line)
			if i+1 < len(lines) {
				under = append(under, lines[i+1])
			}
		}
	}
	return marked, under
}

// at fails unless the selection stands on comment i of #7 and on its file
// of that name ("" for the comment itself), with the mark on that line and
// no other, and Enter opens that file and no other: the mark and the target
// are one question, asked at every step.
func (h *harness) at(i int, file string) {
	h.t.Helper()
	d := h.m.app.detail
	if d.post != sevenPostID(i) {
		h.t.Fatalf("comment %q, want %d", d.post, i)
	}
	marked, under := h.markedLines()
	if len(marked) != 1 {
		h.t.Fatalf("%d marks: %q", len(marked), marked)
	}
	_, target, ok := h.m.fileTarget()
	if file == "" {
		if d.postFile != "" || ok || h.offers(OpenList) {
			h.t.Fatalf("on comment %d: file %q, target %+v %v", i, d.postFile, target, ok)
		}
		if len(under) != 1 || !strings.Contains(under[0], fmt.Sprintf("Seven comment %d", i)) {
			h.t.Fatalf("the mark is on %q above %q", marked, under)
		}
		return
	}
	if !strings.HasPrefix(marked[0], "> "+file) {
		h.t.Fatalf("the mark is on %q, want %q", marked[0], file)
	}
	if !ok || target.Kind != launch.CommentFile || target.Name != file || target.ID != d.postFile || !h.offers(OpenList) {
		h.t.Fatalf("target %+v %v under the file %q", target, ok, file)
	}
}

func TestAFileStandsUnderItsTextWithFaintFacts(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.toComments()
	tl := ticketText(h.m.app.detail, ticketContext{paged: true, when: h.m.app.when})
	at := slices.Index(tl.lines, "seven-private.pdf · application/pdf · 3.0 KiB")
	if at < 0 {
		t.Fatalf("no line of the file in:\n%s", strings.Join(tl.lines, "\n"))
	}
	if tl.lines[at-1] != "Seven comment 28" || tl.lines[at+1] != "" {
		t.Fatalf("around the file: %q, %q", tl.lines[at-1], tl.lines[at+1])
	}
	want := present.LineStyle{Indent: 2, Lead: len("seven-private.pdf"), LeadStyle: present.Plain, RestStyle: present.Faint}
	if tl.styles[at] != want {
		t.Fatalf("style %+v", tl.styles[at])
	}
	// One line per file, in the server's order, then the blank line.
	a := slices.Index(tl.lines, "seven-a.png · image/png · 2.0 KiB")
	if a < 0 || tl.lines[a+1] != "seven-b.log · text/plain · 512 B" || tl.lines[a+2] != "" {
		t.Fatalf("the files of 29 at %d:\n%s", a, strings.Join(tl.lines, "\n"))
	}
}

func TestTheArrowsWalkACommentItsFilesTheNextComment(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.toComments()
	walk := []struct {
		comment int
		file    string
	}{
		{30, ""}, {29, ""}, {29, "seven-a.png"}, {29, "seven-b.log"},
		{28, ""}, {28, "seven-private.pdf"},
		{27, ""}, {26, ""}, {25, ""}, {24, ""},
	}
	h.at(30, "")
	for _, stop := range walk[1:] {
		h.press("down")
		h.at(stop.comment, stop.file)
	}
	for i := len(walk) - 2; i >= 0; i-- {
		h.press("up")
		h.at(walk[i].comment, walk[i].file)
	}
}

func TestTheWalkStopsAtTheEndsOfThePage(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.toComments()
	h.press("up")
	h.at(30, "")
	for range 40 {
		h.press("down")
	}
	h.at(6, "seven-last.png")
	h.press("down")
	h.at(6, "seven-last.png")
	h.press("up")
	h.at(6, "")
}

func TestOneCommentWithAFileOffersTheArrows(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.editSeven(func(posts []api.Post) []api.Post { return []api.Post{*postAt(posts, 6)} })
	h.toComments()
	if !h.offers(Up) || !h.offers(Down) || !strings.Contains(helpText(h.m), "comment or file") {
		t.Fatalf("the arrows are not offered: %q", helpText(h.m))
	}
	h.press("down")
	h.at(6, "seven-last.png")
}

func TestCommentsWithoutFilesWalkAsBefore(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.withLauncher()
	h.openSix()
	h.press("tab")
	first := h.m.app.detail.post
	if help := helpText(h.m); !strings.Contains(help, "comment") || strings.Contains(help, "comment or file") {
		t.Fatalf("help %q", help)
	}
	h.press("down")
	d := h.m.app.detail
	if d.post == first || d.post != sixPostID(29) || d.postFile != "" || d.follow != anchorPost {
		t.Fatalf("post %q file %q follow %v", d.post, d.postFile, d.follow)
	}
	if strings.Contains(h.paneText(), "not listed") {
		t.Fatal("a hint without files")
	}
}

func TestEnterOpensTheFileUnderTheMark(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	f := h.onSevenA()
	h.press("down")
	if !strings.Contains(helpText(h.m), "open file") {
		t.Fatalf("enter is not offered: %q", helpText(h.m))
	}
	cmd := h.press("enter")
	want := launch.Ticket{Org: orgA, Project: website, ID: ticketID(website, 7), Number: 7}
	file := launch.File{Kind: launch.CommentFile, ID: postFileID(2), Name: "seven-b.log"}
	if calls := f.opened(); len(calls) != 1 || calls[0].ticket != want || calls[0].file != file {
		t.Fatalf("opened %+v", calls)
	}
	if got := h.status(); got != "Opening seven-b.log…" {
		t.Fatalf("status %q", got)
	}
	h.drive(cmd)
	if got := h.status(); got != "Sent seven-b.log to the browser" {
		t.Fatalf("status %q", got)
	}
	if d := h.m.app.detail; d.postFile != postFileID(2) || h.m.focus != view.Object {
		t.Fatalf("the selection moved: %q, focus %v", d.postFile, h.m.focus)
	}
}

func TestEnterOnACommentDoesNothing(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	f := h.toComments()
	h.press("down")
	h.at(29, "")
	if h.offers(OpenList) {
		t.Fatal("enter offered on a comment")
	}
	h.drive(h.press("enter"))
	if calls := f.opened(); len(calls) != 0 {
		t.Fatalf("opened %+v", calls)
	}
}

func TestTheWalkAndAnOpenFileReadNothing(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.toComments()
	before := len(h.f.detailReads())
	h.press("down")
	h.press("down")
	h.drive(h.press("enter"))
	h.press("up")
	if after := len(h.f.detailReads()); after != before {
		t.Fatalf("%d reads, then %d", before, after)
	}
}

func TestTheFileIsTheRecordsNotTheListsCursor(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	f := h.onSevenA()
	h.m.app.tickets.selected = ticketID(website, 1)
	h.drive(h.press("enter"))
	if calls := f.opened(); len(calls) != 1 || calls[0].ticket.ID != ticketID(website, 7) || calls[0].file.ID != postFileID(1) {
		t.Fatalf("opened %+v", calls)
	}
}

func TestEOnAFileExpandsItsCommentAndShowsIt(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.onSevenA()
	if !strings.Contains(helpText(h.m), "expand") {
		t.Fatalf("e is not offered: %q", helpText(h.m))
	}
	h.drive(h.press("e"))
	d := h.m.app.detail
	if e := d.expansion; e == nil || e.post != sevenPostID(29) || !e.known {
		t.Fatalf("expansion %+v", e)
	}
	if read := h.lastRead(); read.PostID != sevenPostID(29) {
		t.Fatalf("read %+v", read)
	}
	mustContain(t, h.paneText(), "Seven comment 29 begins and ends here.", "seven-a.png")
	marked, _ := h.markedLines()
	if d.postFile != postFileID(1) || len(marked) != 1 || !strings.HasPrefix(marked[0], "> seven-a.png") {
		t.Fatalf("file %q, marks %q", d.postFile, marked)
	}
	// The text opened above the file: the pane shows the comment, not the
	// file alone with its text out of sight.
	if d.follow != anchorPost {
		t.Fatalf("follow %v", d.follow)
	}
}

func TestBAndXOnAFileActOnTheTicket(t *testing.T) {
	t.Parallel()

	h := newRESTHarness(t)
	h.setToken(boolPtr(false))
	f := h.onSevenA()
	if !h.offers(BookTime) || !h.offers(Launch) {
		t.Fatalf("b or x is not offered: %q", helpText(h.m))
	}
	h.press("b")
	if h.m.dialog == nil || h.m.dialog.spec.Target.Ticket != ticketID(website, 7) {
		t.Fatalf("dialog %+v", h.m.dialog)
	}
	h.press("esc")
	h.at(29, "seven-a.png")
	h.drive(h.press("x"))
	if calls := f.launched(); len(calls) != 1 || calls[0].ticket.ID != ticketID(website, 7) {
		t.Fatalf("launched %+v", calls)
	}
	h.at(29, "seven-a.png")
}

// The way to the reads is the same on either way: the comments' files come
// with the page over REST as over the tools.
func TestAFileOverRESTOpensAsOverTheTools(t *testing.T) {
	t.Parallel()

	h := newRESTHarness(t)
	f := h.onSevenA()
	h.drive(h.press("enter"))
	if calls := f.opened(); len(calls) != 1 || calls[0].file.Kind != launch.CommentFile || calls[0].file.ID != postFileID(1) {
		t.Fatalf("opened %+v", calls)
	}
}

func TestTheOrderSwitchedLeavesAFileThatLeftThePage(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	f := h.onSevenA()
	h.drive(h.press("o"))
	// Oldest first, comment 29 stands on the second page.
	h.at(1, "")
	h.drive(h.press("enter"))
	if calls := f.opened(); len(calls) != 0 {
		t.Fatalf("opened %+v", calls)
	}
}

func TestTheOrderSwitchedKeepsAFileStillOnThePage(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.editSeven(func(posts []api.Post) []api.Post { return posts[26:] })
	h.onSevenA()
	h.drive(h.press("o"))
	h.at(29, "seven-a.png")
	h.press("down")
	h.at(29, "seven-b.log")
	h.press("down")
	h.at(30, "")
}

func TestAPageTurnLeavesTheFile(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.onSevenA()
	h.drive(h.press("]"))
	h.at(5, "")
	h.press("down")
	h.press("down")
	h.press("down")
	h.at(3, "seven-old.txt")
	h.drive(h.press("["))
	h.at(30, "")
}

func TestWhileThePageLoadsNoFileIsSelected(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.onSevenA()
	cmd := h.press("]")
	mustContain(t, h.paneText(), "Loading this page")
	if marked, _ := h.markedLines(); len(marked) != 0 {
		t.Fatalf("marks %q", marked)
	}
	if _, _, ok := h.m.fileTarget(); ok || h.offers(OpenList) {
		t.Fatal("a file is offered while its page loads")
	}
	h.drive(cmd)
	h.at(5, "")
}

func TestARefreshWithoutTheFileFallsBackToItsComment(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	gone := false
	h.editSeven(func(posts []api.Post) []api.Post {
		if gone {
			postAt(posts, 29).Attachments = postAt(posts, 29).Attachments[1:]
		}
		return posts
	})
	h.onSevenA()
	gone = true
	h.drive(h.press("R"))
	h.at(29, "")
	h.press("down")
	h.at(29, "seven-b.log")
}

func TestARefreshKeepsTheFileByItsID(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	gone := false
	h.editSeven(func(posts []api.Post) []api.Post {
		if gone {
			postAt(posts, 29).Attachments = postAt(posts, 29).Attachments[1:]
		}
		return posts
	})
	h.onSevenA()
	h.press("down")
	gone = true
	h.drive(h.press("R"))
	h.at(29, "seven-b.log")
}

func TestARefreshWithoutTheCommentFallsBackToTheFirst(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	gone := false
	h.editSeven(func(posts []api.Post) []api.Post {
		if gone {
			return slices.DeleteFunc(posts, func(p api.Post) bool { return p.ID == sevenPostID(29) })
		}
		return posts
	})
	h.onSevenA()
	gone = true
	h.drive(h.press("R"))
	h.at(30, "")
}

func TestACountWithoutAListIsSaidAndNothingIsSelected(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.toComments()
	tl := ticketText(h.m.app.detail, ticketContext{paged: true, when: h.m.app.when})
	at := slices.Index(tl.lines, "2 files, not listed by this server")
	if at < 0 || tl.lines[at-1] != "Seven comment 27" || tl.lines[at+1] != "" || tl.styles[at] != noteLine {
		t.Fatalf("the hint at %d:\n%s", at, strings.Join(tl.lines, "\n"))
	}
	for range 6 {
		h.press("down")
	}
	h.at(27, "")
	h.press("down")
	h.at(26, "")
}

func TestOneFileWithoutAListIsOneFile(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.editSeven(func(posts []api.Post) []api.Post {
		postAt(posts, 27).AttachmentsCount = 1
		return posts
	})
	h.toComments()
	mustContain(t, h.paneText(), "1 file, not listed by this server")
}

// The list of a server since R7 is what there is, also when it is empty and
// the count is not: only a comment without a list has the hint.
func TestAnEmptyListBesideACountSaysNothing(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.toComments()
	tl := ticketText(h.m.app.detail, ticketContext{paged: true, when: h.m.app.when})
	at := slices.Index(tl.lines, "Seven comment 26")
	if at < 0 || tl.lines[at+1] != "" {
		t.Fatalf("under comment 26:\n%s", strings.Join(tl.lines, "\n"))
	}
	if n := strings.Count(h.paneText(), "not listed"); n != 1 {
		t.Fatalf("%d hints", n)
	}
}

func TestAFileWithoutAnIDIsNamedAndSkipped(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.editSeven(func(posts []api.Post) []api.Post {
		empty := ""
		postAt(posts, 25).Attachments = append(postAt(posts, 25).Attachments, api.Attachment{ID: &empty, Filename: "seven-empty.txt"})
		return posts
	})
	h.toComments()
	mustContain(t, h.paneText(), "seven-noid.txt", "seven-empty.txt")
	for range 8 {
		h.press("down")
	}
	h.at(25, "")
	h.press("down")
	h.at(24, "")
}

// A private comment's file is what the page holds: a line like any other,
// which the server has already decided this reader may see.
func TestAPrivateCommentsFileAddsNothing(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	f := h.toComments()
	before := len(h.f.detailReads())
	for range 5 {
		h.press("down")
	}
	h.at(28, "seven-private.pdf")
	tl := ticketText(h.m.app.detail, ticketContext{paged: true, when: h.m.app.when})
	mustContain(t, tl.lines[tl.posts[sevenPostID(28)]], "private")
	h.drive(h.press("enter"))
	want := launch.File{Kind: launch.CommentFile, ID: postFileID(3), Name: "seven-private.pdf"}
	if calls := f.opened(); len(calls) != 1 || calls[0].file != want {
		t.Fatalf("opened %+v", calls)
	}
	if after := len(h.f.detailReads()); after != before {
		t.Fatalf("%d reads, then %d", before, after)
	}
}

func TestEachSectionKeepsItsFile(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	f := h.onSevenA()
	h.press("tab")
	if marked, _ := h.markedLines(); len(marked) != 1 || !strings.HasPrefix(marked[0], "> six-01.txt") {
		t.Fatalf("marks %q", marked)
	}
	h.drive(h.press("enter"))
	h.press("tab")
	if marked, _ := h.markedLines(); len(marked) != 0 {
		t.Fatalf("marks in the description %q", marked)
	}
	h.press("tab")
	h.at(29, "seven-a.png")
	h.drive(h.press("enter"))
	calls := f.opened()
	if len(calls) != 2 || calls[0].file != (launch.File{Kind: launch.TicketFile, ID: fileID(1), Name: "six-01.txt"}) ||
		calls[1].file != (launch.File{Kind: launch.CommentFile, ID: postFileID(1), Name: "seven-a.png"}) {
		t.Fatalf("opened %+v", calls)
	}
}

func TestAnIDInBothPlacesMarksOneLine(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.editSeven(func(posts []api.Post) []api.Post {
		id := fileID(1)
		postAt(posts, 29).Attachments[0].ID = &id
		return posts
	})
	h.onSevenA()
	h.press("tab")
	if marked, _ := h.markedLines(); len(marked) != 1 || !strings.HasPrefix(marked[0], "> six-01.txt") {
		t.Fatalf("marks %q", marked)
	}
}

// A server is trusted with the ids of its comments (api.CheckTicket), not
// with a list of files: one named twice is one stop, and the walk goes on.
func TestAFileListedTwiceIsOneStop(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.editSeven(func(posts []api.Post) []api.Post {
		p := postAt(posts, 29)
		p.Attachments = []api.Attachment{p.Attachments[0], p.Attachments[0], p.Attachments[1]}
		return posts
	})
	h.onSevenA()
	h.press("down")
	h.at(29, "seven-b.log")
	h.press("up")
	h.at(29, "seven-a.png")
}

func TestAMissingCommentTakesItsFilesWithIt(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	f := h.onSevenA()
	h.f.setTicket(func(project, id string, o api.TicketOptions) transport.Answer {
		if o.PostID == sevenPostID(29) {
			return transport.Answer{Class: transport.NotFound, Message: "No comment found for " + o.PostID + ".", Status: 200, Sent: true}
		}
		return h.d.defaultTicket(project, id, o)
	})
	h.drive(h.press("e"))
	text := h.paneText()
	mustContain(t, text, "Comment unavailable")
	mustNotContain(t, text, "seven-a.png", "seven-b.log")
	marked, under := h.markedLines()
	if len(marked) != 1 || len(under) != 1 || under[0] != "Comment unavailable" {
		t.Fatalf("marks %q above %q", marked, under)
	}
	h.drive(h.press("enter"))
	if calls := f.opened(); len(calls) != 0 {
		t.Fatalf("opened %+v", calls)
	}
	h.press("down")
	h.at(28, "")
}

func TestAForeignFileNameUnderACommentIsCleaned(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.editSeven(func(posts []api.Post) []api.Post {
		postAt(posts, 29).Attachments[0].Filename = "a\x1b[2J\nb.png"
		return posts
	})
	f := h.toComments()
	h.press("down")
	h.press("down")
	if strings.ContainsRune(h.paneText(), 0x1b) {
		t.Fatalf("pane %q", h.paneText())
	}
	if marked, _ := h.markedLines(); len(marked) != 1 || !strings.Contains(marked[0], "b.png") {
		t.Fatalf("marks %q", marked)
	}
	cmd := h.press("enter")
	if got := h.status(); strings.ContainsRune(got, 0x1b) {
		t.Fatalf("status %q", got)
	}
	f.openKind = launch.FileOpened
	h.drive(cmd)
	if got := h.status(); strings.ContainsRune(got, 0x1b) {
		t.Fatalf("status %q", got)
	}
}

func TestTheWalkKeepsTheFileInSight(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.toComments()
	top := h.m.objectColumn().Offset
	for range 40 {
		h.press("down")
	}
	d := h.m.app.detail
	file := h.m.objectColumn().Offset
	if file <= top || d.follow != anchorFile {
		t.Fatalf("offset %d, was %d, follow %v", file, top, d.follow)
	}
	// The file's line, two below its comment's head.
	d.follow = anchorPost
	if comment := h.m.objectColumn().Offset; file != comment+2 {
		t.Fatalf("the file at %d, its comment at %d", file, comment)
	}
}

// The file left with the page: the comment that took the selection is what
// the pane keeps in sight.
func TestAFileGoneFromThePageLeavesTheCommentInSight(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.onSevenA()
	h.drive(h.press("]"))
	got := h.m.objectColumn().Offset
	h.m.app.detail.follow = anchorPost
	if want := h.m.objectColumn().Offset; got != want {
		t.Fatalf("offset %d, the comment at %d", got, want)
	}
}

// A file belongs to its comment: when the selection moves to another one,
// the file stays behind, even one of the same id under the new comment.
func TestAnotherCommentTakesNoFileAlong(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	gone := false
	h.editSeven(func(posts []api.Post) []api.Post {
		if gone {
			postAt(posts, 30).Attachments = postAt(posts, 29).Attachments
			return slices.DeleteFunc(posts, func(p api.Post) bool { return p.ID == sevenPostID(29) })
		}
		return posts
	})
	h.onSevenA()
	gone = true
	h.drive(h.press("R"))
	h.at(30, "")
}

func TestATicketOpenedAnewStandsOnNoFile(t *testing.T) {
	t.Parallel()

	d := &detailView{area: areaComments, post: "p", postFile: "f", file: "g"}
	d.startAtHead()
	if d.post != "" || d.postFile != "" || d.file != "" || d.area != areaDescription {
		t.Fatalf("%+v", d)
	}
}
