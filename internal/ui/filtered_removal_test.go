package ui

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/queue"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
	"github.com/gboyke/lalabase-cli/internal/worker"
)

func TestR3ResourceRemovalRecovery(t *testing.T) {
	t.Parallel()

	for _, again := range []bool{false, true} {
		t.Run(map[bool]string{false: "shrink", true: "removed-again"}[again], func(t *testing.T) {
			h := newHarness(t)
			h.start()
			h.drive(h.choose(""))
			h.drive(h.press("]"))
			h.drive(h.press("enter"))
			h.press("left")
			h.press("left")
			h.f.set(func(api.NotificationQuery) transport.Answer { return failing(transport.NoAccess, 200) })
			removed := messages(t, h.press("R"))
			// Keep details focused when the counters' removal of page 1 arrives.
			h.press("right")
			h.press("right")
			h.drive(h.feed(removed...)...)
			a := h.m.app
			if a.active.page != 2 || len(a.active.rows) != 0 || a.active.selected != "" || a.active.opened != "" || h.m.focus != view.List {
				t.Fatalf("removal left state: focus=%d view=%+v", h.m.focus, a.active)
			}
			for p := 1; p <= 2; p++ {
				if _, ok := a.store.Get(a.inboxKey("", p)); ok {
					t.Fatalf("page %d survived removal", p)
				}
			}
			before := len(h.f.inboxCalls())
			if !again {
				h.f.set(func(q api.NotificationQuery) transport.Answer {
					rows := slice(h.d.rows(orgA, 501, 3), q.Page, q.PerPage)
					return pageAnswer(q.Page, q.PerPage, 3, 3, rows, map[string]int{orgA: 3})
				})
			}
			h.drive(h.press("R"))
			reads := h.f.inboxCalls()[before:]
			t.Logf("again=%v recovery queries=%+v page=%d rows=%d notice=%q", again, reads, a.active.page, len(a.active.rows), h.m.listColumn().Notice.String())
			if again {
				if len(reads) != 2 || len(a.active.rows) != 0 || a.active.known || a.store.Len() != 0 {
					t.Fatal("repeat removal resurrected resource or multiplied reads")
				}
			} else {
				if len(reads) != 3 || reads[0].Page != 1 || reads[1].Page != 2 || reads[2].Page != 1 || a.active.page != 1 || len(a.active.rows) != 3 || a.active.rows[0].ID != idOf(501) {
					t.Fatal("R failed page-2 read plus one corrective page-1 read")
				}
			}
		})
	}
}

func TestR3FilteredRemovalLeavesGlobalView(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.start()
	h.drive(h.choose(""))
	h.drive(h.press("enter"))
	a := h.m.app
	before := h.m.screen()
	global := a.global
	key := a.active.key
	h.f.set(func(api.NotificationQuery) transport.Answer { return failing(transport.NoAccess, 200) })
	h.drive(a.issue(gateway.Inbox, a.inboxKey(orgA, 1), forInbox, queue.User))
	after := h.m.screen()
	if !reflect.DeepEqual(before.List, after.List) || !reflect.DeepEqual(before.Object, after.Object) || before.Context != after.Context || a.global != global || a.active.opened == "" {
		t.Fatal("foreign resource removal changed global view")
	}
	if _, ok := a.store.Get(key); !ok {
		t.Fatal("foreign resource removal cleared global cache")
	}
}

func TestR3SnapshotRankDoesNotSuppressFilteredRows(t *testing.T) {
	t.Parallel()

	for _, removal := range []bool{false, true} {
		t.Run(map[bool]string{false: "rows", true: "removal"}[removal], func(t *testing.T) {
			h := newHarness(t)
			global := h.startHolding()
			if removal {
				h.f.set(func(api.NotificationQuery) transport.Answer { return failing(transport.NoAccess, 200) })
			}
			old := messages(t, h.choose(orgA))[0].(gateway.InboxResult)
			h.f.set(func(q api.NotificationQuery) transport.Answer {
				return pageAnswer(q.Page, q.PerPage, 3, 3, h.d.rows(orgB, 101, 3), map[string]int{orgB: 3})
			})
			newer := messages(t, global)[0].(gateway.InboxResult)
			if old.Flight >= newer.Flight {
				t.Fatal("setup order")
			}
			h.feed(newer, old)
			a := h.m.app
			if a.countOf(orgA).known || countShown(h.m, orgA) != "?" {
				t.Fatal("old filtered counter defeated snapshot")
			}
			_, cached := a.store.Get(old.Key)
			if !removal && (len(a.active.rows) != 25 || !cached || a.active.loading || a.active.rows[0].ID != idOf(1)) {
				t.Fatal("snapshot suppressed valid per-key rows")
			}
			if removal && (len(a.active.rows) != 0 || cached || a.active.loading) {
				t.Fatal("filtered removal did not settle")
			}
		})
	}
}

func TestR3ExpiredMailboxEveryMessageAndLateCommands(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"wake", "mode", "batch", "nil", "pause", "resize", "key", "late-me"} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.start()
			h.drive(h.choose(orgA))
			h.drive(h.press("enter"))
			a := h.m.app
			store := a.store
			held := h.press("R")
			before := len(h.f.inboxCalls())
			h.events.Observe(func(context.Context) transport.Answer { return failing(transport.Protocol, 401) })(context.Background())
			var msg tea.Msg
			switch name {
			case "wake":
				msg = gateway.Wake{Session: a.session}
			case "mode":
				msg = tea.ModeReportMsg{Mode: ansi.ModeUnicodeCore, Value: ansi.ModeSet}
			case "batch":
				msg = tea.BatchMsg{}
			case "pause":
				msg = gateway.Expired{Session: a.session, Until: h.now.Add(time.Second)}
			case "resize":
				msg = tea.WindowSizeMsg{Width: 90, Height: 20}
			case "key":
				msg = press("R")
			case "late-me":
				msg = gateway.StartupResult{StartupTicket: gateway.StartupTicket{Session: a.session, Step: gateway.StepMe, Attempt: 1}, Me: h.d.me, Answer: transport.Answer{Class: transport.Object, Status: 200}}
			}
			h.drive(h.feed(msg)...)
			h.drive(held)
			for _, key := range []string{"R", "enter", "esc", "]", "[", "left", "right", "up", "down", "pgup", "pgdown"} {
				h.drive(h.press(key))
			}
			h.drive(h.feed(gateway.Wake{Session: a.session})...)
			if !a.expired || a.active != nil || a.store != nil || a.scope != nil || len(a.directory) != 0 || len(a.counters) != 0 || len(a.waiters) != 0 || len(a.filed) != 0 || a.snapshot != 0 || a.me.Token.ID != "" || store.Len() != 0 || len(h.f.inboxCalls()) != before {
				t.Fatal("revoked session retained state or late command sent")
			}
			if strings.Contains(h.m.View().Content, "Message 0001") {
				t.Fatal("expired row rendered")
			}
		})
	}
}

func TestR3RefreshHelpDisappearsWhileBothReadsPending(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.start()
	h.drive(h.choose(""))
	h.drive(h.press("]"))
	gate := make(chan struct{})
	defer close(gate)
	h.f.gate, h.f.gateOrg, h.f.gatePage = gate, "", 1
	h.press("esc")
	before := len(h.f.inboxCalls())
	batch := h.press("R")().(tea.BatchMsg)
	for _, cmd := range batch {
		h.background(cmd)
	}
	h.waitFor("counters on wire", func() bool { return len(h.f.inboxCalls()) == before+1 })
	h.press("enter")
	h.f.set(func(q api.NotificationQuery) transport.Answer {
		if q.Page == 2 {
			return failing(transport.Temporary, 503)
		}
		return h.d.defaultInbox(q)
	})
	h.drive(h.press("R"))
	if h.m.app.steps[stepCounters].state != failed {
		t.Fatal("page 2 must fail counters step")
	}
	// R starts another page-2 read; page 1 is still on the wire.
	secondGate := make(chan struct{})
	defer close(secondGate)
	h.f.mu.Lock()
	h.f.gate, h.f.gatePage = secondGate, 2
	h.f.mu.Unlock()
	beforeRetry := len(h.f.inboxCalls())
	pending := h.press("R")
	if pending == nil || !h.m.app.pending(h.m.app.inboxKey("", 1)) || !h.m.app.pending(h.m.app.inboxKey("", 2)) {
		t.Fatal("need both reads pending")
	}
	h.background(pending)
	h.waitFor("page 2 retry on wire", func() bool { return len(h.f.inboxCalls()) == beforeRetry+1 })
	help := helpText(h.m)
	offered := h.m.allowed()[Refresh]
	cmd := h.press("R")
	if offered && cmd == nil {
		t.Fatalf("R advertised but idle while page 1 and 2 pending: counters state=%d help=%q", h.m.app.steps[stepCounters].state, help)
	}
}

func TestR3FatalMessageStillExpiresObserved401(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.start()
	h.drive(h.choose(orgA))
	h.drive(h.press("enter"))
	a, oldStore := h.m.app, h.m.app.store
	h.events.Observe(func(context.Context) transport.Answer { return failing(transport.Protocol, 401) })(context.Background())
	// A command's supervised panic and the 401 can arrive in either order.
	worker.Run(h.live, "review3: pending command", func() bool { panic("synthetic private panic value") })
	g := guarded{model: h.m, sup: h.live}
	msg := watchFatal(h.live.Failed(), h.live.Context().Done())()
	next, quit := g.Update(msg)
	if _, ok := quit().(tea.QuitMsg); !ok {
		t.Fatal("fatal did not quit")
	}
	finalView := next.View().Content
	if !a.expired || oldStore.Len() != 0 || strings.Contains(finalView, "Message 0001") {
		t.Fatalf("Fatal bypassed 401 expiration: expired=%v cache entries=%d old row in final view=%v", a.expired, oldStore.Len(), strings.Contains(finalView, "Message 0001"))
	}
}
