package ui

import (
	"slices"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/agents"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

// The ticket beside the agents' list: Enter shows it and the list keeps
// the focus, right goes into it and esc back; leaving the list for
// anywhere but the ticket empties the pane. The viewer follows the
// ticket shown: its running agent while the focus is on the ticket or in
// the agents' list, none elsewhere.

// fakeViewer is a launcher with a viewer: it records every agent View
// was asked for.
type fakeViewer struct {
	fakeLauncher
	mu    sync.Mutex
	views []string
}

func (f *fakeViewer) View(agent string) tea.Cmd {
	f.mu.Lock()
	f.views = append(f.views, agent)
	f.mu.Unlock()
	return nil
}

func (f *fakeViewer) asked() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.views...)
}

func (h *harness) withViewer() *fakeViewer {
	f := &fakeViewer{fakeLauncher: fakeLauncher{kind: 0}}
	h.m.app.deps.Launcher = f
	return f
}

func TestEnterShowsTheTicketBesideTheAgents(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		width int
		focus view.Pane
	}{{120, view.Agents}, {100, view.Agents}, {60, view.Object}} {
		h := newHarness(t)
		h.withWatcher(agentIn(1, orgA, 6, agents.Working, h.now), agentIn(2, orgA, 7, agents.Working, h.now))
		h.start()
		h.m.width = c.width
		h.drive(h.press("A"))
		h.drive(h.press("enter"))
		first := h.m.app.detail
		if first == nil || h.m.focus != c.focus {
			t.Fatalf("%d: detail %v, focus %d", c.width, first != nil, h.m.focus)
		}
		if c.focus == view.Object {
			continue
		}
		if !h.offers(Right) {
			t.Fatalf("%d: right is not offered", c.width)
		}
		// The next agent is one key away.
		h.press("down")
		h.drive(h.press("enter"))
		if d := h.m.app.detail; d == nil || d.id == first.id || h.m.focus != view.Agents {
			t.Fatalf("%d: the second agent's ticket is not shown", c.width)
		}
		h.press("right")
		if h.m.focus != view.Object || !h.m.intoTicket {
			t.Fatalf("%d: right: focus %d", c.width, h.m.focus)
		}
		h.press("esc")
		if h.m.focus != view.Agents || h.m.app.detail == nil {
			t.Fatalf("%d: esc: focus %d", c.width, h.m.focus)
		}
	}
}

// Leaving the list for the organisations empties the pane; leaving it
// for the ticket pane it was opened from keeps the ticket there.
func TestLeavingTheAgentsTakesTheirTicket(t *testing.T) {
	t.Parallel()

	for _, key := range []string{"A", "esc"} {
		h := newHarness(t)
		h.withWatcher(agentIn(1, orgA, 6, agents.Working, h.now))
		h.start()
		h.m.focus = view.Navigation
		h.drive(h.press("A"))
		h.drive(h.press("enter"))
		if h.m.app.detail == nil {
			t.Fatal("no ticket shown")
		}
		h.drive(h.press(key))
		if h.m.focus != view.Navigation || h.m.app.detail != nil {
			t.Fatalf("%s: focus %d, detail %v", key, h.m.focus, h.m.app.detail != nil)
		}
	}
	h := newHarness(t)
	h.withWatcher(agentIn(1, orgA, 7, agents.Working, h.now))
	h.openSix()
	h.drive(h.press("A"))
	h.drive(h.press("enter"))
	h.drive(h.press("A"))
	if h.m.focus != view.Object || h.m.app.detail == nil || h.m.app.detail.id != ticketID(website, 7) {
		t.Fatalf("back in the pane: focus %d, detail %+v", h.m.focus, h.m.app.detail)
	}
	// Opening the list anew, from the list this time, forgets the
	// earlier Enter: leaving it keeps the ticket shown.
	h.m.focus = view.List
	h.drive(h.press("A"))
	h.drive(h.press("A"))
	// The ticket stays open; an emptied pane would get the list's own
	// preview of the same row, so the open ticket is what tells.
	if v := h.m.app.tickets; v == nil || v.opened != ticketID(website, 7) {
		t.Fatalf("a list opened without Enter took the ticket: %+v", v)
	}
}

func TestTheViewerFollowsTheTicketShown(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	six := agentIn(1, orgA, 6, agents.Working, h.now)
	ended := agentIn(2, orgA, 7, agents.Ended, h.now)
	h.withWatcher(six, ended)
	f := h.withViewer()
	h.start()
	h.m.focus = view.Navigation
	h.drive(h.press("A"))
	if got := f.asked(); len(got) != 0 {
		t.Fatalf("asked before a ticket showed: %v", got)
	}
	h.drive(h.press("enter"))
	h.press("right")
	h.press("esc")
	// One ask for the agent: the moves between list and ticket keep it.
	if got := f.asked(); !slices.Equal(got, []string{six.ID}) {
		t.Fatalf("asked %v", got)
	}
	// An ended agent has no session to show.
	h.press("down")
	h.drive(h.press("enter"))
	if got := f.asked(); !slices.Equal(got, []string{six.ID, ""}) {
		t.Fatalf("asked %v", got)
	}
	h.press("up")
	h.drive(h.press("enter"))
	h.drive(h.press("A"))
	if got := f.asked(); !slices.Equal(got, []string{six.ID, "", six.ID, ""}) {
		t.Fatalf("after leaving: asked %v", got)
	}
}

// A launcher without a viewer is never asked.
func TestNoViewerNoAsk(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.withWatcher(agentIn(1, orgA, 6, agents.Working, h.now))
	h.withLauncher()
	h.start()
	h.drive(h.press("A"))
	h.drive(h.press("enter"))
	if h.m.app.viewing != "" {
		t.Fatalf("viewing %q without a viewer", h.m.app.viewing)
	}
}
