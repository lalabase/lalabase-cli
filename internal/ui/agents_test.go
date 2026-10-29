package ui

import (
	"errors"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/agents"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/launch"
)

// C9a: the model reads the agents' files every 500 ms by its own clock,
// through the Events' Expiry, while there is an agent or a launch runs;
// these tests give a fake watcher.

type fakeWatcher struct {
	mu     sync.Mutex
	refs   []uint64
	agents []agents.Agent
	err    error
	// marks are the agents See recorded as seen (C9b).
	marks []string
	// seeErr and removeErr are the errors See and Remove answer with;
	// removed are the agents Remove was asked for (C9c).
	seeErr, removeErr error
	removed           []string
	// settled are the settlements asked for (C9c); settleLost and
	// settleErr what they answer.
	settled    []agents.After
	settleLost bool
	settleErr  error
}

// Settle answers as agents.Settle would on the fake's agents: a lost
// mark turns the agent lost, or takes it when it replaced another; else
// the replaced agent goes.
func (f *fakeWatcher) Settle(after agents.After, _ time.Time) tea.Cmd {
	f.mu.Lock()
	f.settled = append(f.settled, after)
	lost, err := f.settleLost && !after.Opened, f.settleErr
	if err == nil {
		gone := after.Replaces
		if lost && after.Replaces != "" {
			gone = after.Agent
		}
		kept := f.agents[:0:0]
		for _, ag := range f.agents {
			if lost && ag.ID == after.Agent {
				ag.State = agents.Lost
			}
			if ag.ID != gone {
				kept = append(kept, ag)
			}
		}
		f.agents = kept
	}
	f.mu.Unlock()
	return func() tea.Msg { return agents.Settled{After: after, Lost: lost, Err: err} }
}

func (f *fakeWatcher) settlements() []agents.After {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]agents.After(nil), f.settled...)
}

func (f *fakeWatcher) Remove(id string, _ time.Time) tea.Cmd {
	f.mu.Lock()
	f.removed = append(f.removed, id)
	err := f.removeErr
	if err == nil {
		kept := f.agents[:0:0]
		for _, ag := range f.agents {
			if ag.ID != id {
				kept = append(kept, ag)
			}
		}
		f.agents = kept
	}
	f.mu.Unlock()
	return func() tea.Msg { return agents.Removed{ID: id, Err: err} }
}

func (f *fakeWatcher) removals() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.removed...)
}

func (f *fakeWatcher) See(id string, _ time.Time) tea.Cmd {
	f.mu.Lock()
	f.marks = append(f.marks, id)
	err := f.seeErr
	f.mu.Unlock()
	return func() tea.Msg { return agents.Marked{ID: id, Err: err} }
}

func (f *fakeWatcher) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.marks...)
}

func (f *fakeWatcher) Read(ref uint64) tea.Cmd {
	f.mu.Lock()
	f.refs = append(f.refs, ref)
	list, err := append([]agents.Agent(nil), f.agents...), f.err
	f.mu.Unlock()
	return func() tea.Msg { return agents.Snapshot{Ref: ref, Agents: list, Err: err} }
}

func (f *fakeWatcher) set(list ...agents.Agent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.agents = list
}

func (f *fakeWatcher) reads() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.refs)
}

func (h *harness) withWatcher(list ...agents.Agent) *fakeWatcher {
	f := &fakeWatcher{agents: list}
	h.m.app.deps.Agents = f
	return f
}

// agentOn is an agent at ticket #n of the website project.
func agentOn(n int, state agents.State, since time.Time) agents.Agent {
	return agents.Agent{
		ID: "0f8fad5b-d9cb-469f-a165-7086772895" + string(rune('a'+n%26)) + "e", Organisation: orgA,
		Project: website, Ticket: ticketID(website, n), Number: n, State: state, Since: since,
	}
}

// due is the expiry of the agents' next read, as the Events deliver it.
func (h *harness) due() {
	h.t.Helper()
	until := h.m.app.agents.until
	if until.IsZero() {
		h.t.Fatal("no read is due")
	}
	h.now = until
	h.drive(h.feed(gateway.Expired{Session: h.m.app.session, Until: until})...)
}

func TestTheAgentsAreReadAtTheStartAndSleepWithoutOne(t *testing.T) {
	h := newHarness(t)
	f := h.withWatcher()
	h.start()
	if f.reads() != 1 {
		t.Fatalf("%d reads at the start", f.reads())
	}
	if a := h.m.app.agents; !a.until.IsZero() || a.reading {
		t.Fatalf("reading goes on without an agent: %+v", a)
	}
}

func TestTheAgentsAreReadEveryHalfSecondWhileThereIsOne(t *testing.T) {
	h := newHarness(t)
	f := h.withWatcher(agentOn(6, agents.Starting, h.now))
	h.start()
	if got, want := h.m.app.agents.until, h.now.Add(500*time.Millisecond); !got.Equal(want) {
		t.Fatalf("next read at %v, want %v", got, want)
	}
	// Another deadline (a hint's, an older read's) reads nothing.
	h.feed(gateway.Expired{Session: h.m.app.session, Until: h.now.Add(time.Second)})
	h.feed(gateway.Expired{Session: h.m.app.session + 1, Until: h.m.app.agents.until})
	if f.reads() != 1 {
		t.Fatalf("%d reads on foreign deadlines", f.reads())
	}
	for i := 2; i <= 4; i++ {
		h.due()
		if f.reads() != i {
			t.Fatalf("%d reads, want %d", f.reads(), i)
		}
		if got, want := h.m.app.agents.until, h.now.Add(500*time.Millisecond); !got.Equal(want) {
			t.Fatalf("read %d: next at %v, want %v", i, got, want)
		}
	}
	// The last agent gone: reading sleeps.
	f.set()
	h.due()
	if !h.m.app.agents.until.IsZero() {
		t.Fatal("reading goes on without an agent")
	}
}

// A launch running keeps the reading going: its agent's record comes
// with its end. Its end reads at once.
func TestALaunchKeepsTheReadingGoing(t *testing.T) {
	h := newHarness(t)
	f := h.withWatcher()
	h.withLauncher()
	h.openSix()
	before := f.reads()
	cmd := h.press("x")
	a := h.m.app
	if !a.agents.reading || f.reads() != before+1 {
		t.Fatalf("x issued no read: %+v", a.agents)
	}
	// The read answers while the launch runs: the next is due.
	h.feed(agents.Snapshot{Ref: a.agents.ref})
	if a.agents.until.IsZero() {
		t.Fatal("reading sleeps while a launch runs")
	}
	// The launch ends; the due read finds its agent.
	f.set(agentOn(6, agents.Starting, h.now))
	h.drive(cmd)
	h.due()
	if len(a.agents.list) != 1 || a.agents.until.IsZero() {
		t.Fatalf("after the launch: %+v", a.agents)
	}
}

// One chain of reads at a time: a read out or due is not doubled.
func TestOneReadAtATime(t *testing.T) {
	h := newHarness(t)
	f := h.withWatcher(agentOn(6, agents.Working, h.now))
	a := h.m.app
	first := a.readAgents()
	// Asked for twice while one is out: no second read now, one owed.
	for range 2 {
		if a.readAgents() != nil {
			t.Fatal("a second read while one is out")
		}
	}
	h.drive(first)
	if f.reads() != 2 {
		t.Fatalf("%d reads, want the first and the one owed", f.reads())
	}
	if a.readAgents() != nil {
		t.Fatal("a second read while one is due")
	}
	if f.reads() != 2 || a.agents.again {
		t.Fatalf("%d reads, again %v", f.reads(), a.agents.again)
	}
	// An older read's answer changes nothing.
	h.due()
	h.feed(agents.Snapshot{Ref: a.agents.ref - 1})
	if len(a.agents.list) != 1 {
		t.Fatal("an older read's answer was taken")
	}
}

// The spinner moves one frame with every read while an agent works, and
// stands still otherwise.
func TestTheSpinnerMovesWithEachReadWhileOneWorks(t *testing.T) {
	h := newHarness(t)
	f := h.withWatcher(agentOn(6, agents.Working, h.now), agentOn(7, agents.WaitingForYou, h.now))
	h.start()
	if got := h.m.app.agents.frame; got != 1 {
		t.Fatalf("frame %d after one read", got)
	}
	h.due()
	h.due()
	if got := h.m.app.agents.frame; got != 3 {
		t.Fatalf("frame %d after three reads", got)
	}
	f.set(agentOn(6, agents.WaitingForYou, h.now))
	h.due()
	h.due()
	if got := h.m.app.agents.frame; got != 3 {
		t.Fatalf("frame %d without a working agent", got)
	}
}

// A read that failed shows no agent.
func TestAFailedReadShowsNone(t *testing.T) {
	h := newHarness(t)
	f := h.withWatcher(agentOn(6, agents.Working, h.now))
	h.start()
	f.mu.Lock()
	f.err = errors.New("the agents' directory is open to others")
	f.mu.Unlock()
	h.due()
	if len(h.m.app.agents.list) != 0 {
		t.Fatalf("%+v", h.m.app.agents.list)
	}
}

// The most urgent agent at a ticket wins; of two equally urgent the first.
func TestTheMostUrgentAgentWins(t *testing.T) {
	h := newHarness(t)
	working := agentOn(6, agents.Working, h.now)
	ended := agentOn(6, agents.Ended, h.now)
	approval := agentOn(6, agents.WaitingForApproval, h.now)
	approval.ID = "1f8fad5b-d9cb-469f-a165-70867728950e"
	h.m.app.agents.list = []agents.Agent{ended, working, approval, agentOn(7, agents.WaitingForYou, h.now)}
	if got, ok := h.m.app.agentAt(ticketID(website, 6)); !ok || got.State != agents.WaitingForApproval {
		t.Fatalf("%+v %v", got, ok)
	}
	if _, ok := h.m.app.agentAt(ticketID(website, 8)); ok {
		t.Fatal("an agent at a ticket without one")
	}
}

// Review round 1 of #34, F1: the launch ends while a read is out that saw
// the directory before the launch's record. The read after it must still
// come: a read asked for while one is out is owed, and comes when it ends.
func TestALaunchsEndDuringAReadIsReadAgain(t *testing.T) {
	h := newHarness(t)
	f := h.withWatcher()
	h.withLauncher()
	h.openSix()
	h.press("x")
	a := h.m.app
	ref := a.agents.ref
	if !a.agents.reading {
		t.Fatal("x issued no read")
	}
	// The record is written now; the launch's answer comes first.
	f.set(agentOn(6, agents.Starting, h.now))
	calls := h.m.app.deps.Launcher.(*fakeLauncher).launched()
	h.drive(h.feed(launch.Done{Ref: calls[0].ref, Ticket: calls[0].ticket, Kind: launch.Opened})...)
	// Then the read that began before the record, empty.
	h.drive(h.feed(agents.Snapshot{Ref: ref})...)
	if len(a.agents.list) != 1 {
		t.Fatalf("the launch's agent was never read: %+v", a.agents)
	}
	if a.agents.until.IsZero() {
		t.Fatal("reading sleeps with an agent")
	}
}

// Review round 4 of #34, F3: a failed read hides its agents but keeps
// reading, so the agents come back once the directory reads again; only a
// read that succeeds and finds none lets reading sleep.
func TestAFailedReadKeepsReading(t *testing.T) {
	h := newHarness(t)
	f := h.withWatcher(agentOn(6, agents.Working, h.now))
	h.start()
	f.mu.Lock()
	f.err = errors.New("the directory cannot be read")
	f.mu.Unlock()
	h.due()
	if len(h.m.app.agents.list) != 0 {
		t.Fatalf("a failed read shows agents: %+v", h.m.app.agents.list)
	}
	if h.m.app.agents.until.IsZero() {
		t.Fatal("reading sleeps after a failed read")
	}
	f.mu.Lock()
	f.err = nil
	f.mu.Unlock()
	h.due()
	if got := h.m.app.agents.list; len(got) != 1 || got[0].State != agents.Working {
		t.Fatalf("the agent did not come back: %+v", got)
	}
	f.mu.Lock()
	f.agents = nil
	f.mu.Unlock()
	h.due()
	if !h.m.app.agents.until.IsZero() {
		t.Fatal("reading goes on after a read that found no agent")
	}
}
