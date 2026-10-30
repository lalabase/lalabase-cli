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
//
// On an agent that waits for the person, d ends its tmux session first
// (owner, 2026-10-30): such an agent stays forever otherwise, nothing else
// in the client ends it. This stops a process, so a dialog asks first. An
// agent at work is never ended.

// removable: the agent's session is over or never began, by the one rule
// the removal asks again under the agent's lock (agents.Removable).
func (a *app) removable(ag agents.Agent) bool { return agents.Removable(ag, a.now()) }

// agentEnder is a launcher that can end an agent's session.
type agentEnder interface {
	Ends() bool
	EndSession(agent string) error
}

// ender is the launcher when it can end sessions.
func (a *app) ender() (agentEnder, bool) {
	e, ok := a.deps.Launcher.(agentEnder)
	return e, ok && e.Ends()
}

// endable: d ends ag's session, by the rule agents.End asks again.
func (a *app) endable(ag agents.Agent) bool {
	_, ok := a.ender()
	return ok && agents.Endable(ag)
}

// removeTarget is the agent d removes now; ok is false when there is
// none, it still runs, or its removal is out already.
func (m Model) removeTarget() (agents.Agent, bool) {
	ag, ok := m.dReach()
	if !ok || !m.app.removable(ag) {
		return agents.Agent{}, false
	}
	return ag, true
}

// endTarget is the waiting agent d ends now.
func (m Model) endTarget() (agents.Agent, bool) {
	ag, ok := m.dReach()
	if !ok || !m.app.endable(ag) {
		return agents.Agent{}, false
	}
	return ag, true
}

// dBinding is d for the dispatch and the help, by what it does now.
func (m Model) dBinding() []binding {
	if _, ok := m.removeTarget(); ok {
		return []binding{{RemoveAgent, uitext.RemoveAgentKey, uitext.RemoveAgentHelp}}
	}
	if _, ok := m.endTarget(); ok {
		return []binding{{RemoveAgent, uitext.RemoveAgentKey, uitext.EndAgentHelp}}
	}
	return nil
}

// dReach is the agent d is about, unless its removal is out already.
func (m Model) dReach() (agents.Agent, bool) {
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
	if !ok {
		return agents.Agent{}, false
	}
	if _, out := a.agents.removing[ag.ID]; out {
		return agents.Agent{}, false
	}
	return ag, true
}

// removeAgent is d: the removal goes to the watcher; the agent stays
// until its answer, so a removal that fails changes nothing on screen. On
// a waiting agent it opens the question instead.
func (m Model) removeAgent() (Model, tea.Cmd) {
	ag, ok := m.removeTarget()
	if !ok {
		return m.endAgent(), nil
	}
	a := m.app
	a.markRemoving(ag)
	return m, a.deps.Agents.Remove(ag.ID, a.now())
}

// endAgent is d on a waiting agent: the dialog asks, Enter ends it. The
// agent is taken here; the watcher decides again under its lock.
func (m Model) endAgent() Model {
	ag, ok := m.endTarget()
	if !ok {
		return m
	}
	a := m.app
	title, _ := a.agentTitle(ag)
	return m.openDialog(DialogSpec{
		Action:   uitext.Inline(uitext.EndAgentAction),
		SendHelp: uitext.Inline(uitext.EndAgentHelp),
		Text:     uitext.Inline(uitext.EndAgentText),
		Target:   DialogTarget{Project: ag.Project, Ticket: ag.Ticket, Number: ag.Number, Title: title},
		Send:     func(s Submission) tea.Cmd { return a.sendEnd(s, ag) },
	})
}

// sendEnd is Enter in that dialog. Its answer is an agents.Removed, which
// receiveRemoved takes back to the dialog that sent it.
func (a *app) sendEnd(s Submission, ag agents.Agent) tea.Cmd {
	e, _ := a.ender()
	a.markRemoving(ag)
	if a.agents.ending == nil {
		a.agents.ending = map[string]uint64{}
	}
	a.agents.ending[ag.ID] = s.Dialog
	return a.deps.Agents.End(ag.ID, a.now(), e.EndSession)
}

// markRemoving notes that ag's removal goes out.
func (a *app) markRemoving(ag agents.Agent) {
	if a.agents.removing == nil {
		a.agents.removing = map[string]int{}
	}
	a.agents.removing[ag.ID] = ag.Number
	a.clearAgentsNotice()
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
	asked, ending := a.agents.ending[r.ID]
	delete(a.agents.ending, r.ID)
	open := ending && m.dialog != nil && m.dialog.id == asked
	if r.Err != nil {
		text := uitext.Fill(uitext.AgentNotRemoved, number, r.Err.Error())
		if open {
			return m.receiveOutcome(DialogOutcome{Dialog: asked, Refusal: text})
		}
		a.agentsProblem(r.ID, text)
		return m, nil
	}
	a.dropAgent(r.ID)
	cmds := []tea.Cmd{a.agentsHint(uitext.Fill(uitext.AgentRemoved, number)), a.readAgents()}
	if open {
		var closed tea.Cmd
		m, closed = m.receiveOutcome(DialogOutcome{Dialog: asked, Done: true})
		cmds = append(cmds, closed)
	}
	if m.focus == view.Agents && len(a.agents.list) == 0 {
		var back tea.Cmd
		m, back = m.closeAgents()
		cmds = append(cmds, back)
	}
	return m, tea.Batch(cmds...)
}

// dropAgent takes the agent id, whose files went, out of the list at once;
// a read that was out before may still bring it (gone).
func (a *app) dropAgent(id string) {
	if a.agents.gone == nil {
		a.agents.gone = map[string]uint64{}
	}
	a.agents.gone[id] = a.agents.ref
	a.agents.list = withoutGone(a.agents.list, a.agents.gone, a.agents.ref)
	delete(a.agents.seen, id)
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
