package ui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/launch"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/uitext"
)

// Launcher starts Claude Code beside a ticket (C7): one command per launch,
// whose message is a launch.Done with the same ref. The command gives the
// session one (launch.New); tests give a fake.
type Launcher interface {
	Launch(ref uint64, t launch.Ticket) tea.Cmd
}

// launchState is the one launch at a time: its number, whether it runs, and
// the notice of the newest one with the ticket it was for.
type launchState struct {
	ref    uint64
	busy   bool
	ticket string
	notice present.Inline
}

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
	return launch.Ticket{Project: d.project, ID: d.id, Number: d.record.Number}, true
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
	a.launch.notice = uitext.Fill(uitext.Launching, t.Number)
	return m, a.deps.Launcher.Launch(a.launch.ref, t)
}

// receiveLaunch takes the answer of the launch running and frees x; any
// other answer is late and changes nothing.
func (m Model) receiveLaunch(d launch.Done) {
	a := m.app
	if a == nil || !a.launch.busy || d.Ref != a.launch.ref {
		return
	}
	a.launch.busy, a.launch.ticket = false, d.Ticket.ID
	a.launch.notice = launchNotice(d)
}

// launchShown: the pane shows the ticket the notice is about.
func (a *app) launchShown() bool {
	return a.launch.notice.String() != "" && a.detail != nil && a.detail.id == a.launch.ticket
}

// launchTexts names the status text of each outcome: the texts of Opened
// and Ended take the ticket's number, the ones in withDetail the outcome's
// detail, the others nothing.
var launchTexts = map[launch.Kind]uitext.ID{
	launch.Opened:           uitext.LaunchOpened,
	launch.Ended:            uitext.LaunchEnded,
	launch.EndedWithError:   uitext.LaunchEndedError,
	launch.NotHandedOver:    uitext.LaunchNotHandedOver,
	launch.NoFolder:         uitext.LaunchNoFolder,
	launch.NotADirectory:    uitext.LaunchNotADirectory,
	launch.BadFolder:        uitext.LaunchBadFolder,
	launch.ConfigUnreadable: uitext.LaunchConfigUnreadable,
	launch.NoServer:         uitext.LaunchNoServer,
	launch.Conflict:         uitext.LaunchConflict,
	launch.BadName:          uitext.LaunchBadName,
	launch.NoClaude:         uitext.LaunchNoClaude,
	launch.NoPermission:     uitext.LaunchNoPermission,
	launch.SessionGone:      uitext.LaunchSessionGone,
	launch.NoAnswer:         uitext.LaunchNoAnswer,
	launch.ITermFailed:      uitext.LaunchFailed,
}

var withDetail = map[launch.Kind]bool{
	launch.EndedWithError: true, launch.NoFolder: true, launch.NotADirectory: true, launch.BadFolder: true,
	launch.ConfigUnreadable: true, launch.NoServer: true, launch.Conflict: true, launch.BadName: true,
}

// launchNotice is the status text of an outcome; nothing for a launch the
// session's end canceled. The detail passes the cleaning of Fill: it may
// name a server from a project's .mcp.json.
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
