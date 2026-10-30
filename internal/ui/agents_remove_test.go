package ui

// C9c: d removes an ended or lost agent, in the agents' list and in the
// pane of its ticket, without asking (owner, 2026-10-28); the result and a
// seen mark that could not be written are told in the status line.

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/agents"
	"github.com/gboyke/lalabase-cli/internal/launch"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

// d is offered on an ended or lost agent alone, removes it through the
// watcher, takes it out of the area at once and says so.
func TestDRemovesAnEndedAgentFromTheList(t *testing.T) {
	t.Parallel()

	for _, state := range []agents.State{agents.Ended, agents.Lost} {
		h := newHarness(t)
		f := h.withWatcher(agentIn(1, orgA, 6, state, h.now), agentIn(2, orgA, 7, agents.Working, h.now))
		h.start()
		h.drive(h.press("A"))
		if ag, _ := h.m.app.agentUnderCursor(); ag.Number != 6 {
			// The working #7 is the more urgent of the two: d on it is
			// not offered, so the cursor moves to #6.
			if h.offers(RemoveAgent) {
				t.Fatalf("%v: d offered on the working #7", state)
			}
			h.drive(h.press("down"))
		}
		if ag, _ := h.m.app.agentUnderCursor(); ag.Number != 6 {
			t.Fatalf("cursor on #%d", ag.Number)
		}
		if !h.offers(RemoveAgent) {
			t.Fatalf("%v: d is not offered", state)
		}
		h.drive(h.press("d"))
		if got := f.removals(); len(got) != 1 || got[0] != agentIn(1, orgA, 6, state, h.now).ID {
			t.Fatalf("%v: removed %v", state, got)
		}
		for _, r := range h.m.agentsColumn().Rows {
			if strings.HasPrefix(r.Primary.String(), "#6") {
				t.Fatalf("%v: #6 still shown", state)
			}
		}
		if got := h.status(); got != "Agent #6 removed." {
			t.Fatalf("%v: status %q", state, got)
		}
		if h.m.focus != view.Agents {
			t.Fatalf("%v: the list closed with an agent left", state)
		}
	}
}

// An agent that still runs, waits or starts is not removed: d is not
// offered, and pressing it does nothing.
func TestDLeavesARunningAgentAlone(t *testing.T) {
	t.Parallel()

	for _, state := range []agents.State{agents.Working, agents.WaitingForYou, agents.WaitingForApproval, agents.Starting} {
		h := newHarness(t)
		f := h.withWatcher(agentIn(1, orgA, 6, state, h.now))
		h.start()
		h.drive(h.press("A"))
		if h.offers(RemoveAgent) {
			t.Fatalf("%v: d offered", state)
		}
		h.drive(h.press("d"))
		if len(f.removals()) != 0 {
			t.Fatalf("%v: removed", state)
		}
	}
}

// In the pane d removes the ticket's agent when that one ended; the
// last agent removed from the agents' list closes it.
func TestDInTheTicketsPaneAndTheLastAgent(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	f := h.withWatcher(agentIn(1, orgA, 6, agents.Ended, h.now))
	h.start()
	h.drive(h.press("A"))
	h.drive(h.press("enter")) // the ticket in the pane
	h.press("right")
	if h.m.focus != view.Object || !h.offers(RemoveAgent) {
		t.Fatalf("focus %v, d offered %v", h.m.focus, h.offers(RemoveAgent))
	}
	h.drive(h.press("d"))
	if len(f.removals()) != 1 || len(h.m.app.agents.list) != 0 {
		t.Fatalf("removed %v, left %v", f.removals(), h.m.app.agents.list)
	}
	h2 := newHarness(t)
	h2.withWatcher(agentIn(1, orgA, 6, agents.Ended, h2.now))
	h2.start()
	h2.drive(h2.press("A"))
	h2.drive(h2.press("d"))
	if h2.m.focus == view.Agents {
		t.Fatal("the empty agents' list stayed open")
	}
}

// A removal that fails keeps the agent and says why, as a problem; a seen
// mark that could not be written says so too (C9b left it silent).
func TestAFailedRemovalOrSeenMarkIsTold(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	f := h.withWatcher(agentIn(1, orgA, 6, agents.Ended, h.now))
	f.removeErr = errors.New("disk gone")
	f.seeErr = errors.New("disk gone")
	h.start()
	h.drive(h.press("A"))
	h.drive(h.press("d"))
	if len(h.m.app.agents.list) != 1 {
		t.Fatal("the agent left though its files stayed")
	}
	if got := h.status(); !strings.Contains(got, "#6") || !strings.Contains(got, "disk gone") {
		t.Fatalf("status %q", got)
	}
	h.drive(h.press("enter")) // seen: an ended agent
	if got := h.status(); !strings.Contains(got, "seen") || !strings.Contains(got, "disk gone") {
		t.Fatalf("status after the open %q", got)
	}
}

// Owner, 2026-10-28 (c): an agent stuck in starting (its trust dialog
// closed, no pane opened) offers d once its start is an hour old; a
// younger one may still be starting and offers none.
func TestDRemovesAnAgentStuckInStarting(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		since time.Duration
		d     bool
	}{{59 * time.Minute, false}, {61 * time.Minute, true}} {
		h := newHarness(t)
		h.withWatcher(agentIn(1, orgA, 6, agents.Starting, h.now.Add(-c.since)))
		h.start()
		h.drive(h.press("A"))
		if got := h.offers(RemoveAgent); got != c.d {
			t.Fatalf("starting for %v: d offered %v", c.since, got)
		}
	}
}

// Review round 1 of C9c-2: a read that was out when the removal answered
// may have seen the agent before its files went; its answer does not
// bring the agent back, and a read follows that sees the directory after.
func TestAReadFromBeforeTheRemovalDoesNotBringItBack(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	f := h.withWatcher(agentIn(1, orgA, 6, agents.Ended, h.now), agentIn(2, orgA, 7, agents.Working, h.now))
	h.start()
	h.drive(h.press("A"))
	h.drive(h.press("down")) // #6, after the working #7
	a := h.m.app
	// The next read is due and goes out before the removal: it sees #6.
	before := messages(t, a.agentsDue(a.agents.until))
	if len(before) != 1 {
		t.Fatalf("no read out: %v", before)
	}
	removal := messages(t, h.press("d"))
	h.drive(h.feed(removal...)...)
	// The old read's answer, before the read owed after it runs: no
	// moment shows #6 again.
	next := h.feed(before...)
	for _, ag := range a.agents.list {
		if ag.Number == 6 {
			t.Fatal("the read from before the removal brought #6 back")
		}
	}
	if len(next) == 0 {
		t.Fatal("no read follows the old one")
	}
	h.drive(next...)
	if len(f.removals()) != 1 {
		t.Fatalf("removals %v", f.removals())
	}
}

// Review round 1 of C9c-2: the newest notice shows. A launch's problem on
// the ticket does not hide what d did there, and a launch after d shows
// its own.
func TestTheNewestNoticeShows(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	l := h.withLauncher()
	l.kind, l.again = launch.NoClaude, launch.NoClaude // a first start, and x on the ended agent (C9c)
	h.withWatcher(agentIn(1, orgA, 6, agents.Ended, h.now))
	h.openSix()
	h.drive(h.press("x"))
	if got := h.status(); !strings.Contains(got, "laude") {
		t.Fatalf("no launch problem: %q", got)
	}
	h.drive(h.press("d"))
	if got := h.status(); got != "Agent #6 removed." {
		t.Fatalf("the launch's problem hid the removal: %q", got)
	}
	h.drive(h.press("x"))
	if got := h.status(); got == "Agent #6 removed." {
		t.Fatal("the removal's hint hid the newer launch")
	}
}

// Review round 1 of C9c-2: a problem told of an agent goes with the agent:
// once a read no longer has it, nothing could clear it otherwise.
func TestAProblemGoesWithItsAgent(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	f := h.withWatcher(agentIn(1, orgA, 6, agents.Ended, h.now), agentIn(2, orgA, 7, agents.Working, h.now))
	f.removeErr = errors.New("disk gone")
	h.start()
	h.drive(h.press("A"))
	h.drive(h.press("down"))
	h.drive(h.press("d"))
	if got := h.status(); !strings.Contains(got, "disk gone") {
		t.Fatalf("status %q", got)
	}
	f.set(agentIn(2, orgA, 7, agents.Working, h.now)) // #6 went by other means
	h.due()
	if got := h.status(); strings.Contains(got, "#6") {
		t.Fatalf("the problem stayed without its agent: %q", got)
	}
}

// Review round 2 of C9c-2: the newest notice shows also when a launch
// answers after d told something: the launch's problem is newer.
func TestALaunchAnsweringAfterDShowsItsProblem(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	l := h.withLauncher()
	l.kind, l.again = launch.NoClaude, launch.NoClaude // a first start, and x on the ended agent (C9c)
	f := h.withWatcher(agentIn(1, orgA, 6, agents.Ended, h.now))
	f.removeErr = errors.New("disk gone")
	h.openSix()
	answer := messages(t, h.press("x")) // the launch, answered later
	h.drive(h.press("d"))
	if got := h.status(); !strings.Contains(got, "disk gone") {
		t.Fatalf("status %q", got)
	}
	h.drive(h.feed(answer...)...)
	if got := h.status(); strings.Contains(got, "disk gone") {
		t.Fatalf("the older removal's problem hid the launch's: %q", got)
	}
}

// Completion decides a launch's final tone and hint deadline. Even an
// opened launch can have a tracking problem; both must replace an older
// removal notice once the complete launch result is ready to show.
func TestACompletedLaunchReplacesTheRemovalNotice(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		kind      launch.Kind
		untracked string
		want      string
	}{
		{launch.Opened, "", "Claude Code started again beside #6"},
		{launch.Ended, "", "Claude Code ended; back at #6"},
		{launch.Opened, "no id", "The state of Claude Code beside #6 cannot be shown: no id"},
		{launch.Ended, "no id", "The state of Claude Code beside #6 cannot be shown: no id"},
	} {
		t.Run(c.want, func(t *testing.T) {
			h := newHarness(t)
			l := h.withLauncher()
			f := h.withWatcher(agentIn(1, orgA, 6, agents.Ended, h.now))
			f.removeErr = errors.New("disk gone")
			h.openSix()
			_ = h.press("x") // since C9c the ended agent's start again
			call := l.restarts[0]
			h.drive(h.press("d"))
			if got := h.status(); !strings.Contains(got, "disk gone") {
				t.Fatalf("no removal problem: %q", got)
			}
			h.drive(func() tea.Msg {
				return launch.Done{Ref: h.m.app.launch.ref, Ticket: call.ticket, Kind: c.kind, Untracked: c.untracked, Action: launch.AgainAction}
			})
			if got := h.status(); got != c.want {
				t.Fatalf("launch notice %q, want %q", got, c.want)
			}
		})
	}
}

// Review round 2 of C9c-2: a failed read lists no agent, but says nothing
// about whether the agent of a problem went: the problem stays until a
// read that succeeded no longer has it.
func TestAFailedReadKeepsTheProblem(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	f := h.withWatcher(agentIn(1, orgA, 6, agents.Ended, h.now))
	f.removeErr = errors.New("disk gone")
	h.start()
	h.drive(h.press("A"))
	h.drive(h.press("d"))
	f.mu.Lock()
	f.err = errors.New("directory refused")
	f.mu.Unlock()
	h.due()
	if got := h.status(); !strings.Contains(got, "disk gone") {
		t.Fatalf("a failed read took the problem: %q", got)
	}
}

// Owner, 2026-10-28: a directory with more files than a read takes shows
// a notice instead of an empty area without a word; a read that succeeds
// takes it away.
func TestTooManyAgentFilesAreTold(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	f := h.withWatcher(agentIn(1, orgA, 6, agents.Working, h.now))
	h.start()
	f.mu.Lock()
	f.err = fmt.Errorf("/x/a: %w", agents.ErrTooManyFiles)
	f.mu.Unlock()
	h.due()
	if got := h.status(); !strings.Contains(got, "too many") {
		t.Fatalf("status %q", got)
	}
	f.mu.Lock()
	f.err = nil
	f.mu.Unlock()
	h.due()
	if got := h.status(); strings.Contains(got, "too many") {
		t.Fatalf("status after a good read %q", got)
	}
}
