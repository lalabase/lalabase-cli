package inboxfixture

import (
	"context"
	"strings"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/contract"
	"github.com/gboyke/lalabase-cli/internal/features"
	"github.com/gboyke/lalabase-cli/internal/transport"
)

// K7a: the fixture serves a project's meetings as the core's R8 does, and
// every answer passes the client's own adapter and checks; a fixture
// before R1 lists no meetings and has no route for them.
func TestTheMeetingListWorksAgainstTheFixture(t *testing.T) {
	_, a, c := serve(t, Normal)
	ctx := context.Background()
	if report := reportOf(t, a, c); report.Via["meetings"] != features.REST {
		t.Fatalf("meetings via %v (missing %v)", report.Via["meetings"], report.Missing["meetings"])
	}
	list := func(q api.MeetingQuery) api.MeetingPage {
		t.Helper()
		page, ans := api.DecodeMeetingsREST(a.MeetingsRESTAnswer(ctx, q))
		if !ans.OK() || api.CheckMeetingPage(page) != nil {
			t.Fatalf("%+v: %v %q", q, ans.Class, ans.Message)
		}
		for _, row := range page.Data {
			if row.ProjectID != q.ProjectID {
				t.Fatalf("a meeting of another project: %+v", row)
			}
		}
		return page
	}
	first := list(api.MeetingQuery{ProjectID: ProjectWebsite, Page: 1, PerPage: 25})
	if len(first.Data) != 25 || first.Meta.Total != 30 || first.Data[0].ID != MeetingID(ProjectWebsite, 1) {
		t.Fatalf("page 1: %+v", first.Meta)
	}
	if first.Data[1].DurationMinutes != nil || first.Data[2].Creator.Name != nil || first.Data[3].Title != HostileMeetingTitle {
		t.Fatalf("the special rows: %+v", first.Data[:4])
	}
	if first.Data[0].OccurredAt <= first.Data[1].OccurredAt {
		t.Fatal("not newest first")
	}
	if p := list(api.MeetingQuery{ProjectID: ProjectWebsite, Page: 2, PerPage: 25}); len(p.Data) != 5 || p.Data[0].ID != MeetingID(ProjectWebsite, 26) {
		t.Fatalf("page 2: %+v", p.Meta)
	}
	if p := list(api.MeetingQuery{ProjectID: ProjectWebsite, Q: "meeting 1", Page: 1, PerPage: 25}); p.Meta.Total != 11 {
		t.Fatalf("search: %+v", p.Meta)
	}
	for _, project := range []string{ProjectIntern, ProjectHandbuch} {
		if p := list(api.MeetingQuery{ProjectID: project}); len(p.Data) != 0 || p.Meta.Total != 0 {
			t.Fatalf("%s: %+v", project, p)
		}
	}
	for q, class := range map[api.MeetingQuery]transport.Class{
		{ProjectID: ProjectArchiv}:                          transport.NotFound,
		{ProjectID: ProjectWebsite, PerPage: 101}:           transport.InvalidInput,
		{ProjectID: "00000000-0000-4000-8000-000000000999"}: transport.NotFound,
	} {
		if _, ans := api.DecodeMeetingsREST(a.MeetingsRESTAnswer(ctx, q)); ans.Class != class {
			t.Errorf("%+v: %v %q", q, ans.Class, ans.Message)
		}
	}
	// Before R1: no capability, no route.
	before, err := New(Normal, contract.Raw(), BeforeR1())
	if err != nil {
		t.Fatal(err)
	}
	old, oc := clientOf(t, before)
	if report := reportOf(t, old, oc); report.Enabled["meetings"] {
		t.Fatal("a fixture before R1 lists the meetings")
	}
	if _, ans := api.DecodeMeetingsREST(old.MeetingsRESTAnswer(ctx, api.MeetingQuery{ProjectID: ProjectWebsite})); ans.Class != transport.NotFound || strings.Contains(ans.Message, "Meeting") {
		t.Fatalf("before R1: %v %q", ans.Class, ans.Message)
	}
}

// K7b: one meeting, whole, as the core's R8 answers it, through the
// client's own adapter and checks; the transcript above the client's bound
// is refused by the transport, never cut.
func TestOneMeetingWorksAgainstTheFixture(t *testing.T) {
	_, a, c := serve(t, Normal)
	ctx := context.Background()
	if report := reportOf(t, a, c); report.Via["meeting"] != features.REST {
		t.Fatalf("meeting via %v (missing %v)", report.Via["meeting"], report.Missing["meeting"])
	}
	show := func(n int) (api.Meeting, transport.Answer) {
		t.Helper()
		m, ans := api.DecodeMeetingREST(a.MeetingRESTAnswer(ctx, MeetingID(ProjectWebsite, n)))
		if ans.OK() && (api.CheckMeeting(m) != nil || m.ID != MeetingID(ProjectWebsite, n) || m.ProjectID != ProjectWebsite) {
			t.Fatalf("#%d is not itself: %+v", n, m.MeetingRow)
		}
		return m, ans
	}
	if m, ans := show(1); !ans.OK() || *m.Summary == "" || *m.Notes == "" || !strings.HasSuffix(*m.Transcript, "Ende.") || len(m.Participants) != 2 {
		t.Fatalf("#1: %v %+v", ans.Class, m)
	}
	if m, ans := show(2); !ans.OK() || *m.Summary != "" || m.Transcript != nil || *m.Notes != "Nur Notizen." {
		t.Fatalf("#2: %v %+v", ans.Class, m)
	}
	if m, ans := show(4); !ans.OK() || *m.Transcript != HostileTranscript {
		t.Fatalf("#4: %v", ans.Class)
	}
	if _, ans := show(5); ans.Class != transport.NotFound {
		t.Fatalf("#5: %v", ans.Class)
	}
	if m, ans := show(6); !ans.OK() || len(*m.Transcript) < LongTranscriptBytes {
		t.Fatalf("#6: %v", ans.Class)
	}
	if _, ans := show(7); ans.Class != transport.Protocol || !strings.Contains(ans.Message, "larger than") {
		t.Fatalf("#7: %v %q", ans.Class, ans.Message)
	}
	if _, ans := api.DecodeMeetingREST(a.MeetingRESTAnswer(ctx, MeetingID(ProjectIntern, 1))); ans.Class != transport.NotFound {
		t.Fatalf("a meeting of no list: %v", ans.Class)
	}
}
