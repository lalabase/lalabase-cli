package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/worker"
)

func TestR2LiveObserved401BeforeResizeClearsScreenAndCache(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.start()
	h.events = gateway.NewEvents(h.live)
	h.m.app.deps.Events = h.events
	h.m.app.session = h.events.Session()
	h.m.app.deps.Gateway = gateway.New(h.live, h.q, h.f, gateway.WithEvents(h.events))
	wakeResult := h.background(h.events.Wait())
	gate := make(chan struct{})
	h.f.gate, h.f.gateOrg, h.f.ignoreCancel = gate, orgA, true
	h.f.set(func(q api.NotificationQuery) transport.Answer {
		if q.OrganisationID == orgA {
			return failing(transport.Unauthenticated, 401)
		}
		return h.d.defaultInbox(q)
	})
	oldResult := h.background(h.choose(orgA))
	h.waitFor("A request on wire", func() bool { return len(h.f.inboxCalls()) == 2 })
	h.drive(h.choose(orgB))
	h.drive(h.press("enter"))
	_ = h.receive(oldResult)
	oldStore, oldKey := h.m.app.store, h.m.app.active.key
	close(gate)
	h.waitFor("real request's 401 observation", func() bool { return h.events.State().Revoked })
	wake := h.receive(wakeResult) // completed watcher, but model delivery withheld
	h.feed(tea.WindowSizeMsg{Width: 100, Height: 30})
	_, retained := oldStore.Get(oldKey)
	if !h.m.app.expired || retained || strings.Contains(h.m.View().Content, "Message 0101") {
		t.Errorf("resize before queued live Wake: expired=%v cache retained=%v foreign row rendered=%v", h.m.app.expired, retained, strings.Contains(h.m.View().Content, "Message 0101"))
	}
	h.feed(wake)
	if !h.m.app.expired {
		t.Fatal("queued Wake did not expire")
	}
}

// Hold the mailbox's Wake until after the next input. The observation is
// real; only delivery to the single-threaded model is deliberately delayed.
func TestR2Observed401BeforeNonDataEventClearsScreenAndCache(t *testing.T) {
	t.Parallel()

	for _, event := range []string{"resize", "pause expiry", "q", "ctrl+c", "late startup", "unknown waiter"} {
		t.Run(event, func(t *testing.T) {
			h := newHarness(t)
			h.start()
			h.drive(h.choose(orgA))
			h.drive(h.press("enter"))
			a := h.m.app
			oldStore, oldKey := a.store, a.active.key
			until := h.now.Add(time.Second)
			a.pause = until
			h.events.Observe(func(context.Context) transport.Answer {
				return transport.Answer{Class: transport.NoAccess, Status: 401, Sent: true}
			})(context.Background())
			switch event {
			case "resize":
				h.feed(tea.WindowSizeMsg{Width: 100, Height: 30})
			case "pause expiry":
				h.feed(gateway.Expired{Session: a.session, Until: until})
			case "q", "ctrl+c":
				h.press(event)
			case "late startup":
				h.feed(gateway.StartupResult{StartupTicket: gateway.StartupTicket{Session: a.session, Step: gateway.StepMe, Attempt: 1}})
			case "unknown waiter":
				h.feed(gateway.InboxResult{})
			}
			_, retained := oldStore.Get(oldKey)
			screen := h.m.View().Content
			if !a.expired || a.active != nil || a.store != nil || retained || !strings.Contains(screen, "Session expired") {
				t.Errorf("after %s: expired=%v active=%v store=%v old cache retained=%v old row rendered=%v", event, a.expired, a.active != nil, a.store != nil, retained, strings.Contains(screen, "Message 0001"))
			}
			// The eventual wake must still clear everything; this distinguishes
			// a revocation timing gap from a session that resurrects later.
			h.feed(gateway.Wake{Session: a.session})
			if !a.expired || a.active != nil || a.store != nil {
				t.Fatal("wake did not expire")
			}
		})
	}
}

type r2PanicModel struct{ where string }

func (m r2PanicModel) Init() tea.Cmd {
	if m.where == "init" {
		panic("R2 untrusted panic value")
	}
	if m.where == "init command" {
		return func() tea.Msg { panic("R2 untrusted panic value") }
	}
	return nil
}

func (m r2PanicModel) Update(tea.Msg) (tea.Model, tea.Cmd) {
	if m.where == "update command" {
		return m, func() tea.Msg { panic("R2 untrusted panic value") }
	}
	return r2PanicModel{where: "view"}, tea.Quit
}

func (m r2PanicModel) View() tea.View {
	if m.where == "view" {
		panic("R2 untrusted panic value")
	}
	return tea.NewView("ready")
}

func TestR2GuardSeams(t *testing.T) {
	t.Parallel()

	for _, where := range []string{"init", "final view", "init command", "update command"} {
		t.Run(where, func(t *testing.T) {
			sup := worker.New(context.Background())
			defer sup.Stop(time.Second)
			g := guarded{model: r2PanicModel{where: where}, sup: sup}
			var escaped any
			func() {
				defer func() { escaped = recover() }()
				switch where {
				case "init":
					g.Init()
				case "final view":
					next, cmd := g.Update("quit")
					if _, ok := cmd().(tea.QuitMsg); !ok {
						t.Fatal("no quit")
					}
					next.View()
				case "init command":
					batch := g.Init()().(tea.BatchMsg)
					batch[0]()
				case "update command":
					_, cmd := g.Update("start")
					cmd()
				}
			}()
			if escaped != nil || sup.Err() == nil {
				t.Errorf("%s: panic escaped=%v supervisor error=%v", where, escaped != nil, sup.Err())
			}
		})
	}
}

func TestR2ExpireCancelsHeldInboxWaiter(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.start()
	gate := make(chan struct{})
	defer close(gate)
	h.f.gate, h.f.gateOrg = gate, orgA
	result := h.background(h.choose(orgA))
	h.waitFor("held inbox on wire", func() bool { return len(h.f.inboxCalls()) == 2 })
	h.events.Observe(func(context.Context) transport.Answer {
		return transport.Answer{Class: transport.Refused, Status: 401, Sent: true}
	})(context.Background())
	h.press("R")
	select {
	case msg := <-result:
		if r, ok := msg.(gateway.InboxResult); !ok || r.Answer.Class != transport.Canceled {
			t.Fatalf("expire returned %T, want canceled InboxResult", msg)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("expired session left inbox waiter running")
	}
}

func TestR2Mailbox401OutranksOlderRemovalOrder(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.start()
	// A's first flight completes while its model message remains undelivered.
	old := messages(t, h.choose(orgA))[0].(gateway.InboxResult)
	h.drive(h.choose(orgB))
	h.drive(h.choose(orgA))
	if h.m.app.filed[old.Key] <= uint64(old.Flight) {
		t.Fatal("setup has no newer filed flight")
	}
	// The mailbox can also learn 401 on a different concurrent request.
	// The next model message is an old removal that order/scope must reject.
	h.events.Observe(func(context.Context) transport.Answer {
		return transport.Answer{Class: transport.NoAccess, Status: 401, Sent: true}
	})(context.Background())
	old.Answer = transport.Answer{Class: transport.NoAccess, Status: 200, Sent: true}
	h.feed(old)
	if !h.m.app.expired || h.m.app.active != nil || h.m.app.store != nil {
		t.Fatal("older removal's refusal hid the mailbox revocation")
	}
}

func TestR2ForeignFatalCurrentlyQuitsWithoutOwnFailure(t *testing.T) {
	t.Parallel()

	other := worker.New(context.Background())
	defer other.Stop(time.Second)
	worker.Run(other, "other session", func() bool { panic("foreign") })
	msg := watchFatal(other.Failed(), other.Context().Done())()
	own := worker.New(context.Background())
	defer own.Stop(time.Second)
	g := guarded{model: r2PanicModel{}, sup: own}
	_, cmd := g.Update(msg)
	if _, ok := cmd().(tea.QuitMsg); !ok || own.Err() != nil {
		t.Fatal("foreign-fatal probe changed")
	}
	t.Log("injected foreign Fatal quits although own supervisor is healthy; no production cross-program message path found")
}

type r2FailDuringUpdate struct{ sup *worker.Supervisor }

func (m r2FailDuringUpdate) Init() tea.Cmd  { return nil }
func (m r2FailDuringUpdate) View() tea.View { return tea.NewView("ready") }
func (m r2FailDuringUpdate) Update(tea.Msg) (tea.Model, tea.Cmd) {
	m.sup.Start("r2 concurrent failure", func(context.Context) { panic("R2 untrusted panic value") })
	select {
	case <-m.sup.Failed():
	case <-time.After(time.Second):
		panic("failure did not arrive")
	}
	return m, nil
}

func TestR2FatalDuringUpdateIsDurableAndQuits(t *testing.T) {
	t.Parallel()

	sup := worker.New(context.Background())
	defer sup.Stop(time.Second)
	g := guarded{model: r2FailDuringUpdate{sup: sup}, sup: sup}
	watch := g.Init()
	model, _ := g.Update("update")
	msg := watch()
	if _, ok := msg.(worker.Fatal); !ok {
		t.Fatal("the close during Update lost Fatal")
	}
	_, quit := model.Update(msg)
	if _, ok := quit().(tea.QuitMsg); !ok || sup.Err() == nil || strings.Contains(sup.Err().Error(), "untrusted") {
		t.Fatal("the concurrent failure was not safely durable")
	}
}
