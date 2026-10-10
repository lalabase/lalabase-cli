package gateway

import (
	"testing"

	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/cache"
	"github.com/gboyke/lalabase-cli/internal/queue"
)

// C1d: the ticket is read page by page, in either order, and its
// description or one comment whole. The pages are the Detail area's; an
// expansion is a read of its own, in the Expansion area, so closing it
// refuses its late answer without touching the pages.

func TestTheChecksOfTheTicketsC1dSends(t *testing.T) {
	ns := confirmed(t)
	s := NewScope(ns)
	paged := func(page int, newest bool, attachments int) cache.Key {
		k := detailKey(ns, website, login, true)
		k.PostsPage, k.Newest, k.AttachmentsPage = page, newest, attachments
		return k
	}
	with := func(k cache.Key, change func(*cache.Key)) cache.Key { change(&k); return k }
	good := map[string]Ticket{
		"comments page 3, oldest first":       s.Begin(Detail, paged(3, false, 1), queue.User),
		"attachments page 2":                  s.Begin(Detail, paged(1, true, 2), queue.User),
		"the description whole, paged":        s.Begin(Expansion, with(paged(2, false, 3), func(k *cache.Key) { k.FullText = true }), queue.User),
		"the description whole, older server": s.Begin(Expansion, with(detailKey(ns, website, login, false), func(k *cache.Key) { k.FullText = true }), queue.User),
		"one comment whole":                   s.Begin(Expansion, with(detailKey(ns, website, login, false), func(k *cache.Key) { k.Post = "c-1" }), queue.User),
	}
	for name, g := range good {
		if err := CheckReadTicket(g); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	bad := map[string]Ticket{
		"a page without its size":              s.Begin(Detail, with(paged(2, true, 1), func(k *cache.Key) { k.PostsPerPage = 0 }), queue.User),
		"comments paged, attachments not":      s.Begin(Detail, paged(2, true, 0), queue.User),
		"attachments paged, comments not":      s.Begin(Detail, with(paged(0, false, 2), func(k *cache.Key) { k.PostsPerPage = 0 }), queue.User),
		"newest first without a page":          s.Begin(Detail, with(detailKey(ns, website, login, false), func(k *cache.Key) { k.Newest = true }), queue.User),
		"another page size":                    s.Begin(Detail, with(paged(1, true, 1), func(k *cache.Key) { k.PostsPerPage = 50 }), queue.User),
		"an expansion in the pages' area":      s.Begin(Detail, with(paged(1, true, 1), func(k *cache.Key) { k.FullText = true }), queue.User),
		"a comment in the pages' area":         s.Begin(Detail, with(detailKey(ns, website, login, false), func(k *cache.Key) { k.Post = "c-1" }), queue.User),
		"a page in the expansions' area":       s.Begin(Expansion, paged(1, true, 1), queue.User),
		"a plain read in the expansions' area": s.Begin(Expansion, detailKey(ns, website, login, false), queue.User),
		"a comment with a page":                s.Begin(Expansion, with(paged(1, true, 1), func(k *cache.Key) { k.Post = "c-1" }), queue.User),
		"a comment with the whole description": s.Begin(Expansion, with(detailKey(ns, website, login, false), func(k *cache.Key) { k.Post, k.FullText = "c-1", true }), queue.User),
		"a comment newest first":               s.Begin(Expansion, with(detailKey(ns, website, login, false), func(k *cache.Key) { k.Post, k.Newest = "c-1", true }), queue.User),
		"an expansion without a ticket":        s.Begin(Expansion, with(detailKey(ns, website, "", false), func(k *cache.Key) { k.FullText = true }), queue.User),
		"an expansion of a list":               s.Begin(Expansion, ticketsKey(ns, website, "", 1), queue.User),
	}
	for name, b := range bad {
		if err := CheckReadTicket(b); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	// The options sent are the key's, every one of them.
	k := with(paged(4, false, 2), func(k *cache.Key) { k.FullText = true })
	if got := DetailOptions(k); got != (api.TicketOptions{PostsPage: 4, PostsPerPage: PostsPageSize, AttachmentsPage: 2, FullText: true}) {
		t.Fatalf("options %+v", got)
	}
	k = with(detailKey(ns, website, login, false), func(k *cache.Key) { k.Post = "c-9" })
	if got := DetailOptions(k); got != (api.TicketOptions{PostID: "c-9"}) {
		t.Fatalf("options %+v", got)
	}
}

// Every option is part of the key and of the share key: two reads that
// differ in one option are two cache entries and two requests; every one
// of them is the same ticket for a removal or an invalidation.
func TestEveryTicketOptionSeparatesKeysAndSharingButNotTheResource(t *testing.T) {
	ns := confirmed(t)
	s := NewScope(ns)
	base := detailKey(ns, website, login, true)
	variants := []func(*cache.Key){
		func(k *cache.Key) { k.PostsPage = 2 },
		func(k *cache.Key) { k.Newest = false },
		func(k *cache.Key) { k.AttachmentsPage = 2 },
		func(k *cache.Key) { k.FullText = true },
		func(k *cache.Key) { *k = detailKey(ns, website, login, false); k.Post = "c-1" },
		func(k *cache.Key) { *k = detailKey(ns, website, login, false); k.Post = "c-2" },
	}
	seen := map[string]bool{s.Begin(Detail, base, queue.User).ShareKey(): true}
	for i, change := range variants {
		k := base
		change(&k)
		if k == base {
			t.Fatalf("variant %d is the base key", i)
		}
		if k.Resource() != base.Resource() {
			t.Errorf("variant %d is another resource", i)
		}
		share := s.Begin(Detail, k, queue.User).ShareKey()
		if seen[share] {
			t.Errorf("variant %d shares a request with another", i)
		}
		seen[share] = true
	}
}

// The answer to one comment is that comment: another one, or more than
// one, is not the answer to the question (a protocol error, never shown).
func TestTheAnswerToOneCommentIsThatComment(t *testing.T) {
	ns := confirmed(t)
	k := detailKey(ns, website, login, false)
	k.Post = "c-1"
	ticket := func(postID string, ids ...string) api.Ticket {
		t := api.Ticket{ID: login, Project: api.Ref{ID: website}, PostID: postID}
		for _, id := range ids {
			t.Posts = append(t.Posts, api.Post{ID: id})
		}
		return t
	}
	if err := CheckDetail(k, ticket("c-1", "c-1")); err != nil {
		t.Fatal(err)
	}
	for name, bad := range map[string]api.Ticket{
		"another echo":    ticket("c-2", "c-1"),
		"another comment": ticket("c-1", "c-2"),
		"two comments":    ticket("c-1", "c-1", "c-3"),
		"none":            ticket("c-1"),
	} {
		if err := CheckDetail(k, bad); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
