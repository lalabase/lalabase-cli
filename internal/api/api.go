package api

import (
	"context"
	"encoding/json"
	"net/url"
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
	ans := a.c.CallTool(ctx, transport.Call{
		Tool:           spec.Tool,
		Args:           args,
		OneRecord:      spec.OneRecord,
		LegacyNotFound: legacyNotFound(spec, args),
	})
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

func protocol(ans transport.Answer, msg string, err error) transport.Answer {
	return transport.Answer{Class: transport.Protocol, Status: ans.Status, Message: msg, Err: err}
}

// legacyNotFound are the texts a server before 0.7.0 answers a missing
// record with: "No ticket found for <id>." and its kin, naming exactly what
// was asked for.
func legacyNotFound(spec Spec, args map[string]any) []string {
	if !spec.OneRecord {
		return nil
	}
	var texts []string
	switch spec.Tool {
	case "get_ticket", "post_comment":
		if id, ok := args["ticket_id"].(string); ok {
			texts = append(texts, "No ticket found for "+id+".")
		}
		if n, ok := args["number"].(string); ok {
			texts = append(texts, "No ticket found for number "+n+".")
		}
	case "mark_notification_read":
		if id, ok := args["notification_id"].(string); ok {
			texts = append(texts, "No notification found for "+id+".")
		}
	}
	return texts
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
	Args: []string{"status", "page", "per_page", "project_id"},
	Required: []string{
		"data[].id", "data[].read", "data[].message", "data[].event_type", "data[].last_event_at",
		"data[].organisation.id", "data[].organisation.name", "data[].ticket", "data[].project",
		"meta.page", "meta.per_page", "meta.total", "meta.unread_total", "meta.by_organisation",
	},
}

type NotificationQuery struct {
	Status    string // unread, read or all
	Page      int
	PerPage   int
	ProjectID string
}

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
	args := map[string]any{}
	setString(args, "status", q.Status)
	setInt(args, "page", q.Page)
	setInt(args, "per_page", q.PerPage)
	setString(args, "project_id", q.ProjectID)
	var page NotificationPage
	return page, a.call(ctx, NotificationsSpec, args, &page)
}

var MarkReadSpec = Spec{
	Name: "mark_notification_read", Tool: "mark_notification_read",
	Args:      []string{"notification_id", "idempotency_key"},
	OneRecord: true,
	Required:  []string{"id", "state", "read_at"},
}

type NotificationState struct {
	ID     string  `json:"id"`
	State  string  `json:"state"`
	ReadAt *string `json:"read_at"`
}

// MarkNotificationRead is a write: key is the idempotency key of the
// person's action, the same on every retry of that action.
func (a *API) MarkNotificationRead(ctx context.Context, id, key string) (NotificationState, transport.Answer) {
	args := map[string]any{"notification_id": id}
	setString(args, "idempotency_key", key)
	var state NotificationState
	return state, a.call(ctx, MarkReadSpec, args, &state)
}

// --- tickets -----------------------------------------------------------------

var TicketsSpec = Spec{
	Name: "list_tickets", Tool: "list_tickets",
	Args: []string{"project_id", "q", "status", "page", "per_page"},
	Required: []string{
		"data[].id", "data[].number", "data[].title", "data[].status", "data[].assignees", "data[].updated_at",
		"meta.page", "meta.per_page", "meta.total", "meta.available_statuses",
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
	args := map[string]any{"project_id": q.ProjectID}
	setString(args, "q", q.Q)
	setString(args, "status", q.Status)
	setInt(args, "page", q.Page)
	setInt(args, "per_page", q.PerPage)
	var page TicketPage
	return page, a.call(ctx, TicketsSpec, args, &page)
}

var TicketSpec = Spec{
	Name: "get_ticket", Tool: "get_ticket",
	Args:      []string{"project_id", "ticket_id", "number"},
	OneRecord: true,
	Required: []string{
		"id", "number", "title", "status", "description_text", "assignees", "creator", "updated_at",
		"project.id", "project.name",
		"posts[].id", "posts[].author.name", "posts[].body_text", "posts[].is_private", "posts[].created_at",
		"posts_total", "posts_truncated", "attachments", "attachments_total",
	},
}

type Post struct {
	ID        string `json:"id"`
	Author    Ref    `json:"author"`
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
}

func (a *API) Ticket(ctx context.Context, projectID, ticketID string) (Ticket, transport.Answer) {
	var t Ticket
	return t, a.call(ctx, TicketSpec, map[string]any{"project_id": projectID, "ticket_id": ticketID}, &t)
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
	Required:  []string{"id", "ticket_id", "author.name", "body_text", "is_private", "created_at"},
}

type Comment struct {
	ID        string `json:"id"`
	TicketID  string `json:"ticket_id"`
	Author    Ref    `json:"author"`
	BodyText  string `json:"body_text"`
	IsPrivate bool   `json:"is_private"`
	CreatedAt string `json:"created_at"`
}

// PostComment is a write: key is the idempotency key of the person's action.
func (a *API) PostComment(ctx context.Context, projectID, ticketID, body, key string) (Comment, transport.Answer) {
	args := map[string]any{"project_id": projectID, "ticket_id": ticketID, "body": body}
	setString(args, "idempotency_key", key)
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
	Required: []string{"data.user.id", "data.user.display_name", "data.token.id", "data.token.prefix", "data.scoped_organisations"},
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
	query := url.Values{}
	if organisationID != "" {
		query.Set("organisation_id", organisationID)
	}
	var body struct {
		Data []Project `json:"data"`
	}
	ans := a.get(ctx, ProjectsSpec, query, &body)
	return body.Data, ans
}
