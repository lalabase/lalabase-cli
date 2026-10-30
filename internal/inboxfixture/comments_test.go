package inboxfixture

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/contract"
	"github.com/gboyke/lalabase-cli/internal/transport"
)

// A comment over REST (K3a) against the fixture, through the real adapter and
// the real transport.

// unreadTicket is the number of a Website ticket with an unread row of OrgA.
func unreadTicket(t *testing.T, f *Fixture) int {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, row := range f.rows[OrgA] {
		ticket, _ := row["ticket"].(map[string]any)
		id, _ := ticket["id"].(string)
		if read, _ := row["read"].(bool); !read && id != "" {
			_, n := ticketByID(id)
			return n
		}
	}
	return 0
}

// It is kept as the tool's is: the ticket reads it, as the core stores it;
// the change and its key are logged, a repeat of the key writes nothing,
// core #989 reads the author's rows, and the answer has the recorded shape.
func TestACommentOverRESTIsKept(t *testing.T) {
	f, a, c := serve(t, Normal)
	ctx := context.Background()
	n := unreadTicket(t, f)
	id, key := TicketID(ProjectWebsite, n), "key-00000001"
	comment, ans := a.PostCommentREST(ctx, id, "\n Zeile 1\nZeile 2\n\n\nAbsatz <b>&\n \n", key)
	if !ans.OK() || ans.Status != http.StatusCreated || comment.TicketID != id || comment.BodyText != " Zeile 1\nZeile 2\n\nAbsatz <b>&" {
		t.Fatalf("%v %d %+v", ans.Class, ans.Status, comment)
	}
	got, ans := a.Ticket(ctx, ProjectWebsite, id)
	if !ans.OK() || !slices.ContainsFunc(got.Posts, func(p api.Post) bool { return p.ID == comment.ID }) {
		t.Fatalf("%v, comments %+v", ans.Class, got.Posts)
	}
	if again, ans := a.PostCommentREST(ctx, id, "\n Zeile 1\nZeile 2\n\n\nAbsatz <b>&\n \n", key); !ans.OK() || again.ID != comment.ID {
		t.Fatalf("the repeat: %v %+v", ans.Class, again)
	}
	if _, ans := a.PostCommentREST(ctx, id, "anders", key); ans.Class != transport.InvalidInput || ans.RefusalText() != textKeyMismatch {
		t.Fatalf("the mismatch: %v %q", ans.Class, ans.RefusalText())
	}
	if m := f.Mutations(); len(m) != 1 || m[0] != (Mutation{Kind: MutationComment, Ticket: id, Key: key}) {
		t.Fatalf("mutations %+v", m)
	}
	log := f.Requests()
	if at := slices.IndexFunc(log, func(r Request) bool { return r.Route == "posts.create" }); at < 0 || log[at].Status != http.StatusCreated || log[at].Args["format"] != "plain" {
		t.Fatalf("logged %+v", log)
	}
	if unreadTicket(t, f) == n {
		t.Fatal("the author's row stayed unread")
	}
	file, err := contract.Load()
	if err != nil {
		t.Fatal(err)
	}
	var recorded, answered struct {
		Data map[string]any `json:"data"`
	}
	_ = json.Unmarshal(file.Contract.REST["POST /api/v1/tickets/:ticket_id/posts"]["plain"].Response.Body, &recorded)
	sent := c.Write(ctx, http.MethodPost, "/api/v1/tickets/"+id+"/posts", json.RawMessage(`{"post":{"format":"plain","body":"x"}}`), "key-00000002")
	if err := json.Unmarshal(sent.Payload, &answered); err != nil {
		t.Fatal(err)
	}
	if want, have := slices.Sorted(maps.Keys(recorded.Data)), slices.Sorted(maps.Keys(answered.Data)); !slices.Equal(want, have) {
		t.Fatalf("fields %v, recorded %v", have, want)
	}
}

// What a scenario makes of a comment, in the core's order: a bad input on a
// ticket without the right is a 422, on a vanished one the same 404.
func TestTheRefusalsOfACommentOverREST(t *testing.T) {
	website := func(n int) string { return TicketID(ProjectWebsite, n) }
	for _, tc := range []struct {
		name         string
		scenario     Scenario
		ticket, body string
		class        transport.Class
		text         string
	}{
		{"blanks", Normal, website(6), " \n ", transport.InvalidInput, textCommentEmpty},
		{"a NUL", Normal, website(6), "a\x00b", transport.InvalidInput, textCommentNUL},
		{"forbidden", Refusals, website(refusedWrite), "x", transport.NoAccess, ""},
		{"forbidden, as the tool's", Refusals, website(refusedValues), "x", transport.NoAccess, ""},
		{"blanks before the right", Refusals, website(refusedWrite), " ", transport.InvalidInput, textCommentEmpty},
		{"vanished before the input", Refusals, website(refusedVanished), " ", transport.NotFound, ""},
		{"no such ticket", Normal, "00000000-0000-4000-8000-000000000999", "x", transport.NotFound, ""},
		{"read-only", ReadOnly, website(6), "x", transport.ReadOnly, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, a, _ := serve(t, tc.scenario)
			comment, ans := a.PostCommentREST(context.Background(), tc.ticket, tc.body, "key-00000003")
			if ans.Class != tc.class || ans.RefusalText() != tc.text || comment.ID != "" || len(f.Mutations()) != 0 {
				t.Fatalf("%v %q %+v, mutations %+v", ans.Class, ans.RefusalText(), comment, f.Mutations())
			}
		})
	}
}

// A lost answer: the comment is written, nobody hears of it. A 429 comes
// once per ticket: the next try writes one.
func TestALostAndARateLimitedComment(t *testing.T) {
	ctx := context.Background()
	six := TicketID(ProjectWebsite, 6)
	f, a, _ := serve(t, Lost)
	if _, ans := a.PostCommentREST(ctx, six, "x", "key-00000004"); ans.OK() || !ans.Sent || len(f.Mutations()) != 1 {
		t.Fatalf("lost: %v sent %v, mutations %+v", ans.Class, ans.Sent, f.Mutations())
	}
	f, a, _ = serve(t, RateLimited)
	if _, ans := a.PostCommentREST(ctx, six, "x", "key-00000005"); ans.Status != http.StatusTooManyRequests || len(f.Mutations()) != 0 {
		t.Fatalf("429: %d, mutations %+v", ans.Status, f.Mutations())
	}
	if _, ans := a.PostCommentREST(ctx, six, "x", "key-00000005"); !ans.OK() || len(f.Mutations()) != 1 {
		t.Fatalf("the retry: %v, mutations %+v", ans.Class, f.Mutations())
	}
}
