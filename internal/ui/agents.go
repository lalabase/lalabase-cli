package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/agents"
)

// AgentWatcher reads the agents of the profile (C9a): one command per
// read, whose message is an agents.Snapshot with the same ref. The command
// gives the session one (agents.NewWatcher); tests give a fake.
type AgentWatcher interface {
	Read(ref uint64) tea.Cmd
}

// agentsEvery is how often the agents' files are read while there is one,
// or a launch runs: often enough for a state to show within a moment of
// its hook, and each read moves the working spinner one frame.
const agentsEvery = 500 * time.Millisecond

// agentsState is what the model knows of the agents: the newest read's
// number, whether a read is out, the deadline of the next one (zero while
// reading sleeps), the agents of the newest read, and the spinner's frame.
type agentsState struct {
	ref     uint64
	reading bool
	until   time.Time
	list    []agents.Agent
	frame   int
}

// readAgents issues a read unless one is out or due already: one chain of
// reads at a time.
func (a *app) readAgents() tea.Cmd {
	if a.deps.Agents == nil || a.agents.reading || !a.agents.until.IsZero() {
		return nil
	}
	a.agents.ref++
	a.agents.reading = true
	return a.deps.Agents.Read(a.agents.ref)
}

// receiveAgents takes the newest read: its agents (none when it failed:
// what cannot be read is not shown), the spinner one frame on while one
// works, and the next read in agentsEvery by the model's clock, while
// there is an agent or a launch runs. Otherwise reading sleeps until the
// next launch.
func (a *app) receiveAgents(s agents.Snapshot) tea.Cmd {
	if !a.agents.reading || s.Ref != a.agents.ref {
		return nil
	}
	a.agents.reading = false
	a.agents.list = s.Agents
	if s.Err != nil {
		a.agents.list = nil
	}
	for _, ag := range a.agents.list {
		if ag.State == agents.Working {
			a.agents.frame++
			break
		}
	}
	if (len(a.agents.list) == 0 && !a.launch.busy) || a.deps.Events == nil {
		return nil
	}
	now := a.now()
	a.agents.until = now.Add(agentsEvery)
	return a.deps.Events.Expiry(a.agents.until, now)
}

// agentsDue is the deadline of the next read: it reads, if it is still
// the one set.
func (a *app) agentsDue(until time.Time) tea.Cmd {
	if a.agents.until.IsZero() || !until.Equal(a.agents.until) {
		return nil
	}
	a.agents.until = time.Time{}
	return a.readAgents()
}

// agentAt is the most urgent agent at a ticket; ok is false when it has
// none.
func (a *app) agentAt(ticket string) (agents.Agent, bool) {
	var best agents.Agent
	found := false
	for _, ag := range a.agents.list {
		if ag.Ticket != ticket {
			continue
		}
		if !found || ag.State.Urgent(best.State) {
			best, found = ag, true
		}
	}
	return best, found
}
