// Package gateway carries reads from the queue to the Bubble Tea model.
//
// The queue shares an answer between the waiters of one read, never a decoded
// value: every waiter's command decodes its own value from the answer, and
// its message names the waiter, the data and the generations it was asked
// for. A command changes no model and no cache; the model accepts a result
// through Scope.Receive, which checks it before anything is cached or shown.
package gateway

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/cache"
	"github.com/gboyke/lalabase-cli/internal/features"
	"github.com/gboyke/lalabase-cli/internal/queue"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/worker"
)

// Doer runs a shared read and names its request; *queue.Queue in the
// program.
type Doer interface {
	DoShared(ctx context.Context, job queue.Job) (transport.Answer, queue.Flight)
}

// Source sends the requests of the reads, undecoded; *api.API in the
// program. Each has its decode in internal/api, run by every waiter. The
// writes (C1f, C2) are decoded by the adapter: a write has one waiter and
// is never shared. The reads of the ticket view come in two ways (K1):
// the tools, and REST, which a ticket's Way picks.
type Source interface {
	NotificationsAnswer(ctx context.Context, q api.NotificationQuery) transport.Answer
	ProjectsAnswer(ctx context.Context, organisationID string) transport.Answer
	TicketsAnswer(ctx context.Context, q api.TicketQuery) transport.Answer
	TicketAnswer(ctx context.Context, projectID, ticketID string, o api.TicketOptions) transport.Answer
	TicketsRESTAnswer(ctx context.Context, q api.TicketQuery) transport.Answer
	TicketRESTAnswer(ctx context.Context, ticketID string, o api.TicketOptions) transport.Answer
	PostRESTAnswer(ctx context.Context, ticketID, postID string) transport.Answer
	MarkNotificationRead(ctx context.Context, id, key string) (api.NotificationState, transport.Answer)
	UpdateTicket(ctx context.Context, projectID, ticketID string, c api.TicketChange, key string) (api.TicketRow, transport.Answer)
	PostComment(ctx context.Context, projectID, ticketID, body string, o api.CommentOptions, key string) (api.Comment, transport.Answer)
	BookedTimeAnswer(ctx context.Context, projectID, ticketID string) transport.Answer
}

// Gateway turns tickets into commands.
type Gateway struct {
	sup    *worker.Supervisor
	queue  Doer
	src    Source
	events *Events
}

// Option changes a Gateway at construction.
type Option func(*Gateway)

// WithEvents lets the gateway observe its requests for the session's
// mailbox: a 401 on one of them is recorded there, whatever becomes of the
// result.
func WithEvents(e *Events) Option { return func(g *Gateway) { g.events = e } }

func New(sup *worker.Supervisor, queue Doer, src Source, opts ...Option) *Gateway {
	g := &Gateway{sup: sup, queue: queue, src: src}
	for _, opt := range opts {
		opt(g)
	}
	return g
}

// InboxResult is one waiter's inbox page. Page is empty unless Answer is an
// object.
type InboxResult struct {
	Ticket
	Page   api.NotificationPage
	Answer transport.Answer
	// Flight is the request the answer came from: the waiters that shared
	// one request carry the same flight.
	Flight queue.Flight
}

func (r InboxResult) Header() Result {
	return Result{Ticket: r.Ticket, Answer: r.Answer, Flight: r.Flight}
}

// ProjectsResult is one waiter's list of an organisation's projects (C1c).
// Projects is nil unless Answer is an object.
type ProjectsResult struct {
	Ticket
	Projects []api.Project
	Answer   transport.Answer
	Flight   queue.Flight
}

func (r ProjectsResult) Header() Result {
	return Result{Ticket: r.Ticket, Answer: r.Answer, Flight: r.Flight}
}

// TicketsResult is one waiter's page of a project's tickets (C1c).
type TicketsResult struct {
	Ticket
	Page   api.TicketPage
	Answer transport.Answer
	Flight queue.Flight
}

func (r TicketsResult) Header() Result {
	return Result{Ticket: r.Ticket, Answer: r.Answer, Flight: r.Flight}
}

// TicketResult is one waiter's ticket, previewed or opened (C1c), or one
// text of it read whole (C1d). Record is the ticket of a page or of the
// description whole; Post is the one comment a key with Post reads, on
// either way (K1), Record staying empty then. Both are empty unless Answer
// is an object.
type TicketResult struct {
	Ticket
	Record api.Ticket
	Post   api.Post
	Answer transport.Answer
	Flight queue.Flight
}

func (r TicketResult) Header() Result {
	return Result{Ticket: r.Ticket, Answer: r.Answer, Flight: r.Flight}
}

// Read is the one entry for a read: the key's operation says what is sent
// and which result comes back. A ticket of an operation the gateway does
// not read answers a plain Result, refused before anything is sent, so its
// waiter is freed like any other.
func (g *Gateway) Read(ctx context.Context, t Ticket) tea.Cmd {
	switch t.Key.Operation {
	case api.NotificationsSpec.Tool:
		return g.Inbox(ctx, t)
	case api.ProjectsSpec.Name:
		return g.Projects(ctx, t)
	case api.TicketsSpec.Tool:
		return g.Tickets(ctx, t)
	case api.TicketSpec.Tool:
		return g.Detail(ctx, t)
	case api.BookedTimeSpec.Tool:
		return g.BookedTime(ctx, t)
	}
	return func() tea.Msg {
		err := fmt.Errorf("gateway: no read of %q", t.Key.Operation)
		return Result{Ticket: t, Answer: refusedTicket(err)}
	}
}

// refusedTicket is the answer of a ticket refused before a request was made.
func refusedTicket(err error) transport.Answer {
	return transport.Answer{Class: transport.InvalidInput, Message: err.Error(), Err: err}
}

// Inbox reads the inbox page t names, for the waiter whose context ctx is.
// The shared request runs on the queue's context, so this waiter leaving
// cancels it only when it was the last one. A ticket that is no inbox read
// (CheckInboxTicket) is refused before anything is sent, and a page that
// contradicts the question (CheckInboxPage) before the model sees it.
func (g *Gateway) Inbox(ctx context.Context, t Ticket) tea.Cmd {
	if err := CheckInboxTicket(t); err != nil {
		return func() tea.Msg { return InboxResult{Ticket: t, Answer: refusedTicket(err)} }
	}
	query := InboxQuery(t.Key)
	return shared(g, ctx, t, "gateway: inbox", func(shared context.Context) transport.Answer {
		return g.src.NotificationsAnswer(shared, query)
	}, func(ans transport.Answer, flight queue.Flight) InboxResult {
		page, ans := api.DecodeNotifications(ans)
		if ans.OK() {
			if err := CheckInboxPage(t.Key, page); err != nil {
				page, ans = api.NotificationPage{}, contradiction(ans, err)
			}
		}
		return InboxResult{Ticket: t, Page: page, Answer: ans, Flight: flight}
	})
}

// Projects reads the projects of the organisation t names (C1c).
func (g *Gateway) Projects(ctx context.Context, t Ticket) tea.Cmd {
	if err := CheckProjectsTicket(t); err != nil {
		return func() tea.Msg { return ProjectsResult{Ticket: t, Answer: refusedTicket(err)} }
	}
	org := t.Key.Organisation
	return shared(g, ctx, t, "gateway: projects", func(shared context.Context) transport.Answer {
		return g.src.ProjectsAnswer(shared, org)
	}, func(ans transport.Answer, flight queue.Flight) ProjectsResult {
		list, ans := api.DecodeProjects(ans)
		if ans.OK() {
			if err := CheckProjectsList(t.Key, list); err != nil {
				list, ans = nil, contradiction(ans, err)
			}
		}
		return ProjectsResult{Ticket: t, Projects: list, Answer: ans, Flight: flight}
	})
}

// Tickets reads the page of a project's tickets t names, with its search
// (C1c), on the way of its ticket (K1).
func (g *Gateway) Tickets(ctx context.Context, t Ticket) tea.Cmd {
	if err := CheckTicketsTicket(t); err != nil {
		return func() tea.Msg { return TicketsResult{Ticket: t, Answer: refusedTicket(err)} }
	}
	query := TicketsQuery(t.Key)
	send, decode := g.src.TicketsAnswer, api.DecodeTickets
	if t.Way == features.REST {
		send, decode = g.src.TicketsRESTAnswer, api.DecodeTicketsREST
	}
	return shared(g, ctx, t, "gateway: tickets", func(shared context.Context) transport.Answer {
		return send(shared, query)
	}, func(ans transport.Answer, flight queue.Flight) TicketsResult {
		page, ans := decode(ans)
		if ans.OK() {
			if err := CheckTicketsPage(t.Key, page); err != nil {
				page, ans = api.TicketPage{}, contradiction(ans, err)
			}
		}
		return TicketsResult{Ticket: t, Page: page, Answer: ans, Flight: flight}
	})
}

// Detail reads the ticket t names, with the options of its key: a page
// (C1c, C1d) or one text whole (C1d), on the way of its ticket (K1). The
// preview and the open of one ticket share the request; each decodes for
// itself. What a waiter's answer carries to the cache has one form on both
// ways: the ticket, or for one comment the comment.
func (g *Gateway) Detail(ctx context.Context, t Ticket) tea.Cmd {
	if err := CheckDetailTicket(t); err != nil {
		return func() tea.Msg { return TicketResult{Ticket: t, Answer: refusedTicket(err)} }
	}
	project, id, options := t.Key.Project, t.Key.Ticket, DetailOptions(t.Key)
	var send func(context.Context) transport.Answer
	var build func(transport.Answer) TicketResult
	switch {
	case t.Way == features.REST && options.PostID != "":
		send = func(shared context.Context) transport.Answer { return g.src.PostRESTAnswer(shared, id, options.PostID) }
		build = func(ans transport.Answer) TicketResult {
			post, ans := api.DecodePostREST(ans)
			return onePost(t, post, ans)
		}
	case t.Way == features.REST:
		send = func(shared context.Context) transport.Answer { return g.src.TicketRESTAnswer(shared, id, options) }
		build = func(ans transport.Answer) TicketResult {
			return aTicket(t, func() (api.Ticket, transport.Answer) {
				record, ans := api.DecodeTicketREST(ans, options)
				return movedAway(t.Key, record, ans)
			})
		}
	default:
		send = func(shared context.Context) transport.Answer { return g.src.TicketAnswer(shared, project, id, options) }
		build = func(ans transport.Answer) TicketResult {
			r := aTicket(t, func() (api.Ticket, transport.Answer) { return api.DecodeTicket(ans, options) })
			if options.PostID != "" && r.Answer.OK() {
				// The tool brings the ticket with the one comment; the
				// read is of the comment (CheckDetail made sure it is the
				// one asked for).
				r.Post, r.Record = r.Record.Posts[0], api.Ticket{}
			}
			return r
		}
	}
	return shared(g, ctx, t, "gateway: ticket", send, func(ans transport.Answer, flight queue.Flight) TicketResult {
		r := build(ans)
		r.Flight = flight
		return r
	})
}

// movedAway: REST reads a ticket by its id alone, and a ticket moved to
// another project (the core keeps its id and gives it a new number there)
// answers from its new project. Under a key of its old project it is not
// found, as the tools answer in that project's scope: every variant of it
// leaves the cache. Another ticket answered stays a contradiction
// (CheckDetail). One comment's answer names no project; a move between a
// page and the comment read whole is found by the next read of the page.
func movedAway(k cache.Key, t api.Ticket, ans transport.Answer) (api.Ticket, transport.Answer) {
	if ans.OK() && t.ID == k.Ticket && t.Project.ID != "" && t.Project.ID != k.Project {
		return api.Ticket{}, transport.Answer{Class: transport.NotFound, Status: ans.Status, Message: "ticket: moved to another project", Sent: ans.Sent}
	}
	return t, ans
}

// aTicket is the result of a decoded ticket, checked against the question
// its key asked.
func aTicket(t Ticket, decode func() (api.Ticket, transport.Answer)) TicketResult {
	record, ans := decode()
	if ans.OK() {
		if err := CheckDetail(t.Key, record); err != nil {
			record, ans = api.Ticket{}, contradiction(ans, err)
		}
	}
	return TicketResult{Ticket: t, Record: record, Answer: ans}
}

// onePost is the result of a decoded comment, checked against the one
// comment its key asked for.
func onePost(t Ticket, post api.Post, ans transport.Answer) TicketResult {
	if ans.OK() {
		if err := CheckPost(t.Key, post); err != nil {
			post, ans = api.Post{}, contradiction(ans, err)
		}
	}
	return TicketResult{Ticket: t, Post: post, Answer: ans}
}

// TimeResult is one waiter's booked time of a ticket (S6). Time is empty
// unless Answer is an object.
type TimeResult struct {
	Ticket
	Time   api.BookedTime
	Answer transport.Answer
	Flight queue.Flight
}

func (r TimeResult) Header() Result {
	return Result{Ticket: r.Ticket, Answer: r.Answer, Flight: r.Flight}
}

// BookedTime reads the time the person booked on the ticket t names (S6),
// shared like every read; an answer that names another ticket is a
// contradiction, never filed.
func (g *Gateway) BookedTime(ctx context.Context, t Ticket) tea.Cmd {
	if err := CheckBookedTimeTicket(t); err != nil {
		return func() tea.Msg { return TimeResult{Ticket: t, Answer: refusedTicket(err)} }
	}
	project, id := t.Key.Project, t.Key.Ticket
	return shared(g, ctx, t, "gateway: booked time", func(shared context.Context) transport.Answer {
		return g.src.BookedTimeAnswer(shared, project, id)
	}, func(ans transport.Answer, flight queue.Flight) TimeResult {
		booked, ans := api.DecodeBookedTime(ans)
		if ans.OK() {
			if err := CheckBookedTime(t.Key, booked); err != nil {
				booked, ans = api.BookedTime{}, contradiction(ans, err)
			}
		}
		return TimeResult{Ticket: t, Time: booked, Answer: ans, Flight: flight}
	})
}

// CheckBookedTimeTicket says whether a ticket is the read S6 sends: the
// time of one ticket of one project, in the Time area, no other option.
func CheckBookedTimeTicket(t Ticket) error {
	k := t.Key
	switch {
	case k.Operation != api.BookedTimeSpec.Tool:
		return fmt.Errorf("booked time: a ticket of %q", k.Operation)
	case t.Area != Time:
		return errors.New("booked time: another area")
	case k.Project == "" || k.Ticket == "":
		return errors.New("booked time: no project or no ticket id")
	case k.Resource() != k || k.Status != "" || k.Organisation != "" || k.Global:
		return errors.New("booked time: an option the read does not have")
	}
	return nil
}

// CheckBookedTime says whether an answer is to the question the key asked:
// the time of that ticket, summed in a scope the server names, never
// below zero.
func CheckBookedTime(k cache.Key, b api.BookedTime) error {
	switch {
	case b.Ticket != k.Ticket:
		return fmt.Errorf("booked time: the time of ticket %q, asked of %q", b.Ticket, k.Ticket)
	case b.Scope != "all" && b.Scope != "own":
		return fmt.Errorf("booked time: an unknown scope %q", b.Scope)
	case b.Minutes < 0:
		return fmt.Errorf("booked time: %d minutes", b.Minutes)
	}
	return nil
}

// shared is the command of one shared read: the request under the
// ticket's share key and priority, observed for the mailbox, the answer
// cloned for this waiter and built into its result by build. A command
// that comes after Stop is a canceled result with its ticket; a panic in it
// is the fatal error.
func shared[R tea.Msg](g *Gateway, ctx context.Context, t Ticket, where string, send func(context.Context) transport.Answer, build func(transport.Answer, queue.Flight) R) tea.Cmd {
	// Observed on the request itself: a 401 is recorded whatever becomes of
	// this waiter's result (canceled, refused as old data).
	job := queue.Job{Priority: t.Priority, Kind: queue.Read, Key: t.ShareKey(), Run: g.events.Observe(send)}
	return func() tea.Msg {
		msg, ok := worker.Run(g.sup, where, func() tea.Msg {
			ans, flight := g.queue.DoShared(ctx, job)
			ans.Payload = bytes.Clone(ans.Payload) // the waiters share the answer
			return build(ans, flight)
		})
		if !ok {
			if g.sup.Err() != nil {
				return worker.Fatal{}
			}
			// Not run: the session stopped before this command came.
			return build(transport.Answer{Class: transport.Canceled, Message: "canceled before it was sent"}, 0)
		}
		return msg
	}
}

// contradiction is the answer for an object that is not to the question
// the key asked: a protocol error that keeps what the transport knew.
func contradiction(ans transport.Answer, err error) transport.Answer {
	return transport.Answer{Class: transport.Protocol, Status: ans.Status, Message: err.Error(), Err: err, Sent: ans.Sent}
}

// InboxQuery is the request for an inbox key: everything sent comes from the
// key, so the cache cannot file an answer under other options.
func InboxQuery(k cache.Key) api.NotificationQuery {
	return api.NotificationQuery{Status: k.Status, Page: k.Page, PerPage: k.PerPage, ProjectID: k.Project, OrganisationID: k.Organisation}
}

// TicketsQuery is the request for a ticket list key.
func TicketsQuery(k cache.Key) api.TicketQuery {
	return api.TicketQuery{ProjectID: k.Project, Q: k.Query, Status: k.Status, Page: k.Page, PerPage: k.PerPage}
}

// DetailOptions are the options of a ticket key: none for a plain read.
func DetailOptions(k cache.Key) api.TicketOptions {
	return api.TicketOptions{PostsPage: k.PostsPage, PostsPerPage: k.PostsPerPage, Newest: k.Newest, AttachmentsPage: k.AttachmentsPage, FullText: k.FullText, PostID: k.Post}
}

// The page sizes C1 reads with.
const (
	InboxPageSize  = 25
	TicketPageSize = 25
	PostsPageSize  = 25
)

// CheckReadTicket says whether a ticket is one of the reads C1 sends, by
// its operation.
func CheckReadTicket(t Ticket) error {
	switch t.Key.Operation {
	case api.NotificationsSpec.Tool:
		return CheckInboxTicket(t)
	case api.ProjectsSpec.Name:
		return CheckProjectsTicket(t)
	case api.TicketsSpec.Tool:
		return CheckTicketsTicket(t)
	case api.TicketSpec.Tool:
		return CheckDetailTicket(t)
	case api.BookedTimeSpec.Tool:
		return CheckBookedTimeTicket(t)
	}
	return fmt.Errorf("gateway: no read of %q", t.Key.Operation)
}

// CheckInboxTicket says whether a ticket is one of the reads C1b sends:
// unread rows only, a page above zero of InboxPageSize rows, no project
// filter, a key that is either global or of one organisation, the counters'
// area (Organisations) for the global read only. Anything else is refused
// before a request is made.
func CheckInboxTicket(t Ticket) error {
	k := t.Key
	switch {
	case k.Operation != api.NotificationsSpec.Tool:
		return fmt.Errorf("inbox: a ticket of %q", k.Operation)
	case k.Status != "unread":
		return fmt.Errorf("inbox: status %q, only unread is read", k.Status)
	case k.Page <= 0:
		return errors.New("inbox: a page below 1")
	case k.PerPage != InboxPageSize:
		return fmt.Errorf("inbox: a page size of %d, not %d", k.PerPage, InboxPageSize)
	case k.Project != "":
		return errors.New("inbox: a project filter")
	case k.Global == (k.Organisation != ""):
		return errors.New("inbox: a key that is neither global nor of one organisation")
	case t.Area == Organisations && !k.Global:
		return errors.New("inbox: a filtered read in the counters' area")
	case t.Area != Organisations && t.Area != Inbox:
		return errors.New("inbox: an unknown area")
	}
	return nil
}

// CheckProjectsTicket: the projects of one organisation, nothing else on
// the key, in their own area.
func CheckProjectsTicket(t Ticket) error {
	k := t.Key
	switch {
	case k.Operation != api.ProjectsSpec.Name:
		return fmt.Errorf("projects: a ticket of %q", k.Operation)
	case k.Organisation == "" || k.Global:
		return errors.New("projects: not of one organisation")
	case k.Page != 0 || k.PerPage != 0 || k.Status != "" || k.Project != "" || k.Query != "" || k.Ticket != "":
		return errors.New("projects: an option the read does not have")
	case t.Area != Projects:
		return errors.New("projects: another area")
	}
	return nil
}

// CheckTicketsTicket: one project's tickets, a page above zero of
// TicketPageSize rows, a search or none, no status, in their own area.
func CheckTicketsTicket(t Ticket) error {
	k := t.Key
	switch {
	case k.Operation != api.TicketsSpec.Tool:
		return fmt.Errorf("tickets: a ticket of %q", k.Operation)
	case k.Project == "":
		return errors.New("tickets: no project")
	case k.Page <= 0:
		return errors.New("tickets: a page below 1")
	case k.PerPage != TicketPageSize:
		return fmt.Errorf("tickets: a page size of %d, not %d", k.PerPage, TicketPageSize)
	case k.Status != "" || k.Organisation != "" || k.Global || k.Ticket != "":
		return errors.New("tickets: an option the read does not have")
	case t.Area != Tickets:
		return errors.New("tickets: another area")
	}
	return nil
}

// CheckDetailTicket: one ticket of one project, in one of the forms C1
// sends. In the Detail area a page: the plain read of an older server, or
// a page of comments (PostsPageSize, either order) with a page of
// attachments. In the Expansion area one text whole: the description, with
// the options of a page (or plain), or one comment alone. Anything else is
// refused before a request is made.
func CheckDetailTicket(t Ticket) error {
	k := t.Key
	plain := k.PostsPage == 0 && k.PostsPerPage == 0 && !k.Newest && k.AttachmentsPage == 0
	paged := k.PostsPage >= 1 && k.PostsPerPage == PostsPageSize && k.AttachmentsPage >= 1
	switch {
	case k.Operation != api.TicketSpec.Tool:
		return fmt.Errorf("ticket: a ticket of %q", k.Operation)
	case k.Project == "" || k.Ticket == "":
		return errors.New("ticket: no project or no ticket id")
	case k.Page != 0 || k.PerPage != 0 || k.Status != "" || k.Organisation != "" || k.Global || k.Query != "":
		return errors.New("ticket: an option the read does not have")
	case !plain && !paged:
		return errors.New("ticket: a page that is neither plain nor a whole page")
	}
	switch t.Area {
	case Detail, Titles:
		if k.FullText || k.Post != "" {
			return errors.New("ticket: an expansion in the area of the pages")
		}
	case Expansion:
		switch {
		case k.FullText == (k.Post != ""):
			return errors.New("ticket: an expansion is the description or one comment")
		case k.Post != "" && !plain:
			return errors.New("ticket: one comment with a page")
		}
	default:
		return errors.New("ticket: another area")
	}
	return nil
}

// CheckInboxPage says whether an answer is to the question the key asked:
// the page and page size the server names are the ones asked for, and a
// filtered page carries rows and counts of that organisation alone. The
// numbers of the page itself are checked by api.CheckNotificationPage.
func CheckInboxPage(k cache.Key, p api.NotificationPage) error {
	if err := api.CheckNotificationPage(p); err != nil {
		return err
	}
	if p.Meta.Page != k.Page {
		return fmt.Errorf("inbox: page %d answered for page %d", p.Meta.Page, k.Page)
	}
	if p.Meta.PerPage != k.PerPage {
		return fmt.Errorf("inbox: a page size of %d answered for %d", p.Meta.PerPage, k.PerPage)
	}
	if k.Organisation == "" {
		return nil
	}
	for i, row := range p.Data {
		if row.Organisation.ID != k.Organisation {
			return fmt.Errorf("inbox: data[%d] belongs to another organisation", i)
		}
	}
	for i, c := range p.Meta.ByOrganisation {
		if c.ID != k.Organisation {
			return fmt.Errorf("inbox: meta.by_organisation[%d] counts another organisation", i)
		}
	}
	return nil
}

// CheckProjectsList: every project is of the organisation asked for.
func CheckProjectsList(k cache.Key, list []api.Project) error {
	if err := api.CheckProjects(list); err != nil {
		return err
	}
	for i, p := range list {
		if p.Organisation.ID != k.Organisation {
			return fmt.Errorf("projects: data[%d] belongs to another organisation", i)
		}
	}
	return nil
}

// CheckTicketsPage: the page and page size the server names are the ones
// asked for.
func CheckTicketsPage(k cache.Key, p api.TicketPage) error {
	if err := api.CheckTicketPage(p); err != nil {
		return err
	}
	if p.Meta.Page != k.Page {
		return fmt.Errorf("tickets: page %d answered for page %d", p.Meta.Page, k.Page)
	}
	if p.Meta.PerPage != k.PerPage {
		return fmt.Errorf("tickets: a page size of %d answered for %d", p.Meta.PerPage, k.PerPage)
	}
	return nil
}

// CheckDetail: the ticket answered is the one asked for, in its project,
// with the page the key asked for (api.CheckTicket), and for one comment
// that comment alone.
func CheckDetail(k cache.Key, t api.Ticket) error {
	if err := api.CheckTicket(t, DetailOptions(k)); err != nil {
		return err
	}
	if t.ID != k.Ticket {
		return errors.New("ticket: another ticket answered")
	}
	if t.Project.ID != k.Project {
		return errors.New("ticket: a ticket of another project answered")
	}
	if k.Post != "" && (t.PostID != k.Post || len(t.Posts) != 1 || t.Posts[0].ID != k.Post) {
		return errors.New("ticket: not the one comment asked for")
	}
	return nil
}

// CheckPost: the comment answered is the one the key asked for (K1). Over
// REST the answer names no ticket: the route carries it, and a comment of
// another ticket is not found there.
func CheckPost(k cache.Key, p api.Post) error {
	if k.Post == "" || p.ID != k.Post {
		return errors.New("ticket: not the one comment asked for")
	}
	return nil
}
