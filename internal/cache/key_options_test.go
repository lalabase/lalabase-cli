package cache

import (
	"testing"
	"time"
)

// C1c: the keys of a project's ticket list and of one ticket.

func ticketList(ns Namespace, project, q string, page int) Key {
	k := ns.Key("list_tickets")
	k.Project, k.Query, k.Page, k.PerPage = project, q, page, 25
	return k
}

func ticket(ns Namespace, project, id string) Key {
	k := ns.Key("get_ticket")
	k.Project, k.Ticket = project, id
	k.PostsPage, k.PostsPerPage, k.Newest, k.AttachmentsPage = 1, 25, true, 1
	return k
}

// Every option of a request is part of its key: a search, a page of
// comments, their order, a page of attachments, the full text and one
// comment are each another entry.
func TestEveryRequestOptionIsolates(t *testing.T) {
	ns := namespace(t, "default", "https://a.example", "tok-1")
	base := ticket(ns, "p-1", "t-1")
	variants := map[string]Key{}
	set := func(name string, f func(*Key)) { k := base; f(&k); variants[name] = k }
	set("ticket", func(k *Key) { k.Ticket = "t-2" })
	set("project", func(k *Key) { k.Project = "p-2" })
	set("query", func(k *Key) { k.Query = "login" })
	set("posts page", func(k *Key) { k.PostsPage = 2 })
	set("posts per page", func(k *Key) { k.PostsPerPage = 10 })
	set("order", func(k *Key) { k.Newest = false })
	set("attachments page", func(k *Key) { k.AttachmentsPage = 2 })
	set("full text", func(k *Key) { k.FullText = true })
	set("one comment", func(k *Key) { k.Post = "c-1" })
	set("plain", func(k *Key) { k.PostsPage, k.PostsPerPage, k.Newest, k.AttachmentsPage = 0, 0, false, 0 })
	s := NewStore()
	s.Apply(base, Replace, []byte(`base`), t0)
	for name, k := range variants {
		if k == base || k.String() == base.String() {
			t.Errorf("%s: equal to the base key", name)
		}
		if _, ok := s.Get(k); ok {
			t.Errorf("%s: sees the base entry", name)
		}
	}
	if len(variants) != 10 {
		t.Fatalf("%d dimensions", len(variants))
	}
	// Two searches of one project are two entries; so are two pages of one
	// search.
	a, b := ticketList(ns, "p-1", "a", 1), ticketList(ns, "p-1", "b", 1)
	s.Apply(a, Replace, []byte(`a`), t0)
	if _, ok := s.Get(b); ok || a.String() == b.String() {
		t.Fatal("two searches share an entry")
	}
	if _, ok := s.Get(ticketList(ns, "p-1", "a", 2)); ok {
		t.Fatal("two pages share an entry")
	}
}

// A key is scoped by one organisation, explicitly by all of them, or by a
// project (with or without a ticket); a key scoped by nothing, or by an
// organisation and all of them at once, is never filed.
func TestAKeyIsScopedByOrganisationAllOrProject(t *testing.T) {
	ns := namespace(t, "default", "https://a.example", "tok")
	s := NewStore()
	for name, k := range map[string]Key{
		"project":      ticketList(ns, "p-1", "", 1),
		"ticket":       ticket(ns, "p-1", "t-1"),
		"organisation": func() Key { k := ns.Key("GET /api/v1/projects"); k.Organisation = "org-1"; return k }(),
	} {
		s.Apply(k, Replace, []byte(`x`), t0)
		if _, ok := s.Get(k); !ok {
			t.Errorf("%s: a valid key was not filed: %+v", name, k)
		}
	}
	for name, k := range map[string]Key{
		"nothing":          ns.Key("list_tickets"),
		"org and all":      func() Key { k := ticketList(ns, "p-1", "", 1); k.Organisation, k.Global = "org-1", true; return k }(),
		"ticket, no proj.": func() Key { k := ticket(ns, "", "t-1"); return k }(),
	} {
		s.Apply(k, Replace, []byte(`x`), t0)
		if _, ok := s.Get(k); ok {
			t.Errorf("%s: an invalid key was filed: %+v", name, k)
		}
	}
}

// The resource of a ticket is the ticket, whatever options the read
// carried: a removal takes every cached variant of it. The resource of a
// ticket list is the project: a removed project takes every search and
// every page. Another ticket, project or operation is another resource.
func TestResourceStripsTheOptionsOfOneRecord(t *testing.T) {
	ns := namespace(t, "default", "https://a.example", "tok")
	base := ticket(ns, "p-1", "t-1")
	for name, change := range map[string]func(*Key){
		"posts page":       func(k *Key) { k.PostsPage = 3 },
		"per page":         func(k *Key) { k.PostsPerPage = 5 },
		"order":            func(k *Key) { k.Newest = false },
		"attachments page": func(k *Key) { k.AttachmentsPage = 2 },
		"full text":        func(k *Key) { k.FullText = true },
		"one comment":      func(k *Key) { k.Post = "c-9" },
		"plain":            func(k *Key) { *k = Key{ns: ns, Operation: "get_ticket", Project: "p-1", Ticket: "t-1"} },
	} {
		k := base
		change(&k)
		if k.Resource() != base.Resource() {
			t.Errorf("%s: another resource", name)
		}
	}
	for name, change := range map[string]func(*Key){
		"ticket":    func(k *Key) { k.Ticket = "t-2" },
		"project":   func(k *Key) { k.Project = "p-2" },
		"operation": func(k *Key) { k.Operation = "list_tickets"; k.Ticket = "" },
	} {
		k := base
		change(&k)
		if k.Resource() == base.Resource() {
			t.Errorf("%s: the same resource", name)
		}
	}
	list := ticketList(ns, "p-1", "", 1)
	for name, change := range map[string]func(*Key){
		"page":   func(k *Key) { k.Page = 2 },
		"search": func(k *Key) { k.Query = "login" },
	} {
		k := list
		change(&k)
		if k.Resource() != list.Resource() {
			t.Errorf("list %s: another resource", name)
		}
	}
	s := NewStore()
	other := ticket(ns, "p-1", "t-2")
	s.Apply(base, Replace, []byte(`t1`), t0)
	s.Apply(other, Replace, []byte(`t2`), t0)
	plain := base
	plain.PostsPage, plain.PostsPerPage, plain.Newest, plain.AttachmentsPage = 0, 0, false, 0
	s.Apply(plain, Remove, nil, t0)
	if _, ok := s.Get(base); ok {
		t.Fatal("the removal missed a variant of the ticket")
	}
	if _, ok := s.Get(other); !ok {
		t.Fatal("the removal took another ticket")
	}
	s.Apply(ticketList(ns, "p-1", "a", 1), Replace, []byte(`a`), t0)
	s.Apply(ticketList(ns, "p-1", "b", 2), Replace, []byte(`b`), t0)
	s.Apply(ticketList(ns, "p-2", "a", 1), Replace, []byte(`c`), t0)
	s.Apply(ticketList(ns, "p-1", "", 3), Remove, nil, t0)
	for _, k := range []Key{ticketList(ns, "p-1", "a", 1), ticketList(ns, "p-1", "b", 2)} {
		if _, ok := s.Get(k); ok {
			t.Fatalf("the removal of the project missed %+v", k)
		}
	}
	if _, ok := s.Get(ticketList(ns, "p-2", "a", 1)); !ok {
		t.Fatal("the removal took another project")
	}
}

// A status is a list of its own (C11), and a project's removal takes it:
// the resource of a project's list has no status. The inbox keeps its own.
func TestAStatusIsAKeyOfItsOwnButNoResourceOfItsOwn(t *testing.T) {
	ns, ok := Confirmed("default", "https://example.invalid", "token")
	if !ok {
		t.Fatal("no namespace")
	}
	key := func(status string) Key {
		k := ns.Key("list_tickets")
		k.Project, k.Page, k.PerPage, k.Status = "p", 1, 25, status
		return k
	}
	a, b := key("new"), key("closed")
	if a == b || a.String() == b.String() || a.Record() == b.Record() {
		t.Fatal("two statuses share an entry")
	}
	if a.Resource() != b.Resource() || a.Resource() != key("").Resource() {
		t.Fatal("a status has a resource of its own")
	}
	s := NewStore()
	s.Apply(a, Replace, []byte("{}"), time.Time{})
	s.Apply(b, Replace, []byte("{}"), time.Time{})
	s.Apply(key(""), Remove, nil, time.Time{})
	if s.Len() != 0 {
		t.Fatalf("%d entries survived the removal", s.Len())
	}
	inbox := ns.Key("list_notifications")
	inbox.Global, inbox.Status = true, "unread"
	if inbox.Resource().Status != "unread" {
		t.Fatal("the inbox lost its status")
	}
}
