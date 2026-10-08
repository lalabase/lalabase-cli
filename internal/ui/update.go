package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
	"github.com/gboyke/lalabase-cli/internal/worker"
)

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case worker.Fatal:
		// The command reads the error after the terminal is restored.
		return m, tea.Quit
	case tea.WindowSizeMsg:
		m.width, m.height = max(0, msg.Width), max(0, msg.Height)
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
	case gateway.Wake:
		return m.wake(msg)
	case gateway.PauseEnded:
		if a := m.app; a != nil && msg.Session == a.session && msg.Until.Equal(a.pause) {
			a.pause = time.Time{}
		}
		return m, nil
	case tea.KeyPressMsg:
		return m.key(msg)
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
		return m, tea.Quit
	}
	if action.Kind == Interrupt {
		return m, tea.Interrupt
	}
	if m.small() || !m.allowed()[action.Kind] {
		return m, nil
	}
	if m.app == nil {
		return m.shellKey(action.Kind), nil
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
	v := a.active
	switch kind {
	case Left:
		if m.focus > view.Navigation {
			m.focus--
		}
	case Right:
		if m.focus < view.Object {
			m.focus++
		}
	case Up, Down:
		delta := 1
		if kind == Up {
			delta = -1
		}
		if m.focus == view.Navigation {
			a.moveHighlight(delta)
		} else if m.focus == view.List && v != nil {
			v.moveSelection(delta)
		}
	case PageUp, PageDown:
		g := m.geometry()
		delta := 1
		if kind == PageUp {
			delta = -1
		}
		switch m.focus {
		case view.List:
			if v != nil {
				v.moveSelection(delta * max(1, view.Capacity(m.listColumn(), g)))
			}
		case view.Object:
			if v != nil {
				v.detailOffset = max(0, v.detailOffset+delta*max(1, view.Capacity(m.objectColumn(), g)))
			}
		}
	case OpenList:
		if m.focus == view.Navigation {
			return m.activate(a.highlight)
		}
		if m.focus == view.List && v != nil && v.selected != "" {
			v.opened, v.detailOffset = v.selected, 0
			m.focus = view.Object
		}
	case Back:
		switch m.focus {
		case view.Navigation:
			if v != nil {
				m.focus = view.List
			}
		case view.List:
			m.focus = view.Navigation
		case view.Object:
			if v != nil {
				v.opened = ""
			}
			m.focus = view.List
		}
	case PrevPage:
		return m.turnPage(-1)
	case NextPage:
		return m.turnPage(1)
	case Refresh:
		return m.retryOrRefresh()
	}
	return m, nil
}

// moveHighlight moves the cursor of the chooser; nothing is loaded by it.
func (a *app) moveHighlight(delta int) {
	entries := a.entries()
	at := 0
	for i, e := range entries {
		if e.org == a.highlight {
			at = i
		}
	}
	at = max(0, min(len(entries)-1, at+delta))
	a.highlight = entries[at].org
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
