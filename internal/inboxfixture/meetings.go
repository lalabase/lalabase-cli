package inboxfixture

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// A project's meetings over REST (K7a, the core's R8), in the shape the
// core sends them, newest first. Website has 30 (two pages of 25): #2
// without a duration, #3 by a creator who is gone, #4 with a title that
// tries to reach the terminal. Intern has none; Handbuch is a project
// whose reader lacks the meetings area (an empty list, as the core
// answers); Archiv is out of reach (404). A fixture before R1 lists no
// meeting capability and has no route here.
//
// One meeting, whole (K7b): Website #1 has a summary, notes and a
// transcript of a few screens with its speakers as the recorder writes them, #2 notes alone, #3 nothing written, #4 a
// transcript that tries to reach the terminal, #5 is gone (404), #6 has a
// transcript of 500 KB and #7 one above the client's bound of 8 MiB.

// MeetingCount is the number of meetings of a project.
var MeetingCount = map[string]int{ProjectWebsite: 30, ProjectIntern: 0, ProjectHandbuch: 0}

// HostileMeetingTitle is the title of Website's meeting #4: a colour, a
// title change and a right-to-left override.
const HostileMeetingTitle = "Plan \x1b[31mred\x1b[0m \x1b]0;owned\x07\u202eevil"

// MeetingID is the id of meeting n of a project.
func MeetingID(project string, n int) string {
	for i, p := range projectOrder {
		if p == project {
			return fmt.Sprintf("00000000-0000-4000-8000-%012d", 800000+1000*i+n)
		}
	}
	return ""
}

// meetingRow is meeting n of a project: n days before the 31st of January
// 2026, so the list is newest first.
func meetingRow(project string, n int) map[string]any {
	title := fmt.Sprintf("Meeting %d", n)
	if project == ProjectWebsite && n == 4 {
		title = HostileMeetingTitle
	}
	var duration any = 30 + n
	creator := map[string]any{"id": "00000000-0000-4000-8000-000000000011", "name": "Anna Albers"}
	switch n {
	case 2:
		duration = nil
	case 3:
		creator = map[string]any{"id": "00000000-0000-4000-8000-000000000107", "name": nil}
	}
	return map[string]any{
		"id": MeetingID(project, n), "project_id": project, "title": title,
		"occurred_at":      time.Date(2026, 1, 31-n, 9, 0, 0, 0, time.UTC).Format(time.RFC3339),
		"duration_minutes": duration, "creator": creator,
	}
}

// restMeetings is GET /api/v1/projects/:project_id/meetings: the paging
// checked before the project is looked up, as the core does; the search
// on the titles.
func (f *Fixture) restMeetings(w http.ResponseWriter, r *http.Request, req Request, project string) {
	q := r.URL.Query()
	page, perPage := 1, 10
	for name, v := range map[string]*int{"page": &page, "per_page": &perPage} {
		if raw := q.Get(name); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 || name == "per_page" && n > 100 {
				restError(w, req, f, http.StatusUnprocessableEntity, "invalid_input", name+" must be a whole number from 1 to 100")
				return
			}
			*v = n
		}
	}
	count, ok := MeetingCount[project]
	if !ok {
		restError(w, req, f, http.StatusNotFound, "not_found", "")
		return
	}
	rows := []map[string]any{}
	for n := 1; n <= count; n++ {
		row := meetingRow(project, n)
		if s := q.Get("q"); s == "" || strings.Contains(strings.ToLower(row["title"].(string)), strings.ToLower(s)) {
			rows = append(rows, row)
		}
	}
	from := min(len(rows), (page-1)*perPage)
	restObject(w, req, f, map[string]any{
		"data": rows[from:min(len(rows), from+perPage)],
		"meta": map[string]any{"page": page, "per_page": perPage, "total": len(rows)},
	})
}

// HostileTranscript is the transcript of Website's meeting #4: a copy to
// the clipboard, a cleared screen and a right-to-left override.
const HostileTranscript = "Anna: \x1b]52;c;cGF3bmVk\x07copied \x1b[2Jcleared \u202egone"

// Sizes of the long transcripts of Website's #6 and #7.
const (
	LongTranscriptBytes  = 500 << 10
	HugeTranscriptBytes  = 9 << 20
	meetingTranscriptRow = "Anna: Wir starten im März mit allen Seiten, dann die Texte, dann das Logo.\n"
)

// speakersOf is a transcript of at least n bytes as the core's recorder
// writes it: each turn a line "**Name**" with the words below it.
func speakersOf(n int) string {
	turn := "**Anna Albers**\nWir starten im März mit allen Seiten, dann die Texte.\n\n**Erika Beispiel**\nGut, dann das Logo zuletzt.\n\n"
	return strings.Repeat(turn, n/len(turn)+1) + "**Anna Albers**\nEnde."
}

// transcriptOf is a transcript of at least n bytes, its last line "Ende.".
func transcriptOf(n int) string {
	return strings.Repeat(meetingTranscriptRow, n/len(meetingTranscriptRow)+1) + "Ende."
}

// restMeeting is GET /api/v1/meetings/:id: a listed meeting of Website,
// whole; any other is out of reach, as the core answers every failure.
func (f *Fixture) restMeeting(w http.ResponseWriter, req Request, id string) {
	n := 0
	for i := 1; i <= MeetingCount[ProjectWebsite]; i++ {
		if MeetingID(ProjectWebsite, i) == id {
			n = i
		}
	}
	if n == 0 || n == 5 {
		restError(w, req, f, http.StatusNotFound, "not_found", "")
		return
	}
	var summary, notes, transcript any = "", nil, nil
	switch n {
	case 1:
		summary, notes = "**Ergebnis:** Start im März.\n\n- Seiten-Vorlagen\n- Logo", "Termin im März halten."
		transcript = speakersOf(4 << 10)
	case 2:
		notes = "Nur Notizen."
	case 4:
		transcript = HostileTranscript
	case 6:
		transcript = transcriptOf(LongTranscriptBytes)
	case 7:
		transcript = transcriptOf(HugeTranscriptBytes)
	default:
		summary, transcript = fmt.Sprintf("Zusammenfassung von Meeting %d.", n), fmt.Sprintf("Anna: Meeting %d.", n)
	}
	m := meetingRow(ProjectWebsite, n)
	m["participants"] = []map[string]any{{"display_name": "Anna Albers"}, {"display_name": "Erika Beispiel"}}
	m["generated_summary"], m["notes"], m["transcript"] = summary, notes, transcript
	m["updated_at"] = m["occurred_at"]
	m["path"] = "/org/" + OrgA + "/projects/" + ProjectWebsite + "/meetings/" + id
	restObject(w, req, f, map[string]any{"data": m})
}
