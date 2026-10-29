package ui

// C9c: x on a ticket with an agent jumps to the agent's pane while it
// runs, waits or starts, and starts it again once its session is over;
// in the agents' list it acts on the agent under the cursor (A3).

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gboyke/lalabase-cli/internal/agents"
	"github.com/gboyke/lalabase-cli/internal/launch"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

const (
	agentPane = "18C12925-0000-4000-8000-000000000001"
	// newAgent is the agent a start again records.
	newAgent = "0f8fad5b-d9cb-469f-a165-0000000000ff"
	cleared  = "16fd2706-8baf-433b-82eb-8c7fada847da"
)

// paned is the agent at ticket #6 in state, with a pane.
func paned(h *harness, state agents.State) agents.Agent {
	ag := agentIn(1, orgA, 6, state, h.now.Add(-4*time.Minute))
	ag.Pane = agentPane
	ag.Session, ag.Resumable = cleared, true
	return ag
}

// agentTicket is the launch's address of an agent's ticket.
func agentTicket(ag agents.Agent) launch.Ticket {
	return launch.Ticket{Org: ag.Organisation, Project: ag.Project, ID: ag.Ticket, Number: ag.Number}
}

func TestXJumpsToTheAgentThatRuns(t *testing.T) {
	for _, state := range []agents.State{agents.Working, agents.WaitingForYou, agents.WaitingForApproval, agents.Starting} {
		h := newHarness(t)
		l := h.withLauncher()
		ag := paned(h, state)
		f := h.withWatcher(ag)
		h.openSix()
		h.m.width = 80 // the head's line, below the meta bar
		if !h.offers(Launch) || !strings.Contains(helpText(h.m), "jump to agent") {
			t.Fatalf("%v: x not offered as the jump: %q", state, helpText(h.m))
		}
		if line, _ := h.headLine(); !strings.HasSuffix(line, " · x jump to it") {
			t.Fatalf("%v: head %q", state, line)
		}
		cmd := h.press("x")
		if got := h.status(); got != "Switching to the agent of #6…" {
			t.Fatalf("%v: status %q", state, got)
		}
		if h.offers(Launch) {
			t.Fatalf("%v: x offered while the jump runs", state)
		}
		h.drive(cmd)
		if got := l.jumps; len(got) != 1 || got[0].agent != ag.ID || got[0].pane != agentPane || got[0].ticket != agentTicket(ag) {
			t.Fatalf("%v: jumps %+v", state, got)
		}
		if len(l.launched()) != 0 || len(l.restarts) != 0 || len(f.settlements()) != 0 {
			t.Fatalf("%v: a start or a settlement beside the jump", state)
		}
		if got := h.status(); got != "Switched to the agent of #6" {
			t.Fatalf("%v: status %q", state, got)
		}
	}
}

// Without iTerm, or without a pane the launch recorded, there is nothing
// to jump to: x says so, but neither the help nor the head offers it.
func TestXWithoutAPaneSaysSo(t *testing.T) {
	for _, c := range []struct {
		name    string
		pane    string
		noITerm bool
	}{{"no pane", "", false}, {"no iTerm", agentPane, true}} {
		h := newHarness(t)
		l := h.withLauncher()
		l.noITerm = c.noITerm
		ag := paned(h, agents.Working)
		ag.Pane = c.pane
		h.withWatcher(ag)
		h.openSix()
		h.m.width = 80
		if help := helpText(h.m); strings.Contains(help, "jump") || strings.Contains(help, "Claude Code beside") || strings.Contains(help, "not listed") {
			t.Fatalf("%s: help %q", c.name, helpText(h.m))
		}
		if line, _ := h.headLine(); strings.Contains(line, "x ") {
			t.Fatalf("%s: head %q", c.name, line)
		}
		h.drive(h.press("x"))
		if len(l.jumps) != 0 || len(l.launched()) != 0 {
			t.Fatalf("%s: jumped %+v, launched %+v", c.name, l.jumps, l.launched())
		}
		if got := h.status(); got != "No pane of the agent of #6 is known in this terminal" {
			t.Fatalf("%s: status %q", c.name, got)
		}
	}
}

// The pane gone (4201): the agent is settled, which marks a starting one
// lost; one with a state stays and the status says the pane was closed.
func TestAJumpToAPaneThatIsGone(t *testing.T) {
	for _, c := range []struct {
		state agents.State
		lost  bool
		want  string
	}{
		{agents.Starting, true, "The agent of #6 is lost: its pane is gone"},
		{agents.Working, false, "The pane of the agent of #6 was not found; it was closed"},
	} {
		h := newHarness(t)
		l := h.withLauncher()
		l.jumpKind = launch.PaneGone
		ag := paned(h, c.state)
		f := h.withWatcher(ag)
		f.settleLost = c.lost
		h.openSix()
		h.drive(h.press("x"))
		if got := f.settlements(); len(got) != 1 || got[0] != (agents.After{Agent: ag.ID}) {
			t.Fatalf("%v: settled %+v", c.state, got)
		}
		if got := h.status(); got != c.want {
			t.Fatalf("%v: status %q", c.state, got)
		}
	}
}

// Other outcomes of a jump are told, and settle nothing.
func TestAJumpThatFails(t *testing.T) {
	for kind, want := range map[launch.Kind]string{
		launch.NoPermission: "macOS does not let this terminal control iTerm",
		launch.ITermFailed:  "iTerm did not switch to the agent's pane",
		launch.NoAnswer:     "iTerm did not switch to the agent's pane",
		launch.NoPane:       "No pane of the agent of #6 is known in this terminal",
	} {
		h := newHarness(t)
		l := h.withLauncher()
		l.jumpKind = kind
		f := h.withWatcher(paned(h, agents.Working))
		h.openSix()
		h.drive(h.press("x"))
		if got := h.status(); !strings.HasPrefix(got, want) {
			t.Fatalf("%v: status %q", kind, got)
		}
		if len(f.settlements()) != 0 {
			t.Fatalf("%v: settled", kind)
		}
	}
}

// x on an agent whose session is over starts it again: the latest
// session it recorded, a new agent; once the pane opened the old one is
// settled away, and the head and the list show the new one.
func TestXStartsAnAgentWhoseSessionIsOverAgain(t *testing.T) {
	for _, state := range []agents.State{agents.Ended, agents.Lost} {
		h := newHarness(t)
		l := h.withLauncher()
		l.recorded = newAgent
		old := paned(h, state)
		f := h.withWatcher(old)
		h.openSix()
		h.m.width = 80
		if !h.offers(Launch) || !strings.Contains(helpText(h.m), "start agent again") {
			t.Fatalf("%v: help %q", state, helpText(h.m))
		}
		if line, _ := h.headLine(); !strings.HasSuffix(line, " · x start again · d remove") {
			t.Fatalf("%v: head %q", state, line)
		}
		cmd := h.press("x")
		if got := h.status(); got != "Starting the agent of #6 again…" {
			t.Fatalf("%v: status %q", state, got)
		}
		// The new agent as the launch records it, for the next read.
		started := old
		started.ID, started.State, started.Since, started.Pane, started.Resumable = newAgent, agents.Starting, h.now, agentPane, false
		f.set(old, started)
		h.drive(cmd)
		if got := l.restarts; len(got) != 1 || got[0].again != (launch.Again{Agent: old.ID, Session: cleared}) || got[0].ticket != agentTicket(old) {
			t.Fatalf("%v: again %+v", state, got)
		}
		if got := f.settlements(); len(got) != 1 || got[0] != (agents.After{Agent: newAgent, Replaces: old.ID, Opened: true}) {
			t.Fatalf("%v: settled %+v", state, got)
		}
		if got := h.status(); got != "Claude Code started again beside #6" {
			t.Fatalf("%v: status %q", state, got)
		}
		// The old agent left at once; the new one comes with the next read.
		for _, ag := range h.m.app.agents.list {
			if ag.ID == old.ID {
				t.Fatalf("%v: the old agent stayed", state)
			}
		}
		h.due()
		if len(h.m.app.agents.list) != 1 || h.m.app.agents.list[0].ID != newAgent {
			t.Fatalf("%v: agents %+v", state, h.m.app.agents.list)
		}
		if line, _ := h.headLine(); !strings.Contains(line, "starting") {
			t.Fatalf("%v: head %q", state, line)
		}
	}
}

// An agent whose hooks never recorded a session is started afresh: no
// session to resume.
func TestXStartsANeverStartedAgentAfresh(t *testing.T) {
	h := newHarness(t)
	l := h.withLauncher()
	old := paned(h, agents.Lost)
	old.Resumable = false
	h.withWatcher(old)
	h.openSix()
	h.drive(h.press("x"))
	if got := l.restarts; len(got) != 1 || got[0].again != (launch.Again{Agent: old.ID}) {
		t.Fatalf("again %+v", got)
	}
}

// A start again that opened nothing keeps the old agent and settles
// nothing; x offers it again.
func TestAFailedStartAgainKeepsTheOldAgent(t *testing.T) {
	h := newHarness(t)
	l := h.withLauncher()
	l.again = launch.NoPermission
	l.recorded = newAgent
	old := paned(h, agents.Ended)
	f := h.withWatcher(old)
	h.openSix()
	h.drive(h.press("x"))
	if len(f.settlements()) != 0 || len(h.m.app.agents.list) != 1 {
		t.Fatalf("settled %+v, agents %+v", f.settlements(), h.m.app.agents.list)
	}
	if !strings.HasPrefix(h.status(), "macOS does not let") {
		t.Fatalf("status %q", h.status())
	}
	if !h.offers(Launch) {
		t.Fatal("x is not offered on the old agent")
	}
}

// Without iTerm claude runs here and the settlement follows its end: a
// start again whose new agent got no hook did not start, the old one
// stays; a first start whose agent got none is lost.
func TestHereTheEndSettles(t *testing.T) {
	h := newHarness(t)
	l := h.withLauncher()
	l.noITerm, l.again, l.recorded = true, launch.Ended, newAgent
	old := paned(h, agents.Ended)
	f := h.withWatcher(old)
	f.settleLost = true
	h.openSix()
	cmd := h.press("x")
	// A read saw the new agent before claude ended here.
	started := old
	started.ID, started.State, started.Pane = newAgent, agents.Starting, ""
	h.m.app.agents.list = append(h.m.app.agents.list, started)
	h.drive(cmd)
	if got := f.settlements(); len(got) != 1 || got[0] != (agents.After{Agent: newAgent, Replaces: old.ID}) {
		t.Fatalf("settled %+v", got)
	}
	if got := h.status(); got != "Claude Code did not start again beside #6; the agent stays" {
		t.Fatalf("status %q", got)
	}
	if len(h.m.app.agents.list) != 1 || h.m.app.agents.list[0].ID != old.ID {
		t.Fatalf("agents %+v", h.m.app.agents.list)
	}

	h = newHarness(t)
	l = h.withLauncher()
	l.noITerm, l.kind, l.recorded = true, launch.Ended, newAgent
	f = h.withWatcher()
	h.openSix()
	h.drive(h.press("x"))
	if got := f.settlements(); len(got) != 1 || got[0] != (agents.After{Agent: newAgent}) {
		t.Fatalf("a first start here settled %+v", got)
	}
	// One opened in iTerm settles nothing.
	h = newHarness(t)
	l = h.withLauncher()
	l.recorded = newAgent
	f = h.withWatcher()
	h.openSix()
	h.drive(h.press("x"))
	if got := f.settlements(); len(got) != 0 {
		t.Fatalf("an opened first start settled %+v", got)
	}
}

// A settlement that fails is told as a problem.
func TestAFailedSettlementIsTold(t *testing.T) {
	h := newHarness(t)
	l := h.withLauncher()
	l.recorded = newAgent
	f := h.withWatcher(paned(h, agents.Ended))
	f.settleErr = errors.New("it is running again")
	h.openSix()
	h.drive(h.press("x"))
	if got := h.status(); got != "The agents of #6 could not be updated: it is running again" {
		t.Fatalf("status %q", got)
	}
}

// A3: in the agents' list x acts on the agent under the cursor, by the
// same rules, and its notice shows there.
func TestXInTheAgentsList(t *testing.T) {
	h := newHarness(t)
	l := h.withLauncher()
	l.recorded = newAgent
	working := paned(h, agents.Working)
	ended := agentIn(2, orgA, 7, agents.Ended, h.now)
	ended.Session, ended.Resumable = cleared, true
	h.withWatcher(working, ended)
	h.start()
	h.drive(h.press("A"))
	if ag, _ := h.m.app.agentUnderCursor(); ag.ID != working.ID {
		t.Fatalf("cursor on %+v", ag)
	}
	if !strings.Contains(helpText(h.m), "jump to agent") {
		t.Fatalf("help %q", helpText(h.m))
	}
	h.drive(h.press("x"))
	if len(l.jumps) != 1 || l.jumps[0].agent != working.ID {
		t.Fatalf("jumps %+v", l.jumps)
	}
	if got := h.status(); got != "Switched to the agent of #6" {
		t.Fatalf("status %q", got)
	}
	h.drive(h.press("down"))
	if !strings.Contains(helpText(h.m), "start agent again") {
		t.Fatalf("help %q", helpText(h.m))
	}
	h.drive(h.press("x"))
	if len(l.restarts) != 1 || l.restarts[0].again.Agent != ended.ID || l.restarts[0].ticket != agentTicket(ended) {
		t.Fatalf("restarts %+v", l.restarts)
	}
	if h.m.focus != view.Agents {
		t.Fatalf("focus %v", h.m.focus)
	}
	if got := h.status(); got != "Claude Code started again beside #7" {
		t.Fatalf("status %q", got)
	}
}

// While the old agent's settlement is out it is not started again a
// second time.
func TestAnAgentBeingReplacedIsNotStartedAgainTwice(t *testing.T) {
	h := newHarness(t)
	l := h.withLauncher()
	l.recorded = newAgent
	old := paned(h, agents.Lost)
	h.withWatcher(old)
	h.openSix()
	done := messages(t, h.press("x"))
	var settle []any
	for _, msg := range done {
		if d, ok := msg.(launch.Done); ok {
			// Only the launch's answer; the settlement it asks for is
			// held back.
			_ = h.feed(d)
			settle = append(settle, d)
		}
	}
	if len(settle) != 1 {
		t.Fatalf("messages %v", done)
	}
	if h.offers(Launch) {
		t.Fatal("x offered on the agent being replaced")
	}
}

// Where the meta bar stands the hints stand there, each whole.
func TestTheMetaBarNamesTheKeys(t *testing.T) {
	for _, c := range []struct {
		state agents.State
		want  string
	}{
		{agents.Working, "working · 4 min · x jump to it"},
		{agents.Ended, "ended · 4 min · x start again · d remove"},
	} {
		h := newHarness(t)
		h.withLauncher()
		h.withWatcher(paned(h, c.state))
		h.openSix()
		h.m.width = 160
		var value []string
		for _, l := range h.m.objectColumn().Meta[1:] {
			if l.Text.String() == "" {
				break
			}
			value = append(value, strings.TrimSpace(l.Text.String()))
		}
		if got := strings.Join(value, " "); got != c.want {
			t.Fatalf("%v: bar %q", c.state, got)
		}
		for _, line := range value {
			if strings.HasSuffix(line, " x") || strings.HasSuffix(line, "·") {
				t.Fatalf("%v: a hint broken: %q", c.state, value)
			}
		}
	}
}
