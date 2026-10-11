package inboxfixture

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/contract"
	"github.com/gboyke/lalabase-cli/internal/features"
	"github.com/gboyke/lalabase-cli/internal/transport"
)

func serve(t *testing.T, scenario Scenario) (*Fixture, *api.API, *transport.Client) {
	t.Helper()
	f, err := New(scenario, contract.Raw())
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	c, err := transport.New(srv.URL, Token)
	if err != nil {
		t.Fatal(err)
	}
	return f, api.New(c), c
}

// The whole start works against the fixture through the real adapters,
// and every answer passes the client's own checks.
func TestTheStartWorksAgainstTheFixture(t *testing.T) {
	f, a, c := serve(t, Normal)
	ctx := context.Background()
	me, ans := a.Me(ctx)
	if !ans.OK() || me.Token.ID == "" || me.User.DisplayName != "Anna Albers" {
		t.Fatalf("/me: %v %+v", ans.Class, me)
	}
	info, ans := c.Initialize(ctx)
	if !ans.OK() || info.Version == "" {
		t.Fatalf("initialize: %v", ans.Class)
	}
	tools, ans := c.ListTools(ctx)
	if !ans.OK() {
		t.Fatalf("tools: %v", ans.Class)
	}
	report := features.Detect(info, tools)
	if !report.Enabled["inbox"] || !report.Enabled["inbox_by_organisation"] || report.BelowTested {
		t.Fatalf("report %+v", report)
	}
	orgs, ans := a.Organisations(ctx)
	if !ans.OK() || len(orgs) != 2 || orgs[0].ID != OrgA || orgs[1].ID != OrgB {
		t.Fatalf("organisations: %v %+v", ans.Class, orgs)
	}
	global, ans := a.Notifications(ctx, api.NotificationQuery{Status: "unread", Page: 1, PerPage: 25})
	if !ans.OK() || len(global.Data) != 25 || global.Meta.Total != 33 || global.Meta.UnreadTotal != 33 || len(global.Meta.ByOrganisation) != 2 {
		t.Fatalf("global: %v %+v", ans.Class, global.Meta)
	}
	if err := api.CheckNotificationPage(global); err != nil {
		t.Fatal(err)
	}
	second, ans := a.Notifications(ctx, api.NotificationQuery{Status: "unread", Page: 2, PerPage: 25, OrganisationID: OrgA})
	if !ans.OK() || len(second.Data) != 5 || second.Meta.Total != 30 || second.Meta.ByOrganisation[0].ID != OrgA {
		t.Fatalf("A page 2: %v %+v", ans.Class, second.Meta)
	}
	for _, row := range second.Data {
		if row.Organisation.ID != OrgA {
			t.Fatalf("a foreign row on A's page: %+v", row.Organisation)
		}
	}
	b, ans := a.Notifications(ctx, api.NotificationQuery{Status: "unread", Page: 1, PerPage: 25, OrganisationID: OrgB})
	if !ans.OK() || len(b.Data) != 3 || b.Data[0].Ticket == nil || b.Data[0].Ticket.Number != 101 {
		t.Fatalf("B: %v %+v", ans.Class, b.Data)
	}
	// The variations: a row without a project, a ticket, an actor; a
	// deleted actor; a ticket without a title.
	first, _ := a.Notifications(ctx, api.NotificationQuery{Status: "unread", Page: 1, PerPage: 25, OrganisationID: OrgA})
	if first.Data[3].Project != nil || first.Data[4].Ticket != nil || first.Data[6].Actor != nil ||
		first.Data[1].Actor == nil || first.Data[1].Actor.Name != "" || first.Data[2].Ticket.Title != "" {
		t.Fatalf("variations: %+v", first.Data[:7])
	}
	for _, row := range first.Data {
		if !strings.HasSuffix(row.LastEventAt, "Z") {
			t.Fatalf("a time not in UTC: %s", row.LastEventAt)
		}
	}
	log := f.Requests()
	if len(log) != 8 {
		t.Fatalf("%d requests: %+v", len(log), log)
	}
	for _, r := range log {
		if r.Status != http.StatusOK {
			t.Fatalf("%+v", r)
		}
	}
}

// Other credentials are refused with 401, logged without the token; a
// missing header too.
func TestOtherCredentialsAreRefused(t *testing.T) {
	f, err := New(Normal, contract.Raw())
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(f)
	defer srv.Close()
	for _, auth := range []string{"", "Bearer other-token", "Bearer " + Token + "x", "Basic abc"} {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/api/v1/me", nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%q: %d", auth, resp.StatusCode)
		}
	}
	for _, r := range f.Requests() {
		if r.Status != http.StatusUnauthorized {
			t.Fatalf("accepted: %+v", r)
		}
		if raw, _ := json.Marshal(r); strings.Contains(string(raw), "other-token") || strings.Contains(string(raw), "abc") {
			t.Fatalf("the log carries a credential: %s", raw)
		}
	}
}

// Other tools, writes, other methods and reads outside C1 are refused,
// visibly.
func TestEverythingButTheReadsOfC1IsRefused(t *testing.T) {
	f, a, c := serve(t, Normal)
	ctx := context.Background()
	if _, ans := a.Projects(ctx, ""); ans.Class != transport.NotFound || ans.Status != http.StatusNotFound {
		t.Fatalf("projects of no organisation: %v %d", ans.Class, ans.Status)
	}
	if _, ans := a.Tickets(ctx, api.TicketQuery{ProjectID: "p"}); ans.Class != transport.NotFound {
		t.Fatalf("tickets of an unknown project: %v", ans.Class)
	}
	if _, ans := a.Tickets(ctx, api.TicketQuery{ProjectID: ProjectWebsite, Status: "new"}); ans.Class != transport.Refused {
		t.Fatalf("a status filter: %v", ans.Class)
	}
	if _, ans := a.TicketByNumber(ctx, ProjectWebsite, 1); ans.Class != transport.InvalidInput {
		t.Fatalf("by number: %v", ans.Class)
	}
	if _, ans := a.Ticket(ctx, ProjectWebsite, "00000000-0000-4000-8000-000000000999"); ans.Class != transport.NotFound {
		t.Fatalf("a missing ticket: %v", ans.Class)
	}
	// The writes answer a missing record as the server does (marks.go,
	// writes.go); a tool outside C1 and C2 stays refused.
	if _, ans := a.MarkNotificationRead(ctx, "n", "key-00000001"); ans.Class != transport.NotFound {
		t.Fatalf("mark read of a missing row: %v", ans.Class)
	}
	if _, ans := a.SearchKnowledge(ctx, api.KnowledgeQuery{ProjectID: ProjectWebsite, Q: "deploy"}); ans.Class != transport.Refused {
		t.Fatalf("search knowledge: %v", ans.Class)
	}
	if _, ans := a.Notifications(ctx, api.NotificationQuery{Status: "unread", ProjectID: "p"}); ans.Class != transport.Refused {
		t.Fatalf("project filter: %v", ans.Class)
	}
	if _, ans := a.Notifications(ctx, api.NotificationQuery{Status: "all"}); ans.Class != transport.InvalidInput {
		t.Fatalf("status all: %v", ans.Class)
	}
	if _, ans := a.Notifications(ctx, api.NotificationQuery{Status: "unread", OrganisationID: "00000000-0000-4000-8000-000000000009"}); ans.Class != transport.NotFound {
		t.Fatalf("unknown organisation: %v", ans.Class)
	}
	if _, ans := a.Notifications(ctx, api.NotificationQuery{Status: "unread", Page: 0, PerPage: 500}); ans.Class != transport.InvalidInput {
		t.Fatalf("out of range: %v", ans.Class)
	}
	_ = c
	seen := map[string]bool{}
	for _, r := range f.Requests() {
		seen[r.Tool] = true
	}
	if !seen["list_tickets"] || !seen["mark_notification_read"] || !seen["get_ticket"] || !seen["search_knowledge"] {
		t.Fatalf("the log does not name the refused tools: %v", seen)
	}
}

// The projects and tickets (C1c) work against the fixture through the real
// adapters, and every answer passes the client's own checks.
func TestTheTicketsSectionWorksAgainstTheFixture(t *testing.T) {
	_, a, _ := serve(t, Normal)
	ctx := context.Background()
	projects, ans := a.Projects(ctx, OrgA)
	if !ans.OK() || len(projects) != 4 || api.CheckProjects(projects) != nil || projects[3].ID != ProjectWebsite {
		t.Fatalf("projects of A: %v %+v", ans.Class, projects)
	}
	if list, ans := a.Projects(ctx, OrgB); !ans.OK() || len(list) != 0 {
		t.Fatalf("projects of B: %v %+v", ans.Class, list)
	}
	page, ans := a.Tickets(ctx, api.TicketQuery{ProjectID: ProjectWebsite, Page: 1, PerPage: 25})
	if !ans.OK() || api.CheckTicketPage(page) != nil || len(page.Data) != 25 || page.Meta.Total != 30 || page.Data[0].ID != TicketLogin || page.Data[0].Number != 1 {
		t.Fatalf("page 1: %v %+v", ans.Class, page.Meta)
	}
	if page.Data[2].Assignees == nil || len(page.Data[2].Assignees) != 0 || page.Data[3].Status != nil || *page.Data[0].Status != "in_progress" {
		t.Fatalf("variations: %+v", page.Data[:4])
	}
	second, ans := a.Tickets(ctx, api.TicketQuery{ProjectID: ProjectWebsite, Page: 2, PerPage: 25})
	if !ans.OK() || len(second.Data) != 5 || second.Data[0].Number != 26 {
		t.Fatalf("page 2: %v %+v", ans.Class, second.Meta)
	}
	found, ans := a.Tickets(ctx, api.TicketQuery{ProjectID: ProjectWebsite, Q: "export", Page: 1, PerPage: 25})
	if !ans.OK() || len(found.Data) != 1 || found.Data[0].Number != 2 || found.Meta.Total != 1 {
		t.Fatalf("search: %v %+v", ans.Class, found.Meta)
	}
	if empty, ans := a.Tickets(ctx, api.TicketQuery{ProjectID: ProjectIntern, Page: 1, PerPage: 25}); !ans.OK() || len(empty.Data) != 0 || empty.Meta.Total != 0 {
		t.Fatalf("empty project: %v %+v", ans.Class, empty.Meta)
	}
	paged := api.TicketOptions{PostsPage: 1, PostsPerPage: 25, Newest: true, AttachmentsPage: 1}
	ticket, ans := a.TicketWith(ctx, ProjectWebsite, TicketLogin, paged)
	if !ans.OK() || api.CheckTicket(ticket, paged) != nil || ticket.Number != 1 || len(ticket.Posts) != 2 || ticket.Posts[0].ID != "00000000-0000-4000-8000-000000000052" || ticket.PostsOrder != "newest" || len(ticket.Attachments) != 2 {
		t.Fatalf("#1: %v %+v", ans.Class, ticket)
	}
	bare, ans := a.TicketWith(ctx, ProjectWebsite, TicketID(ProjectWebsite, 2), paged)
	if !ans.OK() || bare.DescriptionText != "" || bare.Creator != nil || bare.Status != nil || len(bare.Posts) != 0 || bare.PostsTotal != 0 || len(bare.Attachments) != 0 {
		t.Fatalf("#2: %v %+v", ans.Class, bare)
	}
	many, ans := a.TicketWith(ctx, ProjectWebsite, TicketID(ProjectWebsite, 3), paged)
	if !ans.OK() || len(many.Posts) != 25 || many.PostsTotal != 30 || many.PostsPages != 2 || many.Posts[0].BodyText != "Comment 30" || !many.PostsTruncated {
		t.Fatalf("#3: %v %+v", ans.Class, many.Posts)
	}
	files, ans := a.TicketWith(ctx, ProjectWebsite, TicketID(ProjectWebsite, 4), paged)
	if !ans.OK() || len(files.Attachments) != 10 || files.AttachmentsTotal != 12 || files.AttachmentsPages != 2 || !files.AttachmentsTruncated {
		t.Fatalf("#4: %v %+v", ans.Class, files.Attachments)
	}
	plain, ans := a.Ticket(ctx, ProjectWebsite, TicketID(ProjectWebsite, 3))
	if !ans.OK() || api.CheckTicket(plain, api.TicketOptions{}) != nil || len(plain.Posts) != 30 || plain.Posts[0].BodyText != "Comment 1" || plain.PostsTruncated {
		t.Fatalf("#3 plain: %v %d comments", ans.Class, len(plain.Posts))
	}
	if _, ans := a.Tickets(ctx, api.TicketQuery{ProjectID: ProjectHandbuch, Page: 1, PerPage: 25}); !ans.OK() {
		t.Fatalf("handbuch: %v", ans.Class)
	}
}

// The scenarios act on Website's ticket page 2 and on ticket #2 as they
// act on A's inbox page 2.
func TestTheTicketScenarios(t *testing.T) {
	ctx := context.Background()
	w2 := api.TicketQuery{ProjectID: ProjectWebsite, Page: 2, PerPage: 25}
	t.Run("temporary", func(t *testing.T) {
		_, a, _ := serve(t, Temporary)
		for i := range 2 {
			if _, ans := a.Tickets(ctx, w2); ans.Class != transport.Temporary || ans.Status != http.StatusServiceUnavailable {
				t.Fatalf("attempt %d: %v %d", i+1, ans.Class, ans.Status)
			}
		}
		if _, ans := a.Tickets(ctx, w2); !ans.OK() {
			t.Fatalf("attempt 3: %v", ans.Class)
		}
	})
	t.Run("401", func(t *testing.T) {
		_, a, _ := serve(t, Unauthorized)
		if _, ans := a.Tickets(ctx, w2); ans.Class != transport.Unauthenticated {
			t.Fatalf("page 2: %v", ans.Class)
		}
		if _, ans := a.Ticket(ctx, ProjectWebsite, TicketID(ProjectWebsite, 2)); ans.Class != transport.Unauthenticated {
			t.Fatalf("ticket #2: %v", ans.Class)
		}
		if _, ans := a.Ticket(ctx, ProjectWebsite, TicketLogin); !ans.OK() {
			t.Fatalf("ticket #1: %v", ans.Class)
		}
	})
	t.Run("older", func(t *testing.T) {
		_, _, c := serve(t, Older)
		info, _ := c.Initialize(ctx)
		tools, _ := c.ListTools(ctx)
		report := features.Detect(info, tools)
		if !report.Enabled["tickets"] || !report.Enabled["ticket"] || !report.Enabled["tickets_search"] || report.Enabled["ticket_posts_paged"] || report.Enabled["ticket_full_text"] {
			t.Fatalf("report %+v", report.Enabled)
		}
	})
}

// Each scenario does what it says, on A's page 2 and nowhere else.
func TestTheScenarios(t *testing.T) {
	ctx := context.Background()
	a2 := api.NotificationQuery{Status: "unread", Page: 2, PerPage: 25, OrganisationID: OrgA}
	a1 := api.NotificationQuery{Status: "unread", Page: 1, PerPage: 25, OrganisationID: OrgA}
	t.Run("temporary", func(t *testing.T) {
		_, a, _ := serve(t, Temporary)
		for i := range 2 {
			if _, ans := a.Notifications(ctx, a2); ans.Class != transport.Temporary || ans.Status != http.StatusServiceUnavailable {
				t.Fatalf("attempt %d: %v %d", i+1, ans.Class, ans.Status)
			}
		}
		if _, ans := a.Notifications(ctx, a2); !ans.OK() {
			t.Fatalf("attempt 3: %v", ans.Class)
		}
		if _, ans := a.Notifications(ctx, a1); !ans.OK() {
			t.Fatalf("page 1: %v", ans.Class)
		}
	})
	t.Run("429", func(t *testing.T) {
		_, a, _ := serve(t, RateLimited)
		if _, ans := a.Notifications(ctx, a2); ans.Class != transport.Temporary || ans.Status != http.StatusTooManyRequests || ans.RetryAt.IsZero() {
			t.Fatalf("attempt 1: %v %d %v", ans.Class, ans.Status, ans.RetryAt)
		}
		if _, ans := a.Notifications(ctx, a2); !ans.OK() {
			t.Fatalf("attempt 2: %v", ans.Class)
		}
	})
	t.Run("401", func(t *testing.T) {
		_, a, _ := serve(t, Unauthorized)
		if _, ans := a.Notifications(ctx, a1); !ans.OK() {
			t.Fatalf("page 1: %v", ans.Class)
		}
		if _, ans := a.Notifications(ctx, a2); ans.Class != transport.Unauthenticated {
			t.Fatalf("page 2: %v", ans.Class)
		}
	})
	t.Run("older", func(t *testing.T) {
		_, a, c := serve(t, Older)
		info, _ := c.Initialize(ctx)
		tools, _ := c.ListTools(ctx)
		report := features.Detect(info, tools)
		if !report.Enabled["inbox"] || report.Enabled["inbox_by_organisation"] {
			t.Fatalf("report %+v", report)
		}
		if _, ans := a.Notifications(ctx, a1); ans.Class != transport.InvalidInput {
			t.Fatalf("a filtered read: %v", ans.Class)
		}
		if page, ans := a.Notifications(ctx, api.NotificationQuery{Status: "unread", Page: 1, PerPage: 25}); !ans.OK() || len(page.Data) != 25 {
			t.Fatalf("global: %v", ans.Class)
		}
	})
	if _, err := New("bogus", contract.Raw()); err == nil {
		t.Fatal("an unknown scenario was accepted")
	}
	if _, err := New(Normal, []byte(`{}`)); err == nil {
		t.Fatal("an empty contract was accepted")
	}
}

// The listener is the loopback address and a numeric port, nothing else.
func TestListenBindsTheLoopbackAddressOnly(t *testing.T) {
	for _, port := range []int{-1, 65536} {
		if _, err := Listen(port); err == nil {
			t.Fatalf("port %d accepted", port)
		}
	}
	ln, err := Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok || !addr.IP.Equal(net.IPv4(127, 0, 0, 1)) || addr.Port == 0 {
		t.Fatalf("bound to %v", ln.Addr())
	}
	if got := Describe(Normal, ln.Addr()); !strings.HasPrefix(got, "inbox fixture: scenario normal at http://127.0.0.1:") {
		t.Fatalf("describe %q", got)
	}
}

// Ticket #6 (C1d) through the real adapters: pages in both orders, the
// description and one comment whole, attachment metadata of every kind,
// every answer passing the client's checks.
func TestTicketSixWorksAgainstTheFixture(t *testing.T) {
	_, a, _ := serve(t, Normal)
	ctx := context.Background()
	six := TicketID(ProjectWebsite, 6)
	for _, o := range []api.TicketOptions{
		{PostsPage: 1, PostsPerPage: 25, Newest: true, AttachmentsPage: 1},
		{PostsPage: 2, PostsPerPage: 25, AttachmentsPage: 2},
		{PostsPage: 1, PostsPerPage: 25, Newest: true, AttachmentsPage: 1, FullText: true},
		{FullText: true},
		{},
	} {
		ticket, ans := a.TicketWith(ctx, ProjectWebsite, six, o)
		if !ans.OK() || api.CheckTicket(ticket, o) != nil || ticket.PostsTotal != 30 || ticket.AttachmentsTotal != 12 {
			t.Fatalf("%+v: %v %s", o, ans.Class, ans.Message)
		}
		body, short := api.Shortened(ticket.DescriptionText)
		if short == o.FullText || (o.FullText && body != SixDescriptionWhole) {
			t.Fatalf("%+v: description %q", o, ticket.DescriptionText)
		}
	}
	first, _ := a.TicketWith(ctx, ProjectWebsite, six, api.TicketOptions{PostsPage: 1, PostsPerPage: 25, Newest: true, AttachmentsPage: 1})
	if first.Posts[0].ID != SixPostID(30) || len(first.Posts) != 25 {
		t.Fatalf("newest first: %s", first.Posts[0].ID)
	}
	if _, short := api.Shortened(first.Posts[0].BodyText); !short {
		t.Fatal("the newest comment is not shortened")
	}
	a1, a2, a12 := first.Attachments[0], first.Attachments[1], api.Attachment{}
	if a1.ContentType == nil || *a1.ContentType != "text/plain" || a1.ByteSize == nil || *a1.ByteSize != 1000 || a1.TextStatus == nil || *a1.TextStatus != "available" {
		t.Fatalf("attachment 1: %+v", a1)
	}
	if a2.ContentType != nil || a2.ByteSize != nil || a2.TextStatus != nil {
		t.Fatalf("attachment 2: %+v", a2)
	}
	second, _ := a.TicketWith(ctx, ProjectWebsite, six, api.TicketOptions{PostsPage: 1, PostsPerPage: 25, Newest: true, AttachmentsPage: 2})
	a12 = second.Attachments[1]
	if a12.Filename != "six-12.txt" || a12.ByteSize == nil || *a12.ByteSize != 0 {
		t.Fatalf("attachment 12: %+v", a12)
	}
	o := api.TicketOptions{PostID: SixPostID(30)}
	one, ans := a.TicketWith(ctx, ProjectWebsite, six, o)
	if !ans.OK() || api.CheckTicket(one, o) != nil || len(one.Posts) != 1 || one.Posts[0].BodyText != "Six comment 30 begins and ends here." || one.PostsTotal != 30 {
		t.Fatalf("one comment: %v %+v", ans.Class, one.Posts)
	}
	if _, ans := a.TicketWith(ctx, ProjectWebsite, six, api.TicketOptions{PostID: SixPostID(99)}); ans.Class != transport.Refused || ans.Reason != api.CommentNotFound {
		t.Fatalf("a missing comment: %v %q", ans.Class, ans.Reason)
	}
}

// The scenarios of C1d: temporary fails page 2 of #6's comments twice;
// vanished hides #6's newest comment read whole, and only that; older
// refuses the arguments it does not know.
func TestTheTicketSixScenarios(t *testing.T) {
	ctx := context.Background()
	six := TicketID(ProjectWebsite, 6)
	p2 := api.TicketOptions{PostsPage: 2, PostsPerPage: 25, Newest: true, AttachmentsPage: 1}
	t.Run("temporary", func(t *testing.T) {
		_, a, _ := serve(t, Temporary)
		for i := range 2 {
			if _, ans := a.TicketWith(ctx, ProjectWebsite, six, p2); ans.Class != transport.Temporary {
				t.Fatalf("attempt %d: %v", i+1, ans.Class)
			}
		}
		if _, ans := a.TicketWith(ctx, ProjectWebsite, six, p2); !ans.OK() {
			t.Fatalf("attempt 3: %v", ans.Class)
		}
	})
	t.Run("vanished", func(t *testing.T) {
		_, a, _ := serve(t, Vanished)
		if _, ans := a.TicketWith(ctx, ProjectWebsite, six, api.TicketOptions{PostID: SixPostID(30)}); ans.Class != transport.Refused || ans.Reason != api.CommentNotFound {
			t.Fatalf("the newest comment: %v %q", ans.Class, ans.Reason)
		}
		if _, ans := a.TicketWith(ctx, ProjectWebsite, six, api.TicketOptions{PostID: SixPostID(29)}); !ans.OK() {
			t.Fatalf("comment 29: %v", ans.Class)
		}
		page, ans := a.TicketWith(ctx, ProjectWebsite, six, api.TicketOptions{PostsPage: 1, PostsPerPage: 25, Newest: true, AttachmentsPage: 1})
		if !ans.OK() || page.Posts[0].ID != SixPostID(30) {
			t.Fatalf("the page: %v", ans.Class)
		}
		if _, ans := a.TicketWith(ctx, ProjectWebsite, six, p2); !ans.OK() {
			t.Fatalf("page 2: %v", ans.Class)
		}
	})
	t.Run("older", func(t *testing.T) {
		_, a, _ := serve(t, Older)
		for _, o := range []api.TicketOptions{{FullText: true}, {PostID: SixPostID(30)}} {
			if _, ans := a.TicketWith(ctx, ProjectWebsite, six, o); ans.Class != transport.InvalidInput {
				t.Fatalf("%+v: %v", o, ans.Class)
			}
		}
	})
}

// The write (C1f) against the fixture through the real adapters: a marked
// row leaves the unread pages and the counts, a key seen before answers
// the original result without a second change, and the three logs tell
// attempts, keys and changes apart.
func TestMarkReadWorksAgainstTheFixture(t *testing.T) {
	f, a, _ := serve(t, Normal)
	ctx := context.Background()
	first, _ := a.Notifications(ctx, api.NotificationQuery{Status: "unread", Page: 1, PerPage: 25, OrganisationID: OrgA})
	row := first.Data[5]
	if row.Ticket == nil || row.Ticket.ID != TicketID(ProjectWebsite, 6) || row.Ticket.ProjectID != ProjectWebsite || row.Ticket.Title != "Ticket 6" {
		t.Fatalf("A's rows do not name Website's tickets: %+v", row.Ticket)
	}
	if b, _ := a.Notifications(ctx, api.NotificationQuery{Status: "unread", Page: 1, PerPage: 25, OrganisationID: OrgB}); b.Data[0].Ticket == nil || b.Data[0].Ticket.Number != 101 {
		t.Fatalf("B's rows changed: %+v", b.Data[0].Ticket)
	}
	state, ans := a.MarkNotificationRead(ctx, row.ID, "key-00000001")
	if !ans.OK() || state.ID != row.ID || state.State != "read" || state.ReadAt == nil {
		t.Fatalf("mark: %v %+v", ans.Class, state)
	}
	after, _ := a.Notifications(ctx, api.NotificationQuery{Status: "unread", Page: 1, PerPage: 25, OrganisationID: OrgA})
	if after.Meta.Total != 29 || after.Meta.ByOrganisation[0].Unread != 29 || len(after.Data) != 25 {
		t.Fatalf("after the mark: %+v", after.Meta)
	}
	for _, n := range after.Data {
		if n.ID == row.ID {
			t.Fatal("the marked row is still unread")
		}
	}
	global, _ := a.Notifications(ctx, api.NotificationQuery{Status: "unread", Page: 1, PerPage: 25})
	if global.Meta.Total != 32 || global.Meta.UnreadTotal != 32 {
		t.Fatalf("global after the mark: %+v", global.Meta)
	}
	// The same key again: the original result, no second change.
	again, ans := a.MarkNotificationRead(ctx, row.ID, "key-00000001")
	if !ans.OK() || again.ID != state.ID || again.State != state.State || again.ReadAt == nil || *again.ReadAt != *state.ReadAt {
		t.Fatalf("replay: %v %+v", ans.Class, again)
	}
	// Marking an already read row with a new key: read it stays, one more
	// change is recorded (the server's "a row that is already read stays
	// read" is one write call all the same).
	if _, ans := a.MarkNotificationRead(ctx, row.ID, "key-00000002"); !ans.OK() {
		t.Fatalf("second mark: %v", ans.Class)
	}
	if _, ans := a.MarkNotificationRead(ctx, "00000000-0000-4000-8000-000000000999", "key-00000003"); ans.Class != transport.NotFound {
		t.Fatalf("a missing row: %v", ans.Class)
	}
	if got := f.Mutations(); len(got) != 2 || got[0] != (Mutation{Kind: MutationMarkRead, Notification: row.ID, Key: "key-00000001"}) || got[1].Key != "key-00000002" {
		t.Fatalf("mutations %+v", got)
	}
	if got := f.Keys(); len(got) != 4 || got[0] != "key-00000001" || got[1] != "key-00000001" || got[3] != "key-00000003" {
		t.Fatalf("keys %v", got)
	}
	writes := 0
	for _, r := range f.Requests() {
		if r.Tool == "mark_notification_read" {
			writes++
			if key, _ := r.Args["idempotency_key"].(string); key == "" {
				t.Fatalf("an attempt without its key: %+v", r)
			}
		}
	}
	if writes != 4 {
		t.Fatalf("%d attempts logged", writes)
	}
}

// The scenarios of the write: each does what it says, and nothing else
// changes.
func TestTheMarkReadScenarios(t *testing.T) {
	ctx := context.Background()
	a1 := api.NotificationQuery{Status: "unread", Page: 1, PerPage: 25, OrganisationID: OrgA}
	rowOf := func(t *testing.T, a *api.API, n int) string {
		t.Helper()
		page, ans := a.Notifications(ctx, a1)
		if !ans.OK() {
			t.Fatalf("page 1: %v", ans.Class)
		}
		return page.Data[n-1].ID
	}
	t.Run("read-only", func(t *testing.T) {
		f, a, _ := serve(t, ReadOnly)
		me, _ := a.Me(ctx)
		if me.Token.ReadOnly == nil || !*me.Token.ReadOnly {
			t.Fatalf("/me: %+v", me.Token)
		}
		if _, ans := a.MarkNotificationRead(ctx, rowOf(t, a, 1), "key-00000001"); ans.Class != transport.ReadOnly {
			t.Fatalf("mark: %v", ans.Class)
		}
		if len(f.Mutations()) != 0 {
			t.Fatal("a read-only token wrote")
		}
	})
	t.Run("unknown-write", func(t *testing.T) {
		_, a, _ := serve(t, UnknownWrite)
		me, ans := a.Me(ctx)
		if !ans.OK() || me.Token.ReadOnly != nil || me.Token.ID == "" {
			t.Fatalf("/me: %v %+v", ans.Class, me.Token)
		}
	})
	t.Run("no-mark-read", func(t *testing.T) {
		_, _, c := serve(t, NoMarkRead)
		info, _ := c.Initialize(ctx)
		tools, _ := c.ListTools(ctx)
		report := features.Detect(info, tools)
		if report.Enabled["mark_read"] || !report.Enabled["inbox"] || !report.Enabled["ticket"] {
			t.Fatalf("report %+v", report.Enabled)
		}
	})
	t.Run("refusals", func(t *testing.T) {
		f, a, _ := serve(t, Refusals)
		if _, ans := a.MarkNotificationRead(ctx, rowOf(t, a, refusedArchived), "key-00000001"); ans.Class != transport.Refused || ans.Reason != "archived" {
			t.Fatalf("#2: %v %q", ans.Class, ans.Reason)
		}
		if _, ans := a.MarkNotificationRead(ctx, rowOf(t, a, refusedMissing), "key-00000002"); ans.Class != transport.NotFound {
			t.Fatalf("#3: %v", ans.Class)
		}
		if _, ans := a.MarkNotificationRead(ctx, rowOf(t, a, refusedForbidden), "key-00000003"); ans.Class != transport.NoAccess {
			t.Fatalf("#4: %v", ans.Class)
		}
		if _, ans := a.MarkNotificationRead(ctx, rowOf(t, a, 5), "key-00000004"); !ans.OK() {
			t.Fatalf("#5: %v", ans.Class)
		}
		if got := f.Mutations(); len(got) != 1 {
			t.Fatalf("mutations %+v", got)
		}
	})
	t.Run("lost", func(t *testing.T) {
		f, a, _ := serve(t, Lost)
		row := rowOf(t, a, 1)
		_, ans := a.MarkNotificationRead(ctx, row, "key-00000001")
		if ans.Class != transport.Temporary || !ans.Sent {
			t.Fatalf("mark: %v sent %v", ans.Class, ans.Sent)
		}
		if got := f.Mutations(); len(got) != 1 || got[0].Notification != row {
			t.Fatalf("mutations %+v", got)
		}
		page, _ := a.Notifications(ctx, a1)
		if page.Meta.Total != 29 {
			t.Fatalf("after the lost answer: %+v", page.Meta)
		}
		log := f.Requests()
		if last := log[len(log)-2]; last.Tool != "mark_notification_read" || last.Status != 0 {
			t.Fatalf("the lost attempt is logged as %+v", last)
		}
	})
	t.Run("429", func(t *testing.T) {
		f, a, _ := serve(t, RateLimited)
		row := rowOf(t, a, 1)
		if _, ans := a.MarkNotificationRead(ctx, row, "key-00000001"); ans.Class != transport.Temporary || ans.Status != http.StatusTooManyRequests {
			t.Fatalf("attempt 1: %v %d", ans.Class, ans.Status)
		}
		if len(f.Mutations()) != 0 {
			t.Fatal("a rate-limited attempt wrote")
		}
		if _, ans := a.MarkNotificationRead(ctx, row, "key-00000001"); !ans.OK() {
			t.Fatalf("attempt 2: %v", ans.Class)
		}
		if got := f.Mutations(); len(got) != 1 {
			t.Fatalf("mutations %+v", got)
		}
	})
	t.Run("401", func(t *testing.T) {
		f, a, _ := serve(t, Unauthorized)
		if _, ans := a.MarkNotificationRead(ctx, rowOf(t, a, 1), "key-00000001"); ans.Class != transport.Unauthenticated {
			t.Fatalf("mark: %v", ans.Class)
		}
		if len(f.Mutations()) != 0 {
			t.Fatal("a refused token wrote")
		}
	})
	t.Run("slow", func(t *testing.T) {
		f, a, _ := serve(t, Slow)
		row := rowOf(t, a, 1)
		started := time.Now()
		if _, ans := a.MarkNotificationRead(ctx, row, "key-00000001"); !ans.OK() {
			t.Fatalf("mark: %v", ans.Class)
		}
		if time.Since(started) < slowAnswer {
			t.Fatal("the answer came at once")
		}
		if got := f.Mutations(); len(got) != 1 {
			t.Fatalf("mutations %+v", got)
		}
	})
}
