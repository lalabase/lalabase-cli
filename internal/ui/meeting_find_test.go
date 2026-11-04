package ui

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

// F1: the find in the meeting's pane: / opens its line, Enter finds in the
// section shown, n and N move through the hits round the end, Esc ends it;
// the mode of its own keeps the list's search line out of it.

// findTranscript is a transcript of forty turns as the recorder writes
// them, Anna Albers and Erika Beispiel by turns, each numbered; the second
// turn and the thirtieth speak of the logo.
func findTranscript() string {
	var b strings.Builder
	for i := 1; i <= 40; i++ {
		name := "Anna Albers"
		if i%2 == 0 {
			name = "Erika Beispiel"
		}
		fmt.Fprintf(&b, "**%s**\nPunkt %d: Wir starten im März mit allen Seiten, dann die Texte.\n", name, i)
		if i == 2 || i == 30 {
			b.WriteString("Das Logo kommt zuletzt.\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// findMeetings answers Website's first meeting with the find's transcript,
// both speakers its participants; any other as the harness does.
func findMeetings(id string) transport.Answer {
	r := meetingRows(website)[0]
	if id != r.ID {
		return defaultMeeting(id)
	}
	m := fullMeeting(r, "**Ergebnis:** Start im März.", "Termin im März halten, im März starten.", findTranscript())
	m["data"].(map[string]any)["participants"] = []map[string]any{{"display_name": "Anna Albers"}, {"display_name": "Erika Beispiel"}}
	return objectAnswer(m)
}

// newFindHarness opens Website's first meeting, with the find's
// transcript, in the focused pane: its summary, or its transcript.
func newFindHarness(t *testing.T, transcript bool) *harness {
	t.Helper()
	h := newRESTHarness(t)
	h.f.setMeeting(findMeetings)
	h.openMeetings(website)
	h.drive(h.press("enter"))
	if !h.m.app.meetingInPane() || h.m.focus != view.Object {
		t.Fatalf("the meeting is not in the focused pane: focus %d", h.m.focus)
	}
	if transcript {
		h.press("tab")
		h.press("tab")
	}
	return h
}

// find opens the find line, empties it, types query and presses Enter.
func (h *harness) find(query string) {
	h.t.Helper()
	h.press("/")
	if h.m.mode != FindEntry {
		h.t.Fatalf("no find line: mode %d", h.m.mode)
	}
	for i := 0; h.m.app.meeting.find.entry != ""; i++ {
		if i == 200 {
			h.t.Fatalf("Backspace does not empty the line: %q", h.m.app.meeting.find.entry)
		}
		h.feed(key(tea.KeyBackspace))
	}
	for _, r := range query {
		h.press(string(r))
	}
	h.press("enter")
}

// scrollDown pages down until the top of the pane is below line past, or,
// for past -1, until it stops at the end. A pane that will not move fails
// the test rather than hold it.
func (h *harness) scrollDown(past int) {
	h.t.Helper()
	for i, before := 0, -1; ; i++ {
		top, _ := h.paneTop()
		if past >= 0 && top > past || past < 0 && top == before {
			return
		}
		if i == 100 {
			h.t.Fatalf("the pane does not move on: top %d", top)
		}
		before = top
		h.press("space")
	}
}

// paneNotice is the notice line of the object pane.
func (h *harness) paneNotice() string { return h.m.objectColumn().Notice.String() }

// paneTop is the first line of the pane's text in sight, and how many
// lines are.
func (h *harness) paneTop() (int, int) {
	c := h.m.objectColumn()
	g := h.m.geometry()
	capacity := max(1, view.Capacity(c, g))
	return max(0, min(c.Offset, view.Extent(c, g, view.Object, h.m.widthMethod)-capacity)), capacity
}

// marks are the runs of the pane's lines from..to drawn as the current hit
// and as the other hits.
func (h *harness) marks(from, to int) (current, others []string) {
	c := h.m.objectColumn()
	for i := from; i < min(to, len(c.Wrapped)); i++ {
		line, st := c.Wrapped[i], c.Styles[i]
		for at := 0; at < len(line); {
			style, end := st.StyleAt(at), at
			for end < len(line) && st.StyleAt(end) == style {
				end++
			}
			switch style {
			case present.MatchCurrent:
				current = append(current, line[at:end])
			case present.Match:
				others = append(others, line[at:end])
			}
			at = end
		}
	}
	return current, others
}

// inSight are the marks in sight.
func (h *harness) inSight() (current, others []string) {
	top, capacity := h.paneTop()
	return h.marks(top, top+capacity)
}

// allMarks are the marks of the whole text.
func (h *harness) allMarks() (current, others []string) { return h.marks(0, 1<<30) }

// / in the meeting's pane opens the find line in the pane's notice: what
// is typed is text there, q too, and no key of the pane acts or opens a
// dialog; Backspace deletes, Esc discards it, Ctrl-C interrupts as
// everywhere. The list shows no search line meanwhile, and its own /
// stays the list's.
func TestSlashOpensTheFindLineInTheMeetingsPane(t *testing.T) {
	t.Parallel()

	h := newFindHarness(t, false)
	if !h.offers(Search) || !strings.Contains(h.helpText(), "/ find") {
		t.Fatalf("no find in the help: %s", h.helpText())
	}
	listNotice := h.m.listColumn().Notice.String()
	h.press("/")
	if h.m.mode != FindEntry || h.paneNotice() != "Find: _" {
		t.Fatalf("mode %d, notice %q", h.m.mode, h.paneNotice())
	}
	for _, k := range []string{"q", "b", "k", "x", "w", "d", "R", "A", "n", "/", "o", "ö"} {
		if cmd := h.press(k); cmd != nil || h.m.dialog != nil || h.m.mode != FindEntry {
			t.Fatalf("%s acted: dialog %v, mode %d", k, h.m.dialog != nil, h.m.mode)
		}
	}
	if h.paneNotice() != "Find: qbkxwdRAn/oö_" || h.m.listColumn().Notice.String() != listNotice || strings.Contains(h.m.listColumn().Notice.String(), "Search") {
		t.Fatalf("pane %q, list %q", h.paneNotice(), h.m.listColumn().Notice.String())
	}
	if help := h.helpText(); help != "enter find | esc cancel | backspace delete | ctrl+c interrupt" {
		t.Fatalf("help %q", help)
	}
	h.feed(key(tea.KeyBackspace))
	// A format character never reaches the line.
	h.feed(tea.KeyPressMsg{Code: 0x202e, Text: "\u202e"}, tea.KeyPressMsg{Code: 'x', Text: "x\u202ey"})
	if h.paneNotice() != "Find: qbkxwdRAn/o_" || h.m.app.meetings.entry != "" {
		t.Fatalf("backspace: %q, the list's line %q", h.paneNotice(), h.m.app.meetings.entry)
	}
	h.press("esc")
	if h.m.mode != Navigate || strings.Contains(h.paneNotice(), "Find") || h.m.app.meeting.find.query != "" || h.m.focus != view.Object {
		t.Fatalf("esc: mode %d, notice %q, query %q", h.m.mode, h.paneNotice(), h.m.app.meeting.find.query)
	}
	// The list's own / opens the list's search line, the pane keeps none.
	h.press("left")
	h.press("/")
	if h.m.mode != TextEntry || h.m.listColumn().Notice.String() != "Search: _" || strings.Contains(h.paneNotice(), "Find") {
		t.Fatalf("the list's /: mode %d, list %q, pane %q", h.m.mode, h.m.listColumn().Notice.String(), h.paneNotice())
	}
	h.press("esc")
	h.drive(h.press("right"))
	h.press("/")
	cmd := h.press("ctrl+c")
	if cmd == nil {
		t.Fatal("ctrl+c did nothing in the find line")
	}
	if _, ok := cmd().(tea.InterruptMsg); !ok {
		t.Fatal("ctrl+c did not interrupt")
	}
}

// Enter marks every hit in the section shown and jumps to the first at or
// below the top of the pane, else to the first: it is drawn as the
// current one and in sight, and the notice says where the find stands.
func TestEnterMarksTheHitsAndJumpsToTheFirstInSight(t *testing.T) {
	t.Parallel()

	h := newFindHarness(t, true)
	h.find("logo")
	if h.m.mode != Navigate || h.paneNotice() != "1 of 2" {
		t.Fatalf("mode %d, notice %q", h.m.mode, h.paneNotice())
	}
	if current, _ := h.inSight(); !slices.Equal(current, []string{"Logo"}) {
		t.Fatalf("the current hit in sight: %q", current)
	}
	if current, others := h.allMarks(); len(current) != 1 || !slices.Equal(others, []string{"Logo"}) {
		t.Fatalf("the marks: %q / %q", current, others)
	}
	// The line opens with the query; Esc there keeps the find.
	h.press("/")
	if h.paneNotice() != "Find: logo_" || h.press("esc") != nil || h.paneNotice() != "1 of 2" {
		t.Fatalf("the line again: %q", h.paneNotice())
	}
	// Below the first hit, Enter finds the second; again, it stays on it,
	// the hit on the top line.
	first, _ := h.paneTop()
	h.scrollDown(first)
	for range 2 {
		h.find("logo")
		if current, _ := h.inSight(); h.paneNotice() != "2 of 2" || !slices.Equal(current, []string{"Logo"}) {
			t.Fatalf("from further down: %q, %q", h.paneNotice(), current)
		}
	}
	// Below every hit, Enter goes round to the first.
	h.scrollDown(-1)
	h.find("logo")
	if current, _ := h.inSight(); h.paneNotice() != "1 of 2, from the top" || !slices.Equal(current, []string{"Logo"}) {
		t.Fatalf("from the end: %q, %q", h.paneNotice(), current)
	}
}

// n and N move to the next and the previous hit round the ends, with a
// note; the current one is always in sight. The help offers them while
// there are hits.
func TestNAndShiftNGoRoundTheEnds(t *testing.T) {
	t.Parallel()

	h := newFindHarness(t, true)
	h.find("punkt 3")
	if !h.offers(NextHit) || !h.offers(PrevHit) || !strings.Contains(h.helpText(), "n/N next/prev hit") || !strings.Contains(h.helpText(), "esc end find") {
		t.Fatalf("help %s", h.helpText())
	}
	for _, step := range []struct{ key, want string }{
		{"n", "2 of 11"}, {"N", "1 of 11"}, {"N", "11 of 11, from the bottom"}, {"n", "1 of 11, from the top"}, {"N", "11 of 11, from the bottom"}, {"N", "10 of 11"},
	} {
		h.press(step.key)
		current, _ := h.inSight()
		if h.paneNotice() != step.want || !slices.Equal(current, []string{"Punkt 3"}) {
			t.Fatalf("%s: %q, current in sight %q", step.key, h.paneNotice(), current)
		}
	}
	for range 10 {
		h.press("n")
	}
	if h.paneNotice() != "9 of 11" {
		t.Fatalf("ten steps on: %q", h.paneNotice())
	}
}

// A query without a hit says so; n and N are not offered and do nothing.
func TestAFindWithoutHitsSaysNoMatch(t *testing.T) {
	t.Parallel()

	h := newFindHarness(t, false)
	h.find("zuletzt")
	if h.paneNotice() != "no match" || h.offers(NextHit) || strings.Contains(h.helpText(), "n/N") {
		t.Fatalf("the summary: %q, help %s", h.paneNotice(), h.helpText())
	}
	if cmd := h.press("n"); cmd != nil || h.paneNotice() != "no match" {
		t.Fatal("n acted without a hit")
	}
	// The same query in the transcript has its hits there.
	h.press("tab")
	h.press("tab")
	if h.paneNotice() != "1 of 2" {
		t.Fatalf("the transcript: %q", h.paneNotice())
	}
}

// Esc ends the find first: the marks go and the pane stays where it is;
// the next Esc goes back to the list. A blank find line ends it too.
func TestEscEndsTheFindFirstAndThePaneStays(t *testing.T) {
	t.Parallel()

	for _, end := range []string{"esc", "blank"} {
		t.Run(end, func(t *testing.T) {
			h := newFindHarness(t, true)
			h.find("logo")
			h.press("n")
			top, _ := h.paneTop()
			if top == 0 {
				t.Fatal("the second hit is in sight from the top: this test proves nothing then")
			}
			if end == "esc" {
				h.press("esc")
			} else {
				h.find("  ")
			}
			current, others := h.allMarks()
			if after, _ := h.paneTop(); after != top || len(current)+len(others) != 0 || h.m.focus != view.Object || h.paneNotice() != "" || h.m.app.meeting.find.query != "" {
				t.Fatalf("%s: top %d → %d, marks %q %q, focus %d, notice %q", end, top, after, current, others, h.m.focus, h.paneNotice())
			}
			if strings.Contains(h.helpText(), "end find") || strings.Contains(h.helpText(), "n/N") {
				t.Fatalf("help %s", h.helpText())
			}
			h.press("esc")
			if h.m.focus != view.List {
				t.Fatalf("the second esc: focus %d", h.m.focus)
			}
		})
	}
}

// Tab keeps the query and finds it in the section it moves to, the first
// hit there current and in sight, whichever was current before.
func TestTabKeepsTheFindForTheNextSection(t *testing.T) {
	t.Parallel()

	h := newFindHarness(t, false)
	h.find("märz")
	for _, want := range []struct{ notice, line string }{
		{"1 of 1", "Ergebnis: Start im März."},
		{"1 of 2", "Termin im März halten, im März starten."},
		{"1 of 40", "Punkt 1: Wir starten im März"},
		{"1 of 1", "Ergebnis: Start im März."},
	} {
		current, _ := h.inSight()
		top, capacity := h.paneTop()
		c := h.m.objectColumn()
		shown := strings.Join(c.Wrapped[top:min(top+capacity, len(c.Wrapped))], "\n")
		if h.paneNotice() != want.notice || !slices.Equal(current, []string{"März"}) || !strings.Contains(shown, want.line) {
			t.Fatalf("want %q in %q: %q, current %q", want.notice, want.line, h.paneNotice(), current)
		}
		h.press("n")
		h.press("tab")
	}
}

// The current hit stays in sight, its line found again, through a resize,
// a reload, a failed reload (its notice before the place) and the way to
// the list and back; a scroll by hand lets it go, and a resize after it
// leaves the pane where the person put it. A reload that leaves no hit
// says so.
func TestTheCurrentHitStaysInSightUntilThePersonScrolls(t *testing.T) {
	t.Parallel()

	h := newFindHarness(t, true)
	h.find("logo")
	h.press("n")
	for _, size := range []tea.WindowSizeMsg{{Width: 70, Height: 24}, {Width: 160, Height: 30}, {Width: 120, Height: 16}} {
		h.drive(h.feed(size)...)
		if current, _ := h.inSight(); !slices.Equal(current, []string{"Logo"}) || h.paneNotice() != "2 of 2" {
			t.Fatalf("%dx%d: current in sight %q, %q", size.Width, size.Height, current, h.paneNotice())
		}
	}
	h.drive(h.press("R"))
	if current, _ := h.inSight(); !slices.Equal(current, []string{"Logo"}) || h.paneNotice() != "2 of 2" || len(h.f.meetingShows()) != 2 {
		t.Fatalf("the reload: current in sight %q, %q", current, h.paneNotice())
	}
	h.f.setMeeting(func(string) transport.Answer {
		return transport.Answer{Class: transport.Temporary, Message: "connection reset", Sent: true}
	})
	h.drive(h.press("R"))
	if current, _ := h.inSight(); !slices.Equal(current, []string{"Logo"}) || h.paneNotice() != "Possibly stale · 2 of 2" {
		t.Fatalf("a failed reload: current in sight %q, %q", current, h.paneNotice())
	}
	h.f.setMeeting(findMeetings)
	h.drive(h.press("R"))
	h.press("left")
	h.drive(h.press("enter"))
	if current, _ := h.inSight(); h.m.focus != view.Object || !slices.Equal(current, []string{"Logo"}) || h.paneNotice() != "2 of 2" {
		t.Fatalf("back from the list: focus %d, current in sight %q, %q", h.m.focus, current, h.paneNotice())
	}
	top, _ := h.paneTop()
	h.press("up")
	h.press("up")
	h.press("up")
	if after, _ := h.paneTop(); after != top-3 || h.m.app.meeting.follow {
		t.Fatalf("the scroll: %d → %d, follow %v", top, after, h.m.app.meeting.follow)
	}
	h.drive(h.feed(tea.WindowSizeMsg{Width: 70, Height: 24})...)
	if after, _ := h.paneTop(); after != top-3 {
		t.Fatalf("the resize took the pane back to the hit: %d, want %d", after, top-3)
	}
	// n takes it up again.
	h.press("n")
	if current, _ := h.inSight(); !slices.Equal(current, []string{"Logo"}) || h.paneNotice() != "1 of 2, from the top" {
		t.Fatalf("n after the scroll: %q, %q", current, h.paneNotice())
	}
	h.f.setMeeting(func(string) transport.Answer {
		return objectAnswer(fullMeeting(meetingRows(website)[0], "", "", "**Anna Albers**\nKein Wort mehr davon."))
	})
	h.drive(h.press("R"))
	if current, others := h.allMarks(); h.paneNotice() != "no match" || len(current)+len(others) != 0 {
		t.Fatalf("a reload without the hits: %q, marks %q %q", h.paneNotice(), current, others)
	}
}

// Another meeting in the pane has no find, nor the first one again: the
// find belonged to the pane's meeting. The find line closes when its
// meeting leaves the pane.
func TestAnotherMeetingInThePaneHasNoFind(t *testing.T) {
	t.Parallel()

	h := newFindHarness(t, false)
	h.find("märz")
	h.press("left")
	h.drive(h.press("down"))
	h.drive(h.press("right"))
	current, others := h.allMarks()
	if h.m.app.meeting.id != meetingID(website, 2) || h.m.app.meeting.find.query != "" || len(current)+len(others) != 0 || strings.Contains(h.paneNotice(), " of ") || h.offers(NextHit) {
		t.Fatalf("meeting %s: query %q, marks %q %q, notice %q", h.m.app.meeting.id, h.m.app.meeting.find.query, current, others, h.paneNotice())
	}
	h.press("left")
	h.drive(h.press("up"))
	h.drive(h.press("right"))
	if h.m.app.meeting.id != meetingID(website, 1) || h.m.app.meeting.find.query != "" {
		t.Fatalf("the first again: %s, query %q", h.m.app.meeting.id, h.m.app.meeting.find.query)
	}
	// The line open while the meeting's read answers that it is gone.
	gate := make(chan struct{})
	h.f.gate, h.f.gateMeeting = gate, true
	h.f.setMeeting(func(string) transport.Answer { return restNotFound() })
	reread := h.background(h.press("R"))
	h.press("/")
	if h.m.mode != FindEntry {
		t.Fatalf("mode %d", h.m.mode)
	}
	close(gate)
	h.drive(h.feed(h.receive(reread))...)
	if h.m.mode != Navigate || h.m.app.meetingInPane() {
		t.Fatalf("the line stayed open over a meeting gone: mode %d", h.m.mode)
	}
	if cmd := h.press("q"); cmd == nil {
		t.Fatal("q is still text")
	}
}

// While the find line is open no preview is read: the state is not the
// one a preview is read in, and a timer that falls due reads nothing.
func TestTheFindLineReadsNoPreview(t *testing.T) {
	t.Parallel()

	h := newFindHarness(t, false)
	if !h.m.meetingPreviewable() {
		t.Fatal("no preview in navigation: this test proves nothing then")
	}
	h.press("/")
	reads := len(h.f.meetingShows())
	h.m.app.meeting.cached = true
	h.drive(h.feed(gateway.Due{Session: h.m.app.session, Token: h.m.app.previewToken})...)
	if h.m.meetingPreviewable() || h.m.previewable() || len(h.f.meetingShows()) != reads {
		t.Fatalf("a preview in the find line: reads %d → %d", reads, len(h.f.meetingShows()))
	}
}

// Moving through many hits draws the current one anew and wraps nothing
// again: the text is wrapped once for the query.
func TestNThroughManyHitsWrapsNothingAgain(t *testing.T) {
	t.Parallel()

	h := newFindHarness(t, true)
	h.find("e")
	d := h.m.app.meeting
	_ = h.m.View()
	wraps := d.wraps
	for range 60 {
		h.press("n")
		_ = h.m.View()
	}
	h.press("N")
	_ = h.m.View()
	if d.wraps != wraps || !strings.HasPrefix(h.paneNotice(), "60 of ") {
		t.Fatalf("%d wraps → %d, %q", wraps, d.wraps, h.paneNotice())
	}
	if current, _ := h.inSight(); len(current) != 1 || current[0] != "e" {
		t.Fatalf("current %q", current)
	}
}

// A hit in a speaker's head is drawn as a hit (bold, view.styleOf) and the
// rest of the name keeps the head's tone.
func TestAHitInASpeakersHeadKeepsTheHead(t *testing.T) {
	t.Parallel()

	h := newFindHarness(t, true)
	h.find("albers")
	if h.paneNotice() != "1 of 20" {
		t.Fatalf("notice %q", h.paneNotice())
	}
	top, capacity := h.paneTop()
	c := h.m.objectColumn()
	for i := top; i < top+capacity; i++ {
		if c.Wrapped[i] != "Anna Albers" {
			continue
		}
		st := c.Styles[i]
		if st.StyleAt(0) != st.RestStyle || (st.RestStyle != present.Author && st.RestStyle != present.Own) || st.StyleAt(5) != present.MatchCurrent || st.StyleAt(10) != present.MatchCurrent {
			t.Fatalf("the head %+v", st)
		}
		return
	}
	t.Fatalf("no head in sight: %q", c.Wrapped[top:top+capacity])
}
