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

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/cache"
	"github.com/gboyke/lalabase-cli/internal/queue"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/worker"
)

// Doer runs a job; *queue.Queue in the program.
type Doer interface {
	Do(ctx context.Context, job queue.Job) transport.Answer
}

// Source sends the inbox request; *api.API in the program.
type Source interface {
	NotificationsAnswer(ctx context.Context, q api.NotificationQuery) transport.Answer
}

// Gateway turns tickets into commands.
type Gateway struct {
	sup   *worker.Supervisor
	queue Doer
	src   Source
}

func New(sup *worker.Supervisor, queue Doer, src Source) *Gateway {
	return &Gateway{sup: sup, queue: queue, src: src}
}

// InboxResult is one waiter's inbox page. Page is empty unless Answer is an
// object.
type InboxResult struct {
	Ticket
	Page   api.NotificationPage
	Answer transport.Answer
}

// Inbox reads the inbox page t names, for the waiter whose context ctx is.
// The shared request runs on the queue's context, so this waiter leaving
// cancels it only when it was the last one.
func (g *Gateway) Inbox(ctx context.Context, t Ticket) tea.Cmd {
	query := InboxQuery(t.Key)
	job := queue.Job{Priority: t.Priority, Kind: queue.Read, Key: t.ShareKey(), Run: func(shared context.Context) transport.Answer {
		return g.src.NotificationsAnswer(shared, query)
	}}
	return func() tea.Msg {
		msg, ok := worker.Run(g.sup, "gateway: inbox", func() tea.Msg {
			ans := g.queue.Do(ctx, job)
			ans.Payload = bytes.Clone(ans.Payload) // the waiters share the answer
			page, ans := api.DecodeNotifications(ans)
			return InboxResult{Ticket: t, Page: page, Answer: ans}
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
