package ui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		m.width, m.height = max(0, size.Width), max(0, size.Height)
		return m, nil
	}
	if report, ok := msg.(tea.ModeReportMsg); ok {
		if report.Mode == ansi.ModeUnicodeCore && (report.Value == ansi.ModeReset || report.Value == ansi.ModeSet || report.Value == ansi.ModePermanentlySet) {
			m.widthMethod = ansi.GraphemeWidth
		}
		return m, nil
	}
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	action := routeKey(m.mode, key)
	if action.Kind == Quit {
		return m, tea.Quit
	}
	if action.Kind == Interrupt {
		return m, tea.Interrupt
	}
	if layout(m.width, m.height).Small {
		return m, nil
	}
	switch action.Kind {
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
	return m, nil
}
