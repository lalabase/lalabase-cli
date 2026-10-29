package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/agents"
	"github.com/gboyke/lalabase-cli/internal/launch"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
	"github.com/gboyke/lalabase-cli/internal/uitext"
)

// x on an agent (C9c): it jumps to the pane of an agent that runs, waits
// or starts, and starts one whose session is over again. In the ticket's
// pane it is the agent the head shows (agentAt), in the agents' list the
// one under the cursor (A3); a ticket without an agent starts one (C7).

// xKind is what x does now.
type xKind uint8

const (
	xStart xKind = iota + 1
	xJump
	// xNoPane: the agent runs, but no pane of it can be selected (no
	// iTerm, or none recorded): x says so, and neither the help nor the
	// head offers it.
	xNoPane
	xAgain
)

// xHelp names x in the help by what it does; x without a pane acts, but
// the help does not promise a jump it cannot make.
var xHelp = map[xKind]uitext.ID{xStart: uitext.LaunchHelp, xJump: uitext.JumpHelp, xNoPane: uitext.Unlisted, xAgain: uitext.AgainHelp}

// xTarget is what x acts on: the ticket, and the agent unless it starts
// one.
type xTarget struct {
	kind   xKind
	ticket launch.Ticket
	agent  agents.Agent
}

// xNow is what x does now; ok is false when it does nothing: no
// launcher, an action of x running, no ticket or agent in reach, or an
// agent whose start again is out (its replacement may still be settling).
func (m Model) xNow() (xTarget, bool) {
	a := m.app
	if a == nil || a.deps.Launcher == nil || a.launch.busy {
		return xTarget{}, false
	}
	var ag agents.Agent
	switch m.focus {
	case view.Agents:
		var ok bool
		if ag, ok = a.agentUnderCursor(); !ok {
			return xTarget{}, false
		}
	case view.Object:
		t, ok := m.launchTarget()
		if !ok {
			return xTarget{}, false
		}
		if ag, ok = a.agentAt(t.ID); !ok {
			return xTarget{kind: xStart, ticket: t}, true
		}
	default:
		return xTarget{}, false
	}
	if a.agents.replacing[ag.ID] {
		return xTarget{}, false
	}
	return xTarget{kind: a.xFor(ag), ticket: agentAddress(ag), agent: ag}, true
}

// xFor is what x does on ag: start again once its session is over, else
// jump to its pane when there is one to select.
func (a *app) xFor(ag agents.Agent) xKind {
	switch {
	case ag.State == agents.Ended || ag.State == agents.Lost:
		return xAgain
	case ag.Pane != "" && a.deps.Launcher.Jumps():
		return xJump
	}
	return xNoPane
}

// agentAddress is the ticket of an agent as its record keeps it: the
// launch takes it from there, not from the pane, which may show another.
func agentAddress(ag agents.Agent) launch.Ticket {
	return launch.Ticket{Org: ag.Organisation, Project: ag.Project, ID: ag.Ticket, Number: ag.Number}
}

// xBinding is x for the dispatch and the help, when it does something.
func (m Model) xBinding() []binding {
	if x, ok := m.xNow(); ok {
		return []binding{{Launch, uitext.LaunchKey, xHelp[x.kind]}}
	}
	return nil
}

// pressX is x.
func (m Model) pressX() (Model, tea.Cmd) {
	x, ok := m.xNow()
	if !ok {
		return m, nil
	}
	a := m.app
	if x.kind == xStart {
		return m, m.startClaude(x.ticket)
	}
	a.clearAgentsNotice()
	a.launch.everywhere = m.focus == view.Agents
	if x.kind == xNoPane {
		a.launch.ticket = x.ticket.ID
		a.launch.notice, a.launch.tone = uitext.Fill(uitext.JumpNoPane, x.ticket.Number), present.Plain
		return m, a.launchHint()
	}
	a.launch.ref++
	a.launch.busy, a.launch.ticket, a.launch.until = true, x.ticket.ID, time.Time{}
	if x.kind == xJump {
		a.launch.notice, a.launch.tone = uitext.Fill(uitext.Jumping, x.ticket.Number), present.NoticeWaiting
		return m, a.deps.Launcher.Jump(a.launch.ref, x.ticket, x.agent.ID, x.agent.Pane)
	}
	again := launch.Again{Agent: x.agent.ID}
	if x.agent.Resumable {
		again.Session = x.agent.Session
	}
	if a.agents.replacing == nil {
		a.agents.replacing = map[string]bool{}
	}
	a.agents.replacing[x.agent.ID] = true
	a.launch.notice, a.launch.tone = uitext.Fill(uitext.StartingAgain, x.ticket.Number), present.NoticeWaiting
	return m, tea.Batch(a.deps.Launcher.Again(a.launch.ref, x.ticket, again), a.readAgents())
}

// settleAfter is the settlement a launch's answer asks for (agents.Settle),
// nil for none: a jump that found the pane gone; an agent that ran here
// (its end may have come before any hook); a start again whose pane
// opened. A start again that went no further keeps the old agent, which x
// may start again.
func (a *app) settleAfter(d launch.Done) tea.Cmd {
	var after agents.After
	switch {
	case d.Action == launch.JumpAction && d.Kind == launch.PaneGone:
		after = agents.After{Agent: d.Agent}
	case d.Agent != "" && (d.Kind == launch.Ended || d.Kind == launch.EndedWithError):
		after = agents.After{Agent: d.Agent, Replaces: d.Replaces}
	case d.Agent != "" && d.Replaces != "" && d.Kind == launch.Opened:
		after = agents.After{Agent: d.Agent, Replaces: d.Replaces, Opened: true}
	}
	if after.Agent == "" || a.deps.Agents == nil {
		delete(a.agents.replacing, d.Replaces)
		return nil
	}
	if a.agents.settling == nil {
		a.agents.settling = map[agents.After]settling{}
	}
	a.agents.settling[after] = settling{number: d.Ticket.Number, jump: d.Action == launch.JumpAction}
	return a.deps.Agents.Settle(after, a.now())
}

// settling is what the model keeps of a settlement that is out: the
// ticket's number for its notice, and whether a jump asked for it.
type settling struct {
	number int
	jump   bool
}

// receiveSettled takes a settlement's answer: the agent that went leaves
// the list at once (as after d); what became of it is told when it is
// news: an agent lost with its pane, a start again that never began, a
// failure.
func (m Model) receiveSettled(s agents.Settled) tea.Cmd {
	a := m.app
	if a == nil {
		return nil
	}
	st, ok := a.agents.settling[s.After]
	if !ok {
		return nil
	}
	delete(a.agents.settling, s.After)
	delete(a.agents.replacing, s.After.Replaces)
	read := a.readAgents()
	switch {
	case s.Err != nil:
		a.agentsProblem(s.After.Agent, uitext.Fill(uitext.AgentNotSettled, st.number, s.Err.Error()))
	case s.After.Replaces != "" && s.Lost:
		a.dropAgent(s.After.Agent)
		a.agentsProblem(s.After.Replaces, uitext.Fill(uitext.AgainNotStarted, st.number))
	case s.After.Replaces != "":
		a.dropAgent(s.After.Replaces)
		if a.agents.cursor == s.After.Replaces {
			a.agents.cursor = s.After.Agent
		}
	case s.Lost && st.jump:
		a.agentsProblem(s.After.Agent, uitext.Fill(uitext.AgentLostPane, st.number))
	}
	return read
}
