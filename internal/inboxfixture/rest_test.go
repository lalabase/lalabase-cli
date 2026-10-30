package inboxfixture

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/contract"
	"github.com/gboyke/lalabase-cli/internal/features"
	"github.com/gboyke/lalabase-cli/internal/transport"
)

// K1: the fixture serves the ticket view over REST, and its /me says so;
// every answer passes the client's own REST adapters and checks.

// clientOf serves a fixture and returns the client's adapters on it.
func clientOf(t *testing.T, f *Fixture) (*api.API, *transport.Client) {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	c, err := transport.New(srv.URL, Token)
	if err != nil {
		t.Fatal(err)
	}
	return api.New(c), c
}

// reportOf runs the start's three reads and detects the features, as the
// client does.
func reportOf(t *testing.T, a *api.API, c *transport.Client) features.Report {
	t.Helper()
	ctx := context.Background()
	me, ans := a.Me(ctx)
	if !ans.OK() {
		t.Fatalf("/me: %v", ans.Class)
	}
	info, _ := c.Initialize(ctx)
	tools, _ := c.ListTools(ctx)
	return features.Detect(info, tools, me.Capabilities)
}

var ticketView = []string{"tickets", "tickets_search", "ticket", "ticket_posts_paged", "ticket_full_text"}

func TestTheTicketViewWorksOverRESTAgainstTheFixture(t *testing.T) {
	f, a, c := serve(t, Normal)
	ctx := context.Background()
	report := reportOf(t, a, c)
	for _, name := range ticketView {
		if report.Via[name] != features.REST {
			t.Fatalf("%s via %v", name, report.Via[name])
		}
	}
	list := func(q api.TicketQuery) api.TicketPage {
		t.Helper()
		page, ans := api.DecodeTicketsREST(a.TicketsRESTAnswer(ctx, q))
		if !ans.OK() || api.CheckTicketPage(page) != nil {
			t.Fatalf("%+v: %v %q", q, ans.Class, ans.Message)
		}
		return page
	}
	if p := list(api.TicketQuery{ProjectID: ProjectWebsite, Page: 1, PerPage: 25}); len(p.Data) != 25 || p.Meta.Total != 30 || p.Data[0].ID != TicketLogin || len(p.Meta.AvailableStatuses) != 5 {
		t.Fatalf("page 1: %+v", p.Meta)
	}
	if p := list(api.TicketQuery{ProjectID: ProjectWebsite, Page: 2, PerPage: 25}); len(p.Data) != 5 || p.Data[0].Number != 26 {
		t.Fatalf("page 2: %+v", p.Meta)
	}
	if p := list(api.TicketQuery{ProjectID: ProjectWebsite, Q: "export", Page: 1, PerPage: 25}); len(p.Data) != 1 || p.Data[0].Number != 2 {
		t.Fatalf("search: %+v", p.Data)
	}
	paged := api.TicketOptions{PostsPage: 1, PostsPerPage: 25, Newest: true, AttachmentsPage: 1}
	ticket := func(id string, o api.TicketOptions) api.Ticket {
		t.Helper()
		record, ans := api.DecodeTicketREST(a.TicketRESTAnswer(ctx, id, o), o)
		if !ans.OK() || api.CheckTicket(record, o) != nil || record.ID != id {
			t.Fatalf("%s %+v: %v %q", id, o, ans.Class, ans.Message)
		}
		return record
	}
	if r := ticket(TicketLogin, paged); r.Number != 1 || len(r.Posts) != 2 || r.PostsOrder != "newest" || len(r.Attachments) != 2 {
		t.Fatalf("#1: %+v", r)
	}
	if r := ticket(TicketID(ProjectWebsite, 3), api.TicketOptions{PostsPage: 2, PostsPerPage: 25, Newest: true, AttachmentsPage: 1}); len(r.Posts) != 5 || r.PostsTotal != 30 || r.PostsPage != 2 {
		t.Fatalf("#3 page 2: %+v", r.Posts)
	}
	// A plain key is asked for the first page: the counts are there.
	if r := ticket(TicketID(ProjectWebsite, 3), api.TicketOptions{}); len(r.Posts) != 30 || r.PostsTotal != 30 || r.Posts[0].BodyText != "Comment 1" {
		t.Fatalf("#3 plain: %d comments, total %d", len(r.Posts), r.PostsTotal)
	}
	// REST cuts no text: #6 and #8 whole.
	six := ticket(TicketID(ProjectWebsite, 6), paged)
	if six.DescriptionText != SixDescriptionWhole || six.Posts[0].BodyText != "Six comment 30 begins and ends here." {
		t.Fatalf("#6: %q / %q", six.DescriptionText, six.Posts[0].BodyText)
	}
	eight := ticket(TicketID(ProjectWebsite, 8), paged)
	if !strings.HasSuffix(eight.DescriptionText, EightEnd) || len(eight.DescriptionText) != 9000 || eight.Posts[0].ID != EightPostID || !strings.HasSuffix(eight.Posts[0].BodyText, EightReply) {
		t.Fatalf("#8: %d characters, newest %q", len(eight.DescriptionText), eight.Posts[0].ID)
	}
	post, ans := api.DecodePostREST(a.PostRESTAnswer(ctx, TicketID(ProjectWebsite, 8), EightPostID))
	if !ans.OK() || post.ID != EightPostID || !strings.HasSuffix(post.BodyText, EightReply) {
		t.Fatalf("one comment: %v %q", ans.Class, ans.Message)
	}
	// Another project's ticket of the same number is another ticket.
	if r := ticket(TicketID(ProjectHandbuch, 2), paged); r.Project.ID != ProjectHandbuch || r.ID == TicketID(ProjectWebsite, 2) {
		t.Fatalf("Handbuch #2: %+v", r.Project)
	}
	if r, ans := a.TicketByNumberREST(ctx, ProjectWebsite, 6); !ans.OK() || r.ID != TicketID(ProjectWebsite, 6) {
		t.Fatalf("by number: %v %q", ans.Class, ans.Message)
	}
	// What is missing or refused, in the API's own form.
	if _, ans := api.DecodeTicketREST(a.TicketRESTAnswer(ctx, "00000000-0000-4000-8000-000000000999", paged), paged); ans.Class != transport.NotFound || ans.Code != "not_found" {
		t.Fatalf("a missing ticket: %v %q", ans.Class, ans.Code)
	}
	if _, ans := api.DecodePostREST(a.PostRESTAnswer(ctx, TicketLogin, "00000000-0000-4000-8000-000000000999")); ans.Class != transport.Refused || ans.Reason != api.CommentNotFound {
		t.Fatalf("a missing comment: %v %q", ans.Class, ans.Reason)
	}
	if _, ans := api.DecodeTicketREST(a.TicketRESTAnswer(ctx, TicketLogin, api.TicketOptions{PostsPerPage: 80}), paged); ans.Class != transport.InvalidInput || ans.Code != "invalid_input" {
		t.Fatalf("a page of 80: %v %q", ans.Class, ans.Code)
	}
	if _, ans := api.DecodeTicketsREST(a.TicketsRESTAnswer(ctx, api.TicketQuery{ProjectID: "00000000-0000-4000-8000-000000000999"})); ans.Class != transport.NotFound {
		t.Fatalf("an unknown project: %v", ans.Class)
	}
	// The log names the route and the arguments as the tools would.
	var shows []Request
	for _, r := range f.Requests() {
		if r.Route == "tickets.show" && r.Status == http.StatusOK {
			shows = append(shows, r)
		}
	}
	if len(shows) == 0 || shows[0].Args["ticket_id"] != TicketLogin || shows[0].Args["posts_page"] != float64(1) || shows[0].Args["posts_order"] != "newest" || shows[0].Tool != "" {
		t.Fatalf("log %+v", shows)
	}
}

// The tools and REST send the same ticket, field for field, but for the
// texts the tools cut (#6, #8).
func TestTheToolsAndRESTSendTheSameTicket(t *testing.T) {
	_, a, _ := serve(t, Normal)
	ctx := context.Background()
	for _, c := range []struct {
		n int
		o api.TicketOptions
	}{
		{1, api.TicketOptions{PostsPage: 1, PostsPerPage: 25, Newest: true, AttachmentsPage: 1}},
		{2, api.TicketOptions{PostsPage: 1, PostsPerPage: 25, Newest: true, AttachmentsPage: 1}},
		{3, api.TicketOptions{PostsPage: 2, PostsPerPage: 25, Newest: true, AttachmentsPage: 1}},
		{3, api.TicketOptions{PostsPage: 1, PostsPerPage: 25, AttachmentsPage: 1}},
		{4, api.TicketOptions{PostsPage: 1, PostsPerPage: 25, Newest: true, AttachmentsPage: 2}},
	} {
		id := TicketID(ProjectWebsite, c.n)
		tools, ans := a.TicketWith(ctx, ProjectWebsite, id, c.o)
		if !ans.OK() {
			t.Fatalf("#%d tools: %v", c.n, ans.Class)
		}
		rest, ans := api.DecodeTicketREST(a.TicketRESTAnswer(ctx, id, c.o), c.o)
		if !ans.OK() {
			t.Fatalf("#%d REST: %v %q", c.n, ans.Class, ans.Message)
		}
		if !reflect.DeepEqual(tools, rest) {
			t.Errorf("#%d %+v differs:\ntools %+v\nREST  %+v", c.n, c.o, tools, rest)
		}
	}
	// #6 and #8: the same tickets, their long texts cut by the tools
	// alone, each cut a start of REST's text.
	o := api.TicketOptions{PostsPage: 1, PostsPerPage: 25, Newest: true, AttachmentsPage: 1}
	for _, n := range []int{6, 8} {
		id := TicketID(ProjectWebsite, n)
		tools, _ := a.TicketWith(ctx, ProjectWebsite, id, o)
		rest, _ := api.DecodeTicketREST(a.TicketRESTAnswer(ctx, id, o), o)
		cuts := 0
		same := func(cut *string, whole string) {
			if body, short := api.Shortened(*cut); short {
				if !strings.HasPrefix(whole, strings.TrimSuffix(body, " ")) || len(body) >= len(whole) {
					t.Errorf("#%d: %.40q is no start of %.40q", n, body, whole)
				}
				*cut, cuts = whole, cuts+1
			}
		}
		same(&tools.DescriptionText, rest.DescriptionText)
		for i := range tools.Posts {
			same(&tools.Posts[i].BodyText, rest.Posts[i].BodyText)
		}
		if cuts < 2 || !reflect.DeepEqual(tools, rest) {
			t.Fatalf("#%d: %d cut texts, and beyond them:\ntools %+v\nREST  %+v", n, cuts, tools, rest)
		}
	}
	if body, _ := api.Shortened(toolsText(eightDescription)); len([]rune(body)) != toolsCut {
		t.Fatalf("the tools cut #8 at %d", len([]rune(body)))
	}
	id := TicketID(ProjectWebsite, 8)
	rest, _ := api.DecodeTicketREST(a.TicketRESTAnswer(ctx, id, o), o)
	// Read whole through the tools, they are REST's texts.
	whole, _ := a.TicketWith(ctx, ProjectWebsite, id, api.TicketOptions{FullText: true})
	one, _ := a.TicketWith(ctx, ProjectWebsite, id, api.TicketOptions{PostID: EightPostID})
	if whole.DescriptionText != rest.DescriptionText || one.Posts[0].BodyText != rest.Posts[0].BodyText {
		t.Fatal("the tools' whole texts are not REST's")
	}
}

// A server before R1 lists the capabilities of then, and the client takes
// the tools; its REST answers are those it would give: no status
// catalogue, no pages, no route for one comment or a number.
func TestAFixtureBeforeR1KeepsTheTools(t *testing.T) {
	ctx := context.Background()
	for name, c := range map[string]struct {
		scenario Scenario
		opts     []Option
		version  string
	}{
		"before R1":  {Normal, []Option{BeforeR1()}, "1.2.0"},
		"before R0":  {Older, nil, ""},
		"S1 only":    {S1Only, nil, ""},
		"S2 only":    {S2Only, nil, ""},
		"read-only":  {ReadOnly, []Option{BeforeR1()}, "1.2.0"},
		"refusals":   {Refusals, []Option{BeforeR1()}, "1.2.0"},
		"the normal": {Normal, nil, "1.5.0"},
	} {
		t.Run(name, func(t *testing.T) {
			f, err := New(c.scenario, contract.Raw(), c.opts...)
			if err != nil {
				t.Fatal(err)
			}
			a, cl := clientOf(t, f)
			me, _ := a.Me(ctx)
			if me.APIVersion != c.version {
				t.Fatalf("api_version %q", me.APIVersion)
			}
			report := reportOf(t, a, cl)
			want := features.MCP
			if c.version == "1.5.0" {
				want = features.REST
			}
			for _, name := range ticketView {
				if report.Enabled[name] && report.Via[name] != want {
					t.Errorf("%s via %v", name, report.Via[name])
				}
			}
			if want == features.REST {
				return
			}
			if c.version == "" && me.Capabilities != nil {
				t.Fatalf("capabilities %v", me.Capabilities)
			}
			if c.version != "" && (slices.Contains(me.Capabilities, "tickets.paging") || !slices.Contains(me.Capabilities, "tickets.show")) {
				t.Fatalf("capabilities %v", me.Capabilities)
			}
			if _, ans := api.DecodeTicketsREST(a.TicketsRESTAnswer(ctx, api.TicketQuery{ProjectID: ProjectWebsite, Page: 1, PerPage: 25})); ans.Class != transport.Protocol || !strings.Contains(ans.Message, "available_statuses") {
				t.Fatalf("a list: %v %q", ans.Class, ans.Message)
			}
			o := api.TicketOptions{PostsPage: 1, PostsPerPage: 25, Newest: true, AttachmentsPage: 1}
			if _, ans := api.DecodeTicketREST(a.TicketRESTAnswer(ctx, TicketLogin, o), o); ans.Class != transport.Protocol || !strings.Contains(ans.Message, "posts_total") {
				t.Fatalf("a ticket: %v %q", ans.Class, ans.Message)
			}
			if _, ans := api.DecodePostREST(a.PostRESTAnswer(ctx, TicketLogin, "00000000-0000-4000-8000-000000000051")); ans.Class != transport.Refused {
				t.Fatalf("one comment: %v", ans.Class)
			}
		})
	}
}

// The scenarios act on REST's reads as on the tools': Website's page 2 of
// tickets, ticket #2 (401), #6's comments page 2 (temporary), the newest
// comments of #6 and #8 read whole (vanished).
func TestTheScenariosOverREST(t *testing.T) {
	ctx := context.Background()
	w2 := api.TicketQuery{ProjectID: ProjectWebsite, Page: 2, PerPage: 25}
	t.Run("temporary", func(t *testing.T) {
		_, a, _ := serve(t, Temporary)
		for i := range 2 {
			if ans := a.TicketsRESTAnswer(ctx, w2); ans.Class != transport.Temporary || ans.Status != http.StatusServiceUnavailable {
				t.Fatalf("list attempt %d: %v %d", i+1, ans.Class, ans.Status)
			}
		}
		if _, ans := api.DecodeTicketsREST(a.TicketsRESTAnswer(ctx, w2)); !ans.OK() {
			t.Fatalf("list attempt 3: %v", ans.Class)
		}
		six, page2 := TicketID(ProjectWebsite, 6), api.TicketOptions{PostsPage: 2, PostsPerPage: 25, Newest: true, AttachmentsPage: 1}
		for i := range 2 {
			if ans := a.TicketRESTAnswer(ctx, six, page2); ans.Class != transport.Temporary {
				t.Fatalf("#6 page 2 attempt %d: %v", i+1, ans.Class)
			}
		}
		if _, ans := api.DecodeTicketREST(a.TicketRESTAnswer(ctx, six, page2), page2); !ans.OK() {
			t.Fatalf("#6 page 2 attempt 3: %v", ans.Class)
		}
	})
	t.Run("401", func(t *testing.T) {
		_, a, _ := serve(t, Unauthorized)
		if ans := a.TicketsRESTAnswer(ctx, w2); ans.Class != transport.Unauthenticated {
			t.Fatalf("page 2: %v", ans.Class)
		}
		if ans := a.TicketRESTAnswer(ctx, TicketID(ProjectWebsite, 2), api.TicketOptions{}); ans.Class != transport.Unauthenticated {
			t.Fatalf("#2: %v", ans.Class)
		}
		if ans := a.TicketRESTAnswer(ctx, TicketLogin, api.TicketOptions{}); !ans.OK() {
			t.Fatalf("#1: %v", ans.Class)
		}
	})
	t.Run("vanished", func(t *testing.T) {
		_, a, _ := serve(t, Vanished)
		for _, c := range [][2]string{{TicketID(ProjectWebsite, 6), SixPostID(30)}, {TicketID(ProjectWebsite, 8), EightPostID}} {
			if _, ans := api.DecodePostREST(a.PostRESTAnswer(ctx, c[0], c[1])); ans.Class != transport.Refused || ans.Reason != api.CommentNotFound {
				t.Fatalf("%s: %v", c[1], ans.Class)
			}
		}
		if _, ans := api.DecodePostREST(a.PostRESTAnswer(ctx, TicketID(ProjectWebsite, 6), SixPostID(29))); !ans.OK() {
			t.Fatalf("an older comment: %v", ans.Class)
		}
	})
}
