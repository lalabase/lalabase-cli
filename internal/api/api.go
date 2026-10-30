package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
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
	missing, err := spec.Missing(ans.Payload)
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

// checkKey refuses a write without a usable idempotency key before anything
// is sent: every write carries the key of the person's action, so a retry
// after a lost answer cannot write twice (plan, Writes). The form is the
// transport's (transport.ValidKey), one rule for MCP and REST.
func checkKey(spec Spec, key string) (transport.Answer, bool) {
	if transport.ValidKey(key) {
		return transport.Answer{}, true
	}
	return transport.Answer{Class: transport.InvalidInput, Message: spec.Name + ": a write needs an idempotency key of 8 to 128 letters, digits or ._:-"}, false
}

// write runs a REST write adapter (K0; BookTime is its first caller, K6). The
// key is checked first. A retry of the queue runs the same job
// again and encodes the same value again: encoding/json writes it the same
// way every time (map keys sorted), so the server's replay, which compares
// the request, sees the same bytes. The value must not change between the
// two attempts.
// path is spec.Path with its ids filled in. The answer is decoded like a
// read's: a written object without a field the client relies on is a
// protocol error that stays sent.
func (a *API) write(ctx context.Context, spec Spec, path string, body any, key string, out any) transport.Answer {
	if ans, ok := checkKey(spec, key); !ok {
		return ans
	}
	if spec.Tool != "" || spec.Method != http.MethodPost && spec.Method != http.MethodPatch {
		return transport.Answer{Class: transport.InvalidInput, Message: spec.Name + ": no REST write"}
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return transport.Answer{Class: transport.InvalidInput, Message: spec.Name + ": the body cannot be encoded", Err: err}
	}
	return decode(spec, a.c.Write(ctx, spec.Method, path, encoded, key), out)
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
	ID     string  `json:"id"`
	Number int     `json:"number"`
	Title  string  `json:"title"`
	Status *string `json:"status"`
	// StatusFunctions says what the status means in its project
	// ("is_in_progress", "is_completed", ...). Not required: a server before
	// the field sends none, and the status is then drawn without a tone.
	StatusFunctions []string `json:"status_functions"`
	Assignees       []Ref    `json:"assignees"`
	UpdatedAt       string   `json:"updated_at"`
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

// postFields are the fields of a comment the client relies on: of every
// comment of a ticket's page, and of one comment read alone (K1).
var postFields = []string{"id", "author?.id", "author?.name?", "body_text", "is_private", "created_at"}

// under puts fields under a path: under("data", ["id"]) is ["data.id"].
func under(path string, fields []string) []string {
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		out = append(out, path+"."+f)
	}
	return out
}

var TicketSpec = Spec{
	Name: "get_ticket", Tool: "get_ticket",
	Args:      []string{"project_id", "ticket_id", "number"},
	Optional:  []string{"posts_page", "posts_per_page", "posts_order", "attachments_page", "full_text", "post_id"},
	OneRecord: true,
	Required: slices.Concat(
		[]string{
			"id", "number", "title", "status?", "description_text?", "updated_at",
			"assignees[].id", "assignees[].name", "creator?.id", "creator?.name?",
			"project.id", "project.name",
		},
		under("posts[]", postFields),
		// attachments_truncated since the server lists attachments at all
		// (core #701, with attachments_total): a missing one is not "complete".
		[]string{"posts_total", "posts_truncated", "attachments[].filename", "attachments_total", "attachments_truncated"},
	),
}

type Post struct {
	ID        string `json:"id"`
	Author    *Ref   `json:"author"`
	BodyText  string `json:"body_text"`
	IsPrivate bool   `json:"is_private"`
	CreatedAt string `json:"created_at"`
	// Attachments are the comment's files (core R7), each ID the comment
	// document's; nil from a server that does not list them, which says
	// how many there are in AttachmentsCount alone. An empty list is a
	// list: it may stand beside a count above zero, of files whose content
	// is gone.
	Attachments      []Attachment `json:"attachments"`
	AttachmentsCount int          `json:"attachments_count"`
}

// UnmarshalJSON reads what the spec names strictly and the files leniently,
// as an attachment's metadata: neither is required, so what does not read
// never refuses a ticket that is valid without it.
func (p *Post) UnmarshalJSON(data []byte) error {
	type fields Post // without this method
	raw := struct {
		*fields
		Attachments      json.RawMessage `json:"attachments"`
		AttachmentsCount json.RawMessage `json:"attachments_count"`
	}{fields: new(fields)}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*p = Post(*raw.fields)
	p.Attachments = postFiles(raw.Attachments)
	// A count is a whole number, written as one, as a size is.
	if n, err := strconv.Atoi(string(raw.AttachmentsCount)); err == nil && n >= 0 {
		p.AttachmentsCount = n
	}
	return nil
}

// postFiles is a comment's list, nil for anything else: an entry that does
// not read or has no name is left out.
func postFiles(raw json.RawMessage) []Attachment {
	var entries []json.RawMessage
	if json.Unmarshal(raw, &entries) != nil || entries == nil {
		return nil
	}
	files := make([]Attachment, 0, len(entries))
	for _, entry := range entries {
		var f Attachment
		if json.Unmarshal(entry, &f) == nil && f.Filename != "" {
			files = append(files, f)
		}
	}
	return files
}

// Attachment is one file of a ticket. The file name is required; the
// metadata beside it (core #701) is optional: an older server sends none of
// it, and a value that does not read (another type, a fraction, a negative
// size) is unknown, nil, never a reason to refuse a ticket that is valid
// without it. The extracted text the server sends along is not read: the
// client shows metadata only.
type Attachment struct {
	// ID is the document's id, what opens the file (C10); nil from a server
	// that sends none.
	ID          *string `json:"id,omitempty"`
	Filename    string  `json:"filename"`
	ContentType *string `json:"content_type,omitempty"`
	ByteSize    *int64  `json:"byte_size,omitempty"`
	TextStatus  *string `json:"text_status,omitempty"`
}

// UnmarshalJSON reads the file name strictly and the metadata leniently.
func (a *Attachment) UnmarshalJSON(data []byte) error {
	var raw struct {
		ID          json.RawMessage `json:"id"`
		Filename    string          `json:"filename"`
		ContentType json.RawMessage `json:"content_type"`
		ByteSize    json.RawMessage `json:"byte_size"`
		TextStatus  json.RawMessage `json:"text_status"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*a = Attachment{ID: optionalString(raw.ID), Filename: raw.Filename, ContentType: optionalString(raw.ContentType), TextStatus: optionalString(raw.TextStatus)}
	// A size is a whole number of bytes, written as one: 1e3 or 1.5 is not.
	if n, err := strconv.ParseInt(string(raw.ByteSize), 10, 64); err == nil && n >= 0 {
		a.ByteSize = &n
	}
	return nil
}

// optionalString is a JSON string, or nil for anything else (absent, null,
// another type).
func optionalString(raw json.RawMessage) *string {
	var s string
	if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &s) != nil {
		return nil
	}
	return &s
}

// TruncationMarker is what the server appends to a text it shortened
// (core Tools::Base::TRUNCATION_MARKER). A person can type it too; explicit
// metadata would be a contract change, accepted for C1.
const TruncationMarker = "\n\n\u2026[truncated]"

// Shortened says whether the server shortened a text, and returns the text
// without its marker when it did, the text unchanged otherwise.
func Shortened(text string) (string, bool) {
	body, ok := strings.CutSuffix(text, TruncationMarker)
	if !ok {
		return text, false
	}
	return body, true
}

type Ticket struct {
	ID               string       `json:"id"`
	Number           int          `json:"number"`
	Title            string       `json:"title"`
	Status           *string      `json:"status"`
	StatusFunctions  []string     `json:"status_functions"` // as on TicketRow
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
//
// For one comment (o.PostID) the answer it returns carries that comment
// alone, cut out of the server's bytes: the form the client keeps one
// comment in, whichever way read it (DecodePost reads it, K1). A ticket
// without exactly one comment is no answer for one comment.
func DecodeTicket(ans transport.Answer, o TicketOptions) (Ticket, transport.Answer) {
	var t Ticket
	ans = decode(ticketSpec(o), ans, &t)
	if o.PostID != "" && ans.Class == transport.NotFound {
		ans.Class, ans.Reason = transport.Refused, CommentNotFound
	}
	if !ans.OK() {
		return Ticket{}, ans
	}
	if o.PostID != "" {
		var raw struct {
			Posts []json.RawMessage `json:"posts"`
		}
		if json.Unmarshal(ans.Payload, &raw) != nil || len(raw.Posts) != 1 {
			return Ticket{}, protocol(ans, TicketSpec.Name+": not one comment for post_id", nil)
		}
		ans.Payload = raw.Posts[0]
	}
	return t, ans
}

// keptPostSpec is one comment as the client keeps it, whichever way read
// it: the comment alone, with the fields of a comment of a ticket's page.
var keptPostSpec = Spec{Name: "comment", Required: postFields}

// DecodePost checks one comment in the form the client keeps it
// (DecodeTicket for one comment, DecodePostREST) and decodes it, sharing
// nothing with an earlier decode. An entry of another form (a ticket the
// tool answered, kept before K1) is a protocol error: never read as a
// comment, dropped by the caller.
func DecodePost(ans transport.Answer) (Post, transport.Answer) {
	var p Post
	if ans = decode(keptPostSpec, ans, &p); !ans.OK() {
		return Post{}, ans
	}
	return p, ans
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

// --- the ticket view over REST (K1, the core's R1) -----------------------------
//
// The same reads as the tools above, on the REST API: the list, a page of
// a ticket, a ticket by its number, one comment. The core builds both
// answers with one serializer and one set of pages (its D5), so each REST
// spec is the tool's fields under data, and what a REST read files is the
// tool's form: an entry reads the same whichever way brought it. REST has
// every text whole; the tool cuts long ones.

// TicketsRESTSpec is the ticket list over REST. Its body has the tool's
// form, data and meta: the fields are the tool's.
var TicketsRESTSpec = Spec{
	Name: "GET /api/v1/projects/:project_id/tickets", Path: "/api/v1/projects/:project_id/tickets",
	Required: TicketsSpec.Required,
}

// TicketRESTSpec is one ticket over REST: the tool's ticket under data,
// the permissions beside it unread. The client asks it for a page alone
// (TicketRESTAnswer): without one the counts are missing.
var TicketRESTSpec = Spec{
	Name: "GET /api/v1/tickets/:id", Path: "/api/v1/tickets/:id",
	Required: under("data", TicketSpec.Required),
}

// TicketByNumberRESTSpec is a ticket by its number in a project, answered
// as TicketRESTSpec.
var TicketByNumberRESTSpec = Spec{
	Name: "GET /api/v1/projects/:project_id/tickets/number/:number", Path: "/api/v1/projects/:project_id/tickets/number/:number",
	Required: under("data", TicketSpec.Required),
}

// PostRESTSpec is one comment over REST, whole: the fields of a comment of
// a ticket's page, under data.
var PostRESTSpec = Spec{
	Name: "GET /api/v1/tickets/:ticket_id/posts/:id", Path: "/api/v1/tickets/:ticket_id/posts/:id",
	Required: under("data", postFields),
}

// ticketRESTSpec is a REST ticket spec with the fields the options rely on
// (ticketSpec), under data.
func ticketRESTSpec(spec Spec, o TicketOptions) Spec {
	spec.Required = under("data", ticketSpec(o).Required)
	return spec
}

// segment is an id as one segment of a REST path, escaped. Refused: an
// empty id, a dot segment, and one with a slash or a backslash, which the
// transport or a server would read as a separator, sending the read to
// another route. Ids come from the server's answers.
func segment(id string) (string, bool) {
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\`) {
		return "", false
	}
	return url.PathEscape(id), true
}

// noSegment is the refusal of an id that is no path segment, before
// anything is sent.
func noSegment(spec Spec, what string) transport.Answer {
	return transport.Answer{Class: transport.InvalidInput, Message: spec.Name + ": the " + what + " id is no path segment"}
}

// withQuery adds a query to a path, when there is one.
func withQuery(path string, query url.Values) string {
	if len(query) == 0 {
		return path
	}
	return path + "?" + query.Encode()
}

// TicketsRESTAnswer sends the read of a ticket list over REST, the query of
// TicketsAnswer, and returns the answer undecoded (DecodeTicketsREST).
func (a *API) TicketsRESTAnswer(ctx context.Context, q TicketQuery) transport.Answer {
	project, ok := segment(q.ProjectID)
	if !ok {
		return noSegment(TicketsRESTSpec, "project")
	}
	query := url.Values{}
	if q.Q != "" {
		query.Set("q", q.Q)
	}
	if q.Status != "" {
		query.Set("status", q.Status)
	}
	if q.Page > 0 {
		query.Set("page", strconv.Itoa(q.Page))
	}
	if q.PerPage > 0 {
		query.Set("per_page", strconv.Itoa(q.PerPage))
	}
	return a.c.Get(ctx, withQuery("/api/v1/projects/"+project+"/tickets", query))
}

// DecodeTicketsREST checks an answer of TicketsRESTAnswer and decodes a new
// page from it, as DecodeTickets does with the tool's: the payload stays
// as it came, the tool's form.
func DecodeTicketsREST(ans transport.Answer) (TicketPage, transport.Answer) {
	var page TicketPage
	if ans = decode(TicketsRESTSpec, ans, &page); !ans.OK() {
		return TicketPage{}, ans
	}
	return page, ans
}

// restPage is the query of a ticket's page over REST. posts_page is always
// sent, the first page when the options name none: GET /api/v1/tickets/:id
// without any page answers every comment and none of the counts. FullText
// is not sent: REST has the description whole.
func restPage(o TicketOptions) url.Values {
	query := url.Values{"posts_page": {strconv.Itoa(max(1, o.PostsPage))}}
	if o.PostsPerPage > 0 {
		query.Set("posts_per_page", strconv.Itoa(o.PostsPerPage))
	}
	if o.Newest {
		query.Set("posts_order", "newest")
	}
	if o.AttachmentsPage > 0 {
		query.Set("attachments_page", strconv.Itoa(o.AttachmentsPage))
	}
	return query
}

// TicketRESTAnswer sends the read of a ticket's page over REST and returns
// the answer undecoded (DecodeTicketREST). One comment is a read of its
// own (PostRESTAnswer); asked here, and a page below zero, it is refused
// before anything is sent. The path carries the ticket's id alone; the
// answer names its project, which the caller checks.
func (a *API) TicketRESTAnswer(ctx context.Context, ticketID string, o TicketOptions) transport.Answer {
	switch {
	case o.PostID != "":
		return transport.Answer{Class: transport.InvalidInput, Message: TicketRESTSpec.Name + ": one comment is read on its own route"}
	case o.PostsPage < 0 || o.PostsPerPage < 0 || o.AttachmentsPage < 0:
		return transport.Answer{Class: transport.InvalidInput, Message: TicketRESTSpec.Name + ": a page or page size below 0"}
	}
	id, ok := segment(ticketID)
	if !ok {
		return noSegment(TicketRESTSpec, "ticket")
	}
	return a.c.Get(ctx, withQuery("/api/v1/tickets/"+id, restPage(o)))
}

// DecodeTicketREST checks an answer of TicketRESTAnswer against the REST
// spec of its options and decodes a new ticket from its data. The answer
// it returns carries the data alone: the tool's form, which the client
// keeps whichever way read it.
func DecodeTicketREST(ans transport.Answer, o TicketOptions) (Ticket, transport.Answer) {
	return decodeTicketREST(TicketRESTSpec, ans, o)
}

func decodeTicketREST(spec Spec, ans transport.Answer, o TicketOptions) (Ticket, transport.Answer) {
	var body struct {
		Data Ticket `json:"data"`
	}
	if ans = decode(ticketRESTSpec(spec, o), ans, &body); !ans.OK() {
		return Ticket{}, ans
	}
	return body.Data, withData(ans)
}

// withData is an answer the spec accepted with its payload cut down to
// its data, the server's own bytes.
func withData(ans transport.Answer) transport.Answer {
	var raw struct {
		Data json.RawMessage `json:"data"`
	}
	// decode read the same payload into the same field: this cannot fail.
	_ = json.Unmarshal(ans.Payload, &raw)
	ans.Payload = raw.Data
	return ans
}

// TicketByNumberREST reads a ticket by its number in a project over REST,
// as TicketRESTAnswer reads one by its id: the first page of it.
func (a *API) TicketByNumberREST(ctx context.Context, projectID string, number int) (Ticket, transport.Answer) {
	if number < 1 {
		return Ticket{}, transport.Answer{Class: transport.InvalidInput, Message: TicketByNumberRESTSpec.Name + ": a number below 1"}
	}
	project, ok := segment(projectID)
	if !ok {
		return Ticket{}, noSegment(TicketByNumberRESTSpec, "project")
	}
	path := "/api/v1/projects/" + project + "/tickets/number/" + strconv.Itoa(number)
	return decodeTicketREST(TicketByNumberRESTSpec, a.c.Get(ctx, withQuery(path, restPage(TicketOptions{}))), TicketOptions{})
}

// PostRESTAnswer sends the read of one comment over REST, whole, and
// returns the answer undecoded (DecodePostREST).
func (a *API) PostRESTAnswer(ctx context.Context, ticketID, postID string) transport.Answer {
	ticket, ok := segment(ticketID)
	if !ok {
		return noSegment(PostRESTSpec, "ticket")
	}
	post, ok := segment(postID)
	if !ok {
		return noSegment(PostRESTSpec, "comment")
	}
	return a.c.Get(ctx, "/api/v1/tickets/"+ticket+"/posts/"+post)
}

// DecodePostREST checks an answer of PostRESTAnswer and decodes the
// comment. "Not found" is about the comment, not the ticket: the refusal
// CommentNotFound, as DecodeTicket gives it for one comment. The answer it
// returns carries the comment alone, the form DecodePost reads.
func DecodePostREST(ans transport.Answer) (Post, transport.Answer) {
	var body struct {
		Data Post `json:"data"`
	}
	ans = decode(PostRESTSpec, ans, &body)
	if ans.Class == transport.NotFound {
		ans.Class, ans.Reason = transport.Refused, CommentNotFound
	}
	if !ans.OK() {
		return Post{}, ans
	}
	return body.Data, withData(ans)
}

// --- update_ticket (server slice S2, MCP 0.10.0) --------------------------------

var UpdateTicketSpec = Spec{
	Name: "update_ticket", Tool: "update_ticket",
	// remove_assignee_ids is declared, so the feature needs the whole tool
	// (S2 as built), and never sent: the client adds, it removes nobody.
	Args:      []string{"project_id", "ticket_id", "title", "status", "add_assignee_ids", "remove_assignee_ids", "idempotency_key"},
	OneRecord: true,
	// The summary the server answers with carries the fields of a list row.
	Required: []string{"id", "number", "title", "status?", "updated_at", "assignees[].id", "assignees[].name"},
}

// TicketChange is the one change of a ticket an update sends: exactly one
// of the three is set. Status is a key of the project's
// meta.available_statuses, Title the whole new title, AddMe adds the person
// the token belongs to (sent as "me", which the server resolves; nobody is
// removed).
type TicketChange struct {
	Status string
	Title  string
	AddMe  bool
}

// kinds counts the changes set.
func (c TicketChange) kinds() int {
	n := 0
	if c.Status != "" {
		n++
	}
	if c.Title != "" {
		n++
	}
	if c.AddMe {
		n++
	}
	return n
}

// UpdateTicket is a write of one change: key is the idempotency key of the
// person's action. A change that sets none or more than one field is
// refused before sending, as the server would after a round trip. The
// answer is the ticket's summary, decoded as a list row.
func (a *API) UpdateTicket(ctx context.Context, projectID, ticketID string, c TicketChange, key string) (TicketRow, transport.Answer) {
	if ans, ok := checkKey(UpdateTicketSpec, key); !ok {
		return TicketRow{}, ans
	}
	if c.kinds() != 1 {
		return TicketRow{}, transport.Answer{Class: transport.InvalidInput, Message: UpdateTicketSpec.Name + ": exactly one change per call: a status, a title or an assignment"}
	}
	args := map[string]any{"project_id": projectID, "ticket_id": ticketID, "idempotency_key": key}
	switch {
	case c.Status != "":
		args["status"] = c.Status
	case c.Title != "":
		args["title"] = c.Title
	case c.AddMe:
		args["add_assignee_ids"] = []string{"me"}
	}
	var row TicketRow
	return row, a.call(ctx, UpdateTicketSpec, args, &row)
}

// --- post_comment --------------------------------------------------------------

var CommentSpec = Spec{
	Name: "post_comment", Tool: "post_comment",
	Args: []string{"project_id", "ticket_id", "body", "idempotency_key"},
	// format is the plain text body (feature comment_plain, MCP 0.11.0): a
	// server without it takes HTML, which this client never writes.
	Optional:  []string{"format"},
	OneRecord: true,
	Required:  []string{"id", "ticket_id", "author?.id", "author?.name?", "body_text", "is_private", "created_at"},
}

// CommentOptions are the arguments of post_comment a newer server brings.
// The zero value sends none of them.
type CommentOptions struct {
	// Plain: the body is literal text; the server escapes it, turns line
	// breaks into markup and keeps body_text as sent. Needs the feature
	// comment_plain.
	Plain bool
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
func (a *API) PostComment(ctx context.Context, projectID, ticketID, body string, o CommentOptions, key string) (Comment, transport.Answer) {
	if ans, ok := checkKey(CommentSpec, key); !ok {
		return Comment{}, ans
	}
	args := map[string]any{"project_id": projectID, "ticket_id": ticketID, "body": body, "idempotency_key": key}
	if o.Plain {
		args["format"] = "plain"
	}
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

// --- booked time (S6) -------------------------------------------------------

// BookedTimeSpec is summarize_time_sessions asked for the time the token's
// own person booked on one ticket. The server decides what the person may
// see (all time or only their own); asking with creator "me" makes it the
// own time either way. One record: no ticket, or one the token cannot
// read, is not found. filters.ticket.id names the ticket the sum is of,
// so an answer for another ticket can be refused (gateway.CheckBookedTime).
var BookedTimeSpec = Spec{
	Name: "summarize_time_sessions", Tool: "summarize_time_sessions",
	// start_date, end_date and project are declared and never sent: S6
	// brought the whole tool, and a feature of a whole tool needs all of
	// it (a server with half of it keeps the feature off), as
	// remove_assignee_ids of update_ticket.
	Args:      []string{"project_id", "ticket", "creator", "start_date", "end_date", "project"},
	OneRecord: true,
	Required:  []string{"scope", "filters.ticket.id", "total.minutes"},
}

// BookedTime is the time booked on one ticket: in minutes, the ticket it is
// of, and the scope the server summed in ("all" or "own").
type BookedTime struct {
	Ticket  string
	Scope   string
	Minutes int
}

// creatorMe asks for the time of the token's own person.
const creatorMe = "me"

// BookedTime reads the own time booked on the ticket id of a project.
func (a *API) BookedTime(ctx context.Context, projectID, ticketID string) (BookedTime, transport.Answer) {
	return DecodeBookedTime(a.BookedTimeAnswer(ctx, projectID, ticketID))
}

// BookedTimeAnswer is BookedTime's raw answer, for the gateway's shared
// reads: every waiter decodes its own (DecodeBookedTime).
func (a *API) BookedTimeAnswer(ctx context.Context, projectID, ticketID string) transport.Answer {
	args := map[string]any{"project_id": projectID, "ticket": ticketID, "creator": creatorMe}
	return a.c.CallTool(ctx, transport.Call{Tool: BookedTimeSpec.Tool, Args: args, OneRecord: BookedTimeSpec.OneRecord})
}

// DecodeBookedTime decodes and checks an answer of BookedTimeAnswer.
func DecodeBookedTime(ans transport.Answer) (BookedTime, transport.Answer) {
	var body struct {
		Scope   string `json:"scope"`
		Filters struct {
			Ticket struct {
				ID string `json:"id"`
			} `json:"ticket"`
		} `json:"filters"`
		Total struct {
			Minutes int `json:"minutes"`
		} `json:"total"`
	}
	ans = decode(BookedTimeSpec, ans, &body)
	if !ans.OK() {
		return BookedTime{}, ans
	}
	return BookedTime{Ticket: body.Filters.Ticket.ID, Scope: body.Scope, Minutes: body.Total.Minutes}, ans
}

// --- booking time (K6) --------------------------------------------------------

// BookTimeSpec is a booking over REST (the core's R6): the row of the new
// time session under data, of which the three fields are what confirms the
// write (gateway.CheckBooking). It has no tool: the client never used
// create_time_session, and a booking has no way but this one.
var BookTimeSpec = Spec{
	Name: "POST /api/v1/tickets/:ticket_id/time_sessions", Path: "/api/v1/tickets/:ticket_id/time_sessions",
	Method:   http.MethodPost,
	Required: []string{"data.id", "data.ticket_id", "data.duration_minutes"},
}

// TimeBooking is a booking as the server filed it.
type TimeBooking struct {
	ID       string `json:"id"`
	TicketID string `json:"ticket_id"`
	Minutes  int    `json:"duration_minutes"`
}

// BookTime books time on a ticket, today, for the token's person. duration
// goes as it was typed: the server's parser is the one parser, a second
// one here would disagree with it ("1,5"), and only its answer says how
// many minutes that was. An empty note is not sent. key is the idempotency
// key of the person's action, the same on every retry of that action.
func (a *API) BookTime(ctx context.Context, ticketID, duration, note, key string) (TimeBooking, transport.Answer) {
	id, ok := segment(ticketID)
	if !ok {
		return TimeBooking{}, noSegment(BookTimeSpec, "ticket")
	}
	type session struct {
		DurationString string `json:"duration_string"`
		Description    string `json:"description,omitempty"`
	}
	body := struct {
		TimeSession session `json:"time_session"`
	}{session{DurationString: duration, Description: note}}
	var out struct {
		Data TimeBooking `json:"data"`
	}
	ans := a.write(ctx, BookTimeSpec, strings.Replace(BookTimeSpec.Path, ":ticket_id", id, 1), body, key, &out)
	if !ans.OK() {
		return TimeBooking{}, ans
	}
	return out.Data, ans
}

// --- REST readers ------------------------------------------------------------

var MeSpec = Spec{
	Name: "GET /api/v1/me", Path: "/api/v1/me",
	Required: []string{"data.user.id", "data.user.display_name", "data.token.id", "data.token.prefix",
		"data.scoped_organisations[].id", "data.scoped_organisations[].name"},
	// Since the core's R0: the REST API's version and what it offers.
	Newer: []string{"data.api_version", "data.capabilities[]"},
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
	// APIVersion and Capabilities are empty and nil on a server before
	// R0, which does not say: the features then take the MCP way
	// (internal/features).
	APIVersion   string   `json:"api_version"`
	Capabilities []string `json:"capabilities"`
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
