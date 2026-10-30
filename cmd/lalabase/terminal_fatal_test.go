//go:build linux || darwin

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/cache"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/queue"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/ui"
	"github.com/gboyke/lalabase-cli/internal/worker"
)

// The fatal scenarios put real work under the session's supervisor and fail
// it at one place each; "!" starts it once the terminal is ready.
var fatalScenarios = map[string]string{
	"panic-flight":  "queue: shared read",
	"panic-pause":   "queue: pause",
	"panic-gateway": "gateway: inbox",
}

// inboxOnly is a source of the inbox alone: the other reads of the
// gateway's Source are refused, so a fault source needs one method.
type inboxOnly struct{}

func (inboxOnly) ProjectsAnswer(context.Context, string) transport.Answer {
	return transport.Answer{Class: transport.Refused, Message: "not in this test"}
}

func (inboxOnly) TicketsAnswer(context.Context, api.TicketQuery) transport.Answer {
	return transport.Answer{Class: transport.Refused, Message: "not in this test"}
}

func (inboxOnly) TicketAnswer(context.Context, string, string, api.TicketOptions) transport.Answer {
	return transport.Answer{Class: transport.Refused, Message: "not in this test"}
}

func (inboxOnly) TicketsRESTAnswer(context.Context, api.TicketQuery) transport.Answer {
	return transport.Answer{Class: transport.Refused, Message: "not in this test"}
}

func (inboxOnly) TicketRESTAnswer(context.Context, string, api.TicketOptions) transport.Answer {
	return transport.Answer{Class: transport.Refused, Message: "not in this test"}
}

func (inboxOnly) PostRESTAnswer(context.Context, string, string) transport.Answer {
	return transport.Answer{Class: transport.Refused, Message: "not in this test"}
}

func (inboxOnly) MarkNotificationRead(context.Context, string, string) (api.NotificationState, transport.Answer) {
	return api.NotificationState{}, transport.Answer{Class: transport.Refused, Message: "not in this test"}
}

func (inboxOnly) UpdateTicket(context.Context, string, string, api.TicketChange, string) (api.TicketRow, transport.Answer) {
	return api.TicketRow{}, transport.Answer{Class: transport.Refused, Message: "not in this test"}
}

func (inboxOnly) PostComment(context.Context, string, string, string, api.CommentOptions, string) (api.Comment, transport.Answer) {
	return api.Comment{}, transport.Answer{Class: transport.Refused, Message: "not in this test"}
}

func (inboxOnly) BookedTimeAnswer(context.Context, string, string) transport.Answer {
	return transport.Answer{Class: transport.Refused, Message: "not in this test"}
}

type panicSource struct{ inboxOnly }

func (panicSource) NotificationsAnswer(context.Context, api.NotificationQuery) transport.Answer {
	panic("C1b flight fault")
}

type throttledSource struct{ inboxOnly }

func (throttledSource) NotificationsAnswer(context.Context, api.NotificationQuery) transport.Answer {
	return transport.Answer{Class: transport.Temporary, Status: 429, RetryAt: time.Now().Add(50 * time.Millisecond), Sent: true}
}

type panicQueue struct{}

func (panicQueue) DoShared(context.Context, queue.Job) (transport.Answer, queue.Flight) {
	panic("C1b gateway fault")
}

// pauseClock panics once, at the first Now after a timer fired: the pause
// worker asking whether the window is open.
type pauseClock struct{ armed atomic.Bool }

func (c *pauseClock) Now() time.Time {
	if c.armed.CompareAndSwap(true, false) {
		panic("C1b pause fault")
	}
	return time.Now()
}

func (c *pauseClock) After(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	time.AfterFunc(d, func() { c.armed.Store(true); ch <- time.Now() })
	return ch
}

func fatalScenario(scenario string, sup *worker.Supervisor) tea.Cmd {
	ns, _ := cache.Confirmed("default", "http://127.0.0.1:1", "token-synthetic")
	key := ns.Key("list_notifications")
	key.Global, key.Status, key.Page, key.PerPage = true, "unread", 1, 25
	ticket := gateway.NewScope(ns).Begin(gateway.Inbox, key, queue.User)
	switch scenario {
	case "panic-flight":
		return gateway.New(sup, queue.New(queue.WithLifetime(sup)), panicSource{}).Inbox(sup.Context(), ticket)
	case "panic-pause":
		q := queue.New(queue.WithLifetime(sup), queue.WithClock(&pauseClock{}))
		return gateway.New(sup, q, throttledSource{}).Inbox(sup.Context(), ticket)
	case "panic-gateway":
		return gateway.New(sup, panicQueue{}, nil).Inbox(sup.Context(), ticket)
	case "worker-hang":
		return func() tea.Msg {
			never := make(chan struct{})
			sup.Start("test: ignores its context", func(context.Context) { <-never })
			return nil
		}
	}
	return nil
}

type triggerModel struct {
	tea.Model
	trigger tea.Cmd
}

func (m triggerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok && key.String() == "!" {
		return m, m.trigger
	}
	next, cmd := m.Model.Update(msg)
	m.Model = next
	return m, cmd
}

// fatalRunner is the child's interface for the scenarios above. It reports
// where the fatal error was recorded once the terminal is restored.
func fatalRunner(scenario string) func(context.Context, io.Reader, io.Writer, []string, *worker.Supervisor) error {
	return func(ctx context.Context, in io.Reader, out io.Writer, environment []string, sup *worker.Supervisor) error {
		err := ui.Run(ctx, in, out, environment, triggerModel{Model: ui.New(ui.WithSupervisor(sup)), trigger: fatalScenario(scenario, sup)}, sup)
		var fatal *worker.FatalError
		if errors.As(sup.Err(), &fatal) {
			_, _ = fmt.Fprintf(os.Stdout, "C1B_FATAL=%s\n", fatal.Where)
		}
		return err
	}
}

// A panic at each of the three places ends the program through the fatal
// path: the terminal comes back as it was, the safe text is printed, nothing
// of the panic is, and the exit code is 1.
func TestPTYFatalErrorsRestoreTheTerminalAndExitOne(t *testing.T) {
	t.Parallel()

	for scenario, where := range fatalScenarios {
		t.Run(scenario, func(t *testing.T) {
			c := startTerminal(t, scenario, 80)
			if err := c.ready(); err != nil {
				t.Fatal(err)
			}
			if err := c.send("!"); err != nil {
				t.Fatal(err)
			}
			c.finish(t, 1)
			out := c.bytes()
			if !strings.Contains(out, "C1B_FATAL="+where+"\r\n") {
				t.Fatalf("not recorded at %q:\n%q", where, out)
			}
			if !strings.Contains(out, "lalabase: terminal interface failed.") {
				t.Fatal("the safe text is missing")
			}
			if strings.Contains(out, "C1b ") || strings.Contains(out, "Caught panic") || strings.Contains(out, "goroutine ") {
				t.Fatalf("the panic reached the terminal:\n%q", out)
			}
		})
	}
}

// Work that ignores its cancellation does not hold the exit: "q" still ends
// with 0 within the cleanup limit.
func TestPTYAHangingWorkerDoesNotHoldTheExit(t *testing.T) {
	t.Parallel()

	c := startTerminal(t, "worker-hang", 80)
	if err := c.ready(); err != nil {
		t.Fatal(err)
	}
	if err := c.send("!q"); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	c.finish(t, 0)
	if took := time.Since(start); took > workerCleanup+exitLimit {
		t.Fatalf("exit took %s", took)
	}
}
