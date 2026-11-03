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
