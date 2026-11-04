package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/cache"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/queue"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
	"github.com/gboyke/lalabase-cli/internal/uitext"
)

// The meeting in the object pane (K7b): read whole when Enter or the
// arrow brings the focus to it, previewed after the pause otherwise. Its
// summary, notes and transcript are sections, one shown at a time and
// Tab to the next; a section with nothing in it is left out. Read only:
// no key acts on a meeting. Like the list, it stays in memory alone
// (api.MeetingRESTSpec is MemoryOnly), and the disk is never asked.
//
// A transcript can be large (the core keeps up to 1 MB): the pane wraps
// its text once per width and section, and hands the view the lines
// wrapped (present.Column.Wrapped), so a frame draws the lines in sight
// alone.

// meetingArea is a section of the meeting.
type meetingArea uint8

const (
	meetingSummary meetingArea = iota
	meetingNotes
	meetingTranscript
	meetingAreas
)

// meetingDetail is the meeting in the pane.
type meetingDetail struct {
	id, project string
	key         cache.Key
	record      api.Meeting
	// gen counts the records shown: a new one is wrapped again.
	gen uint64
	status
	// requested belongs to this selection: a stale cache entry alone is
	// not a failed read of the meeting now in the pane.
	requested bool
	offset    int
	area      meetingArea
	wrap      meetingWrap
	// wraps counts the wraps of the text, for the tests of the cache.
	wraps int
}

// meetingWrap is the pane's text wrapped once: what it was wrapped from
// and at, and the lines.
type meetingWrap struct {
	key    meetingWrapKey
	lines  []string
	styles []present.LineStyle
}

type meetingWrapKey struct {
	width  int
	method ansi.Method
	gen    uint64
	area   meetingArea
	head   string
}

func (a *app) meetingOn() bool { return a.on("meeting") }

// meetingKey is the read of one meeting of a project.
func (a *app) meetingKey(project, id string) cache.Key {
	k := a.ns.Key(api.MeetingRESTSpec.Name)
	k.Project, k.Ticket = project, id
	return k
}

// holds: the record came from the server and nothing failed since.
func (d *meetingDetail) holds() bool {
	return d.known && !d.cached && !d.stale && d.err.String() == ""
}

// texts are the meeting's sections, "" for one with nothing in it.
func (d *meetingDetail) texts() [meetingAreas]string {
	of := func(s *string) string {
		if s == nil || strings.TrimSpace(*s) == "" {
			return ""
		}
		return *s
	}
	r := d.record
	return [meetingAreas]string{of(r.Summary), of(r.Notes), of(r.Transcript)}
}

// areas are the sections with something in them, in their order.
func (d *meetingDetail) areas() []meetingArea {
	var out []meetingArea
	for i, t := range d.texts() {
		if t != "" {
			out = append(out, meetingArea(i))
		}
	}
	return out
}

// settleArea keeps the section shown one that has something in it.
func (d *meetingDetail) settleArea() {
	areas := d.areas()
	for _, a := range areas {
		if a == d.area {
			return
		}
	}
	if len(areas) > 0 {
		d.area = areas[0]
	}
}

// --- the preview and the open ------------------------------------------------

// meetingSelectionChanged follows what may have changed the meeting
// selection, the focus or the panes shown (a resize too): the pane shows
// the selected meeting from memory at once, if it is there. With the
// focus on the pane a meeting not yet requested for this selection is
// read now, a pending preview joined as the person's read, once; else it
// is read after the pause. After a read completes, a resize or an arrow
// does not retry it: R or an explicit open can retry a failure.
func (m Model) meetingSelectionChanged() tea.Cmd {
	a := m.app
	v := a.meetings
	if v == nil || !a.meetingOn() {
		return nil
	}
	if d := a.meeting; d != nil && d.id != v.selected {
		a.dropMeeting()
	}
	if v.index(v.selected) < 0 {
		return nil
	}
	if a.meeting == nil {
		a.showCachedMeeting(v.project, v.selected)
	}
	if m.focus != view.Object {
		return m.scheduleMeetingPreview()
	}
	if d := a.meeting; d != nil && (a.personWaits(d.key) || d.requested && !d.loading) {
		return nil
	}
	return a.readMeetingUnlessFresh(v.project, v.selected)
}

// personWaits: a read of the key is out as the person's.
func (a *app) personWaits(k cache.Key) bool {
	for _, w := range a.waiters {
		if !w.canceled && w.ticket.Key == k && w.ticket.Priority == queue.User {
			return true
		}
	}
	return false
}

// scheduleMeetingPreview starts the timer for the preview of the selected
// row, under the rules of the ticket's (schedulePreview): only with the
// pane in sight and nothing of the row held yet but a record awaiting its
// one reload.
func (m Model) scheduleMeetingPreview() tea.Cmd {
	a := m.app
	if !m.meetingPreviewable() || a.deps.Events == nil {
		return nil
	}
	if d := a.meeting; d != nil && (d.loading || !d.awaitingReload()) {
		return nil
	}
	a.previewToken++
	return a.deps.Events.Timer(previewDelay, a.previewToken)
}

// meetingPreviewable: the state in which a meeting's preview may be read.
func (m Model) meetingPreviewable() bool {
	a := m.app
	v := a.meetings
	return v != nil && !a.expired && m.mode == Navigate && v.index(v.selected) >= 0 && a.meetingOn() && m.objectVisible()
}

// meetingDue is the wake-up of a preview timer in the meetings section.
func (m Model) meetingDue(msg gateway.Due) (Model, tea.Cmd) {
	a := m.app
	if a.expired || msg.Session != a.session || msg.Token != a.previewToken || !m.meetingPreviewable() {
		return m, nil
	}
	v := a.meetings
	if d := a.meeting; d != nil && d.id == v.selected && (d.loading || !d.awaitingReload()) {
		return m, nil
	}
	d := a.ensureMeeting(v.project, v.selected)
	d.loading, d.requested = true, true
	return m, a.issue(gateway.Meeting, d.key, forMeeting, queue.Preview)
}

// openMeeting is Enter on a meeting row: the focus moves to the pane, and
// the meeting is read as the person's unless the pane holds it fresh.
func (m Model) openMeeting() (Model, tea.Cmd) {
	a := m.app
	v := a.meetings
	if v.index(v.selected) < 0 || !a.meetingOn() {
		return m, nil
	}
	m.focus = view.Object
	d := a.ensureMeeting(v.project, v.selected)
	d.offset = 0
	return m, a.readMeetingUnlessFresh(v.project, v.selected)
}

// readMeetingUnlessFresh reads the meeting as the person's unless the
// pane holds it fresh. A pending preview under the same key is joined:
// the queue shares the request and raises it to the person's priority,
// so the preview's budget never holds what the person opened.
func (a *app) readMeetingUnlessFresh(project, id string) tea.Cmd {
	d := a.ensureMeeting(project, id)
	if d.holds() && !d.loading {
		return nil
	}
	d.loading, d.requested = true, true
	return a.issue(gateway.Meeting, d.key, forMeeting, queue.User)
}

// ensureMeeting makes the pane hold the meeting: the one there, the one
// in memory, or a new one with nothing known yet. The disk is never asked.
func (a *app) ensureMeeting(project, id string) *meetingDetail {
	if d := a.meeting; d != nil && d.id == id {
		return d
	}
	a.dropMeeting()
	if !a.showCachedMeeting(project, id) {
		a.meeting = &meetingDetail{id: id, project: project, key: a.meetingKey(project, id)}
	}
	return a.meeting
}

// showCachedMeeting puts the meeting into the pane when the memory holds
// it and it still reads, and says whether it did.
func (a *app) showCachedMeeting(project, id string) bool {
	k := a.meetingKey(project, id)
	e, ok := a.store.Get(k)
	if !ok {
		return false
	}
	record, ans := api.DecodeMeetingREST(transport.Answer{Class: transport.Object, Payload: e.Payload, Status: 200, Sent: true})
	if !ans.OK() || gateway.CheckMeeting(k, record) != nil {
		a.forget(k)
		return false
	}
	d := &meetingDetail{id: id, project: project, key: k}
	d.show(record)
	d.fromCache(e)
	a.meeting = d
	return true
}

// show puts a record into the pane, to be wrapped again.
func (d *meetingDetail) show(record api.Meeting) {
	d.record = record
	d.gen++
	d.settleArea()
}

// dropMeeting empties the pane, canceling its read.
func (a *app) dropMeeting() {
	if a.meeting != nil {
		a.cancelArea(gateway.Meeting)
		a.meeting = nil
	}
}

// nextMeetingArea is Tab on the meeting: the next section with something
// in it, from its top.
func (m Model) nextMeetingArea() Model {
	d := m.app.meeting
	areas := d.areas()
	for i, a := range areas {
		if a == d.area {
			d.area = areas[(i+1)%len(areas)]
			break
		}
	}
	d.offset = 0
	return m
}

// meetingInPane: the pane shows the selected meeting's record.
func (a *app) meetingInPane() bool {
	return a.meetingSelected() && a.meeting.known
}

// meetingSelected: the pane holds the selected meeting, read or not.
func (a *app) meetingSelected() bool {
	d, v := a.meeting, a.meetings
	return d != nil && v != nil && d.id == v.selected
}

// --- results -----------------------------------------------------------------

// receiveMeeting applies a meeting through the one way any result takes.
// A refusal or a removal leaves the list as it is: only the pane says so.
func (m Model) receiveMeeting(r gateway.MeetingResult) (Model, tea.Cmd) {
	a := m.app
	dec, ok := m.receive(r)
	if !ok {
		return m, nil
	}
	d := a.meeting
	if d == nil || d.key != r.Key {
		return m, nil
	}
	switch dec {
	case cache.Replace:
		d.show(r.Meeting)
		d.fresh(a.now())
	case cache.Remove:
		d.removed()
		d.record, d.offset = api.Meeting{}, 0
		d.gen++
	default:
		d.failed(dec, r.Answer)
	}
	return m, nil
}

// --- the view ----------------------------------------------------------------

// meetingColumn is the object pane in the meetings section: the selected
// meeting once read, its facts in the bar where it fits, else in its head;
// before that, and without the meeting feature, the row's facts.
func (m Model) meetingColumn() present.Column {
	a := m.app
	if !a.meetingInPane() {
		c := m.meetingFactsColumn()
		if d, v := a.meeting, a.meetings; d != nil && v != nil && d.id == v.selected {
			c.Notice = d.notice(uitext.NoItem, false)
		}
		return c
	}
	d := a.meeting
	c := present.Column{Title: uitext.Inline(uitext.Meeting), Cursor: -1, Notice: d.notice(uitext.NoItem, false)}
	g := layoutWith(m.width, m.height, 1, m.focus)
	meta := view.MetaFits(view.InnerWidth(g, view.Object))
	if meta {
		c.Meta = a.meetingBar(d)
	}
	c.Wrapped, c.Styles = a.meetingLines(d, c, view.TextWidth(c, g, view.Object), meta, m.widthMethod)
	c.Offset = d.offset
	return c
}

// meetingLines is the pane's text wrapped at width: from the wrap before
// when the width, the record, the section and the head are the same, else
// wrapped once more. The head is small and built for every frame; the
// section's text is laid out (meetingBody, which cleans it first) and
// wrapped only here.
func (a *app) meetingLines(d *meetingDetail, c present.Column, width int, meta bool, method ansi.Method) ([]string, []present.LineStyle) {
	head, headStyles := a.meetingHead(d, meta)
	key := meetingWrapKey{width: width, method: method, gen: d.gen, area: d.area, head: strings.Join(head, "\n")}
	if d.wrap.lines != nil && d.wrap.key == key {
		return d.wrap.lines, d.wrap.styles
	}
	lines, styles := head, headStyles
	if text := d.texts()[d.area]; text != "" {
		body, bodyStyles := meetingBody(text, meetingLayout{me: a.ownName(), speakers: namedParticipants(d.record), transcript: d.area == meetingTranscript})
		lines, styles = append(lines, body...), append(styles, bodyStyles...)
	} else {
		lines, styles = append(lines, uitext.Inline(uitext.NothingWritten).String()), append(styles, plainLine)
	}
	// Cleaned again with the head, as the ticket's text is; the cleaning
	// keeps clean text as it is, so the lines and styles stay in step.
	c.Text, c.Styles = present.NewBody(strings.Join(lines, "\n")), styles
	d.wrap.key = key
	d.wrap.lines, d.wrap.styles = view.Wrap(c, width, method)
	d.wraps++
	return d.wrap.lines, d.wrap.styles
}

// ownName is the person's own name, cleaned, "" for none: a speaker of
// that name is shown in the person's tone.
func (a *app) ownName() string {
	return present.NewInline(a.me.User.DisplayName).String()
}

// meetingHead is the head of the pane, cleaned: the title, the facts when
// the bar has no room for them, then the line of the sections
// (meetingTabs).
func (a *app) meetingHead(d *meetingDetail, meta bool) ([]string, []present.LineStyle) {
	r := d.record
	t := ticketLines{}
	t.addLead(textOr(r.Title, uitext.UntitledMeeting), present.Strong, "", present.Strong)
	if !meta {
		facts := []string{a.when(r.OccurredAt)}
		if dur := durationOf(r.MeetingRow); dur != "" {
			facts = append(facts, dur)
		}
		facts = append(facts, textOr(a.meetings.projectName, uitext.UnnamedProject), createdBy(r.MeetingRow))
		t.add(present.LineStyle{RestStyle: present.Faint}, strings.Join(facts, " · "))
		if len(r.Participants) > 0 {
			t.add(present.LineStyle{RestStyle: present.Faint}, uitext.Fill(uitext.MetaParticipants, strings.Join(participantsOf(r), ", ")).String())
		}
	}
	t.add(plainLine, "")
	if len(d.areas()) > 0 {
		line, style := meetingTabs(d)
		t.add(style, line)
	}
	return t.lines, t.styles
}

// meetingTabs is the line of the sections (K7d): all three in their order,
// each in a run of the same width, so Tab moves the mark and not the
// names. The one shown is a tab, marked in text too; one with nothing in
// it is faint, and Tab leaves it out.
func meetingTabs(d *meetingDetail) (string, present.LineStyle) {
	names := [meetingAreas]uitext.ID{uitext.MeetingSummary, uitext.MeetingNotes, uitext.MeetingTranscript}
	texts := d.texts()
	mark := uitext.Inline(uitext.ActiveArea).String()
	var b strings.Builder
	st := present.LineStyle{Rule: true}
	for i, id := range names {
		run, style := strings.Repeat(" ", len([]rune(mark))), present.Plain
		switch {
		case meetingArea(i) == d.area:
			run, style = mark, present.Tab
		case texts[i] == "":
			style = present.Faint
		}
		run += uitext.Inline(id).String() + " "
		if style != present.Plain {
			st.Spans = append(st.Spans, present.Span{From: b.Len(), To: b.Len() + len(run), Style: style})
		}
		b.WriteString(run)
	}
	return b.String(), st
}

// meetingBar is the meeting's facts for the bar on the pane's right: its
// date, duration, project, creator and participants. No keys: nothing
// acts on a meeting.
func (a *app) meetingBar(d *meetingDetail) []present.MetaLine {
	r := d.record
	label := func(id uitext.ID) present.MetaLine {
		return present.MetaLine{Text: uitext.Inline(id), Style: present.Faint}
	}
	value := func(text string) present.MetaLine { return present.MetaLine{Text: present.NewInline(text)} }
	duration := durationOf(r.MeetingRow)
	if duration == "" {
		duration = uitext.Inline(uitext.NoDuration).String()
	}
	bar := []present.MetaLine{
		label(uitext.BarDate), value(a.when(r.OccurredAt)), {},
		label(uitext.BarDuration), value(duration), {},
		label(uitext.BarProject), value(textOr(a.meetings.projectName, uitext.UnnamedProject)), {},
		label(uitext.BarPeople), value(createdBy(r.MeetingRow)),
	}
	if len(r.Participants) > 0 {
		bar = append(bar, present.MetaLine{}, label(uitext.BarParticipants))
		for _, p := range participantsOf(r) {
			bar = append(bar, value(p))
		}
	}
	return bar
}

// createdBy names the meeting's creator, "Creator unknown" for one who is
// gone.
func createdBy(r api.MeetingRow) string {
	if r.Creator.Name == nil {
		return creatorOf(r)
	}
	return uitext.Fill(uitext.MetaCreator, creatorOf(r)).String()
}

// namedParticipants are the names of the participants who have one,
// cleaned: who may speak in the transcript.
func namedParticipants(r api.Meeting) []string {
	var out []string
	for _, p := range r.Participants {
		if p.DisplayName != nil {
			if name := present.NewInline(*p.DisplayName).String(); name != "" {
				out = append(out, name)
			}
		}
	}
	return out
}

// participantsOf are the names of the meeting's participants, cleaned; a
// participant without one is named so.
func participantsOf(r api.Meeting) []string {
	out := make([]string, 0, len(r.Participants))
	for _, p := range r.Participants {
		if p.DisplayName == nil {
			out = append(out, uitext.Inline(uitext.UnknownParticipant).String())
			continue
		}
		out = append(out, textOr(*p.DisplayName, uitext.UnknownParticipant))
	}
	return out
}
