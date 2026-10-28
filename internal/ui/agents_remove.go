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

// stuckStarting is how long an agent may be starting before d may remove
// it (owner, 2026-10-28): claude's trust dialog may take a while, but one
// an hour old never got past it (closed, or no pane opened).
const stuckStarting = time.Hour

// removable: the agent's session is over (ended, lost) or never began.
func (a *app) removable(ag agents.Agent) bool {
	switch ag.State {
	case agents.Ended, agents.Lost:
		return true
	case agents.Starting:
		return a.now().Sub(ag.Since) > stuckStarting
	}
	return false
}

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
	if !ok || !a.removable(ag) {
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
	a.clearAgentsNotice()
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
		a.agentsProblem(r.ID, uitext.Fill(uitext.AgentNotRemoved, number, r.Err.Error()))
		return m, nil
	}
	if a.agents.gone == nil {
		a.agents.gone = map[string]uint64{}
	}
	a.agents.gone[r.ID] = a.agents.ref
	a.agents.list = withoutGone(a.agents.list, a.agents.gone, a.agents.ref)
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
	for _, ag := range a.agents.list {
		if ag.ID == r.ID {
			a.agentsProblem(r.ID, uitext.Fill(uitext.AgentNotSeen, ag.Number, r.Err.Error()))
		}
	}
}

// agentsProblem shows a problem of the agent id; it stands until the next
// d or open starts, a launch starts, or the agent leaves the list.
func (a *app) agentsProblem(id string, text present.Inline) {
	a.agents.notice, a.agents.tone, a.agents.noticeUntil, a.agents.noticeAgent = text, present.NoticeProblem, time.Time{}, id
}

// clearAgentsNotice takes the agents' notice away.
func (a *app) clearAgentsNotice() {
	a.agents.notice, a.agents.noticeUntil, a.agents.noticeAgent = present.Inline{}, time.Time{}, ""
}

// agentsNoticeShown: there is a notice of the agents, and a hint's
// deadline has not come by the model's clock.
func (a *app) agentsNoticeShown() bool {
	if a.agents.notice.String() == "" {
		return false
	}
	return a.agents.tone != present.Plain || a.agents.noticeUntil.After(a.now())
}

// agentsHint shows a plain notice of the agents until hintFor has passed
// by the model's clock.
func (a *app) agentsHint(text present.Inline) tea.Cmd {
	now := a.now()
	a.agents.notice, a.agents.tone, a.agents.noticeUntil, a.agents.noticeAgent = text, present.Plain, now.Add(hintFor), ""
	if a.deps.Events == nil {
		return nil
	}
	return a.deps.Events.Expiry(a.agents.noticeUntil, now)
}
