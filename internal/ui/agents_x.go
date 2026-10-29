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
	if _, out := a.agents.replacing[ag.ID]; out {
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
		a.agents.replacing = map[string]replacement{}
	}
	a.agents.replacing[x.agent.ID] = replacement{}
	a.launch.notice, a.launch.tone = uitext.Fill(uitext.StartingAgain, x.ticket.Number), present.NoticeWaiting
	return m, tea.Batch(a.deps.Launcher.Again(a.launch.ref, x.ticket, again), a.readAgents())
}

// settleAfter is the settlement a launch's answer asks for (agents.Settle),
// nil for none: a jump that found the pane gone; an agent that ran here
// (its end may have come before any hook). A start again whose pane
// opened waits for the new agent's first hook (settleReplaced): claude
// may not have found the conversation (resumed in another folder, its
// transcript gone), and then the old agent is all that is left of it. A
// start again that went no further keeps the old agent, which x may start
// again.
func (a *app) settleAfter(d launch.Done) tea.Cmd {
	var after agents.After
	switch {
	case d.Action == launch.JumpAction && d.Kind == launch.PaneGone:
		// A start again's agent whose pane went before its first hook:
		// the start failed, and it gives way to the agent it replaces.
		after = agents.After{Agent: d.Agent, Replaces: a.replacedBy(d.Agent)}
	case d.Agent != "" && (d.Kind == launch.Ended || d.Kind == launch.EndedWithError):
		after = agents.After{Agent: d.Agent, Replaces: d.Replaces}
	case d.Agent != "" && d.Replaces != "" && d.Kind == launch.Opened && a.deps.Agents != nil:
		// A read out now may have been issued before the new agent's
		// record was written: only one issued after may end the wait.
		a.agents.replacing[d.Replaces] = replacement{next: d.Agent, after: a.agents.ref}
		return nil
	}
	if after.Agent == "" || a.deps.Agents == nil {
		delete(a.agents.replacing, d.Replaces)
		return nil
	}
	return a.settleAgents(after, d.Ticket.Number, d.Action == launch.JumpAction)
}

// settleAgents issues the settlement after, unless it is out already.
func (a *app) settleAgents(after agents.After, number int, jump bool) tea.Cmd {
	if _, out := a.agents.settling[after]; out {
		return nil
	}
	if a.agents.settling == nil {
		a.agents.settling = map[agents.After]settling{}
	}
	a.agents.settling[after] = settling{number: number, jump: jump}
	return a.deps.Agents.Settle(after, a.now())
}

// leaveAgentsList takes the notice of x in the agents' list away as the
// list closes (review round 2 of #43): it belongs there, and a problem has
// no deadline that would end it. The answer of an x still running then
// belongs to its ticket, as one pressed there.
func (a *app) leaveAgentsList() {
	if a.launch.everywhere {
		a.launch.notice, a.launch.until, a.launch.everywhere = present.Inline{}, time.Time{}, false
	}
}

// replacement is a start again of an agent: the new agent once its pane
// opened ("" while the launch runs), and the newest read issued then:
// only a read issued after it has surely seen the new agent's record.
type replacement struct {
	next  string
	after uint64
}

// replacedBy is the agent the start again that made id replaces, "" for
// none.
func (a *app) replacedBy(id string) string {
	for old, r := range a.agents.replacing {
		if r.next != "" && r.next == id {
			return old
		}
	}
	return ""
}

// settleReplaced looks at each start again whose pane opened, after the
// read ref that succeeded: once a hook of the new agent came, the old
// agent goes; a new agent that left without one (d, a lost mark, another
// client) ends the wait, and the old agent stays and may be started
// again. Only a read issued after the pane opened can tell that it left
// (review round 2 of #43): an earlier one may not have seen its record.
func (a *app) settleReplaced(ref uint64) tea.Cmd {
	var cmds []tea.Cmd
	for old, r := range a.agents.replacing {
		if r.next == "" {
			continue
		}
		ag, ok := a.listed(r.next)
		switch {
		case ok && ag.Resumable:
			cmds = append(cmds, a.settleAgents(agents.After{Agent: r.next, Replaces: old, Opened: true}, ag.Number, false))
		case ref > r.after && (!ok || ag.State == agents.Lost):
			delete(a.agents.replacing, old)
		}
	}
	return tea.Batch(cmds...)
}

// listed is the agent id of the newest read.
func (a *app) listed(id string) (agents.Agent, bool) {
	for _, ag := range a.agents.list {
		if ag.ID == id {
			return ag, true
		}
	}
	return agents.Agent{}, false
}

// replaced says whether ag gives way to a start again of it that is
// listed: the ticket's head, row and meta bar show the new agent, so x
// there acts on it.
func (a *app) replaced(ag agents.Agent) bool {
	r, ok := a.agents.replacing[ag.ID]
	if !ok || r.next == "" {
		return false
	}
	_, listed := a.listed(r.next)
	return listed
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
