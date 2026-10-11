package inboxfixture

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/features"
	"github.com/gboyke/lalabase-cli/internal/transport"
)

// The writes of C2 (C2a) against the fixture through the real adapters,
// and through the raw transport where the adapter refuses before sending.

// rawUpdate and rawComment send a tools/call as written, past the
// adapter's own checks.
func rawUpdate(c *transport.Client, args map[string]any) transport.Answer {
	return c.CallTool(context.Background(), transport.Call{Tool: "update_ticket", Args: args, OneRecord: true})
}

func rawComment(c *transport.Client, args map[string]any) transport.Answer {
	return c.CallTool(context.Background(), transport.Call{Tool: "post_comment", Args: args, OneRecord: true})
}

// A status, an assignment and a title on ticket #6: each answers the
// summary with the change, the list and the ticket show it, a key seen
// before answers the original result without a second change, and the
// refusals the request alone decides come with the server's texts.
func TestTheTicketWritesWorkAgainstTheFixture(t *testing.T) {
	f, a, c := serve(t, Normal)
	ctx := context.Background()
	six := TicketID(ProjectWebsite, 6)
	row, ans := a.UpdateTicket(ctx, ProjectWebsite, six, api.TicketChange{Status: "resolved"}, "key-00000001")
	if !ans.OK() || row.ID != six || row.Number != 6 || row.Status == nil || *row.Status != "resolved" || row.UpdatedAt != writeStamp || row.Title != "Ticket 6" {
		t.Fatalf("status: %v %q %+v", ans.Class, ans.Message, row)
	}
	page, _ := a.Tickets(ctx, api.TicketQuery{ProjectID: ProjectWebsite, Page: 1, PerPage: 25})
	if s := page.Data[5].Status; s == nil || *s != "resolved" || page.Data[4].Status == nil || *page.Data[4].Status != "new" {
		t.Fatalf("the list does not show the status: %+v / %+v", page.Data[5], page.Data[4])
	}
	if f := page.Data[5].StatusFunctions; len(f) != 1 || f[0] != "is_completed" || len(row.StatusFunctions) != 1 || row.StatusFunctions[0] != "is_completed" {
		t.Fatalf("the new status keeps the old meaning: %q / %q", f, row.StatusFunctions)
	}
	// #6 is unassigned (every third ticket): assigning me gives one
	// assignee; #1's recorded assignee is me already, so I stay one.
	row, ans = a.UpdateTicket(ctx, ProjectWebsite, six, api.TicketChange{AddMe: true}, "key-00000002")
	if !ans.OK() || len(row.Assignees) != 1 || row.Assignees[0].ID != f.meID || row.Assignees[0].Name != "Anna Albers" {
		t.Fatalf("assign: %v %q %+v", ans.Class, ans.Message, row.Assignees)
	}
	// Assigned already: one more call, nobody twice.
	row, ans = a.UpdateTicket(ctx, ProjectWebsite, six, api.TicketChange{AddMe: true}, "key-00000003")
	if !ans.OK() || len(row.Assignees) != 1 {
		t.Fatalf("second assign: %v %+v", ans.Class, row.Assignees)
	}
	if one, ans := a.UpdateTicket(ctx, ProjectWebsite, TicketLogin, api.TicketChange{AddMe: true}, "key-00000003b"); !ans.OK() || len(one.Assignees) != 1 || !slices.ContainsFunc(one.Assignees, func(r api.Ref) bool { return r.ID == f.meID }) {
		t.Fatalf("assign on #1: %v %+v", ans.Class, one.Assignees)
	}
	row, ans = a.UpdateTicket(ctx, ProjectWebsite, six, api.TicketChange{Title: "Ticket 6 (CSV)"}, "key-00000004")
	if !ans.OK() || row.Title != "Ticket 6 (CSV)" {
		t.Fatalf("title: %v %q %+v", ans.Class, ans.Message, row)
	}
	o := api.TicketOptions{PostsPage: 1, PostsPerPage: 25, Newest: true, AttachmentsPage: 1}
	ticket, ans := a.TicketWith(ctx, ProjectWebsite, six, o)
	if !ans.OK() || api.CheckTicket(ticket, o) != nil || ticket.Title != "Ticket 6 (CSV)" || ticket.Status == nil || *ticket.Status != "resolved" || len(ticket.Assignees) != len(row.Assignees) {
		t.Fatalf("the ticket does not show the changes: %v %+v", ans.Class, ticket)
	}
	again, ans := a.UpdateTicket(ctx, ProjectWebsite, six, api.TicketChange{Title: "other"}, "key-00000004")
	if !ans.OK() || again.Title != "Ticket 6 (CSV)" {
		t.Fatalf("replay: %v %+v", ans.Class, again)
	}
	if got := f.Mutations(); len(got) != 5 || got[0] != (Mutation{Kind: MutationStatus, Ticket: six, Key: "key-00000001"}) || got[1].Kind != MutationAssign || got[4] != (Mutation{Kind: MutationTitle, Ticket: six, Key: "key-00000004"}) {
		t.Fatalf("mutations %+v", got)
	}
	if got := f.Keys(); len(got) != 6 || got[5] != "key-00000004" {
		t.Fatalf("keys %v", got)
	}
	// A ticket the fixture does not have, a project it does not have, and
	// (through the adapter) a status that is no key of the project.
	if _, ans := a.UpdateTicket(ctx, ProjectWebsite, "00000000-0000-4000-8000-000000000999", api.TicketChange{Status: "new"}, "key-00000005"); ans.Class != transport.NotFound {
		t.Fatalf("a missing ticket: %v", ans.Class)
	}
	if _, ans := a.UpdateTicket(ctx, "00000000-0000-4000-8000-000000000034", six, api.TicketChange{Status: "new"}, "key-00000006"); ans.Class != transport.NotFound {
		t.Fatalf("an unknown project: %v", ans.Class)
	}
	if _, ans := a.UpdateTicket(ctx, ProjectWebsite, six, api.TicketChange{Status: "erledigt"}, "key-00000007"); ans.Class != transport.Refused || ans.Reason != "value_not_allowed" || ans.Message != textStatusNotAllowed {
		t.Fatalf("an unknown status: %v %q %q", ans.Class, ans.Reason, ans.Message)
	}
	// What the request alone decides, as the server says it.
	base := map[string]any{"project_id": ProjectWebsite, "ticket_id": six}
	with := func(kv ...any) map[string]any {
		m := copyMap(base)
		for i := 0; i < len(kv); i += 2 {
			m[kv[i].(string)] = kv[i+1]
		}
		return m
	}
	for name, tc := range map[string]struct {
		args map[string]any
		text string
	}{
		"unknown argument": {with("priority", "high", "status", "new"), "unknown arguments: priority"},
		"mixed":            {with("title", "x", "status", "new"), textOneChange},
		"nothing":          {with(), textOneChange},
		"no ticket id":     {map[string]any{"project_id": ProjectWebsite, "ticket_id": " ", "status": "new"}, "ticket_id is required"},
		"bad ticket id":    {map[string]any{"project_id": ProjectWebsite, "ticket_id": "t-1", "status": "new"}, "invalid ticket_id format"},
		"title not string": {with("title", 1), "title must be a string"},
		"title two lines":  {with("title", "a\nb"), "title must be one line without control characters"},
		"title bidi":       {with("title", "a\u202eb"), "title must be one line without control characters"},
		"title blank":      {with("title", " \u00a0 "), "title is required"},
		"title long":       {with("title", strings.Repeat("x", 256)), "title is longer than 255 characters"},
		"status blank":     {with("status", " "), "status is required"},
		"status number":    {with("status", 1), "status must be a string"},
		"assignees text":   {with("add_assignee_ids", "me"), `add_assignee_ids must be a list of user UUIDs or "me"`},
		"assignee bad id":  {with("add_assignee_ids", []any{"ben"}), "invalid user id in add_assignee_ids"},
		"assignees none":   {with("add_assignee_ids", []any{}), "add_assignee_ids or remove_assignee_ids must name at least one user"},
		"assignee twice":   {with("add_assignee_ids", []any{"me"}, "remove_assignee_ids", []any{"me"}), "add_assignee_ids and remove_assignee_ids name the same user"},
	} {
		if ans := rawUpdate(c, tc.args); ans.Class != transport.InvalidInput || ans.Message != tc.text {
			t.Errorf("%s: %v %q", name, ans.Class, ans.Message)
		}
	}
	// A person not in the project cannot be assigned; a title of 255
	// characters is the longest allowed.
	if ans := rawUpdate(c, with("add_assignee_ids", []any{"00000000-0000-4000-8000-000000000999"})); ans.Class != transport.Refused || ans.Reason != "value_not_allowed" || ans.Message != textUserNotAllowed {
		t.Errorf("a stranger: %v %q %q", ans.Class, ans.Reason, ans.Message)
	}
	if ans := rawUpdate(c, with("title", " "+strings.Repeat("y", 255)+" ")); !ans.OK() {
		t.Errorf("a title of 255: %v %q", ans.Class, ans.Message)
	}
	// Removing me takes me out again (the server takes the list; the
	// client never sends it in C2).
	if ans := rawUpdate(c, with("remove_assignee_ids", []any{"me"})); !ans.OK() || !strings.Contains(string(ans.Payload), `"assignees":[]`) {
		t.Errorf("remove me: %v %s", ans.Class, ans.Payload)
	}
	if len(f.Mutations()) != 7 {
		t.Fatalf("mutations %+v", f.Mutations())
	}
}

// A comment on ticket #6: the answer names the ticket, the body comes
// back as sent (plain) with the edge blank lines gone, the ticket's page 1
// newest first has it on top, and the author's unread row naming the
// ticket is read (core #989), so the counts drop.
func TestPostCommentWorksAgainstTheFixture(t *testing.T) {
	f, a, c := serve(t, Normal)
	ctx := context.Background()
	six := TicketID(ProjectWebsite, 6)
	a1 := api.NotificationQuery{Status: "unread", Page: 1, PerPage: 25, OrganisationID: OrgA}
	before, _ := a.Notifications(ctx, a1)
	if before.Data[5].Ticket == nil || before.Data[5].Ticket.ID != six || before.Data[5].Read {
		t.Fatalf("row 6 does not name ticket #6 unread: %+v", before.Data[5].Ticket)
	}
	body := "\n\nline one\r\nline two\n\nsee <file.txt> &lt;tag&gt;\n \n"
	comment, ans := a.PostComment(ctx, ProjectWebsite, six, body, api.CommentOptions{Plain: true}, "key-00000001")
	if !ans.OK() || comment.TicketID != six || comment.BodyText != "line one\nline two\n\nsee <file.txt> &lt;tag&gt;" || comment.Author == nil || comment.Author.Name != "Anna Albers" || comment.IsPrivate || comment.ID == "" {
		t.Fatalf("comment: %v %q %+v", ans.Class, ans.Message, comment)
	}
	o := api.TicketOptions{PostsPage: 1, PostsPerPage: 25, Newest: true, AttachmentsPage: 1}
	ticket, ans := a.TicketWith(ctx, ProjectWebsite, six, o)
	if !ans.OK() || api.CheckTicket(ticket, o) != nil || ticket.PostsTotal != 31 || ticket.Posts[0].ID != comment.ID || ticket.Posts[0].BodyText != comment.BodyText || ticket.PostsPages != 2 {
		t.Fatalf("the ticket does not show the comment: %v %d comments, first %+v", ans.Class, ticket.PostsTotal, ticket.Posts[0])
	}
	if one, ans := a.TicketWith(ctx, ProjectWebsite, six, api.TicketOptions{PostID: comment.ID}); !ans.OK() || len(one.Posts) != 1 || one.Posts[0].ID != comment.ID {
		t.Fatalf("the comment whole: %v %+v", ans.Class, one.Posts)
	}
	after, _ := a.Notifications(ctx, a1)
	if after.Meta.Total != before.Meta.Total-1 || after.Meta.ByOrganisation[0].Unread != before.Meta.Total-1 {
		t.Fatalf("after the comment: %+v", after.Meta)
	}
	for _, n := range after.Data {
		if n.Ticket != nil && n.Ticket.ID == six {
			t.Fatal("the row naming the ticket is still unread")
		}
	}
	if global, _ := a.Notifications(ctx, api.NotificationQuery{Status: "unread", Page: 1, PerPage: 25}); global.Meta.UnreadTotal != 32 {
		t.Fatalf("global after the comment: %+v", global.Meta)
	}
	again, ans := a.PostComment(ctx, ProjectWebsite, six, "other", api.CommentOptions{Plain: true}, "key-00000001")
	if !ans.OK() || again.ID != comment.ID || again.BodyText != comment.BodyText {
		t.Fatalf("replay: %v %+v", ans.Class, again)
	}
	// An HTML body (no format, the request of an older client): the text
	// comes back without its tags.
	html, ans := a.PostComment(ctx, ProjectWebsite, six, "<p><b>bold</b></p>", api.CommentOptions{}, "key-00000002")
	if !ans.OK() || html.BodyText != "bold" || html.ID == comment.ID {
		t.Fatalf("html: %v %+v", ans.Class, html)
	}
	if got := f.Mutations(); len(got) != 2 || got[0] != (Mutation{Kind: MutationComment, Ticket: six, Key: "key-00000001"}) {
		t.Fatalf("mutations %+v", got)
	}
	// What the request alone decides, as the server says it.
	base := map[string]any{"project_id": ProjectWebsite, "ticket_id": six, "format": "plain", "body": "x"}
	with := func(kv ...any) map[string]any {
		m := copyMap(base)
		for i := 0; i < len(kv); i += 2 {
			m[kv[i].(string)] = kv[i+1]
		}
		return m
	}
	for name, tc := range map[string]struct {
		args map[string]any
		text string
	}{
		"invalid format":    {with("format", "markdown"), textInvalidFormat},
		"format not string": {with("format", 1), textFormatNotString},
		"nul":               {with("body", "a\x00b"), "body must not contain NUL bytes"},
		"whitespace":        {with("body", " \n\t"), "body is required"},
		"unicode space":     {with("body", "\u00a0"), "body is required"},
		"body not string":   {with("body", 1), "Invalid arguments: value at `/body` is not a string"},
		"no ticket id":      {with("ticket_id", ""), "ticket_id is required"},
		"bad ticket id":     {with("ticket_id", "t-1"), "invalid ticket_id format"},
	} {
		if ans := rawComment(c, tc.args); ans.Class != transport.InvalidInput || ans.Message != tc.text {
			t.Errorf("%s: %v %q", name, ans.Class, ans.Message)
		}
	}
	if ans := rawComment(c, with("ticket_id", "00000000-0000-4000-8000-000000000999")); ans.Class != transport.NotFound {
		t.Errorf("a missing ticket: %v", ans.Class)
	}
	if ans := rawComment(c, with("project_id", "00000000-0000-4000-8000-000000000034")); ans.Class != transport.NotFound {
		t.Errorf("an unknown project: %v", ans.Class)
	}
	if ans := rawComment(c, with("is_private", true, "body", "<private>")); !ans.OK() || !strings.Contains(string(ans.Payload), `"is_private":true`) {
		t.Errorf("private: %v %s", ans.Class, ans.Payload)
	}
	// Nothing changed meanwhile.
	if len(f.Mutations()) != 3 {
		t.Fatalf("mutations %+v", f.Mutations())
	}
}

// The scenarios of the ticket writes: each does what it says on both
// tools, and nothing else changes.
func TestTheTicketWriteScenarios(t *testing.T) {
	ctx := context.Background()
	six := TicketID(ProjectWebsite, 6)
	status := func(a *api.API, key string) transport.Answer {
		_, ans := a.UpdateTicket(ctx, ProjectWebsite, six, api.TicketChange{Status: "resolved"}, key)
		return ans
	}
	comment := func(a *api.API, key string) transport.Answer {
		_, ans := a.PostComment(ctx, ProjectWebsite, six, "x", api.CommentOptions{Plain: true}, key)
		return ans
	}
	t.Run("read-only", func(t *testing.T) {
		f, a, _ := serve(t, ReadOnly)
		if ans := status(a, "key-00000001"); ans.Class != transport.ReadOnly {
			t.Fatalf("status: %v", ans.Class)
		}
		if ans := comment(a, "key-00000002"); ans.Class != transport.ReadOnly {
			t.Fatalf("comment: %v", ans.Class)
		}
		if len(f.Mutations()) != 0 {
			t.Fatal("a read-only token wrote")
		}
	})
	t.Run("401", func(t *testing.T) {
		f, a, _ := serve(t, Unauthorized)
		if ans := status(a, "key-00000001"); ans.Class != transport.Unauthenticated {
			t.Fatalf("status: %v", ans.Class)
		}
		if ans := comment(a, "key-00000002"); ans.Class != transport.Unauthenticated {
			t.Fatalf("comment: %v", ans.Class)
		}
		if len(f.Mutations()) != 0 {
			t.Fatal("a refused token wrote")
		}
	})
	t.Run("429", func(t *testing.T) {
		f, a, _ := serve(t, RateLimited)
		for name, write := range map[string]func(*api.API, string) transport.Answer{"status": status, "comment": comment} {
			if ans := write(a, "key-"+name); ans.Class != transport.Temporary || ans.Status != http.StatusTooManyRequests {
				t.Fatalf("%s attempt 1: %v %d", name, ans.Class, ans.Status)
			}
			if ans := write(a, "key-"+name); !ans.OK() {
				t.Fatalf("%s attempt 2: %v", name, ans.Class)
			}
		}
		if got := f.Mutations(); len(got) != 2 {
			t.Fatalf("mutations %+v", got)
		}
	})
	t.Run("lost", func(t *testing.T) {
		f, a, _ := serve(t, Lost)
		if ans := status(a, "key-00000001"); ans.Class != transport.Temporary || !ans.Sent {
			t.Fatalf("status: %v sent %v", ans.Class, ans.Sent)
		}
		if ans := comment(a, "key-00000002"); ans.Class != transport.Temporary || !ans.Sent {
			t.Fatalf("comment: %v sent %v", ans.Class, ans.Sent)
		}
		if got := f.Mutations(); len(got) != 2 || got[0].Kind != MutationStatus || got[1].Kind != MutationComment {
			t.Fatalf("mutations %+v", got)
		}
		ticket, _ := a.TicketWith(ctx, ProjectWebsite, six, api.TicketOptions{PostsPage: 1, PostsPerPage: 25, Newest: true, AttachmentsPage: 1})
		if ticket.Status == nil || *ticket.Status != "resolved" || ticket.PostsTotal != 31 {
			t.Fatalf("the lost writes were not carried out: %+v", ticket)
		}
	})
	t.Run("slow", func(t *testing.T) {
		_, a, _ := serve(t, Slow)
		started := time.Now()
		if ans := comment(a, "key-00000001"); !ans.OK() {
			t.Fatalf("comment: %v", ans.Class)
		}
		if time.Since(started) < slowAnswer {
			t.Fatal("the answer came at once")
		}
	})
	t.Run("refusals", func(t *testing.T) {
		f, a, _ := serve(t, Refusals)
		ticket := func(n int) string { return TicketID(ProjectWebsite, n) }
		cases := []struct {
			name   string
			run    func() transport.Answer
			class  transport.Class
			reason string
		}{
			{"#2 status", func() transport.Answer {
				_, ans := a.UpdateTicket(ctx, ProjectWebsite, ticket(2), api.TicketChange{Status: "new"}, "key-00000001")
				return ans
			}, transport.Refused, "value_not_allowed"},
			{"#2 assign", func() transport.Answer {
				_, ans := a.UpdateTicket(ctx, ProjectWebsite, ticket(2), api.TicketChange{AddMe: true}, "key-00000002")
				return ans
			}, transport.Refused, "value_not_allowed"},
			{"#2 title", func() transport.Answer {
				_, ans := a.UpdateTicket(ctx, ProjectWebsite, ticket(2), api.TicketChange{Title: "x"}, "key-00000003")
				return ans
			}, transport.Refused, "validation_failed"},
			{"#2 comment", func() transport.Answer {
				_, ans := a.PostComment(ctx, ProjectWebsite, ticket(2), "x", api.CommentOptions{Plain: true}, "key-00000004")
				return ans
			}, transport.NoAccess, ""},
			{"#3 status", func() transport.Answer {
				_, ans := a.UpdateTicket(ctx, ProjectWebsite, ticket(3), api.TicketChange{Status: "new"}, "key-00000005")
				return ans
			}, transport.Refused, "no_status_field"},
			{"#4 title", func() transport.Answer {
				_, ans := a.UpdateTicket(ctx, ProjectWebsite, ticket(4), api.TicketChange{Title: "x"}, "key-00000006")
				return ans
			}, transport.NoAccess, ""},
			{"#5 comment", func() transport.Answer {
				_, ans := a.PostComment(ctx, ProjectWebsite, ticket(5), "x", api.CommentOptions{Plain: true}, "key-00000007")
				return ans
			}, transport.NotFound, ""},
			{"#7 assign", func() transport.Answer {
				_, ans := a.UpdateTicket(ctx, ProjectWebsite, ticket(7), api.TicketChange{AddMe: true}, "key-00000008")
				return ans
			}, transport.Refused, "user_not_assignable"},
			{"#6 status", func() transport.Answer {
				_, ans := a.UpdateTicket(ctx, ProjectWebsite, ticket(6), api.TicketChange{Status: "new"}, "key-00000009")
				return ans
			}, transport.Object, ""},
			{"#3 title", func() transport.Answer {
				_, ans := a.UpdateTicket(ctx, ProjectWebsite, ticket(3), api.TicketChange{Title: "x"}, "key-00000010")
				return ans
			}, transport.Object, ""},
		}
		for _, c := range cases {
			if ans := c.run(); ans.Class != c.class || ans.Reason != c.reason || !ans.Sent || (ans.Class != transport.Object && ans.Message == "") {
				t.Errorf("%s: %v %q %q", c.name, ans.Class, ans.Reason, ans.Message)
			}
		}
		if got := f.Mutations(); len(got) != 2 {
			t.Fatalf("mutations %+v", got)
		}
	})
	t.Run("older servers", func(t *testing.T) {
		for scenario, want := range map[Scenario]struct{ update, plain bool }{Older: {false, false}, S1Only: {false, false}, S2Only: {true, false}, Normal: {true, true}} {
			_, a, c := serve(t, scenario)
			info, _ := c.Initialize(ctx)
			tools, _ := c.ListTools(ctx)
			report := features.Detect(info, tools)
			if report.Enabled["ticket_update"] != want.update || report.Enabled["comment_plain"] != want.plain || !report.Enabled["comment"] || !report.Enabled["inbox"] {
				t.Errorf("%s: %+v", scenario, report.Enabled)
			}
			// A tool the server does not have is unknown to it; a format
			// it does not have is ignored, so the body is HTML to it.
			ans := status(a, "key-00000001")
			if want.update != ans.OK() || (!want.update && ans.Class != transport.InvalidInput) {
				t.Errorf("%s update: %v %q", scenario, ans.Class, ans.Message)
			}
			posted, ans := a.PostComment(ctx, ProjectWebsite, six, "a <b>x</b>", api.CommentOptions{Plain: true}, "key-00000002")
			if !ans.OK() || (want.plain && posted.BodyText != "a <b>x</b>") || (!want.plain && posted.BodyText != "a x") {
				t.Errorf("%s comment: %v %q", scenario, ans.Class, posted.BodyText)
			}
		}
	})
}
