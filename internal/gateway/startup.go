package gateway

import (
	"context"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/queue"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/worker"
)

// Step is one of the four reads of the start: who the token belongs to,
// what the server is, what it offers, which organisations there are.
type Step uint8

const (
	StepMe Step = iota
	StepInitialize
	StepTools
	StepOrganisations
)

func (s Step) String() string {
	switch s {
	case StepMe:
		return "me"
	case StepInitialize:
		return "initialize"
	case StepTools:
		return "tools"
	case StepOrganisations:
		return "organisations"
	}
	return "unknown"
}

// StartupTicket names one attempt of one step in one session. The result
// carries it unchanged, and the model accepts the result only for the
// attempt it is waiting for.
type StartupTicket struct {
	Session Session
	Step    Step
	Attempt uint64
}

// StartupResult is the typed answer of one startup command. Only the field
// of its step is filled, and only when Answer is an object: a step whose
// attempts all failed carries no half-made value.
type StartupResult struct {
	StartupTicket
	Me            api.Me
	Server        transport.ServerInfo
	Tools         []transport.Tool
	Organisations []api.Ref
	Answer        transport.Answer
}

// Discoverer answers initialize and tools/list; *transport.Client in the
// program.
type Discoverer interface {
	Initialize(ctx context.Context) (transport.ServerInfo, transport.Answer)
	ListTools(ctx context.Context) ([]transport.Tool, transport.Answer)
}

// Identity answers /me and /organisations; *api.API in the program.
type Identity interface {
	Me(ctx context.Context) (api.Me, transport.Answer)
	Organisations(ctx context.Context) ([]api.Ref, transport.Answer)
}

// Startup turns startup tickets into commands. Every one is an unshared
// read of the person's priority through the queue: /me, initialize and
// tools/list must never join a request somebody else sent, and the queue
// retries them once by its own rules.
type Startup struct {
	sup      *worker.Supervisor
	queue    Doer
	server   Discoverer
	identity Identity
	events   *Events
}

func NewStartup(sup *worker.Supervisor, queue Doer, server Discoverer, identity Identity, events *Events) *Startup {
	return &Startup{sup: sup, queue: queue, server: server, identity: identity, events: events}
}

// Run is the command for one attempt of one step. The closure assigns its
// value on every execution, retries included, and the result publishes it
// only after a final success. A command that comes after Stop is a canceled
// result with its ticket; a panic in it is the fatal error.
func (s *Startup) Run(ctx context.Context, t StartupTicket) tea.Cmd {
	return func() tea.Msg {
		msg, ok := worker.Run(s.sup, "gateway: startup", func() tea.Msg {
			var out StartupResult
			job := queue.Job{Priority: queue.User, Kind: queue.Read, Run: s.events.Observe(func(ctx context.Context) transport.Answer {
				out = s.read(ctx, t.Step)
				return out.Answer
			})}
			ans, _ := s.queue.DoShared(ctx, job)
			if !ans.OK() {
				out = StartupResult{}
			}
			out.StartupTicket, out.Answer = t, ans
			return out
		})
		if !ok {
			if s.sup.Err() != nil {
				return worker.Fatal{}
			}
			return StartupResult{StartupTicket: t, Answer: transport.Answer{Class: transport.Canceled, Message: "canceled before it was sent"}}
		}
		return msg
	}
}

// read performs one step's request.
func (s *Startup) read(ctx context.Context, step Step) StartupResult {
	var out StartupResult
	switch step {
	case StepMe:
		out.Me, out.Answer = s.identity.Me(ctx)
	case StepInitialize:
		out.Server, out.Answer = s.server.Initialize(ctx)
	case StepTools:
		out.Tools, out.Answer = s.server.ListTools(ctx)
	case StepOrganisations:
		out.Organisations, out.Answer = s.identity.Organisations(ctx)
	default:
		out.Answer = transport.Answer{Class: transport.Protocol, Message: "startup: unknown step"}
	}
	return out
}
