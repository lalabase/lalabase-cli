package inboxfixture

import (
	"context"
	"reflect"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/contract"
)

// C12: comments with their files, in the server's shape: on #1 the recorded
// one, on #9 a comment of every kind. Every answer passes the client's own
// adapters, on both ways.

// fileNames are the names of a comment's list; nil for no list.
func fileNames(p api.Post) []string {
	if p.Attachments == nil {
		return nil
	}
	names := []string{}
	for _, f := range p.Attachments {
		names = append(names, f.Filename)
	}
	return names
}

func TestTheRecordedCommentCarriesItsFileOnBothWays(t *testing.T) {
	ctx := context.Background()
	_, a, _ := serve(t, Normal)
	o := api.TicketOptions{PostsPage: 1, PostsPerPage: 25, AttachmentsPage: 1}
	tools, ans := a.TicketWith(ctx, ProjectWebsite, TicketLogin, o)
	if !ans.OK() {
		t.Fatalf("%v %q", ans.Class, ans.Message)
	}
	rest, ans := api.DecodeTicketREST(a.TicketRESTAnswer(ctx, TicketLogin, o), o)
	if !ans.OK() {
		t.Fatalf("%v %q", ans.Class, ans.Message)
	}
	if !reflect.DeepEqual(tools.Posts, rest.Posts) {
		t.Fatalf("the ways differ:\n%+v\n%+v", tools.Posts, rest.Posts)
	}
	first := rest.Posts[0]
	if first.AttachmentsCount != 1 || len(first.Attachments) != 1 || first.Attachments[0].ID == nil || *first.Attachments[0].ID != "00000000-0000-4000-8000-000000000053" || first.Attachments[0].Filename != "test_file.txt" {
		t.Fatalf("the recorded comment: %+v", first)
	}
	if second := rest.Posts[1]; second.Attachments == nil || len(second.Attachments) != 0 {
		t.Fatalf("the other comment: %+v", second)
	}
}

func TestTicketNineHasACommentOfEveryKind(t *testing.T) {
	ctx := context.Background()
	_, a, _ := serve(t, Normal)
	id := TicketID(ProjectWebsite, 9)
	o := api.TicketOptions{PostsPage: 1, PostsPerPage: 25, AttachmentsPage: 1}
	tools, ans := a.TicketWith(ctx, ProjectWebsite, id, o)
	if !ans.OK() {
		t.Fatalf("%v %q", ans.Class, ans.Message)
	}
	rest, ans := api.DecodeTicketREST(a.TicketRESTAnswer(ctx, id, o), o)
	if !ans.OK() {
		t.Fatalf("%v %q", ans.Class, ans.Message)
	}
	if !reflect.DeepEqual(tools.Posts, rest.Posts) {
		t.Fatalf("the ways differ:\n%+v\n%+v", tools.Posts, rest.Posts)
	}
	want := []struct {
		names   []string
		count   int
		private bool
	}{
		{[]string{"nine-shot.png", "nine-trace.log"}, 2, false},
		{[]string{"nine-private.pdf"}, 1, true},
		{nil, 2, false},
		{[]string{}, 1, false},
	}
	if len(rest.Posts) != len(want) {
		t.Fatalf("%d comments", len(rest.Posts))
	}
	for i, c := range want {
		p := rest.Posts[i]
		if !reflect.DeepEqual(fileNames(p), c.names) || p.AttachmentsCount != c.count || p.IsPrivate != c.private {
			t.Errorf("comment %d: %+v", i, p)
		}
	}
	// One comment read alone brings its files too.
	one, ans := a.TicketWith(ctx, ProjectWebsite, id, api.TicketOptions{PostID: rest.Posts[1].ID})
	if !ans.OK() || len(one.Posts) != 1 || !reflect.DeepEqual(fileNames(one.Posts[0]), want[1].names) {
		t.Fatalf("%v %q %+v", ans.Class, ans.Message, one)
	}
}

// A scenario that stands for a server before R7 sends the count alone.
func TestAServerBeforeR7SendsTheCountAlone(t *testing.T) {
	ctx := context.Background()
	check := func(t *testing.T, a *api.API) {
		t.Helper()
		ticket, ans := a.Ticket(ctx, ProjectWebsite, TicketLogin)
		if !ans.OK() {
			t.Fatalf("%v %q", ans.Class, ans.Message)
		}
		if p := ticket.Posts[0]; p.Attachments != nil || p.AttachmentsCount != 1 {
			t.Fatalf("comment %+v", p)
		}
	}
	for _, scenario := range []Scenario{Older, S1Only, S2Only} {
		t.Run(string(scenario), func(t *testing.T) {
			_, a, _ := serve(t, scenario)
			check(t, a)
		})
	}
	t.Run("before R1", func(t *testing.T) {
		f, err := New(Normal, contract.Raw(), BeforeR1())
		if err != nil {
			t.Fatal(err)
		}
		a, _ := clientOf(t, f)
		check(t, a)
	})
}
