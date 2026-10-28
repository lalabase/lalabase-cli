package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/agents"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
	"github.com/gboyke/lalabase-cli/internal/uitext"
)

// d removes an agent (C9c): one that ended or is lost, so nothing that
// still runs loses its record; without asking (owner, 2026-10-28), since
// only the client's own files of it go. In the agents' list it is the
// agent under the cursor, in the pane the ticket's agent the head shows.

// removable: the agent's session is over, ended or lost.
func removable(ag agents.Agent) bool { return ag.State == agents.Ended || ag.State == agents.Lost }

// removeTarget is the agent d removes now; ok is false when there is
// none, it still runs, or its removal is out already.
func (m Model) removeTarget() (agents.Agent, bool) {
	a := m.app
	if a == nil || a.deps.Agents == nil {
		return agents.Agent{}, false
	}
	var ag agents.Agent
	var ok bool
	switch {
	case m.focus == view.Agents:
		ag, ok = a.agentUnderCursor()
	case m.focus == view.Object && a.detail != nil && a.detail.known:
		ag, ok = a.agentAt(a.detail.id)
	}
	if !ok || !removable(ag) {
		return agents.Agent{}, false
	}
	if _, out := a.agents.removing[ag.ID]; out {
		return agents.Agent{}, false
	}
	return ag, true
}

// removeAgent is d: the removal goes to the watcher; the agent stays
// until its answer, so a removal that fails changes nothing on screen.
func (m Model) removeAgent() tea.Cmd {
	ag, ok := m.removeTarget()
	if !ok {
		return nil
	}
	a := m.app
	if a.agents.removing == nil {
		a.agents.removing = map[string]int{}
	}
	a.agents.removing[ag.ID] = ag.Number
	a.agents.notice = present.Inline{}
	return a.deps.Agents.Remove(ag.ID)
}

// receiveRemoved takes a removal's answer: the agent leaves the area at
// once (the next read would take it too) and a hint says so, the empty
// agents' list closes; a failure keeps the agent and says why.
func (m Model) receiveRemoved(r agents.Removed) (Model, tea.Cmd) {
	a := m.app
	if a == nil {
		return m, nil
	}
	number, ok := a.agents.removing[r.ID]
	if !ok {
		return m, nil
	}
	delete(a.agents.removing, r.ID)
	if r.Err != nil {
		a.agents.notice, a.agents.tone = uitext.Fill(uitext.AgentNotRemoved, number, r.Err.Error()), present.NoticeProblem
		a.agents.noticeUntil = time.Time{}
		return m, nil
	}
	kept := a.agents.list[:0:0]
	for _, ag := range a.agents.list {
		if ag.ID != r.ID {
			kept = append(kept, ag)
		}
	}
	a.agents.list = kept
	delete(a.agents.seen, r.ID)
	cmds := []tea.Cmd{a.agentsHint(uitext.Fill(uitext.AgentRemoved, number)), a.readAgents()}
	if m.focus == view.Agents && len(a.agents.list) == 0 {
		var back tea.Cmd
		m, back = m.closeAgents()
		cmds = append(cmds, back)
	}
	return m, tea.Batch(cmds...)
}

// receiveMarked takes a seen mark's answer: one not written says so; the
// agent stays faint for this run (agents.seen) either way.
func (a *app) receiveMarked(r agents.Marked) {
	if r.Err == nil {
		return
	}
	number := 0
	for _, ag := range a.agents.list {
		if ag.ID == r.ID {
			number = ag.Number
		}
	}
	a.agents.notice, a.agents.tone = uitext.Fill(uitext.AgentNotSeen, number, r.Err.Error()), present.NoticeProblem
	a.agents.noticeUntil = time.Time{}
}

// agentsHint shows a plain notice of the agents until hintFor has passed
// by the model's clock.
func (a *app) agentsHint(text present.Inline) tea.Cmd {
	now := a.now()
	a.agents.notice, a.agents.tone, a.agents.noticeUntil = text, present.Plain, now.Add(hintFor)
	if a.deps.Events == nil {
		return nil
	}
	return a.deps.Events.Expiry(a.agents.noticeUntil, now)
}
