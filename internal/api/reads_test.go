package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/transport"
)

// C1c: the three reads of the projects and tickets section come in two
// halves, the request (an undecoded answer the queue can share) and the
// decode every waiter runs for itself, under the one spec.

func TestProjectsAnswerAndDecodeShareOneRule(t *testing.T) {
	api := restServer(t, "GET /api/v1/projects", "ok_one_organisation")
	ans := api.ProjectsAnswer(context.Background(), org)
	if !ans.OK() {
		t.Fatalf("%v %s", ans.Class, ans.Message)
	}
	projects, decoded := DecodeProjects(ans)
	if !decoded.OK() || len(projects) != 4 || projects[0].Organisation.ID != org || projects[3].Name != "Website-Relaunch" {
		t.Fatalf("%v %+v", decoded.Class, projects)
	}
	whole, direct := api.Projects(context.Background(), org)
	if !direct.OK() || len(whole) != len(projects) || whole[0] != projects[0] {
		t.Fatal("Projects and ProjectsAnswer+DecodeProjects differ")
	}
	// The decode checks the spec: a project without an id is a protocol
	// error, never an empty id in a list.
	broken := ans
	broken.Payload = []byte(`{"data":[{"name":"x","archived":false,"organisation":{"id":"o","name":"O"}}]}`)
	if _, bad := DecodeProjects(broken); bad.Class != transport.Protocol || !strings.Contains(bad.Message, "id") {
		t.Fatalf("got %v %q", bad.Class, bad.Message)
	}
	// A failed answer decodes to nothing.
	if list, failed := DecodeProjects(transport.Answer{Class: transport.Temporary}); list != nil || failed.Class != transport.Temporary {
		t.Fatalf("%+v %v", list, failed.Class)
	}
	// An empty list is an object, and the decode gives a list, not nil.
	empty := ans
	empty.Payload = []byte(`{"data":[]}`)
	if list, ok := DecodeProjects(empty); !ok.OK() || list == nil || len(list) != 0 {
		t.Fatalf("empty: %v %v", list, ok.Class)
	}
}

// What the spec cannot say about a list of projects: ids present and
// unique, every project of an organisation.
func TestCheckProjects(t *testing.T) {
	good := []Project{{ID: "a", Name: "A", Organisation: Ref{ID: "o"}}, {ID: "b", Name: "B", Organisation: Ref{ID: "o"}}}
	if err := CheckProjects(good); err != nil {
		t.Fatal(err)
	}
	for name, list := range map[string][]Project{
		"no id":           {{ID: "", Organisation: Ref{ID: "o"}}},
		"twice":           {{ID: "a", Organisation: Ref{ID: "o"}}, {ID: "a", Organisation: Ref{ID: "o"}}},
		"no organisation": {{ID: "a"}},
	} {
		if err := CheckProjects(list); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestTicketsAnswerSendsTheQueryAndDecodeTicketsReadsIt(t *testing.T) {
	api, sent := replayCase(t, "list_tickets", "ok")
	ans := api.TicketsAnswer(context.Background(), TicketQuery{ProjectID: website, Q: "Login", Page: 2, PerPage: 25})
	if !ans.OK() {
		t.Fatalf("%v %s", ans.Class, ans.Message)
	}
	sentExactly(t, *sent, map[string]any{"project_id": website, "q": "Login", "page": float64(2), "per_page": float64(25)})
	assertSentDeclared(t, TicketsSpec, *sent)
	page, decoded := DecodeTickets(ans)
	if !decoded.OK() || len(page.Data) != 2 || page.Data[0].Number != 1 || page.Data[1].Status == nil || *page.Data[1].Status != "new" || page.Meta.Total != 2 {
		t.Fatalf("%v %+v", decoded.Class, page)
	}
	// A blank search is not sent (the server reads q as a filter).
	api, sent = replayCase(t, "list_tickets", "ok")
	api.TicketsAnswer(context.Background(), TicketQuery{ProjectID: website, Page: 1, PerPage: 25})
	sentExactly(t, *sent, map[string]any{"project_id": website, "page": float64(1), "per_page": float64(25)})
	// Two decodes of one answer share nothing.
	first, _ := DecodeTickets(ans)
	second, _ := DecodeTickets(ans)
	first.Data[0].Title = "changed"
	if second.Data[0].Title == "changed" {
		t.Fatal("two decodes share a row")
	}
	if page, failed := DecodeTickets(transport.Answer{Class: transport.NotFound}); len(page.Data) != 0 || failed.Class != transport.NotFound {
		t.Fatalf("%+v %v", page, failed.Class)
	}
}

func TestCheckTicketPage(t *testing.T) {
	row := func(id string) TicketRow { return TicketRow{ID: id, Number: 1, Title: "t"} }
	good := TicketPage{Data: []TicketRow{row("a"), row("b")}}
	good.Meta.Page, good.Meta.PerPage, good.Meta.Total = 1, 25, 2
	if err := CheckTicketPage(good); err != nil {
		t.Fatal(err)
	}
	for name, edit := range map[string]func(p *TicketPage){
		"page 0":        func(p *TicketPage) { p.Meta.Page = 0 },
		"per page 0":    func(p *TicketPage) { p.Meta.PerPage = 0 },
		"negative":      func(p *TicketPage) { p.Meta.Total = -1 },
		"over per page": func(p *TicketPage) { p.Meta.PerPage = 1 },
		"over total":    func(p *TicketPage) { p.Meta.Total = 1 },
		"no id":         func(p *TicketPage) { p.Data[1].ID = "" },
		"twice":         func(p *TicketPage) { p.Data[1].ID = "a" },
	} {
		p := good
		p.Data = append([]TicketRow{}, good.Data...)
		edit(&p)
		if err := CheckTicketPage(p); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

// TicketAnswer and DecodeTicket are TicketWith in two halves: the same
// arguments, the same required fields per option, the same comment rule.
func TestTicketAnswerAndDecodeTicketAreTicketWithInTwoHalves(t *testing.T) {
	ctx := context.Background()
	paged := TicketOptions{PostsPerPage: 1, Newest: true}
	api, sent := replayCase(t, "get_ticket", "page_newest")
	ans := api.TicketAnswer(ctx, website, login, paged)
	sentExactly(t, *sent, map[string]any{"project_id": website, "ticket_id": login, "posts_per_page": float64(1), "posts_order": "newest"})
	ticket, decoded := DecodeTicket(ans, paged)
	if !decoded.OK() || ticket.ID != login || len(ticket.Posts) != 1 || ticket.PostsPages != 2 || ticket.PostsOrder != "newest" {
		t.Fatalf("%v %+v", decoded.Class, ticket)
	}
	api, _ = replayCase(t, "get_ticket", "page_newest")
	whole, direct := api.TicketWith(ctx, website, login, paged)
	if !direct.OK() || whole.ID != ticket.ID || len(whole.Posts) != len(ticket.Posts) || whole.Posts[0].ID != ticket.Posts[0].ID {
		t.Fatal("TicketWith and TicketAnswer+DecodeTicket differ")
	}
	// The page fields are required only with options: a plain answer
	// lacks them and still reads; a paged request needs them.
	plain := recordedText(t, "get_ticket", func(p map[string]any) {
		for _, k := range []string{"posts_page", "posts_per_page", "posts_pages", "posts_order", "attachments_page", "attachments_pages"} {
			delete(p, k)
		}
	})
	raw := replayText(t, plain).TicketAnswer(ctx, website, login, TicketOptions{})
	if _, ok := DecodeTicket(raw, TicketOptions{}); !ok.OK() {
		t.Fatalf("plain: %v %q", ok.Class, ok.Message)
	}
	if _, bad := DecodeTicket(raw, paged); bad.Class != transport.Protocol || !strings.Contains(bad.Message, "posts_page") {
		t.Fatalf("paged without the fields: %v %q", bad.Class, bad.Message)
	}
	// The comment rule: "not found" for one comment is a refusal, so the
	// cache keeps the ticket.
	api, _ = replayCase(t, "get_ticket", "post_not_found")
	one := TicketOptions{PostID: missing}
	if _, refused := DecodeTicket(api.TicketAnswer(ctx, website, login, one), one); refused.Class != transport.Refused || refused.Reason != CommentNotFound {
		t.Fatalf("got %v %q", refused.Class, refused.Reason)
	}
	// A page below zero is refused before anything is sent.
	if bad := api.TicketAnswer(ctx, website, login, TicketOptions{PostsPage: -1}); bad.Class != transport.InvalidInput || bad.Sent {
		t.Fatalf("got %v sent %v", bad.Class, bad.Sent)
	}
	// Two decodes of one answer share nothing.
	a, _ := DecodeTicket(ans, paged)
	b, _ := DecodeTicket(ans, paged)
	a.Posts[0].BodyText = "changed"
	if b.Posts[0].BodyText == "changed" {
		t.Fatal("two decodes share a comment")
	}
}

// What the spec cannot say about a ticket: its id, its project, comment
// ids present and unique, no negative total, and a page that echoes the
// options it was asked with.
func TestCheckTicket(t *testing.T) {
	var ticket Ticket
	_ = json.Unmarshal([]byte(recordedText(t, "get_ticket", func(map[string]any) {})), &ticket)
	if err := CheckTicket(ticket, TicketOptions{}); err != nil {
		t.Fatal(err)
	}
	paged := TicketOptions{PostsPage: 1, PostsPerPage: 25, Newest: true, AttachmentsPage: 1}
	pagedTicket := ticket
	pagedTicket.PostsPage, pagedTicket.PostsPerPage, pagedTicket.PostsPages, pagedTicket.PostsOrder = 1, 25, 1, "newest"
	pagedTicket.AttachmentsPage, pagedTicket.AttachmentsPages = 1, 0
	if err := CheckTicket(pagedTicket, paged); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		edit func(t *Ticket)
		o    TicketOptions
	}{
		"no id":                  {func(t *Ticket) { t.ID = "" }, TicketOptions{}},
		"no project":             {func(t *Ticket) { t.Project.ID = "" }, TicketOptions{}},
		"comment without id":     {func(t *Ticket) { t.Posts[0].ID = "" }, TicketOptions{}},
		"comment twice":          {func(t *Ticket) { t.Posts[1].ID = t.Posts[0].ID }, TicketOptions{}},
		"negative total":         {func(t *Ticket) { t.PostsTotal = -1 }, TicketOptions{}},
		"negative attachments":   {func(t *Ticket) { t.AttachmentsTotal = -1 }, TicketOptions{}},
		"another posts page":     {func(t *Ticket) { t.PostsPage = 2 }, paged},
		"another per page":       {func(t *Ticket) { t.PostsPerPage = 50 }, paged},
		"another order":          {func(t *Ticket) { t.PostsOrder = "oldest" }, paged},
		"more than a page":       {func(t *Ticket) { t.PostsPerPage = 1 }, TicketOptions{PostsPerPage: 1, Newest: true}},
		"negative pages":         {func(t *Ticket) { t.PostsPages = -1 }, paged},
		"another attach. page":   {func(t *Ticket) { t.AttachmentsPage = 2 }, paged},
		"negative attach. pages": {func(t *Ticket) { t.AttachmentsPages = -1 }, paged},
	} {
		x := pagedTicket
		x.Posts = append([]Post{}, pagedTicket.Posts...)
		tc.edit(&x)
		if err := CheckTicket(x, tc.o); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	// Above 50 the server answers 50: asking for more is not an error.
	big := pagedTicket
	big.PostsPerPage = 50
	if err := CheckTicket(big, TicketOptions{PostsPage: 1, PostsPerPage: 80, Newest: true, AttachmentsPage: 1}); err != nil {
		t.Fatal(err)
	}
}
