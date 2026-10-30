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

// Launcher starts Claude Code beside a ticket (C7) and opens a ticket's
// file (C10): one command per launch, whose message is a launch.Done with
// the same ref. The command gives the
// session one (launch.New); tests give a fake.
type Launcher interface {
	Launch(ref uint64, t launch.Ticket) tea.Cmd
	// Jump selects the pane of an agent at t (C9c), Again starts an
	// agent whose session is over again as a new one; their messages are
	// launch.Done with the same ref. Jumps says whether a jump can find a
	// pane at all (the client runs in iTerm).
	Jump(ref uint64, t launch.Ticket, agent, pane string) tea.Cmd
	Again(ref uint64, t launch.Ticket, a launch.Again) tea.Cmd
	Jumps() bool
	// OpenFile opens a file of t in the browser (C10); its message is a
	// launch.Done with the same ref.
	OpenFile(ref uint64, t launch.Ticket, f launch.File) tea.Cmd
}

// launchState is the one launch at a time, a file's opening among them: its number, whether it runs, and
// the notice of the newest one with the ticket it was for. A plain notice
// is a hint (L2): it is shown until its deadline, a problem or a launch
// running has none. everywhere: x was pressed in the agents' list, where
// no ticket is shown, so its notice shows wherever the person is (C9c).
type launchState struct {
	ref        uint64
	busy       bool
	ticket     string
	notice     present.Inline
	tone       present.Style
	until      time.Time
	everywhere bool
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

// fileTarget is what Enter opens (C10, C12): the selected file of the ticket
// the pane shows, of its attachments or of a comment, when x's conditions
// hold and the ticket's organisation is known. Like launchTarget it reads
// the pane's record, never the list's cursor. The name is only for the
// notice.
func (m Model) fileTarget() (launch.Ticket, launch.File, bool) {
	t, ok := m.launchTarget()
	if !ok || m.focus != view.Object || t.Org == "" {
		return launch.Ticket{}, launch.File{}, false
	}
	f, ok := m.app.detail.selectedFile()
	if !ok {
		return launch.Ticket{}, launch.File{}, false
	}
	return t, f, true
}

// openFile is Enter on a file: one child at a time, shared with x, and its
// notice in the same place.
func (m Model) openFile(t launch.Ticket, f launch.File) tea.Cmd {
	a := m.app
	a.launch.ref++
	a.launch.busy, a.launch.ticket, a.launch.everywhere = true, t.ID, false
	a.launch.notice, a.launch.tone = uitext.Fill(uitext.OpeningFile, f.Name), present.NoticeWaiting
	a.clearAgentsNotice()
	a.launch.until = time.Time{}
	return a.deps.Launcher.OpenFile(a.launch.ref, t, f)
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

// startClaude is x on a ticket without an agent: one launch for the
// ticket shown, numbered; until its answer, x does nothing.
func (m Model) startClaude(t launch.Ticket) tea.Cmd {
	a := m.app
	a.launch.ref++
	a.launch.busy, a.launch.ticket, a.launch.everywhere = true, t.ID, false
	a.launch.notice, a.launch.tone = uitext.Fill(uitext.Launching, t.Number), present.NoticeWaiting
	a.clearAgentsNotice()
	a.launch.until = time.Time{}
	// While it runs, the agents are read: its record comes with its end.
	return tea.Batch(a.deps.Launcher.Launch(a.launch.ref, t), a.readAgents())
}

// receiveLaunch takes the answer of the launch running and frees x; any
// other answer is late and changes nothing. A hint gets its deadline, and
// the Events wake the model when it has come, so the path is drawn again.
func (m Model) receiveLaunch(d launch.Done) tea.Cmd {
	a := m.app
	if a == nil || !a.launch.busy || d.Ref != a.launch.ref {
		return nil
	}
	return tea.Batch(m.launchDone(d), a.readAgents(), a.settleAfter(d))
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
		cmd = a.launchHint()
	}
	if a.launchShown(m.focus == view.Agents) {
		// Decide after the final tone and deadline: a plain hint needs
		// its deadline, and an opened launch may have a tracking error.
		a.clearAgentsNotice()
	}
	return cmd
}

// launchHint gives the launch's notice a hint's deadline, hintFor from
// now by the model's clock; the Events wake the model when it has come.
func (a *app) launchHint() tea.Cmd {
	now := a.now()
	a.launch.until = now.Add(hintFor)
	if a.deps.Events == nil {
		return nil
	}
	return a.deps.Events.Expiry(a.launch.until, now)
}

// launchTone is the notice's tone by the outcome: plain when Claude Code
// opened or ended, the jump went or had nowhere to go, or a file went to
// the browser or had no way there, a problem for every outcome that opened
// nothing or ended in an error.
func launchTone(k launch.Kind) present.Style {
	switch k {
	case launch.Opened, launch.Ended, launch.Canceled, launch.Jumped, launch.NoPane, launch.FileOpened, launch.NoOpener:
		return present.Plain
	}
	return present.NoticeProblem
}

// launchShown: the notice's place is shown (the pane shows the ticket the
// notice is about, or x was pressed in the agents' list and that is open:
// inAgents), and a hint's deadline has not come by the model's clock. A
// problem of the list stands there only (review round 1 of #43): it has
// no deadline, and outside the list nothing would take it away.
func (a *app) launchShown(inAgents bool) bool {
	if a.launch.tone == present.Plain && !a.launch.until.After(a.now()) {
		return false
	}
	shown := a.launch.everywhere && inAgents || !a.launch.everywhere && a.detail != nil && a.detail.id == a.launch.ticket
	return a.launch.notice.String() != "" && shown
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

// jumpTexts name the status text of a jump's outcomes (C9c), each with the
// ticket's number but iTerm's refusal; any other is jumpFailed.
var jumpTexts = map[launch.Kind]uitext.ID{
	launch.Jumped:       uitext.Jumped,
	launch.PaneGone:     uitext.JumpPaneGone,
	launch.NoPane:       uitext.JumpNoPane,
	launch.NoPermission: uitext.LaunchNoPermission,
}

// fileNotice is the status text of opening a file (C10): the file's name is
// foreign and passes the cleaning of Fill.
func fileNotice(d launch.Done) present.Inline {
	switch d.Kind {
	case launch.FileOpened:
		return uitext.Fill(uitext.FileOpened, d.Detail)
	case launch.NoOpener:
		return uitext.Inline(uitext.FileNoOpener)
	case launch.NotHandedOver:
		return uitext.Inline(uitext.FileNotHandedOver)
	case launch.NoAnswer:
		return uitext.Inline(uitext.FileNoAnswer)
	case launch.Canceled:
		return present.Inline{}
	}
	return uitext.Inline(uitext.FileOpenFailed)
}

// launchNotice is the status text of an outcome; nothing for a launch the
// session's end canceled. The detail passes the cleaning of Fill: it may
// name a path of the person's config.
func launchNotice(d launch.Done) present.Inline {
	if d.Action == launch.OpenFileAction {
		return fileNotice(d)
	}
	if d.Action == launch.JumpAction {
		id, ok := jumpTexts[d.Kind]
		switch {
		case d.Kind == launch.Canceled:
			return present.Inline{}
		case !ok:
			return uitext.Inline(uitext.JumpFailed)
		case d.Kind == launch.NoPermission:
			return uitext.Inline(id)
		}
		return uitext.Fill(id, d.Ticket.Number)
	}
	if d.Action == launch.AgainAction && d.Kind == launch.Opened {
		return uitext.Fill(uitext.LaunchResumed, d.Ticket.Number)
	}
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

// agentViewer shows an agent's tmux session beside the client
// (launch.Launcher.View); a launcher without it has no viewer.
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
