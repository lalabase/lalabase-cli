package inboxfixture

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/transport"
)

// A comment over REST (K3a) against the fixture, through the real adapter and
// the real transport.

// It is kept as the tool's is: the ticket reads it as the core stores it, and
// a repeat of the key writes nothing.
func TestACommentOverRESTIsKept(t *testing.T) {
	f, a, _ := serve(t, Normal)
	ctx := context.Background()
	id, key, typed := TicketID(ProjectWebsite, 6), "key-00000001", "\n Zeile 1\nZeile 2\n\n\nAbsatz <b>&\n \n"
	comment, ans := a.PostCommentREST(ctx, id, typed, key)
	if !ans.OK() || ans.Status != http.StatusCreated || comment.TicketID != id || comment.BodyText != " Zeile 1\nZeile 2\n\nAbsatz <b>&" {
		t.Fatalf("%v %d %+v", ans.Class, ans.Status, comment)
	}
	got, ans := a.Ticket(ctx, ProjectWebsite, id)
	if !ans.OK() || !slices.ContainsFunc(got.Posts, func(p api.Post) bool { return p.ID == comment.ID }) {
		t.Fatalf("%v, comments %+v", ans.Class, got.Posts)
	}
	if again, ans := a.PostCommentREST(ctx, id, typed, key); !ans.OK() || again.ID != comment.ID {
		t.Fatalf("the repeat: %v %+v", ans.Class, again)
	}
	if _, ans := a.PostCommentREST(ctx, id, "anders", key); ans.Class != transport.InvalidInput || ans.RefusalText() != textKeyMismatch {
		t.Fatalf("the mismatch: %v %q", ans.Class, ans.RefusalText())
	}
	if m := f.Mutations(); len(m) != 1 || m[0] != (Mutation{Kind: MutationComment, Ticket: id, Key: key}) {
		t.Fatalf("mutations %+v", m)
	}
}

// What a scenario makes of a comment, in the core's order: a bad input on a
// ticket without the right is a 422, on a vanished one the same 404. A lost
// answer writes it, a 429 comes once.
func TestTheScenariosOfACommentOverREST(t *testing.T) {
	website := func(n int) string { return TicketID(ProjectWebsite, n) }
	for _, tc := range []struct {
		name         string
		scenario     Scenario
		ticket, body string
		class        transport.Class
		text         string
		wrote        int
	}{
		{"blanks", Normal, website(6), " \n ", transport.InvalidInput, textCommentEmpty, 0},
		{"forbidden", Refusals, website(refusedWrite), "x", transport.NoAccess, "", 0},
		{"blanks before the right", Refusals, website(refusedWrite), " ", transport.InvalidInput, textCommentEmpty, 0},
		{"vanished before the input", Refusals, website(refusedVanished), " ", transport.NotFound, "", 0},
		{"read-only", ReadOnly, website(6), "x", transport.ReadOnly, "", 0},
		{"lost", Lost, website(6), "x", transport.Temporary, "", 1},
		{"a rate limit", RateLimited, website(6), "x", transport.Temporary, "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, a, _ := serve(t, tc.scenario)
			comment, ans := a.PostCommentREST(context.Background(), tc.ticket, tc.body, "key-00000003")
			if ans.Class != tc.class || !ans.Sent || ans.RefusalText() != tc.text || comment.ID != "" || len(f.Mutations()) != tc.wrote {
				t.Fatalf("%v %q %+v, mutations %+v", ans.Class, ans.RefusalText(), comment, f.Mutations())
			}
		})
	}
}
