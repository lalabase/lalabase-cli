package ui

import (
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/agents"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/launch"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
	"github.com/gboyke/lalabase-cli/internal/worker"
)

// Update is one step: the message, then the disk's flush (C1e), which
// sends what the step changed and the disk reads it issued, as one command.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(msg)
	next.fitDialog()
	if a := next.app; a != nil {
		if flush := a.flushDisk(); flush != nil {
			cmd = tea.Batch(cmd, flush)
		}
		if show := next.syncViewer(); show != nil {
			cmd = tea.Batch(cmd, show)
		}
	}
	return next, cmd
}

func (m Model) update(msg tea.Msg) (Model, tea.Cmd) {
	if a := m.app; a != nil && !a.expired && a.revoked() {
		// The mailbox knows the 401 before its wake-up reached the model:
		// whatever this message is, nothing of the session is shown, read
		// or asked any more.
		m.expire()
	}
	switch msg := msg.(type) {
	case worker.Fatal:
		// The command reads the error after the terminal is restored.
		m.app.closeDisk()
		return m, tea.Quit
	case diskOpened:
		return m.receiveDiskOpened(msg)
	case diskDone:
		return m.receiveDiskDone(msg)
	case tea.WindowSizeMsg:
		m.width, m.height = max(0, msg.Width), max(0, msg.Height)
		if m.app != nil {
			// The object pane may have come into view: the preview of the
			// selected ticket, if any, is due after the pause.
			return m, m.selectionChanged()
		}
		return m, nil
	case tea.ModeReportMsg:
		if msg.Mode == ansi.ModeUnicodeCore && (msg.Value == ansi.ModeReset || msg.Value == ansi.ModeSet || msg.Value == ansi.ModePermanentlySet) {
			m.widthMethod = ansi.GraphemeWidth
		}
		return m, nil
	case gateway.StartupResult:
		return m.receiveStartup(msg)
	case gateway.InboxResult:
		return m.receiveInbox(msg)
	case gateway.ProjectsResult:
		return m.receiveProjects(msg)
	case gateway.TicketsResult:
		return m.receiveTickets(msg)
	case gateway.TicketResult:
		switch msg.Area {
		case gateway.Expansion:
			return m.receiveExpansion(msg)
		case gateway.Titles:
			return m.receiveTitle(msg)
		}
		return m.receiveDetail(msg)
	case gateway.TimeResult:
		return m.receiveTime(msg)
	case gateway.MarkReadResult:
		return m.receiveMark(msg)
	case gateway.WriteResult:
		return m.receiveBooking(msg)
	case launch.Done:
		return m, m.receiveLaunch(msg)
	case agents.Snapshot:
		if m.app == nil {
			return m, nil
		}
		cmd := m.app.receiveAgents(msg)
		if m.focus == view.Agents && len(m.app.agents.list) == 0 {
			// The last agent is gone: its list closes, and the pane behind
			// it gets its preview, as after esc. A read that failed leaves
			// no agent either (receiveAgents): what the directory holds
			// cannot be trusted then (internal/agents), and a list that
			// shows nothing has nothing to keep open. The next read that
			// succeeds brings the area back; A opens the list again.
			var back tea.Cmd
			m, back = m.closeAgents()
			cmd = tea.Batch(cmd, back)
		}
		return m, cmd
	case agents.Removed:
		return m.receiveRemoved(msg)
	case agents.Settled:
		return m, m.receiveSettled(msg)
	case agents.Marked:
		if m.app != nil {
			m.app.receiveMarked(msg)
		}
		return m, nil
	case gateway.Result:
		// A read refused before it was sent: its waiter is freed, nothing
		// is shown of it.
		m.receive(msg)
		return m, nil
	case gateway.Due:
		return m.due(msg)
	case gateway.Wake:
		return m.wake(msg)
	case gateway.Expired:
		// The deadline of a pause or of a hint or of a booking's notice:
		// whichever still shows it is over; or of the next read of the agents.
		if a := m.app; a != nil && msg.Session == a.session {
			if msg.Until.Equal(a.pause) {
				a.pause = time.Time{}
			}
			if msg.Until.Equal(a.launch.until) {
				a.launch.notice, a.launch.until = present.Inline{}, time.Time{}
			}
			if msg.Until.Equal(a.agents.noticeUntil) {
				a.clearAgentsNotice()
			}
			a.bookNotices = slices.DeleteFunc(a.bookNotices, func(n bookNotice) bool { return n.until.Equal(msg.Until) })
			return m, a.agentsDue(msg.Until)
		}
		return m, nil
	case tea.KeyPressMsg:
		return m.key(msg)
	case tea.PasteMsg:
		return m.paste(msg), nil
	case DialogOutcome:
		return m.receiveOutcome(msg)
	case tea.MouseWheelMsg:
		return m.wheel(msg), nil
	}
	return m, nil
}

// wake reads the mailbox after a wake-up: a revocation ends the session, a
// later pause is shown with its deadline and cleared when it has come.
func (m Model) wake(msg gateway.Wake) (Model, tea.Cmd) {
	a := m.app
	if a == nil {
		return m, nil
	}
	cmds := []tea.Cmd{a.wait()}
	if msg.Session != a.session {
		return m, tea.Batch(cmds...)
	}
	st := a.deps.Events.State()
	if st.Revoked {
		m.expire()
		return m, tea.Batch(cmds...)
	}
	if now := a.now(); st.Pause.After(now) && st.Pause.After(a.pause) {
		a.pause = st.Pause
		cmds = append(cmds, a.deps.Events.Expiry(a.pause, now))
	}
	return m, tea.Batch(cmds...)
}

func (m Model) key(key tea.KeyPressMsg) (Model, tea.Cmd) {
	action := routeKey(m.mode, key)
	if action.Kind == Quit {
		m.app.closeDisk()
		return m, tea.Quit
	}
	if action.Kind == Interrupt {
		m.app.closeDisk()
		return m, tea.Interrupt
	}
	// allowed is built from the bindings of the state, the small terminal
	// included: what is not offered does nothing.
	if !m.allowed()[action.Kind] {
		return m, nil
	}
	if m.dialog != nil {
		return m.dialogKey(action)
	}
	if m.app == nil {
		return m.shellKey(action.Kind), nil
	}
	if m.mode == TextEntry {
		return m.searchKey(action.Kind, action.Text)
	}
	return m.sessionKey(action.Kind)
}

// shellKey is the C1a shell without a session.
func (m Model) shellKey(kind Action) Model {
	switch kind {
	case Left:
		if m.focus > view.Navigation {
			m.focus--
		}
	case Right:
		if m.focus < view.Object {
			m.focus++
		}
	case OpenList:
		if m.focus == view.Navigation {
			m.focus = view.List
		}
	case Back:
		if m.focus == view.List {
			m.focus = view.Navigation
		} else {
			m.focus = view.List
		}
	}
	return m
}

func (m Model) sessionKey(kind Action) (Model, tea.Cmd) {
	a := m.app
	switch kind {
	case Left:
		m.intoTicket = false
		if m.focus > view.Navigation {
			m.focus--
		}
		return m, m.selectionChanged()
	case Right:
		if m.focus == view.Agents {
			// Into the ticket the agents' list shows.
			if a.detail != nil {
				m.focus, m.intoTicket = view.Object, true
			}
			return m, nil
		}
		if m.focus < view.Object {
			m.focus++
		}
		return m, m.selectionChanged()
	case Up, Down:
		delta := 1
		if kind == Up {
			delta = -1
		}
		return m.move(delta)
	case PageUp, PageDown:
		delta := 1
		if kind == PageUp {
			delta = -1
		}
		return m.movePage(delta)
	case OpenList:
		return m.open()
	case Back:
		return m.back()
	case PrevPage, NextPage:
		delta := 1
		if kind == PrevPage {
			delta = -1
		}
		switch {
		case m.focus == view.Object && a.detail != nil:
			return m.turnArea(delta)
		case a.tickets != nil:
			return m.turnTicketPage(delta)
		}
		return m.turnPage(delta)
	case Refresh:
		return m.retryOrRefresh()
	case Search:
		return m.startSearch(), nil
	case NextArea:
		return m.nextArea(), nil
	case Order:
		// o is contextual: in the details of a notification it opens the
		// ticket the notification names; in a ticket it switches the order
		// of the comments.
		if a.active != nil && a.origin == nil && m.focus == view.Object {
			return m.openLinkedTicket()
		}
		return m.switchOrder()
	case Expand:
		return m.expand()
	case MarkRead:
		return m.markRead()
	case Launch:
		return m.pressX()
	case OpenAgents:
		if m.focus == view.Agents {
			return m.closeAgents()
		}
		return m.openAgents()
	case RemoveAgent:
		return m, m.removeAgent()
	case BookTime:
		return m.pressB(), nil
	}
	return m, nil
}

// move moves the cursor of the focused pane by rows; nothing is loaded by
// it, except that a ticket's preview falls due after the pause.
func (m Model) move(delta int) (Model, tea.Cmd) {
	a := m.app
	switch m.focus {
	case view.Navigation:
		a.moveHighlight(delta)
	case view.Agents:
		a.moveAgent(delta)
	case view.Object:
		switch d := a.detail; {
		case d != nil && d.area == areaAttachments:
			return m.moveFile(delta), nil
		case d != nil:
			return m.moveComment(delta), nil
		}
	case view.List:
		switch {
		case a.active != nil:
			a.active.moveSelection(delta)
		case a.tickets != nil:
			a.tickets.moveSelection(delta)
			return m, m.selectionChanged()
		case a.projects != nil:
			a.projects.moveSelection(delta)
		}
	}
	return m, nil
}

// movePage moves the list's cursor by a screen of rows, or scrolls the
// object pane by a screen of lines.
func (m Model) movePage(delta int) (Model, tea.Cmd) {
	a := m.app
	g := m.geometry()
	switch m.focus {
	case view.List:
		rows := delta * max(1, view.Capacity(m.listColumn(), g))
		switch {
		case a.active != nil:
			a.active.moveSelection(rows)
		case a.tickets != nil:
			a.tickets.moveSelection(rows)
			return m, m.selectionChanged()
		case a.projects != nil:
			a.projects.moveSelection(rows)
		}
	case view.Object:
		m.scrollObject(g, delta*max(1, view.Capacity(m.objectColumn(), g)))
	}
	return m, nil
}

// wheelLines is how far one turn of the mouse wheel scrolls the object
// pane, the step most terminal programs take.
const wheelLines = 3

// wheel scrolls the object pane by a turn of the wheel over its body,
// wherever the focus is: the pane in sight is the one under the pointer.
// Anywhere else, sideways, behind a dialog or without a session, the wheel
// does nothing.
func (m Model) wheel(msg tea.MouseWheelMsg) Model {
	if m.app == nil || m.dialog != nil {
		return m
	}
	g := m.geometry()
	if g.Small || !overObject(g, m.focus, msg.X, msg.Y) {
		return m
	}
	switch msg.Button {
	case tea.MouseWheelUp:
		m.scrollObject(g, -wheelLines)
	case tea.MouseWheelDown:
		m.scrollObject(g, wheelLines)
	}
	return m
}

// overObject is whether the cell x, y lies in the object pane's body: below
// the header, the path line and a notice on a line of its own, above
// the help, and inside the last frame (the pane is the rightmost, or the
// only one when it has the focus in one column).
func overObject(g view.Geometry, focus view.Pane, x, y int) bool {
	top := 2 + g.NoticeHeight
	if y < top || y >= top+g.BodyHeight || x < 0 || x >= g.Width {
		return false
	}
	if g.Columns == 1 {
		return focus == view.Object
	}
	return x >= g.Width-g.ObjectWidth
}

// scrollObject scrolls the object pane by lines, clamped to its text.
func (m Model) scrollObject(g view.Geometry, lines int) {
	a := m.app
	c := m.objectColumn()
	top := max(0, view.Extent(c, g, view.Object, m.widthMethod)-max(1, view.Capacity(c, g)))
	switch {
	case a.detail != nil:
		// From where the pane is now, the anchor's place included; the
		// scroll is the person's, the anchor is let go.
		a.detail.offset = max(0, min(top, min(top, c.Offset)+lines))
		a.detail.follow = anchorNone
	case a.active != nil:
		a.active.detailOffset = max(0, min(top, a.active.detailOffset+lines))
	}
}

// open is Enter: in the chooser the highlighted entry, in the list the
// selected row (the details of a notification, the tickets of a project,
// the ticket).
func (m Model) open() (Model, tea.Cmd) {
	a := m.app
	switch m.focus {
	case view.Navigation:
		if a.highlightSection == sectionTickets {
			return m.openProjects(a.highlight)
		}
		return m.activate(a.highlight)
	case view.Agents:
		return m.openAgent()
	case view.List:
		switch {
		case a.active != nil:
			v := a.active
			if v.selected != "" {
				// The details of another row: a ticket opened from a
				// notification goes with them.
				a.leaveLinkedTicket()
				v.opened, v.detailOffset = v.selected, 0
				m.focus = view.Object
			}
		case a.tickets != nil:
			return m.openTicket()
		case a.projects != nil:
			return m.openTickets(a.projects.selected)
		}
	case view.Object:
		if t, f, ok := m.fileTarget(); ok {
			return m, m.openFile(t, f)
		}
	}
	return m, nil
}

// back is Esc: from the chooser to the list, from the list one level up
// (the chooser; from the tickets the projects), from the pane to the list.
func (m Model) back() (Model, tea.Cmd) {
	a := m.app
	switch m.focus {
	case view.Agents:
		return m.closeAgents()
	case view.Navigation:
		if a.shown() {
			m.focus = view.List
		}
	case view.List:
		if a.tickets != nil {
			a.leaveList()
			return m, nil
		}
		m.focus = view.Navigation
	case view.Object:
		if m.intoTicket {
			// Back to the agents' list it came from; the
			// expansion closes first.
			if a.detail != nil && a.detail.expansion != nil {
				a.closeExpansion()
				return m, nil
			}
			m.focus, m.intoTicket = view.Agents, false
			return m, nil
		}
		switch {
		case a.origin != nil:
			if a.detail != nil && a.detail.expansion != nil {
				// The expansion closes first, without a request.
				a.closeExpansion()
				return m, nil
			}
			return m.closeLinkedTicket(), nil
		case a.active != nil:
			a.active.opened = ""
			m.focus = view.List
		case a.tickets != nil:
			if a.detail != nil && a.detail.expansion != nil {
				// The expansion closes first, without a request.
				a.closeExpansion()
				return m, nil
			}
			m = m.closeTicket()
			return m, m.selectionChanged()
		case a.agentTicket():
			// The ticket an agent opened (C9b): the expansion first, then
			// the projects; the ticket stays in the pane.
			if a.detail.expansion != nil {
				a.closeExpansion()
				return m, nil
			}
			m.focus = view.List
		default:
			m.focus = view.List
		}
	}
	return m, nil
}

// moveHighlight moves the cursor of the chooser; nothing is loaded by it.
func (a *app) moveHighlight(delta int) {
	entries := a.entries()
	at := 0
	for i, e := range entries {
		if e.org == a.highlight && e.section == a.highlightSection {
			at = i
		}
	}
	at = max(0, min(len(entries)-1, at+delta))
	a.highlight, a.highlightSection = entries[at].org, entries[at].section
}

// moveSelection moves the cursor of the inbox by rows; details stay as
// they are.
func (v *inboxView) moveSelection(delta int) {
	if len(v.rows) == 0 {
		return
	}
	at := max(0, v.index(v.selected))
	at = max(0, min(len(v.rows)-1, at+delta))
	v.selected = v.rows[at].ID
}
