package ui

import (
	"time"

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
// notification it was opened from, or of the list it was opened in; ""
// when neither is known (the agent's record keeps it, C9a).
func (a *app) detailOrg() string {
	d := a.detail
	switch {
	case a.origin != nil && a.origin.ticket == d.id:
		return a.origin.org
	case a.tickets != nil && a.tickets.project == d.project:
		return a.tickets.org
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
	a.launch.until = time.Time{}
	return m, a.deps.Launcher.Launch(a.launch.ref, t)
}

// receiveLaunch takes the answer of the launch running and frees x; any
// other answer is late and changes nothing. A hint gets its deadline, and
// the Events wake the model when it has come, so the path is drawn again.
func (m Model) receiveLaunch(d launch.Done) tea.Cmd {
	a := m.app
	if a == nil || !a.launch.busy || d.Ref != a.launch.ref {
		return nil
	}
	a.launch.busy, a.launch.ticket = false, d.Ticket.ID
	a.launch.notice, a.launch.tone = launchNotice(d), launchTone(d.Kind)
	if d.Kind == launch.Opened && d.Untracked != "" {
		// Opened, but its state will not be shown: a problem to see, not
		// a hint that goes.
		a.launch.notice, a.launch.tone = uitext.Fill(uitext.LaunchUntracked, d.Ticket.Number, d.Untracked), present.NoticeProblem
	}
	// The launch cleared the deadline when it started (launchClaude): a
	// problem or a canceled launch has none.
	if a.launch.tone != present.Plain || a.launch.notice.String() == "" {
		return nil
	}
	now := a.now()
	a.launch.until = now.Add(hintFor)
	if a.deps.Events == nil {
		return nil
	}
	return a.deps.Events.Expiry(a.launch.until, now)
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
