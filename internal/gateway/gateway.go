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
	"github.com/gboyke/lalabase-cli/internal/queue"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/worker"
)

// Doer runs a shared read and names its request; *queue.Queue in the
// program.
type Doer interface {
	DoShared(ctx context.Context, job queue.Job) (transport.Answer, queue.Flight)
}

// Source sends the inbox request; *api.API in the program.
type Source interface {
	NotificationsAnswer(ctx context.Context, q api.NotificationQuery) transport.Answer
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

// Inbox reads the inbox page t names, for the waiter whose context ctx is.
// The shared request runs on the queue's context, so this waiter leaving
// cancels it only when it was the last one. A ticket that is no inbox read
// (CheckInboxTicket) is refused before anything is sent, and a page that
// contradicts the question (CheckInboxPage) before the model sees it.
func (g *Gateway) Inbox(ctx context.Context, t Ticket) tea.Cmd {
	if err := CheckInboxTicket(t); err != nil {
		return func() tea.Msg {
			return InboxResult{Ticket: t, Answer: transport.Answer{Class: transport.InvalidInput, Message: err.Error(), Err: err}}
		}
	}
	query := InboxQuery(t.Key)
	// Observed on the request itself: a 401 is recorded whatever becomes of
	// this waiter's result (canceled, refused as old data).
	job := queue.Job{Priority: t.Priority, Kind: queue.Read, Key: t.ShareKey(), Run: g.events.Observe(func(shared context.Context) transport.Answer {
		return g.src.NotificationsAnswer(shared, query)
	})}
	return func() tea.Msg {
		msg, ok := worker.Run(g.sup, "gateway: inbox", func() tea.Msg {
			ans, flight := g.queue.DoShared(ctx, job)
			ans.Payload = bytes.Clone(ans.Payload) // the waiters share the answer
			page, ans := api.DecodeNotifications(ans)
			if ans.OK() {
				if err := CheckInboxPage(t.Key, page); err != nil {
					page, ans = api.NotificationPage{}, transport.Answer{Class: transport.Protocol, Status: ans.Status, Message: err.Error(), Err: err, Sent: ans.Sent}
				}
			}
			return InboxResult{Ticket: t, Page: page, Answer: ans, Flight: flight}
		})
		if !ok {
			if g.sup.Err() != nil {
				return worker.Fatal{}
			}
			// Not run: the session stopped before this command came.
			return InboxResult{Ticket: t, Answer: transport.Answer{Class: transport.Canceled, Message: "canceled before it was sent"}}
		}
		return msg
	}
}

// InboxQuery is the request for an inbox key: everything sent comes from the
// key, so the cache cannot file an answer under other options.
func InboxQuery(k cache.Key) api.NotificationQuery {
	return api.NotificationQuery{Status: k.Status, Page: k.Page, PerPage: k.PerPage, ProjectID: k.Project, OrganisationID: k.Organisation}
}

// InboxPageSize is the one page size C1b reads with.
const InboxPageSize = 25

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
