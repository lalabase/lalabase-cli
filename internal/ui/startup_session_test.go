package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

// Plan test 1: before /me succeeded nothing else is asked, no namespace
// exists and no cache is read; keys do nothing.
func TestNothingIsAskedBeforeMeSucceeded(t *testing.T) {
	h := newHarness(t)
	msgs := messages(t, h.m.Init())
	if h.f.meCalls != 1 || h.f.orgCalls != 0 || h.f.initCall != 0 || h.f.toolCall != 0 || len(h.f.inboxCalls()) != 0 {
		t.Fatalf("Init asked: me %d, orgs %d, init %d, tools %d, inbox %d", h.f.meCalls, h.f.orgCalls, h.f.initCall, h.f.toolCall, len(h.f.inboxCalls()))
	}
	if h.m.app.scope != nil || h.m.app.store != nil {
		t.Fatal("a namespace or a cache before /me")
	}
	for _, key := range []string{"enter", "R", "down", "]", "esc"} {
		if cmd := h.press(key); cmd != nil {
			t.Fatalf("%s acted before /me", key)
		}
	}
	if got := ansi.Strip(h.m.View().Content); !strings.Contains(got, "Verifying identity") {
		t.Fatalf("no identity notice:\n%s", got)
	}
	// The /me result releases initialize and the organisations together;
	// the tools wait for initialize.
	cmds := h.feed(msgs...)
	if h.m.app.scope == nil || h.m.app.store == nil {
		t.Fatal("no namespace after /me")
	}
	results := messages(t, tea.Batch(cmds...))
	if h.f.initCall != 1 || h.f.orgCalls != 1 || h.f.toolCall != 0 || len(h.f.inboxCalls()) != 0 {
		t.Fatalf("after /me: init %d, orgs %d, tools %d, inbox %d", h.f.initCall, h.f.orgCalls, h.f.toolCall, len(h.f.inboxCalls()))
	}
	var initResult, orgResult gateway.StartupResult
	for _, msg := range results {
		r := msg.(gateway.StartupResult)
		if r.Step == gateway.StepInitialize {
			initResult = r
		} else {
			orgResult = r
		}
	}
	if cmds := h.feed(orgResult); len(cmds) != 0 || len(h.m.app.directory) != 2 {
		t.Fatalf("the organisations issued %d commands, directory %d", len(cmds), len(h.m.app.directory))
	}
	cmds = h.feed(initResult)
	toolMsgs := messages(t, tea.Batch(cmds...))
	if h.f.toolCall != 1 || len(h.f.inboxCalls()) != 0 {
		t.Fatalf("after initialize: tools %d, inbox %d", h.f.toolCall, len(h.f.inboxCalls()))
	}
	cmds = h.feed(toolMsgs...)
	messages(t, tea.Batch(cmds...))
	if n := len(h.f.inboxCalls()); n != 1 {
		t.Fatalf("after the tools: %d inbox reads", n)
	}
}

// Plan test 2: a healthy start is exactly five reads, the inbox one being
// the global unread page 1 of 25, and nothing for projects, tickets or
// writes (the fakes have no such readers; the inbox source sees only the
// one query).
func TestAHealthyStartMakesExactlyFiveReads(t *testing.T) {
	h := newHarness(t)
	h.start()
	if h.f.meCalls != 1 || h.f.initCall != 1 || h.f.toolCall != 1 || h.f.orgCalls != 1 {
		t.Fatalf("me %d, init %d, tools %d, orgs %d", h.f.meCalls, h.f.initCall, h.f.toolCall, h.f.orgCalls)
	}
	calls := h.f.inboxCalls()
	if len(calls) != 1 || calls[0] != (api.NotificationQuery{Status: "unread", Page: 1, PerPage: 25}) {
		t.Fatalf("inbox reads %+v", calls)
	}
	a := h.m.app
	for i := range a.steps {
		if a.steps[i].state != ready {
			t.Fatalf("step %d is %d", i, a.steps[i].state)
		}
	}
	if a.active != nil {
		t.Fatal("an inbox was opened without a choice")
	}
	if c := a.countOf(orgA); !c.known || c.value != 30 {
		t.Fatalf("A: %+v", c)
	}
	if c := a.countOf(orgB); !c.known || c.value != 3 {
		t.Fatalf("B: %+v", c)
	}
	if c := a.countOf(""); !c.known || c.value != 33 {
		t.Fatalf("all: %+v", c)
	}
	s := h.m.screen()
	if !strings.Contains(s.Identity.String(), "Anna Albers") || !strings.Contains(s.Identity.String(), "Token can write: yes") {
		t.Fatalf("identity %q", s.Identity.String())
	}
	if len(s.Chooser.Rows) != 3 || s.Chooser.Rows[0].Trailing.String() != "33" || s.Chooser.Rows[1].Trailing.String() != "30" || s.Chooser.Rows[2].Trailing.String() != "3" {
		t.Fatalf("chooser %+v", s.Chooser.Rows)
	}
	if s.Chooser.Notice.String() != "" {
		t.Fatalf("notice %q", s.Chooser.Notice.String())
	}
}

// Plan test 4: every step fails on its own; R repeats only the failed
// frontier and what it blocked, never the whole chain.
func TestRRetriesOnlyTheFailedFrontier(t *testing.T) {
	refused := failing(transport.Refused, 200)
	for _, tc := range []struct {
		name  string
		fail  func(h *harness)
		heal  func(h *harness)
		after func(h *harness) (int, int, int, int, int) // me, init, tools, orgs, inbox after R
	}{
		{"me", func(h *harness) {
			h.f.me = func() (api.Me, transport.Answer) { return api.Me{}, refused }
		}, func(h *harness) {
			h.f.me = func() (api.Me, transport.Answer) {
				return h.d.me, transport.Answer{Class: transport.Object, Status: 200, Sent: true}
			}
		}, func(*harness) (int, int, int, int, int) { return 2, 1, 1, 1, 1 }},
		{"initialize", func(h *harness) {
			h.f.init = func() (transport.ServerInfo, transport.Answer) { return transport.ServerInfo{}, refused }
		}, func(h *harness) {
			h.f.init = func() (transport.ServerInfo, transport.Answer) {
				return h.d.server, transport.Answer{Class: transport.Object, Status: 200, Sent: true}
			}
		}, func(*harness) (int, int, int, int, int) { return 1, 2, 1, 1, 1 }},
		{"tools", func(h *harness) {
			h.f.tools = func() ([]transport.Tool, transport.Answer) { return nil, refused }
		}, func(h *harness) {
			h.f.tools = func() ([]transport.Tool, transport.Answer) {
				return h.d.tools, transport.Answer{Class: transport.Object, Status: 200, Sent: true}
			}
		}, func(*harness) (int, int, int, int, int) { return 1, 1, 2, 1, 1 }},
		{"organisations", func(h *harness) {
			h.f.orgs = func() ([]api.Ref, transport.Answer) { return nil, refused }
		}, func(h *harness) {
			h.f.orgs = func() ([]api.Ref, transport.Answer) {
				return h.d.orgs, transport.Answer{Class: transport.Object, Status: 200, Sent: true}
			}
		}, func(*harness) (int, int, int, int, int) { return 1, 1, 1, 2, 1 }},
		{"counters", func(h *harness) {
			h.f.set(func(api.NotificationQuery) transport.Answer { return refused })
		}, func(h *harness) {
			h.f.set(h.d.defaultInbox)
		}, func(*harness) (int, int, int, int, int) { return 1, 1, 1, 1, 2 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			tc.fail(h)
			h.start()
			if !h.m.app.anyFailed() {
				t.Fatal("no step failed")
			}
			if got := h.m.screen().Chooser.Notice.String(); got != "Refused: refused: synthetic refused" {
				t.Fatalf("the failure is not shown: %q", got)
			}
			if !h.m.allowed()[Refresh] || !strings.Contains(helpText(h.m), "R retry") {
				t.Fatalf("R is not offered: %q", helpText(h.m))
			}
			tc.heal(h)
			h.drive(h.press("R"))
			me, init, tools, orgs, inbox := tc.after(h)
			if h.f.meCalls != me || h.f.initCall != init || h.f.toolCall != tools || h.f.orgCalls != orgs || len(h.f.inboxCalls()) != inbox {
				t.Fatalf("after R: me %d, init %d, tools %d, orgs %d, inbox %d; want %d %d %d %d %d",
					h.f.meCalls, h.f.initCall, h.f.toolCall, h.f.orgCalls, len(h.f.inboxCalls()), me, init, tools, orgs, inbox)
			}
			if h.m.app.anyFailed() {
				t.Fatal("a step is still failed")
			}
			// Healthy R from the chooser refreshes the directory and the
			// counters; nothing else.
			h.m.focus = view.Navigation
			h.drive(h.press("R"))
			if h.f.meCalls != me || h.f.initCall != init || h.f.toolCall != tools || h.f.orgCalls != orgs+1 || len(h.f.inboxCalls()) != inbox+1 {
				t.Fatalf("healthy R: me %d, init %d, tools %d, orgs %d, inbox %d", h.f.meCalls, h.f.initCall, h.f.toolCall, h.f.orgCalls, len(h.f.inboxCalls()))
			}
		})
	}
}

// A temporary failure of a step is retried once by the queue; the step
// then shows its own line and R starts it again.
func TestATemporaryStartupFailureShowsTheStepsLine(t *testing.T) {
	h := newHarness(t)
	h.f.orgs = func() ([]api.Ref, transport.Answer) { return nil, failing(transport.Temporary, 503) }
	h.start()
	if h.f.orgCalls != 2 {
		t.Fatalf("%d attempts", h.f.orgCalls)
	}
	if got := h.m.screen().Chooser.Notice.String(); got != "Cannot load organisations. R retry." {
		t.Fatalf("notice %q", got)
	}
	// A canceled attempt (the program stopping) reads the same way.
	a := h.m.app
	h.f.orgs = func() ([]api.Ref, transport.Answer) {
		return h.d.orgs, transport.Answer{Class: transport.Object, Status: 200, Sent: true}
	}
	h.feed(gateway.StartupResult{StartupTicket: gateway.StartupTicket{Session: a.session, Step: gateway.StepOrganisations, Attempt: 2}})
	cmd := h.press("R")
	if a.steps[stepOrganisations].state != loading || a.steps[stepOrganisations].attempt != 2 {
		t.Fatalf("step %+v", a.steps[stepOrganisations])
	}
	h.feed(gateway.StartupResult{StartupTicket: gateway.StartupTicket{Session: a.session, Step: gateway.StepOrganisations, Attempt: 2},
		Answer: transport.Answer{Class: transport.Canceled}})
	if a.steps[stepOrganisations].state != failed || !h.m.allowed()[Refresh] {
		t.Fatalf("a canceled attempt: %+v", a.steps[stepOrganisations])
	}
	messages(t, cmd) // the real attempt lands later and is for an attempt no longer waited for
}

// Plan test 5: the write permission comes from read_only alone: true,
// false, missing; allow_write is the profile's and never reaches the model.
func TestTheWritePermissionComesFromReadOnlyAlone(t *testing.T) {
	for _, tc := range []struct {
		readOnly *bool
		want     string
	}{
		{ptr(false), "Token can write: yes"}, {ptr(true), "Token can write: no"}, {nil, "Token can write: unknown"},
	} {
		h := newHarness(t)
		me := h.d.me
		me.Token.ReadOnly = tc.readOnly
		h.f.me = func() (api.Me, transport.Answer) {
			return me, transport.Answer{Class: transport.Object, Status: 200, Sent: true}
		}
		h.start()
		if got := h.m.screen().Identity.String(); !strings.Contains(got, tc.want) || strings.Contains(got, me.Token.ID) || strings.Contains(got, me.Token.Prefix) {
			t.Errorf("read_only %v: %q", tc.readOnly, got)
		}
	}
	h := newHarness(t)
	me := h.d.me
	me.User.DisplayName = " "
	h.f.me = func() (api.Me, transport.Answer) {
		return me, transport.Answer{Class: transport.Object, Status: 200, Sent: true}
	}
	h.start()
	if got := h.m.screen().Identity.String(); !strings.HasPrefix(got, "Unknown user") {
		t.Fatalf("blank name: %q", got)
	}
}

func ptr(b bool) *bool { return &b }

// A /me without a usable token id opens no namespace: the step fails.
func TestMeWithoutATokenIDOpensNoNamespace(t *testing.T) {
	h := newHarness(t)
	me := h.d.me
	me.Token.ID = ""
	h.f.me = func() (api.Me, transport.Answer) {
		return me, transport.Answer{Class: transport.Object, Status: 200, Sent: true}
	}
	h.start()
	if h.m.app.scope != nil || h.f.initCall != 0 || h.f.orgCalls != 0 {
		t.Fatal("the start went on without a token id")
	}
	if got := h.m.screen().Identity.String(); !strings.Contains(got, "Unexpected server response") {
		t.Fatalf("identity %q", got)
	}
}

// Plan test 8: the capabilities come from the schemas alone. Without
// organisation_id only the entry for all organisations opens, page 1 only;
// without the tool nothing opens and nothing is asked; a low version alone
// changes nothing but a notice.
func TestCapabilitiesComeFromTheSchemasAlone(t *testing.T) {
	t.Run("without the organisation filter", func(t *testing.T) {
		h := newHarness(t)
		tools := withoutArgument(h.d.tools, "list_notifications", "organisation_id")
		h.f.tools = func() ([]transport.Tool, transport.Answer) {
			return tools, transport.Answer{Class: transport.Object, Status: 200, Sent: true}
		}
		h.start()
		a := h.m.app
		if !a.inboxOn() || a.byOrgOn() || a.canOpen(orgA) || !a.canOpen("") {
			t.Fatalf("capabilities: inbox %v, filter %v", a.inboxOn(), a.byOrgOn())
		}
		if got := h.m.screen().Chooser.Notice.String(); !strings.Contains(got, "first page only") {
			t.Fatalf("notice %q", got)
		}
		if cmd := h.choose(orgA); cmd != nil || a.active != nil {
			t.Fatal("an organisation opened without the filter")
		}
		h.drive(h.choose(""))
		v := a.active
		if v == nil || v.org != "" || len(v.rows) != 25 {
			t.Fatalf("all organisations: %+v", v)
		}
		for _, q := range h.f.inboxCalls() {
			if q.OrganisationID != "" || q.Page != 1 {
				t.Fatalf("a filtered or paged read: %+v", q)
			}
		}
		if h.m.allowed()[NextPage] || h.m.allowed()[PrevPage] {
			t.Fatal("paging offered without the filter")
		}
		before := len(h.f.inboxCalls())
		h.drive(h.press("]"))
		if len(h.f.inboxCalls()) != before || v.page != 1 {
			t.Fatal("] read a global page 2")
		}
	})
	t.Run("without the inbox", func(t *testing.T) {
		h := newHarness(t)
		tools := withoutTool(h.d.tools, "list_notifications")
		h.f.tools = func() ([]transport.Tool, transport.Answer) {
			return tools, transport.Answer{Class: transport.Object, Status: 200, Sent: true}
		}
		h.start()
		a := h.m.app
		if len(h.f.inboxCalls()) != 0 || a.steps[stepCounters].state != unsupported || a.countOf(orgA).known || a.countOf("").known {
			t.Fatalf("inbox reads %d, counters %d", len(h.f.inboxCalls()), a.steps[stepCounters].state)
		}
		s := h.m.screen()
		if s.Chooser.Notice.String() != "Inbox is not supported by this server." || s.Chooser.Rows[0].Trailing.String() != "?" {
			t.Fatalf("notice %q, count %q", s.Chooser.Notice.String(), s.Chooser.Rows[0].Trailing.String())
		}
		for _, org := range []string{"", orgA} {
			if cmd := h.choose(org); cmd != nil || a.active != nil {
				t.Fatalf("%q opened without the inbox", org)
			}
		}
		if h.m.allowed()[OpenList] {
			t.Fatal("enter offered without the inbox")
		}
	})
	t.Run("a low version alone", func(t *testing.T) {
		h := newHarness(t)
		h.f.init = func() (transport.ServerInfo, transport.Answer) {
			return transport.ServerInfo{Name: "lalabase", Version: "0.6.0"}, transport.Answer{Class: transport.Object, Status: 200, Sent: true}
		}
		h.start()
		a := h.m.app
		if !a.inboxOn() || !a.byOrgOn() || !a.canOpen(orgA) {
			t.Fatal("a low version switched something off")
		}
		if got := h.m.screen().Chooser.Notice.String(); !strings.Contains(got, "0.6.0 is older than the tested") {
			t.Fatalf("notice %q", got)
		}
		h.drive(h.choose(orgA))
		if a.active == nil || len(a.active.rows) != 25 {
			t.Fatal("the inbox did not open")
		}
	})
}

// Plan test 9: a 401 ends the session for good: from the active request,
// from one the selection left behind (observed by the gateway, delivered
// through the mailbox), and before the start finished. Every field is
// cleared, later successes refill nothing, keys do nothing but leave.
func TestA401EndsTheSessionForGood(t *testing.T) {
	expired := func(t *testing.T, h *harness) {
		t.Helper()
		a := h.m.app
		if !a.expired || a.scope != nil || a.store != nil || a.report != nil || a.directory != nil || len(a.counters) != 0 ||
			a.global.known || a.active != nil || len(a.remembered) != 0 || len(a.ops) != 0 || len(a.waiters) != 0 || a.me.User.ID != "" {
			t.Fatalf("not cleared: %+v", a)
		}
		s := h.m.screen()
		if s.Status.String() != "Session expired. Restart lalabase." || s.Identity.String() != "Session expired. Restart lalabase." {
			t.Fatalf("status %q identity %q", s.Status.String(), s.Identity.String())
		}
		if len(s.Chooser.Rows) != 0 || len(s.List.Rows) != 0 {
			t.Fatal("rows survived the expiry")
		}
		if len(h.m.actions()) != 2 {
			t.Fatalf("actions %+v", h.m.actions())
		}
		calls := len(h.f.inboxCalls())
		for _, key := range []string{"R", "enter", "down", "]", "esc", "left"} {
			if cmd := h.press(key); cmd != nil {
				t.Fatalf("%s acted after the expiry", key)
			}
		}
		h.feed(tea.WindowSizeMsg{Width: 80, Height: 24})
		if len(h.f.inboxCalls()) != calls || !h.m.app.expired {
			t.Fatal("something reopened the session")
		}
	}
	t.Run("from the active request", func(t *testing.T) {
		h := newHarness(t)
		h.start()
		h.drive(h.choose(orgA))
		h.f.set(func(api.NotificationQuery) transport.Answer { return failing(transport.Unauthenticated, 401) })
		h.drive(h.press("R"))
		expired(t, h)
	})
	t.Run("from an abandoned request", func(t *testing.T) {
		h := newHarness(t)
		h.start()
		// The read of A is on its way to the server when the selection moves
		// on to B: A's waiter is canceled, the server's 401 arrives all the
		// same. The gateway observes it on the request; the waiter's own
		// result is only "canceled".
		gate := make(chan struct{})
		h.f.gate, h.f.gateOrg, h.f.ignoreCancel = gate, orgA, true
		h.f.set(func(q api.NotificationQuery) transport.Answer {
			if q.OrganisationID == orgA {
				return failing(transport.Unauthenticated, 401)
			}
			return h.d.defaultInbox(q)
		})
		result := h.background(h.choose(orgA))
		h.waitFor("the request of A", func() bool { return len(h.f.inboxCalls()) == 2 })
		h.drive(h.choose(orgB))
		canceled, _ := h.receive(result).(gateway.InboxResult)
		if canceled.Answer.Class != transport.Canceled {
			t.Fatalf("the abandoned waiter got %v", canceled.Answer.Class)
		}
		close(gate)
		h.waitFor("the observation", func() bool { return h.events.State().Revoked })
		if h.m.app.expired {
			t.Fatal("expired before any message reached the model")
		}
		h.feed(canceled) // the refused result: the mailbox is read first
		expired(t, h)
	})
	t.Run("before the start finished", func(t *testing.T) {
		h := newHarness(t)
		h.f.me = func() (api.Me, transport.Answer) { return api.Me{}, failing(transport.Unauthenticated, 401) }
		h.start()
		expired(t, h)
		if h.f.initCall != 0 || h.f.orgCalls != 0 {
			t.Fatal("the start went on after a 401")
		}
	})
	t.Run("a late success refills nothing", func(t *testing.T) {
		h := newHarness(t)
		h.start()
		held := h.choose(orgA)
		h.feed(gateway.Wake{Session: h.events.Session()}) // no revocation yet: nothing happens
		if h.m.app.expired {
			t.Fatal("a wake-up without a revocation expired the session")
		}
		h.events.Revoke()
		h.feed(gateway.Wake{Session: h.events.Session()})
		expired(t, h)
		h.feed(messages(t, held)...)
		expired(t, h)
	})
}

// Plan test 16: a 429 that only background work ran into is still shown,
// with its deadline in UTC; an extension survives the old deadline's
// expiry; a wake-up of another session changes nothing.
func TestARateLimitPauseIsShownAndClearedByItsOwnExpiry(t *testing.T) {
	h := newHarness(t)
	h.start()
	first := h.now.Add(30 * time.Second)
	h.events.RateLimit(first)
	h.feed(gateway.Wake{Session: h.events.Session()})
	if got := h.m.screen().Status.String(); got != "Rate limited until 12:00:30 UTC" {
		t.Fatalf("status %q", got)
	}
	if got := ansi.Strip(h.m.View().Content); !strings.Contains(got, "Rate limited until 12:00:30 UTC") {
		t.Fatalf("the pause is not on screen:\n%s", got)
	}
	second := h.now.Add(time.Minute)
	h.events.RateLimit(second)
	h.feed(gateway.Wake{Session: h.events.Session()})
	if got := h.m.screen().Status.String(); got != "Rate limited until 12:01:00 UTC" {
		t.Fatalf("extended status %q", got)
	}
	h.feed(gateway.PauseEnded{Session: h.events.Session(), Until: first})
	if got := h.m.screen().Status.String(); got != "Rate limited until 12:01:00 UTC" {
		t.Fatalf("an old expiry cleared the extended pause: %q", got)
	}
	h.feed(gateway.PauseEnded{Session: h.events.Session() + 1, Until: second})
	if got := h.m.screen().Status.String(); got == "" {
		t.Fatal("another session's expiry cleared the pause")
	}
	h.feed(gateway.PauseEnded{Session: h.events.Session(), Until: second})
	if got := h.m.screen().Status.String(); got != "" {
		t.Fatalf("the pause was not cleared: %q", got)
	}
	h.events.RateLimit(h.now.Add(2 * time.Minute))
	h.feed(gateway.Wake{Session: h.events.Session() + 1})
	if got := h.m.screen().Status.String(); got != "" {
		t.Fatalf("another session's wake-up showed a pause: %q", got)
	}
	// Data stays while the queue waits.
	if len(h.m.app.directory) != 2 || !h.m.app.countOf(orgA).known {
		t.Fatal("the pause lost data")
	}
}
