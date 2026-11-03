package ui

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/queue"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

// K7b: the meeting in the object pane: Enter and the arrow read it at
// once, a selection without the focus after the pause; its sections by
// Tab; read only, memory only.

// meetingText is the pane's text as the view draws it, line by line.
func (h *harness) meetingText() string {
	c := h.m.objectColumn()
	if c.Wrapped != nil {
		return strings.Join(c.Wrapped, "\n")
	}
	return c.Text.String()
}

// rendered is the whole screen as the terminal gets it.
func (h *harness) rendered() string {
	return view.Render(h.m.screen(), h.m.geometry(), h.m.focus, h.m.widthMethod)
}

// previewDue fires the preview timer that is set.
func (h *harness) previewDue() {
	h.t.Helper()
	a := h.m.app
	h.drive(h.feed(gateway.Due{Session: a.session, Token: a.previewToken})...)
}

// A preview held by the queue budget must become the person's request on Enter.
func TestMeetingOpenPromotesPreviewAtBudget(t *testing.T) {
	t.Parallel()
	h := newRESTHarness(t)
	h.openMeetings(website)
	clock := &meetingFrozenClock{now: h.now, scheduled: make(chan struct{}, 1)}
	q := queue.New(queue.WithLifetime(h.live), queue.WithClock(clock), queue.WithRetryDelay(0))
	h.m.app.deps.Gateway = gateway.New(h.live, q, h.f, gateway.WithEvents(h.events))
	ctx, cancel := context.WithTimeout(context.Background(), wall)
	defer cancel()
	for range queue.PreviewBudget {
		answer := q.Do(ctx, queue.Job{Priority: queue.Preview, Run: func(context.Context) transport.Answer {
			return transport.Answer{Class: transport.Object}
		}})
		if !answer.OK() {
			t.Fatalf("filling preview budget: %v", answer.Class)
		}
	}
	cmds := h.feed(gateway.Due{Session: h.m.app.session, Token: h.m.app.previewToken})
	if len(cmds) != 1 {
		t.Fatalf("preview commands: %d", len(cmds))
	}
	preview := h.background(cmds[0])
	select {
	case <-clock.scheduled:
	case <-ctx.Done():
		t.Fatal("preview did not reach the spent budget")
	}
	open := h.press("enter")
	if open == nil {
		t.Fatalf("Enter issued no user request; meeting remains at preview priority behind exhausted %d/min budget (server reads: %v)", queue.PreviewBudget, h.f.meetingShows())
	}
	h.drive(open)
	h.drive(h.feed(h.receive(preview))...)
	if len(h.f.meetingShows()) != 1 || !h.m.app.meeting.known {
		t.Fatalf("open did not read exactly once: reads %v, known %v", h.f.meetingShows(), h.m.app.meeting.known)
	}
}

type meetingFrozenClock struct {
	now       time.Time
	scheduled chan struct{}
}

func (c *meetingFrozenClock) Now() time.Time { return c.now }
func (c *meetingFrozenClock) After(time.Duration) <-chan time.Time {
	select {
	case c.scheduled <- struct{}{}:
	default:
	}
	return make(chan time.Time)
}

// R on a meeting whose first read failed reads the meeting again, not
// the list.
func TestMeetingRefreshRetriesDetailAfterFirstFailure(t *testing.T) {
	t.Parallel()
	h := newRESTHarness(t)
	h.openMeetings(website)
	h.f.setMeeting(func(string) transport.Answer {
		return transport.Answer{Class: transport.Temporary, Message: "connection reset", Sent: true}
	})
	h.drive(h.press("enter"))
	detailBefore, listBefore := len(h.f.meetingShows()), len(h.f.meetingReads())
	h.f.setMeeting(defaultMeeting)
	h.f.setMeetings(func(api.MeetingQuery) transport.Answer {
		return transport.Answer{Class: transport.Temporary, Message: "list unavailable", Sent: true}
	})
	h.drive(h.press("R"))
	if !h.m.app.meeting.known || len(h.f.meetingReads()) != listBefore || len(h.f.meetingShows()) != detailBefore+1 {
		t.Fatalf("R did not retry recovered meeting endpoint: detail calls %d -> %d, list calls %d -> %d", detailBefore, len(h.f.meetingShows()), listBefore, len(h.f.meetingReads()))
	}
}

// Closing the search line without a new search sets the preview timer
// again: one that fired while the line was open read nothing.
func TestMeetingSearchCloseRearmsConsumedPreview(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"esc", "enter"} {
		t.Run(key, func(t *testing.T) {
			h := newRESTHarness(t)
			h.openMeetings(website)
			token := h.m.app.previewToken
			h.press("/")
			h.previewDue()
			cmd := h.press(key)
			if cmd == nil || h.m.app.previewToken == token {
				t.Fatalf("closing search with %s left no new preview timer; token %d, reads %v, mode %v", key, token, h.f.meetingShows(), h.m.mode)
			}
			h.drive(cmd)
			h.previewDue()
			if !h.m.app.meetingInPane() || len(h.f.meetingShows()) != 1 {
				t.Fatalf("preview was not read after closing search: %v", h.f.meetingShows())
			}
		})
	}
}

// A page turned to from memory shows its selected meeting from memory at
// once and reads it again on its own, whatever becomes of the list's reload.
func TestMeetingCachedPageRestoresDetailWithoutListReload(t *testing.T) {
	t.Parallel()
	h := newRESTHarness(t)
	h.openMeetings(website)
	h.drive(h.press("]"))
	h.drive(h.press("enter"))
	detailKey := h.m.app.meeting.key
	h.drive(h.press("esc"))
	h.drive(h.press("["))
	h.previewDue()
	if _, ok := h.m.app.store.Get(detailKey); !ok {
		t.Fatal("detail on page two not cached")
	}
	oldToken := h.m.app.previewToken
	h.f.setMeetings(func(api.MeetingQuery) transport.Answer {
		return transport.Answer{Class: transport.Object, Payload: []byte("{"), Status: 200, Sent: true}
	})
	h.drive(h.press("]"))
	if !h.m.app.meetings.cached || h.m.app.meetings.err.String() == "" {
		t.Fatal("cached page with failed reload not established")
	}
	d := h.m.app.meeting
	if d == nil || !d.known || d.key != detailKey || h.m.app.previewToken == oldToken {
		t.Fatalf("cached detail not restored or preview not scheduled: token=%d, pane=%q", h.m.app.previewToken, h.meetingText())
	}
	reads := len(h.f.meetingShows())
	h.previewDue()
	if d.cached || len(h.f.meetingShows()) != reads+1 {
		t.Fatalf("cached detail was not refreshed independently of the list: cached=%v, reads=%v", d.cached, h.f.meetingShows())
	}
}

// A stale cache entry remembers an earlier selection's failure. Opening
// it again reads it once; if that read fails, resizing does not retry it.
func TestTheArrowReloadsARevisitedStaleMeeting(t *testing.T) {
	t.Parallel()
	for _, recovered := range []bool{true, false} {
		name := "unavailable"
		if recovered {
			name = "recovered"
		}
		t.Run(name, func(t *testing.T) {
			h := newRESTHarness(t)
			h.openMeetings(website)
			h.drive(h.press("enter"))
			h.f.setMeeting(func(string) transport.Answer {
				return transport.Answer{Class: transport.Temporary, Message: "connection reset", Sent: true}
			})
			h.drive(h.press("R"))
			if recovered {
				h.f.setMeeting(defaultMeeting)
			}
			h.press("esc")
			h.drive(h.press("down"))
			h.drive(h.press("up"))
			d := h.m.app.meeting
			if d == nil || !d.cached || !d.stale {
				t.Fatal("the revisited meeting is not stale cached data")
			}
			before := len(h.f.meetingShows())
			h.drive(h.press("right"))
			reads := 2 // a temporary failure is retried once by the queue
			if recovered {
				reads = 1
			}
			if len(h.f.meetingShows()) != before+reads || d.holds() != recovered {
				t.Fatalf("right did not reload the stale meeting: reads %d -> %d, fresh %v", before, len(h.f.meetingShows()), d.holds())
			}
			h.drive(h.feed(tea.WindowSizeMsg{Width: 130, Height: 24})...)
			if len(h.f.meetingShows()) != before+reads {
				t.Fatal("resizing retried the revisited meeting")
			}
		})
	}
}

func TestEnterAndTheArrowShowTheMeetingAtOnce(t *testing.T) {
	t.Parallel()

	h := newRESTHarness(t)
	h.openMeetings(website)
	first := meetingID(website, 1)
	h.drive(h.press("enter"))
	if h.m.focus != view.Object || !slices.Equal(h.f.meetingShows(), []string{first}) {
		t.Fatalf("focus %d, reads %v", h.m.focus, h.f.meetingShows())
	}
	text := h.meetingText()
	for _, want := range []string{"Meeting 1", "» Summary  Notes · Transcript", "**Ergebnis:** Start im März."} {
		if !strings.Contains(text, want) {
			t.Fatalf("no %q in %q", want, text)
		}
	}
	// The facts stand in the bar, with the participants; no key that acts.
	var bar []string
	for _, l := range h.m.objectColumn().Meta {
		bar = append(bar, l.Text.String())
	}
	if got := strings.Join(bar, "|"); !strings.Contains(got, "Date|30 Jan 09:00||Duration|0:31||Project|Website") ||
		!strings.Contains(got, "Participants|Anna Albers|Name unknown") || strings.Contains(got, "Keys") {
		t.Fatalf("bar %q", got)
	}
	// Esc back to the list; the pane keeps the meeting.
	h.press("esc")
	if h.m.focus != view.List || h.m.app.meeting == nil || h.m.app.meeting.id != first {
		t.Fatalf("focus %d, meeting %+v", h.m.focus, h.m.app.meeting)
	}
	// The arrow into the pane reads the next row's meeting at once.
	h.drive(h.press("down"))
	h.drive(h.press("right"))
	second := meetingID(website, 2)
	if h.m.focus != view.Object || !slices.Equal(h.f.meetingShows(), []string{first, second}) || !strings.Contains(h.meetingText(), "Nur Notizen.") {
		t.Fatalf("focus %d, reads %v, text %q", h.m.focus, h.f.meetingShows(), h.meetingText())
	}
	// Opening a fresh one again reads nothing.
	h.press("esc")
	h.drive(h.press("enter"))
	if len(h.f.meetingShows()) != 2 {
		t.Fatalf("reads %v", h.f.meetingShows())
	}
}

// Without the focus a new selection is read only after the pause, as a
// ticket's preview: no request per arrow key.
func TestAMeetingWithoutTheFocusLoadsAfterThePause(t *testing.T) {
	t.Parallel()

	h := newRESTHarness(t)
	h.openMeetings(website)
	h.drive(h.press("down"))
	h.drive(h.press("down"))
	if got := h.f.meetingShows(); len(got) != 0 {
		t.Fatalf("read before the pause: %v", got)
	}
	if !strings.Contains(h.meetingText(), "Title: Meeting 3") {
		t.Fatalf("the row's facts are not shown meanwhile: %q", h.meetingText())
	}
	h.previewDue()
	if got := h.f.meetingShows(); !slices.Equal(got, []string{meetingID(website, 3)}) || h.m.focus != view.List {
		t.Fatalf("reads %v, focus %d", got, h.m.focus)
	}
	// Meeting 3 has nothing written down; it says so.
	if !strings.Contains(h.meetingText(), "Nothing written down for this meeting.") {
		t.Fatalf("%q", h.meetingText())
	}
	// A stale timer reads nothing.
	token := h.m.app.previewToken
	h.drive(h.press("up"))
	h.drive(h.feed(gateway.Due{Session: h.m.app.session, Token: token})...)
	if got := h.f.meetingShows(); len(got) != 1 {
		t.Fatalf("a stale timer read: %v", got)
	}
}

// Tab walks the sections that have something in them; the empty ones are
// left out, and with one section there is no Tab.
func TestTabWalksTheSectionsAndLeavesEmptyOnesOut(t *testing.T) {
	t.Parallel()

	h := newRESTHarness(t)
	h.openMeetings(website)
	h.drive(h.press("enter"))
	for _, want := range []string{"» Notes  Summary · Transcript\nTermin im März halten.", "» Transcript  Summary · Notes\nAnna: Wir starten im März.\nErika: Gut.", "» Summary  Notes · Transcript"} {
		h.press("tab")
		if !strings.Contains(h.meetingText(), want) {
			t.Fatalf("no %q in %q", want, h.meetingText())
		}
	}
	h.press("esc")
	h.drive(h.press("down"))
	h.drive(h.press("enter"))
	if text := h.meetingText(); !strings.Contains(text, "» Notes\nNur Notizen.") || strings.Contains(text, "Summary") || h.offers(NextArea) {
		t.Fatalf("one section: %q, tab %v", text, h.offers(NextArea))
	}
	for _, k := range []Action{Launch, BookTime, AddComment, MarkRead} {
		if h.offers(k) {
			t.Errorf("%v on a meeting", k)
		}
	}
	for _, key := range []string{"x", "b", "k"} {
		if cmd := h.press(key); cmd != nil || h.m.dialog != nil {
			t.Errorf("%s acted on a meeting", key)
		}
	}
	if help := h.helpText(); strings.Contains(help, "book") || strings.Contains(help, "comment") || strings.Contains(help, "claude") {
		t.Fatalf("help %q", help)
	}
}

// A transcript of 500 KB is wrapped once per width: frames, scrolls and
// sections already wrapped take it from memory; a new width wraps once.
func TestALongTranscriptIsWrappedOncePerWidth(t *testing.T) {
	t.Parallel()

	h := newRESTHarness(t)
	line := "Anna: Wir starten im März mit allen Seiten, dann die Texte, dann das Logo. "
	long := strings.Repeat(line, 500*1024/len(line)+1) + "Ende."
	h.f.setMeeting(func(id string) transport.Answer {
		for _, r := range meetingRows(website) {
			if r.ID == id {
				return objectAnswer(fullMeeting(r, "", "", long))
			}
		}
		return restNotFound()
	})
	h.openMeetings(website)
	h.drive(h.press("enter"))
	d := h.m.app.meeting
	if d == nil || !d.known || len(long) < 500*1024 {
		t.Fatalf("not shown: %+v", d)
	}
	for range 20 {
		_ = h.m.View()
		h.press("space")
	}
	if d.wraps != 1 {
		t.Fatalf("%d wraps at one width", d.wraps)
	}
	// The view gets the lines wrapped, and the end is reached.
	c := h.m.objectColumn()
	if len(c.Wrapped) < 6000 || c.Text.String() != "" {
		t.Fatalf("%d lines wrapped, text %d bytes", len(c.Wrapped), len(c.Text.String()))
	}
	for before := -1; before != d.offset; {
		before = d.offset
		h.press("space")
	}
	if !strings.Contains(h.rendered(), "Ende.") {
		t.Fatalf("the end of the transcript is out of reach: offset %d of %d lines", d.offset, len(c.Wrapped))
	}
	// A wider terminal, the bar still beside the text: the width alone
	// changed, and the text is wrapped once more.
	h.feed(tea.WindowSizeMsg{Width: 150, Height: 30})
	_ = h.m.View()
	_ = h.m.View()
	if d.wraps != 2 || len(h.m.objectColumn().Meta) == 0 {
		t.Fatalf("%d wraps after one resize", d.wraps)
	}
}

// An answer above the transport's bound is an error line in the pane,
// never a cut text; a refusal or a meeting gone leaves the list as it is.
func TestAMeetingThatFailsLeavesTheListAndShowsNoText(t *testing.T) {
	t.Parallel()

	for name, c := range map[string]struct {
		answer transport.Answer
		notice string
	}{
		"too large": {transport.Answer{Class: transport.Protocol, Message: "answer larger than 8388608 bytes", Sent: true}, "answer larger than 8388608 bytes"},
		"gone":      {restNotFound(), "Not found"},
		"refused":   {transport.Answer{Class: transport.Refused, Message: "Not allowed.", Status: 403, Sent: true}, "Not allowed."},
	} {
		h := newRESTHarness(t)
		h.openMeetings(website)
		rows, notice := h.meetingLines(), h.m.listColumn().Notice.String()
		h.f.setMeeting(func(string) transport.Answer { return c.answer })
		h.drive(h.press("enter"))
		pane := h.m.objectColumn()
		if !strings.Contains(pane.Notice.String(), c.notice) || pane.Wrapped != nil || strings.Contains(pane.Text.String(), "Ergebnis") {
			t.Errorf("%s: notice %q, text %q", name, pane.Notice.String(), pane.Text.String())
		}
		if !slices.Equal(h.meetingLines(), rows) || h.m.listColumn().Notice.String() != notice || h.m.app.meetings.err.String() != "" {
			t.Errorf("%s: the list changed", name)
		}
		h.press("esc")
		if h.m.focus != view.List {
			t.Errorf("%s: focus %d", name, h.m.focus)
		}
	}
}

// A transcript is foreign: what it would do to a terminal does not reach
// it.
func TestATranscriptCannotReachTheTerminal(t *testing.T) {
	t.Parallel()

	h := newRESTHarness(t)
	h.openMeetings(handbuch)
	h.drive(h.press("down"))
	h.drive(h.press("enter"))
	text := h.meetingText()
	if !strings.Contains(text, "copied") || !strings.Contains(text, "cleared") {
		t.Fatalf("the transcript is not shown: %q", text)
	}
	screen := h.rendered()
	for _, bad := range []string{"\x1b]", "\x1b[2J", "\x07", "\u202e"} {
		if strings.Contains(text, bad) || strings.Contains(screen, bad) {
			t.Fatalf("%q reached the screen", bad)
		}
	}
}

// A whole meeting stays off the disk, its rule bound to its spec: a
// session that read one leaves it nowhere there, while a ticket read
// beside it is written.
func TestNoWholeMeetingReachesTheDisk(t *testing.T) {
	t.Parallel()

	if !slices.Contains(api.MemoryOnly(), api.MeetingRESTSpec.Name) {
		t.Fatal("the meeting is not memory only")
	}
	root := t.TempDir()
	h := newDiskHarness(t, root, restFakes(recorded(t)))
	h.openMeetings(website)
	h.drive(h.press("enter"))
	meeting := h.m.app.meeting.key
	if _, ok := h.m.app.store.Get(meeting); !ok {
		t.Fatal("the meeting is not in memory")
	}
	h.press("esc")
	h.press("esc")
	h.press("esc")
	h.drive(h.chooseTickets(orgA))
	h.drive(h.openProject(website))
	tickets := h.m.app.tickets.key
	h.drive(h.m.app.flushDisk())
	h.press("q")
	c := diskOf(t, root, h.now)
	if !onDisk(t, c, tickets, h.now) {
		t.Fatal("the tickets were not written: this test proves nothing then")
	}
	if onDisk(t, c, meeting, h.now) {
		t.Fatal("the meeting is on disk")
	}
}

// A meeting reaches the pane only through the scope: one no waiter of the
// pane asked for (a late answer, a forged one) is not shown and not filed.
func TestAMeetingIsShownOnlyAfterTheScopeAcceptedIt(t *testing.T) {
	t.Parallel()

	h := newRESTHarness(t)
	h.openMeetings(website)
	h.drive(h.press("enter"))
	d := h.m.app.meeting
	forged := gateway.MeetingResult{Ticket: h.m.app.scope.Begin(gateway.Meeting, d.key, queue.User)}
	forged.Meeting = d.record
	forged.Meeting.Title = "Forged"
	forged.Answer = objectAnswer(map[string]any{"data": forged.Meeting})
	h.feed(forged)
	if strings.Contains(h.meetingText(), "Forged") || d.record.Title == "Forged" {
		t.Fatal("a meeting no waiter asked for was shown")
	}
	if e, ok := h.m.app.store.Get(d.key); !ok || strings.Contains(string(e.Payload), "Forged") {
		t.Fatal("a meeting no waiter asked for was filed")
	}
}

// A creator who is gone is named so, once, in the head and in the bar.
func TestAMeetingWhoseCreatorIsGoneSaysSo(t *testing.T) {
	t.Parallel()

	h := newRESTHarness(t)
	h.openMeetings(website)
	h.drive(h.press("down"))
	h.drive(h.press("down"))
	h.previewDue()
	if text := strings.Join(strings.Fields(h.meetingText()), " "); !strings.Contains(text, "Creator unknown") || strings.Contains(text, "created by") {
		t.Fatalf("head %q", text)
	}
	h.drive(h.press("enter"))
	var bar []string
	for _, l := range h.m.objectColumn().Meta {
		bar = append(bar, l.Text.String())
	}
	if got := strings.Join(bar, "|"); !strings.Contains(got, "People|Creator unknown") {
		t.Fatalf("bar %q", got)
	}
}

// Enter on a meeting whose preview is on the wire joins it as the
// person's read: one request, at the person's priority (review round 1 of
// #8, finding 1).
func TestOpeningAMeetingJoinsItsPreviewAsThePersons(t *testing.T) {
	t.Parallel()

	h := newRESTHarness(t)
	h.openMeetings(website)
	a := h.m.app
	gate := make(chan struct{})
	h.f.gate, h.f.gateMeeting = gate, true
	preview := h.feed(gateway.Due{Session: a.session, Token: a.previewToken})
	if len(preview) != 1 {
		t.Fatalf("%d preview commands", len(preview))
	}
	previewResult := h.background(preview[0])
	var share string
	for _, w := range a.waiters {
		if w.ticket.Area == gateway.Meeting {
			share = w.ticket.ShareKey()
		}
	}
	h.waitFor("the preview on the wire", func() bool { return h.q.Sharers(share) == 1 })
	open := h.press("enter")
	users := 0
	for _, w := range a.waiters {
		if w.ticket.Area == gateway.Meeting && w.ticket.Priority == queue.User {
			users++
		}
	}
	if h.m.focus != view.Object || users != 1 || open == nil {
		t.Fatalf("focus %d, %d waiters of the person", h.m.focus, users)
	}
	openResult := h.background(open)
	h.waitFor("the open joining the preview", func() bool { return h.q.Sharers(share) == 2 })
	close(gate)
	h.feed(h.receive(previewResult), h.receive(openResult))
	if got := h.f.meetingShows(); len(got) != 1 || !strings.Contains(h.meetingText(), "Ergebnis") {
		t.Fatalf("reads %v, text %q", got, h.meetingText())
	}
}

// A meeting that failed is read again by R alone: a resize or the arrows
// with the pane focused send nothing (review round 1 of #8, finding 2).
func TestAFailedMeetingIsReadAgainOnlyByR(t *testing.T) {
	t.Parallel()

	h := newRESTHarness(t)
	h.openMeetings(website)
	h.f.setMeeting(func(string) transport.Answer { return restNotFound() })
	h.drive(h.press("enter"))
	if got := h.f.meetingShows(); len(got) != 1 {
		t.Fatalf("reads %v", got)
	}
	for i := range 4 {
		h.drive(h.feed(tea.WindowSizeMsg{Width: 120 + i, Height: 24})...)
		h.drive(h.press("right"))
	}
	if got := h.f.meetingShows(); len(got) != 1 {
		t.Fatalf("%d reads after resizes and arrows", len(got))
	}
	if !h.offers(Refresh) {
		t.Fatal("no R on a failed meeting")
	}
	h.f.setMeeting(defaultMeeting)
	h.drive(h.press("R"))
	if got := h.f.meetingShows(); len(got) != 2 || !strings.Contains(h.meetingText(), "Ergebnis") {
		t.Fatalf("reads %v, text %q", got, h.meetingText())
	}
}

// R on the focused meeting reads it again and shows what changed, wrapped
// anew; while the read is out R is not offered (review round 1 of #8,
// findings 3 and 4).
func TestRReadsTheFocusedMeetingAgain(t *testing.T) {
	t.Parallel()

	h := newRESTHarness(t)
	h.openMeetings(website)
	h.drive(h.press("enter"))
	listReads := len(h.f.meetingReads())
	h.f.setMeeting(func(id string) transport.Answer {
		// The same sections, the summary changed: only the record tells
		// the wrap apart.
		return objectAnswer(fullMeeting(meetingRows(website)[0], "Neu: Start im April.", "Termin im März halten.", "Anna: Wir starten im März.\nErika: Gut."))
	})
	r := h.press("R")
	if h.offers(Refresh) {
		t.Fatal("R offered while the meeting is read")
	}
	h.drive(r)
	if got := h.f.meetingShows(); len(got) != 2 || len(h.f.meetingReads()) != listReads {
		t.Fatalf("meeting reads %v, list reads %d → %d", got, listReads, len(h.f.meetingReads()))
	}
	if text := h.meetingText(); !strings.Contains(text, "Neu: Start im April.") || strings.Contains(text, "Ergebnis") {
		t.Fatalf("%q", text)
	}
}

// The arrow into the pane raises a preview on the wire to the person's
// read as Enter does, once however often it is pressed.
func TestTheArrowJoinsAPreviewOnceAsThePersons(t *testing.T) {
	t.Parallel()

	h := newRESTHarness(t)
	h.openMeetings(website)
	a := h.m.app
	gate := make(chan struct{})
	h.f.gate, h.f.gateMeeting = gate, true
	preview := h.feed(gateway.Due{Session: a.session, Token: a.previewToken})
	if len(preview) != 1 {
		t.Fatalf("%d preview commands", len(preview))
	}
	previewResult := h.background(preview[0])
	var share string
	for _, w := range a.waiters {
		if w.ticket.Area == gateway.Meeting {
			share = w.ticket.ShareKey()
		}
	}
	h.waitFor("the preview on the wire", func() bool { return h.q.Sharers(share) == 1 })
	var cmds []tea.Cmd
	for range 3 {
		if cmd := h.press("right"); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	users := 0
	for _, w := range a.waiters {
		if w.ticket.Area == gateway.Meeting && w.ticket.Priority == queue.User {
			users++
		}
	}
	if h.m.focus != view.Object || users != 1 {
		t.Fatalf("focus %d, %d waiters of the person", h.m.focus, users)
	}
	var results []<-chan tea.Msg
	for _, cmd := range cmds {
		results = append(results, h.background(cmd))
	}
	h.waitFor("the arrow joining the preview", func() bool { return h.q.Sharers(share) == 2 })
	close(gate)
	h.feed(h.receive(previewResult))
	for _, r := range results {
		h.drive(h.feed(h.receive(r))...)
	}
	if got := h.f.meetingShows(); len(got) != 1 || !strings.Contains(h.meetingText(), "Ergebnis") {
		t.Fatalf("reads %v, text %q", got, h.meetingText())
	}
}
