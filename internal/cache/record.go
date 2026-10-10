package cache

// The key as the disk cache writes it (C1e). Key and Namespace keep their
// fields unexported, so a plain marshal of either would lose the identity;
// KeyRecord names every field, and a test counts them against the key.

// Profile, Server and TokenID are the three parts of a confirmed identity.
func (n Namespace) Profile() string { return n.profile }
func (n Namespace) Server() string  { return n.server }
func (n Namespace) TokenID() string { return n.tokenID }

// KeyRecord is one key with its identity, field by field, for the disk.
// Omitted fields are zero in the key too; a record with one field changed
// is another key or none.
type KeyRecord struct {
	Profile         string `json:"profile"`
	Server          string `json:"server"`
	TokenID         string `json:"token_id"`
	Operation       string `json:"operation"`
	Organisation    string `json:"organisation,omitempty"`
	Global          bool   `json:"global,omitempty"`
	Status          string `json:"status,omitempty"`
	Page            int    `json:"page,omitempty"`
	PerPage         int    `json:"per_page,omitempty"`
	Project         string `json:"project,omitempty"`
	Query           string `json:"query,omitempty"`
	Ticket          string `json:"ticket,omitempty"`
	PostsPage       int    `json:"posts_page,omitempty"`
	PostsPerPage    int    `json:"posts_per_page,omitempty"`
	Newest          bool   `json:"newest,omitempty"`
	AttachmentsPage int    `json:"attachments_page,omitempty"`
	FullText        bool   `json:"full_text,omitempty"`
	Post            string `json:"post,omitempty"`
}

// Record is the key with every field named.
func (k Key) Record() KeyRecord {
	return KeyRecord{
		Profile: k.ns.profile, Server: k.ns.server, TokenID: k.ns.tokenID,
		Operation: k.Operation, Organisation: k.Organisation, Global: k.Global, Status: k.Status,
		Page: k.Page, PerPage: k.PerPage, Project: k.Project, Query: k.Query, Ticket: k.Ticket,
		PostsPage: k.PostsPage, PostsPerPage: k.PostsPerPage, Newest: k.Newest,
		AttachmentsPage: k.AttachmentsPage, FullText: k.FullText, Post: k.Post,
	}
}

// Key rebuilds the key: the identity through Confirmed, so a record of no
// confirmed identity is no key, and the rest field by field. ok is false
// for a record the store would refuse to file.
func (r KeyRecord) Key() (Key, bool) {
	ns, ok := Confirmed(r.Profile, r.Server, r.TokenID)
	if !ok {
		return Key{}, false
	}
	k := Key{
		ns: ns, Operation: r.Operation, Organisation: r.Organisation, Global: r.Global, Status: r.Status,
		Page: r.Page, PerPage: r.PerPage, Project: r.Project, Query: r.Query, Ticket: r.Ticket,
		PostsPage: r.PostsPage, PostsPerPage: r.PostsPerPage, Newest: r.Newest,
		AttachmentsPage: r.AttachmentsPage, FullText: r.FullText, Post: r.Post,
	}
	if !k.valid() {
		return Key{}, false
	}
	return k, true
}
