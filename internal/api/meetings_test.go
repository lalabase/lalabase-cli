package api

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/transport"
)

// K7a: a project's meetings over REST (the core's R8). Every answer is a
// recorded one of the contract.

const (
	meetingsGroup = "GET /api/v1/projects/:project_id/meetings"
	relaunch      = "00000000-0000-4000-8000-000000000031"
)

func TestTheMeetingListSendsTheQueryAndReadsTheRecordedPage(t *testing.T) {
	ctx := context.Background()
	api, sent := restRecorded(t, meetingsGroup, "ok")
	ans := api.MeetingsRESTAnswer(ctx, MeetingQuery{ProjectID: relaunch, Q: "März", Page: 2, PerPage: 25})
	if !ans.OK() {
		t.Fatalf("%v %s", ans.Class, ans.Message)
	}
	sentOnce(t, *sent, "/api/v1/projects/"+relaunch+"/meetings?page=2&per_page=25&q=M%C3%A4rz")
	page, decoded := DecodeMeetingsREST(ans)
	if !decoded.OK() || len(page.Data) != 2 || page.Meta.Total != 2 || page.Meta.Page != 1 {
		t.Fatalf("%v %+v", decoded.Class, page)
	}
	first, second := page.Data[0], page.Data[1]
	if first.Title != "Kickoff Relaunch" || first.OccurredAt != "2026-01-04T09:00:00Z" || first.DurationMinutes == nil || *first.DurationMinutes != 45 ||
		first.ProjectID != relaunch || first.Creator.Name == nil || *first.Creator.Name != "Anna Albers" {
		t.Fatalf("first %+v", first)
	}
	// A meeting without a duration is a meeting.
	if second.Title != "Kurze Abstimmung" || second.DurationMinutes != nil {
		t.Fatalf("second %+v", second)
	}
	if err := CheckMeetingPage(page); err != nil {
		t.Fatal(err)
	}
	// The payload is filed as it came.
	if string(decoded.Payload) != string(ans.Payload) {
		t.Fatal("the list's payload changed")
	}
	// A blank search and an unset page are not sent.
	api, sent = restRecorded(t, meetingsGroup, "ok")
	api.MeetingsRESTAnswer(ctx, MeetingQuery{ProjectID: relaunch})
	sentOnce(t, *sent, "/api/v1/projects/"+relaunch+"/meetings")
	// Two decodes of one answer share nothing; a failure decodes to nothing.
	a, _ := DecodeMeetingsREST(ans)
	b, _ := DecodeMeetingsREST(ans)
	a.Data[0].Title = "changed"
	*a.Data[0].DurationMinutes = 1
	if b.Data[0].Title == "changed" || *b.Data[0].DurationMinutes != 45 {
		t.Fatal("two decodes share a row")
	}
	if page, failed := DecodeMeetingsREST(transport.Answer{Class: transport.NotFound}); len(page.Data) != 0 || failed.Class != transport.NotFound {
		t.Fatalf("%+v %v", page, failed.Class)
	}
}

// Every recorded case reads as the client needs it: the successes as pages
// (a creator gone has no name, a reader without the meetings area an empty
// page), the refusals by their class.
func TestEveryRecordedMeetingListReads(t *testing.T) {
	ctx := context.Background()
	pages := map[string]struct{ rows, total int }{
		"ok": {2, 2}, "ok_search": {1, 1}, "ok_page": {1, 2}, "ok_creator_gone": {2, 2}, "empty": {0, 0}, "empty_no_area": {0, 0},
	}
	refusals := map[string]transport.Class{
		"invalid_input_page": transport.InvalidInput, "invalid_input_per_page": transport.InvalidInput,
		"not_found": transport.NotFound, "not_found_reach": transport.NotFound, "unauthorized": transport.Unauthenticated,
	}
	for name, want := range pages {
		api, _ := restRecorded(t, meetingsGroup, name)
		page, ans := DecodeMeetingsREST(api.MeetingsRESTAnswer(ctx, MeetingQuery{ProjectID: relaunch}))
		if !ans.OK() || len(page.Data) != want.rows || page.Meta.Total != want.total || page.Data == nil {
			t.Errorf("%s: %v %q %+v", name, ans.Class, ans.Message, page)
			continue
		}
		if err := CheckMeetingPage(page); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if missing, err := MeetingsRESTSpec.Missing(recordedREST(t, meetingsGroup, name)); err != nil || len(missing) > 0 {
			t.Errorf("%s lacks %v (%v)", name, missing, err)
		}
	}
	api, _ := restRecorded(t, meetingsGroup, "ok_creator_gone")
	page, _ := DecodeMeetingsREST(api.MeetingsRESTAnswer(ctx, MeetingQuery{ProjectID: relaunch}))
	if gone := page.Data[1]; gone.Creator.ID == "" || gone.Creator.Name != nil {
		t.Errorf("the creator gone: %+v", gone.Creator)
	}
	for name, class := range refusals {
		api, _ := restRecorded(t, meetingsGroup, name)
		page, ans := DecodeMeetingsREST(api.MeetingsRESTAnswer(ctx, MeetingQuery{ProjectID: relaunch}))
		if ans.Class != class || len(page.Data) != 0 {
			t.Errorf("%s: %v %+v", name, ans.Class, page)
		}
	}
}

// A field the client relies on, missing or null where it may not be, fails
// the answer; a project id that is no path segment is refused before
// anything is sent.
func TestAMeetingListThatIsNotOneIsRefused(t *testing.T) {
	for _, c := range []struct {
		name, field string
		change      func(body map[string]any)
	}{
		{"no title", "data[0].title", func(b map[string]any) { firstRow(b)["title"] = nil }},
		{"no date", "data[0].occurred_at", func(b map[string]any) { delete(firstRow(b), "occurred_at") }},
		{"no creator", "data[0].creator", func(b map[string]any) { firstRow(b)["creator"] = nil }},
		{"no creator id", "data[0].creator.id", func(b map[string]any) { delete(firstRow(b)["creator"].(map[string]any), "id") }},
		{"no total", "meta.total", func(b map[string]any) { delete(b["meta"].(map[string]any), "total") }},
	} {
		var body map[string]any
		if err := json.Unmarshal(recordedREST(t, meetingsGroup, "ok"), &body); err != nil {
			t.Fatal(err)
		}
		c.change(body)
		payload, _ := json.Marshal(body)
		_, ans := DecodeMeetingsREST(transport.Answer{Class: transport.Object, Payload: payload, Status: 200, Sent: true})
		if ans.Class != transport.Protocol || !strings.Contains(ans.Message, c.field) || !strings.HasPrefix(ans.Message, MeetingsRESTSpec.Name+": ") {
			t.Errorf("%s: %v %q", c.name, ans.Class, ans.Message)
		}
	}
	for _, id := range []string{"", ".", "..", "a/b", `a\b`} {
		api, sent := restRecorded(t, meetingsGroup, "ok")
		if ans := api.MeetingsRESTAnswer(context.Background(), MeetingQuery{ProjectID: id}); ans.Class != transport.InvalidInput || len(*sent) != 0 {
			t.Errorf("project %q: %v, sent %q", id, ans.Class, *sent)
		}
	}
}

// What the spec cannot say: a page within its own counts, ids that come
// once, no duration below zero.
func TestAMeetingPageAgainstItsOwnCounts(t *testing.T) {
	minus := -1
	row := func(id string) MeetingRow { return MeetingRow{ID: id, Title: "t", OccurredAt: "2026-01-04T09:00:00Z"} }
	good := MeetingPage{Data: []MeetingRow{row("a"), row("b")}}
	good.Meta.Page, good.Meta.PerPage, good.Meta.Total = 1, 2, 2
	if err := CheckMeetingPage(good); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(p *MeetingPage){
		"page 0":           func(p *MeetingPage) { p.Meta.Page = 0 },
		"per page 0":       func(p *MeetingPage) { p.Meta.PerPage = 0 },
		"negative total":   func(p *MeetingPage) { p.Meta.Total = -1 },
		"over per page":    func(p *MeetingPage) { p.Meta.PerPage = 1 },
		"over the total":   func(p *MeetingPage) { p.Meta.Total = 1 },
		"an id twice":      func(p *MeetingPage) { p.Data[1].ID = "a" },
		"an empty id":      func(p *MeetingPage) { p.Data[1].ID = "" },
		"a duration below": func(p *MeetingPage) { p.Data[1].DurationMinutes = &minus },
	} {
		p := good
		p.Data = slices.Clone(good.Data)
		change(&p)
		if err := CheckMeetingPage(p); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// The spec is one of the adapters, a REST read of the recorded route.
func TestTheMeetingListSpecIsARESTRead(t *testing.T) {
	if !slices.ContainsFunc(Specs, func(s Spec) bool { return s.Name == MeetingsRESTSpec.Name }) {
		t.Error("the meeting list is not among Specs")
	}
	if MeetingsRESTSpec.Name != meetingsGroup || MeetingsRESTSpec.Tool != "" || MeetingsRESTSpec.Method != "" || !strings.HasPrefix(MeetingsRESTSpec.Name, "GET "+MeetingsRESTSpec.Path) {
		t.Errorf("no REST read of the recorded route: %+v", MeetingsRESTSpec)
	}
}

// firstRow is the first row of a decoded list body.
func firstRow(body map[string]any) map[string]any {
	return body["data"].([]any)[0].(map[string]any)
}
