package inboxfixture

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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

// Projects, other tools, writes and other methods are refused, visibly.
func TestEverythingButTheStartAndTheInboxIsRefused(t *testing.T) {
	f, a, c := serve(t, Normal)
	ctx := context.Background()
	if _, ans := a.Projects(ctx, ""); ans.Class != transport.NotFound || ans.Status != http.StatusNotFound {
		t.Fatalf("projects: %v %d", ans.Class, ans.Status)
	}
	if _, ans := a.Tickets(ctx, api.TicketQuery{ProjectID: "p"}); ans.Class != transport.Refused {
		t.Fatalf("tickets: %v", ans.Class)
	}
	if _, ans := a.MarkNotificationRead(ctx, "n", "key-00000001"); ans.Class != transport.Refused {
		t.Fatalf("mark read: %v", ans.Class)
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
	if !seen["list_tickets"] || !seen["mark_notification_read"] {
		t.Fatalf("the log does not name the refused tools: %v", seen)
	}
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
