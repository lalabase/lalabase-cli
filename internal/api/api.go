package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/gboyke/lalabase-cli/internal/transport"
)

// API runs the adapters against one server.
type API struct {
	c *transport.Client
}

// New wraps a transport client.
func New(c *transport.Client) *API { return &API{c: c} }

// call runs a tool adapter and decodes its object into out.
func (a *API) call(ctx context.Context, spec Spec, args map[string]any, out any) transport.Answer {
	ans := a.c.CallTool(ctx, transport.Call{Tool: spec.Tool, Args: args, OneRecord: spec.OneRecord})
	return decode(spec, ans, out)
}

// get runs a REST adapter and decodes its object into out.
func (a *API) get(ctx context.Context, spec Spec, query url.Values, out any) transport.Answer {
	path := spec.Path
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	return decode(spec, a.c.Get(ctx, path), out)
}

// decode checks the required fields of an object and decodes it. A missing
// field or a field of the wrong type turns the answer into a protocol error:
// the client never works on an object with silently empty values.
func decode(spec Spec, ans transport.Answer, out any) transport.Answer {
	if !ans.OK() {
		return ans
	}
	missing, err := Missing(ans.Payload, spec.Required)
	if err != nil {
		return protocol(ans, spec.Name+": the answer is "+err.Error(), err)
	}
	if len(missing) > 0 {
		return protocol(ans, spec.Name+": the answer lacks "+strings.Join(missing, ", "), nil)
	}
	if err := json.Unmarshal(ans.Payload, out); err != nil {
		return protocol(ans, spec.Name+": the answer has a field of the wrong type", err)
	}
	return ans
}

// protocol turns an answer into a protocol error and keeps what the
// transport knew about it: a write that reached the server stays sent.
func protocol(ans transport.Answer, msg string, err error) transport.Answer {
	return transport.Answer{Class: transport.Protocol, Status: ans.Status, Message: msg, Err: err, Sent: ans.Sent}
}

// keyForm is what an idempotency key of this client looks like. The server
// strips a key and treats a blank one as none, writing without remembering
// it; a key of this form survives that unchanged.
var keyForm = regexp.MustCompile(`^[A-Za-z0-9._:-]{8,128}$`)

// checkKey refuses a write without a usable idempotency key before anything
// is sent: every write carries the key of the person's action, so a retry
// after a lost answer cannot write twice (plan, Writes).
func checkKey(spec Spec, key string) (transport.Answer, bool) {
	if keyForm.MatchString(key) {
		return transport.Answer{}, true
	}
	return transport.Answer{Class: transport.InvalidInput, Message: spec.Name + ": a write needs an idempotency key of 8 to 128 letters, digits or ._:-"}, false
}

// setString and setInt add an argument only when it carries a value, so an
// optional argument the person did not use is not sent.
func setString(args map[string]any, key, value string) {
	if value != "" {
		args[key] = value
	}
}

func setInt(args map[string]any, key string, value int) {
	if value > 0 {
		args[key] = value
	}
}

// --- inbox -------------------------------------------------------------------

var NotificationsSpec = Spec{
	Name: "list_notifications", Tool: "list_notifications",
	Args:     []string{"status", "page", "per_page", "project_id"},
	Optional: []string{"organisation_id"},
	Required: []string{
		"data[].id", "data[].read", "data[].message", "data[].event_type", "data[].last_event_at",
		"data[].organisation.id", "data[].organisation.name",
		"data[].ticket?.id", "data[].ticket?.number", "data[].ticket?.title", "data[].ticket?.project_id",
		"data[].project?.id", "data[].project?.name?", "data[].actor?.id", "data[].actor?.name?",
		"meta.page", "meta.per_page", "meta.total", "meta.unread_total",
		"meta.by_organisation[].id", "meta.by_organisation[].name", "meta.by_organisation[].unread",
	},
}

type NotificationQuery struct {
	Status    string // unread, read or all
	Page      int
	PerPage   int
	ProjectID string
	// OrganisationID narrows the inbox to one organisation (MCP 0.8.0,
	// feature inbox_by_organisation).
	OrganisationID string
}

// Ref is a person, project or organisation by id and name. The name is
// empty for a deleted user or project: the server keeps the id and answers
// the name with null.
type Ref struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type TicketRef struct {
	ID        string `json:"id"`
	Number    int    `json:"number"`
	Title     string `json:"title"`
	ProjectID string `json:"project_id"`
}

type Notification struct {
	ID           string     `json:"id"`
	Read         bool       `json:"read"`
	Message      string     `json:"message"`
	EventType    string     `json:"event_type"`
	LastEventAt  string     `json:"last_event_at"`
	Organisation Ref        `json:"organisation"`
	Project      *Ref       `json:"project"`
	Ticket       *TicketRef `json:"ticket"`
	Actor        *Ref       `json:"actor"`
}

type OrganisationCount struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Unread int    `json:"unread"`
}

type NotificationPage struct {
	Data []Notification `json:"data"`
	Meta struct {
		Page           int                 `json:"page"`
		PerPage        int                 `json:"per_page"`
		Total          int                 `json:"total"`
		UnreadTotal    int                 `json:"unread_total"`
		ByOrganisation []OrganisationCount `json:"by_organisation"`
	} `json:"meta"`
}

func (a *API) Notifications(ctx context.Context, q NotificationQuery) (NotificationPage, transport.Answer) {
	return DecodeNotifications(a.NotificationsAnswer(ctx, q))
}

// NotificationsAnswer sends the request and returns the answer undecoded,
// for a read shared by several waiters (internal/gateway): the queue shares
// the answer, and every waiter decodes its own value with
// DecodeNotifications.
func (a *API) NotificationsAnswer(ctx context.Context, q NotificationQuery) transport.Answer {
	args := map[string]any{}
	setString(args, "status", q.Status)
	setInt(args, "page", q.Page)
	setInt(args, "per_page", q.PerPage)
	setString(args, "project_id", q.ProjectID)
	setString(args, "organisation_id", q.OrganisationID)
	return a.c.CallTool(ctx, transport.Call{Tool: NotificationsSpec.Tool, Args: args, OneRecord: NotificationsSpec.OneRecord})
}

// DecodeNotifications checks an answer against NotificationsSpec and decodes
// a new page from it, sharing nothing with an earlier decode. An answer that
// is no object, or fails the check, gives the empty page: never a half-filled
// one.
func DecodeNotifications(ans transport.Answer) (NotificationPage, transport.Answer) {
	var page NotificationPage
	if ans = decode(NotificationsSpec, ans, &page); !ans.OK() {
		return NotificationPage{}, ans
	}
	return page, ans
}

// CheckNotificationPage checks what the spec cannot say about a page: the
// numbers the paging divides by are above zero, no total or count is
// negative, the page holds no more rows than its size or its total, every
// row and every count has an id, and no row's id comes twice (it would be
// selected twice). Each one is a protocol error, never a page that looks
// fine with a zero in it.
func CheckNotificationPage(p NotificationPage) error {
	m := p.Meta
	switch {
	case m.Page <= 0:
		return errors.New("list_notifications: meta.page is not above zero")
	case m.PerPage <= 0:
		return errors.New("list_notifications: meta.per_page is not above zero")
	case m.Total < 0:
		return errors.New("list_notifications: meta.total is negative")
	case m.UnreadTotal < 0:
		return errors.New("list_notifications: meta.unread_total is negative")
	case len(p.Data) > m.PerPage:
		return errors.New("list_notifications: more rows than meta.per_page")
	case len(p.Data) > m.Total:
		return errors.New("list_notifications: more rows than meta.total")
	}
	for i, c := range m.ByOrganisation {
		if c.ID == "" {
			return fmt.Errorf("list_notifications: meta.by_organisation[%d] has no id", i)
		}
		if c.Unread < 0 {
			return fmt.Errorf("list_notifications: meta.by_organisation[%d].unread is negative", i)
		}
	}
	seen := map[string]bool{}
	for i, row := range p.Data {
		if row.ID == "" {
			return fmt.Errorf("list_notifications: data[%d] has no id", i)
		}
		if row.Organisation.ID == "" {
			return fmt.Errorf("list_notifications: data[%d] has no organisation id", i)
		}
		if seen[row.ID] {
			return fmt.Errorf("list_notifications: data[%d] carries an id twice", i)
		}
		seen[row.ID] = true
	}
	return nil
}

var MarkReadSpec = Spec{
	Name: "mark_notification_read", Tool: "mark_notification_read",
	Args:      []string{"notification_id", "idempotency_key"},
	OneRecord: true,
	Required:  []string{"id", "state", "read_at?"},
}

type NotificationState struct {
	ID     string  `json:"id"`
	State  string  `json:"state"`
	ReadAt *string `json:"read_at"`
}

// MarkNotificationRead is a write: key is the idempotency key of the
// person's action, the same on every retry of that action.
func (a *API) MarkNotificationRead(ctx context.Context, id, key string) (NotificationState, transport.Answer) {
	if ans, ok := checkKey(MarkReadSpec, key); !ok {
		return NotificationState{}, ans
	}
	args := map[string]any{"notification_id": id, "idempotency_key": key}
	var state NotificationState
	return state, a.call(ctx, MarkReadSpec, args, &state)
}

// --- tickets -----------------------------------------------------------------

var TicketsSpec = Spec{
	Name: "list_tickets", Tool: "list_tickets",
	Args: []string{"project_id", "status", "page", "per_page"},
	// q is the search (feature tickets_search): a list without it is
	// still a list.
	Optional: []string{"q"},
	Required: []string{
		"data[].id", "data[].number", "data[].title", "data[].status?", "data[].updated_at",
		"data[].assignees[].id", "data[].assignees[].name",
		"meta.page", "meta.per_page", "meta.total", "meta.available_statuses[]",
	},
}

type TicketQuery struct {
	ProjectID string
	Q         string
	Status    string
	Page      int
	PerPage   int
}

type TicketRow struct {
	ID        string  `json:"id"`
	Number    int     `json:"number"`
	Title     string  `json:"title"`
	Status    *string `json:"status"`
	Assignees []Ref   `json:"assignees"`
	UpdatedAt string  `json:"updated_at"`
}

type TicketPage struct {
	Data []TicketRow `json:"data"`
	Meta struct {
		Page              int      `json:"page"`
		PerPage           int      `json:"per_page"`
		Total             int      `json:"total"`
		AvailableStatuses []string `json:"available_statuses"`
	} `json:"meta"`
}

func (a *API) Tickets(ctx context.Context, q TicketQuery) (TicketPage, transport.Answer) {
	return DecodeTickets(a.TicketsAnswer(ctx, q))
}

// TicketsAnswer sends the request and returns the answer undecoded, for a
// read shared by several waiters; each decodes with DecodeTickets.
func (a *API) TicketsAnswer(ctx context.Context, q TicketQuery) transport.Answer {
	args := map[string]any{"project_id": q.ProjectID}
	setString(args, "q", q.Q)
	setString(args, "status", q.Status)
	setInt(args, "page", q.Page)
	setInt(args, "per_page", q.PerPage)
	return a.c.CallTool(ctx, transport.Call{Tool: TicketsSpec.Tool, Args: args, OneRecord: TicketsSpec.OneRecord})
}

// DecodeTickets checks an answer against TicketsSpec and decodes a new page
// from it, sharing nothing with an earlier decode; an answer that is no
// object, or fails the check, gives the empty page.
func DecodeTickets(ans transport.Answer) (TicketPage, transport.Answer) {
	var page TicketPage
	if ans = decode(TicketsSpec, ans, &page); !ans.OK() {
		return TicketPage{}, ans
	}
	return page, ans
}

// CheckTicketPage checks what the spec cannot say about a page of tickets:
// the numbers the paging divides by are above zero, the total is not
// negative, the page holds no more rows than its size or its total, and
// every row has an id that comes once.
func CheckTicketPage(p TicketPage) error {
	m := p.Meta
	switch {
	case m.Page <= 0:
		return errors.New("list_tickets: meta.page is not above zero")
	case m.PerPage <= 0:
		return errors.New("list_tickets: meta.per_page is not above zero")
	case m.Total < 0:
		return errors.New("list_tickets: meta.total is negative")
	case len(p.Data) > m.PerPage:
		return errors.New("list_tickets: more rows than meta.per_page")
	case len(p.Data) > m.Total:
		return errors.New("list_tickets: more rows than meta.total")
	}
	return uniqueIDs("list_tickets: data", func(yield func(string) bool) {
		for _, row := range p.Data {
			if !yield(row.ID) {
				return
			}
		}
	})
}

// uniqueIDs says whether every id of a list is present and comes once.
func uniqueIDs(what string, ids func(yield func(string) bool)) error {
	seen := map[string]bool{}
	i := 0
	for id := range ids {
		switch {
		case id == "":
			return fmt.Errorf("%s[%d] has no id", what, i)
		case seen[id]:
			return fmt.Errorf("%s[%d] carries an id twice", what, i)
		}
		seen[id] = true
		i++
	}
	return nil
}

var TicketSpec = Spec{
	Name: "get_ticket", Tool: "get_ticket",
	Args:      []string{"project_id", "ticket_id", "number"},
	Optional:  []string{"posts_page", "posts_per_page", "posts_order", "attachments_page", "full_text", "post_id"},
	OneRecord: true,
	Required: []string{
		"id", "number", "title", "status?", "description_text?", "updated_at",
		"assignees[].id", "assignees[].name", "creator?.id", "creator?.name?",
		"project.id", "project.name",
		"posts[].id", "posts[].author?.id", "posts[].author?.name?", "posts[].body_text", "posts[].is_private", "posts[].created_at",
		// attachments_truncated since the server lists attachments at all
		// (core #701, with attachments_total): a missing one is not "complete".
		"posts_total", "posts_truncated", "attachments[].filename", "attachments_total", "attachments_truncated",
	},
}

type Post struct {
	ID        string `json:"id"`
	Author    *Ref   `json:"author"`
	BodyText  string `json:"body_text"`
	IsPrivate bool   `json:"is_private"`
	CreatedAt string `json:"created_at"`
}

type Attachment struct {
	Filename string `json:"filename"`
}

type Ticket struct {
	ID               string       `json:"id"`
	Number           int          `json:"number"`
	Title            string       `json:"title"`
	Status           *string      `json:"status"`
	DescriptionText  string       `json:"description_text"`
	Assignees        []Ref        `json:"assignees"`
	Creator          *Ref         `json:"creator"`
	UpdatedAt        string       `json:"updated_at"`
	Project          Ref          `json:"project"`
	Posts            []Post       `json:"posts"`
	PostsTotal       int          `json:"posts_total"`
	PostsTruncated   bool         `json:"posts_truncated"`
	Attachments      []Attachment `json:"attachments"`
	AttachmentsTotal int          `json:"attachments_total"`
	// AttachmentsTruncated is required on every answer (TicketSpec). The
	// page, from MCP 0.9.0, is required only when TicketOptions asked for one
	// (see ticketSpec).
	PostsPage            int    `json:"posts_page"`
	PostsPerPage         int    `json:"posts_per_page"`
	PostsPages           int    `json:"posts_pages"`
	PostsOrder           string `json:"posts_order"`
	AttachmentsPage      int    `json:"attachments_page"`
	AttachmentsPages     int    `json:"attachments_pages"`
	AttachmentsTruncated bool   `json:"attachments_truncated"`
	// PostID echoes the one comment asked for with TicketOptions.PostID.
	PostID string `json:"post_id"`
}

// TicketOptions are the S1 arguments of get_ticket (MCP 0.9.0). The zero
// value sends none of them. Paging needs the feature ticket_posts_paged,
// FullText and PostID need ticket_full_text.
type TicketOptions struct {
	PostsPage       int
	PostsPerPage    int
	Newest          bool // oldest first is the server's default and is not sent
	AttachmentsPage int
	FullText        bool
	PostID          string // one comment, whole; no page of comments then
}

func (o TicketOptions) paged() bool {
	return o.PostsPage > 0 || o.PostsPerPage > 0 || o.Newest || o.AttachmentsPage > 0
}

// attachmentPageFields come with every S1 answer, a page of comments or one
// comment; pageFields add the page of comments. A page the client cannot
// place is a protocol error, not page 0 of 0.
var (
	attachmentPageFields = []string{"attachments_page", "attachments_pages"}
	pageFields           = append([]string{"posts_page", "posts_per_page", "posts_pages", "posts_order"}, attachmentPageFields...)
)

// CommentNotFound is the reason of the refusal TicketWith gives for a
// missing or hidden comment (TicketOptions.PostID).
const CommentNotFound = "comment_not_found"

// ticketSpec is TicketSpec with the fields the options rely on.
func ticketSpec(o TicketOptions) Spec {
	spec := TicketSpec
	spec.Required = slices.Clone(TicketSpec.Required)
	switch {
	case o.PostID != "":
		spec.Required = append(append(spec.Required, "post_id"), attachmentPageFields...)
	case o.paged():
		spec.Required = append(spec.Required, pageFields...)
	}
	return spec
}

func (a *API) Ticket(ctx context.Context, projectID, ticketID string) (Ticket, transport.Answer) {
	return a.TicketWith(ctx, projectID, ticketID, TicketOptions{})
}

// TicketWith reads a ticket with the S1 options: a page of comments or
// attachments, the whole description, or one comment whole.
//
// With PostID, "not found" is not about the ticket: the server answers a
// missing or hidden comment like a missing record, and the cache would drop
// the ticket on it. It comes back as a refusal with the reason
// CommentNotFound instead; a ticket that is really gone says so on the next
// plain read.
func (a *API) TicketWith(ctx context.Context, projectID, ticketID string, o TicketOptions) (Ticket, transport.Answer) {
	return DecodeTicket(a.TicketAnswer(ctx, projectID, ticketID, o), o)
}

// TicketAnswer sends the request of TicketWith and returns the answer
// undecoded, for a read shared by several waiters (a preview and an open
// of the same ticket); each decodes with DecodeTicket under the same
// options. A page below zero is refused before anything is sent.
func (a *API) TicketAnswer(ctx context.Context, projectID, ticketID string, o TicketOptions) transport.Answer {
	if o.PostsPage < 0 || o.PostsPerPage < 0 || o.AttachmentsPage < 0 {
		return transport.Answer{Class: transport.InvalidInput, Message: TicketSpec.Name + ": a page or page size below 0"}
	}
	args := map[string]any{"project_id": projectID, "ticket_id": ticketID}
	setInt(args, "posts_page", o.PostsPage)
	setInt(args, "posts_per_page", o.PostsPerPage)
	if o.Newest {
		args["posts_order"] = "newest"
	}
	setInt(args, "attachments_page", o.AttachmentsPage)
	if o.FullText {
		args["full_text"] = true
	}
	setString(args, "post_id", o.PostID)
	return a.c.CallTool(ctx, transport.Call{Tool: TicketSpec.Tool, Args: args, OneRecord: TicketSpec.OneRecord})
}

// DecodeTicket checks an answer against the spec of its options and decodes
// a new ticket from it, sharing nothing with an earlier decode, and turns
// "not found" for one comment into the refusal CommentNotFound.
func DecodeTicket(ans transport.Answer, o TicketOptions) (Ticket, transport.Answer) {
	var t Ticket
	ans = decode(ticketSpec(o), ans, &t)
	if o.PostID != "" && ans.Class == transport.NotFound {
		ans.Class, ans.Reason = transport.Refused, CommentNotFound
	}
	if !ans.OK() {
		return Ticket{}, ans
	}
	return t, ans
}

// maxPostsPerPage is the page size the server caps at: asked for more, it
// answers that many.
const maxPostsPerPage = 50

// CheckTicket checks what the spec cannot say about a ticket: its id and
// its project are there, every comment has an id that comes once, no total
// is negative, and a page echoes the options it was asked with (the page,
// the page size up to the server's cap, the order, the attachments page)
// with a count of pages that is not negative.
func CheckTicket(t Ticket, o TicketOptions) error {
	switch {
	case t.ID == "":
		return errors.New("get_ticket: the ticket has no id")
	case t.Project.ID == "":
		return errors.New("get_ticket: the ticket has no project id")
	case t.PostsTotal < 0:
		return errors.New("get_ticket: posts_total is negative")
	case t.AttachmentsTotal < 0:
		return errors.New("get_ticket: attachments_total is negative")
	}
	if err := uniqueIDs("get_ticket: posts", func(yield func(string) bool) {
		for _, p := range t.Posts {
			if !yield(p.ID) {
				return
			}
		}
	}); err != nil {
		return err
	}
	if o.PostID != "" || !o.paged() {
		return nil
	}
	order := "oldest"
	if o.Newest {
		order = "newest"
	}
	switch {
	case t.PostsPage <= 0:
		return errors.New("get_ticket: posts_page is not above zero")
	case o.PostsPage > 0 && t.PostsPage != o.PostsPage:
		return fmt.Errorf("get_ticket: posts_page %d answered for page %d", t.PostsPage, o.PostsPage)
	case t.PostsPerPage <= 0:
		return errors.New("get_ticket: posts_per_page is not above zero")
	case o.PostsPerPage > 0 && t.PostsPerPage != min(o.PostsPerPage, maxPostsPerPage):
		return fmt.Errorf("get_ticket: posts_per_page %d answered for %d", t.PostsPerPage, o.PostsPerPage)
	case len(t.Posts) > t.PostsPerPage:
		return errors.New("get_ticket: more comments than posts_per_page")
	case t.PostsPages < 0:
		return errors.New("get_ticket: posts_pages is negative")
	case t.PostsOrder != order:
		return fmt.Errorf("get_ticket: posts_order %q answered for %q", t.PostsOrder, order)
	case t.AttachmentsPage <= 0:
		return errors.New("get_ticket: attachments_page is not above zero")
	case o.AttachmentsPage > 0 && t.AttachmentsPage != o.AttachmentsPage:
		return fmt.Errorf("get_ticket: attachments_page %d answered for page %d", t.AttachmentsPage, o.AttachmentsPage)
	case t.AttachmentsPages < 0:
		return errors.New("get_ticket: attachments_pages is negative")
	}
	return nil
}

// TicketByNumber sends the number as text, the form the tool documents
// ("187" or "#187").
func (a *API) TicketByNumber(ctx context.Context, projectID string, number int) (Ticket, transport.Answer) {
	var t Ticket
	return t, a.call(ctx, TicketSpec, map[string]any{"project_id": projectID, "number": strconv.Itoa(number)}, &t)
}

var CommentSpec = Spec{
	Name: "post_comment", Tool: "post_comment",
	Args:      []string{"project_id", "ticket_id", "body", "idempotency_key"},
	OneRecord: true,
	Required:  []string{"id", "ticket_id", "author?.id", "author?.name?", "body_text", "is_private", "created_at"},
}

type Comment struct {
	ID        string `json:"id"`
	TicketID  string `json:"ticket_id"`
	Author    *Ref   `json:"author"`
	BodyText  string `json:"body_text"`
	IsPrivate bool   `json:"is_private"`
	CreatedAt string `json:"created_at"`
}

// PostComment is a write: key is the idempotency key of the person's action.
func (a *API) PostComment(ctx context.Context, projectID, ticketID, body, key string) (Comment, transport.Answer) {
	if ans, ok := checkKey(CommentSpec, key); !ok {
		return Comment{}, ans
	}
	args := map[string]any{"project_id": projectID, "ticket_id": ticketID, "body": body, "idempotency_key": key}
	var c Comment
	return c, a.call(ctx, CommentSpec, args, &c)
}

// --- knowledge ---------------------------------------------------------------

var KnowledgeSpec = Spec{
	Name: "search_knowledge", Tool: "search_knowledge",
	Args:     []string{"project_id", "q", "page", "per_page"},
	Required: []string{"data[].type", "data[].id", "data[].title", "data[].updated_at", "meta.page", "meta.total"},
}

type KnowledgeQuery struct {
	ProjectID string
	Q         string
	Page      int
	PerPage   int
}

type KnowledgeHit struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Title     string `json:"title"`
	UpdatedAt string `json:"updated_at"`
}

type KnowledgePage struct {
	Data []KnowledgeHit `json:"data"`
	Meta struct {
		Page  int `json:"page"`
		Total int `json:"total"`
	} `json:"meta"`
}

func (a *API) SearchKnowledge(ctx context.Context, q KnowledgeQuery) (KnowledgePage, transport.Answer) {
	args := map[string]any{"project_id": q.ProjectID, "q": q.Q}
	setInt(args, "page", q.Page)
	setInt(args, "per_page", q.PerPage)
	var page KnowledgePage
	return page, a.call(ctx, KnowledgeSpec, args, &page)
}

// --- REST readers ------------------------------------------------------------

var MeSpec = Spec{
	Name: "GET /api/v1/me", Path: "/api/v1/me",
	Required: []string{"data.user.id", "data.user.display_name", "data.token.id", "data.token.prefix",
		"data.scoped_organisations[].id", "data.scoped_organisations[].name"},
}

// Me is who the token belongs to. Token.ReadOnly is nil on a server before
// MCP 0.7.0, which does not say: the caller must treat that as "unknown",
// never as "may write" or "read-only".
type Me struct {
	User struct {
		ID          string `json:"id"`
		Email       string `json:"email"`
		DisplayName string `json:"display_name"`
	} `json:"user"`
	Token struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Prefix   string `json:"prefix"`
		ReadOnly *bool  `json:"read_only"`
	} `json:"token"`
	ScopedOrganisations []Ref `json:"scoped_organisations"`
}

func (a *API) Me(ctx context.Context) (Me, transport.Answer) {
	var body struct {
		Data Me `json:"data"`
	}
	ans := a.get(ctx, MeSpec, nil, &body)
	return body.Data, ans
}

var OrganisationsSpec = Spec{
	Name: "GET /api/v1/organisations", Path: "/api/v1/organisations",
	Required: []string{"data[].id", "data[].name"},
}

func (a *API) Organisations(ctx context.Context) ([]Ref, transport.Answer) {
	var body struct {
		Data []Ref `json:"data"`
	}
	ans := a.get(ctx, OrganisationsSpec, nil, &body)
	return body.Data, ans
}

var ProjectsSpec = Spec{
	Name: "GET /api/v1/projects", Path: "/api/v1/projects",
	Required: []string{"data[].id", "data[].name", "data[].archived", "data[].organisation.id", "data[].organisation.name"},
}

type Project struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Archived     bool   `json:"archived"`
	Organisation Ref    `json:"organisation"`
}

// Projects lists the projects the token reaches, of one organisation when
// organisationID is set.
func (a *API) Projects(ctx context.Context, organisationID string) ([]Project, transport.Answer) {
	return DecodeProjects(a.ProjectsAnswer(ctx, organisationID))
}

// ProjectsAnswer sends the request and returns the answer undecoded, for a
// read shared by several waiters; each decodes with DecodeProjects.
func (a *API) ProjectsAnswer(ctx context.Context, organisationID string) transport.Answer {
	query := url.Values{}
	if organisationID != "" {
		query.Set("organisation_id", organisationID)
	}
	path := ProjectsSpec.Path
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	return a.c.Get(ctx, path)
}

// DecodeProjects checks an answer against ProjectsSpec and decodes a new
// list from it (never nil for an object), sharing nothing with an earlier
// decode.
func DecodeProjects(ans transport.Answer) ([]Project, transport.Answer) {
	var body struct {
		Data []Project `json:"data"`
	}
	if ans = decode(ProjectsSpec, ans, &body); !ans.OK() {
		return nil, ans
	}
	if body.Data == nil {
		body.Data = []Project{}
	}
	return body.Data, ans
}

// CheckProjects checks what the spec cannot say about a list of projects:
// every project has an id that comes once and names its organisation.
func CheckProjects(list []Project) error {
	for i, p := range list {
		if p.Organisation.ID == "" {
			return fmt.Errorf("projects: data[%d] has no organisation id", i)
		}
	}
	return uniqueIDs("projects: data", func(yield func(string) bool) {
		for _, p := range list {
			if !yield(p.ID) {
				return
			}
		}
	})
}
