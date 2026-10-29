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

// Launcher starts Claude Code beside a ticket (C7): one command per launch,
// whose message is a launch.Done with the same ref. The command gives the
// session one (launch.New); tests give a fake.
type Launcher interface {
	Launch(ref uint64, t launch.Ticket) tea.Cmd
}

// launchState is the one launch at a time: its number, whether it runs, and
// the notice of the newest one with the ticket it was for. A plain notice
// is a hint (L2): it is shown until its deadline, a problem or a launch
// running has none.
type launchState struct {
	ref    uint64
	busy   bool
	ticket string
	notice present.Inline
	tone   present.Style
	until  time.Time
}

// hintFor is how long a hint stands in the path's place.
const hintFor = 5 * time.Second

// launchTarget is the address of the ticket the pane shows, when x may
// launch: a launcher, no launch running, and a ticket shown whole, the
// record the one asked for. The ids are the pane's, not the list's: its
// cursor may stand on another row.
func (m Model) launchTarget() (launch.Ticket, bool) {
	a := m.app
	if a.deps.Launcher == nil || a.launch.busy {
		return launch.Ticket{}, false
	}
	d := a.detail
	if d == nil || !d.known || d.record.ID != d.id || d.record.Number <= 0 {
		return launch.Ticket{}, false
	}
	return launch.Ticket{Org: a.detailOrg(), Project: d.project, ID: d.id, Number: d.record.Number}, true
}

// detailOrg is the organisation of the ticket in the pane: the one of the
// notification it was opened from, of the list it was opened in, or of
// the projects an agent opened it beside (C9b); "" when none is known
// (the agent's record keeps it, C9a).
func (a *app) detailOrg() string {
	d := a.detail
	switch {
	case a.origin != nil && a.origin.ticket == d.id:
		return a.origin.org
	case a.tickets != nil && a.tickets.project == d.project:
		return a.tickets.org
	case a.agentTicket():
		return a.projects.org
	}
	return ""
}

// launchClaude is x: one launch for the ticket shown, numbered; until its
// answer, x does nothing.
func (m Model) launchClaude() (Model, tea.Cmd) {
	t, ok := m.launchTarget()
	if !ok {
		return m, nil
	}
	a := m.app
	a.launch.ref++
	a.launch.busy, a.launch.ticket = true, t.ID
	a.launch.notice, a.launch.tone = uitext.Fill(uitext.Launching, t.Number), present.NoticeWaiting
	a.clearAgentsNotice()
	a.launch.until = time.Time{}
	// While it runs, the agents are read: its record comes with its end.
	return m, tea.Batch(a.deps.Launcher.Launch(a.launch.ref, t), a.readAgents())
}

// receiveLaunch takes the answer of the launch running and frees x; any
// other answer is late and changes nothing. A hint gets its deadline, and
// the Events wake the model when it has come, so the path is drawn again.
func (m Model) receiveLaunch(d launch.Done) tea.Cmd {
	a := m.app
	if a == nil || !a.launch.busy || d.Ref != a.launch.ref {
		return nil
	}
	return tea.Batch(m.launchDone(d), a.readAgents())
}

// launchDone is receiveLaunch's notice: its text and tone, and a hint's
// deadline.
func (m Model) launchDone(d launch.Done) tea.Cmd {
	a := m.app
	a.launch.busy, a.launch.ticket = false, d.Ticket.ID
	a.launch.notice, a.launch.tone = launchNotice(d), launchTone(d.Kind)
	if (d.Kind == launch.Opened || d.Kind == launch.Ended) && d.Untracked != "" {
		// Opened (or ran here), but its state is not shown: a problem to
		// see, not a hint that goes. iTerm's silence (NoAnswer) says more
		// and keeps its own notice.
		a.launch.notice, a.launch.tone = uitext.Fill(uitext.LaunchUntracked, d.Ticket.Number, d.Untracked), present.NoticeProblem
	}
	// The launch cleared the deadline when it started (launchClaude): a
	// problem or a canceled launch has none.
	var cmd tea.Cmd
	if a.launch.tone == present.Plain && a.launch.notice.String() != "" {
		now := a.now()
		a.launch.until = now.Add(hintFor)
		if a.deps.Events != nil {
			cmd = a.deps.Events.Expiry(a.launch.until, now)
		}
	}
	if a.launchShown() {
		// Decide after the final tone and deadline: a plain hint needs
		// its deadline, and an opened launch may have a tracking error.
		a.clearAgentsNotice()
	}
	return cmd
}

// launchTone is the notice's tone by the outcome: plain when Claude Code
// opened or ended, a problem for every outcome that opened nothing or
// ended in an error.
func launchTone(k launch.Kind) present.Style {
	switch k {
	case launch.Opened, launch.Ended, launch.Canceled:
		return present.Plain
	}
	return present.NoticeProblem
}

// launchShown: the pane shows the ticket the notice is about, and a hint's
// deadline has not come by the model's clock.
func (a *app) launchShown() bool {
	if a.launch.tone == present.Plain && !a.launch.until.After(a.now()) {
		return false
	}
	return a.launch.notice.String() != "" && a.detail != nil && a.detail.id == a.launch.ticket
}

// launchTexts names the status text of each outcome: the texts of Opened
// and Ended take the ticket's number, the ones in withDetail the outcome's
// detail, the others nothing.
var launchTexts = map[launch.Kind]uitext.ID{
	launch.Opened:         uitext.LaunchOpened,
	launch.Ended:          uitext.LaunchEnded,
	launch.EndedWithError: uitext.LaunchEndedError,
	launch.NotHandedOver:  uitext.LaunchNotHandedOver,
	launch.NoFolder:       uitext.LaunchNoFolder,
	launch.NotADirectory:  uitext.LaunchNotADirectory,
	launch.BadFolder:      uitext.LaunchBadFolder,
	launch.NoFiles:        uitext.LaunchNoFiles,
	launch.NoClaude:       uitext.LaunchNoClaude,
	launch.NoPermission:   uitext.LaunchNoPermission,
	launch.SessionGone:    uitext.LaunchSessionGone,
	launch.NoAnswer:       uitext.LaunchNoAnswer,
	launch.ITermFailed:    uitext.LaunchFailed,
	launch.TmuxFailed:     uitext.LaunchTmuxFailed,
}

var withDetail = map[launch.Kind]bool{
	launch.EndedWithError: true, launch.NoFolder: true, launch.NotADirectory: true, launch.BadFolder: true,
	launch.NoFiles: true,
}

// launchNotice is the status text of an outcome; nothing for a launch the
// session's end canceled. The detail passes the cleaning of Fill: it may
// name a path of the person's config.
func launchNotice(d launch.Done) present.Inline {
	id, ok := launchTexts[d.Kind]
	switch {
	case !ok:
		return present.Inline{}
	case d.Kind == launch.Opened || d.Kind == launch.Ended:
		return uitext.Fill(id, d.Ticket.Number)
	case withDetail[d.Kind]:
		return uitext.Fill(id, d.Detail)
	}
	return uitext.Inline(id)
}

// agentViewer shows an agent's tmux session beside the client (prototype,
// launch.Launcher.View); a launcher without it has no viewer.
type agentViewer interface {
	View(agent string) tea.Cmd
}

// syncViewer asks the viewer for the agent that should be seen, when that
// changed: the newest running agent of the ticket in the pane, while the
// focus is on the ticket or in the agents' list; none otherwise.
func (m Model) syncViewer() tea.Cmd {
	a := m.app
	v, ok := a.deps.Launcher.(agentViewer)
	if !ok {
		return nil
	}
	want := m.viewedAgent()
	if want == a.viewing {
		return nil
	}
	a.viewing = want
	return v.View(want)
}

// viewedAgent is the agent the viewer should show, "" for none.
func (m Model) viewedAgent() string {
	a := m.app
	if a.expired || a.detail == nil || (m.focus != view.Object && m.focus != view.Agents) {
		return ""
	}
	var best agents.Agent
	for _, ag := range a.agents.list {
		if ag.Ticket != a.detail.id || ag.State == agents.Ended || ag.State == agents.Lost {
			continue
		}
		if best.ID == "" || ag.Since.After(best.Since) {
			best = ag
		}
	}
	return best.ID
}
