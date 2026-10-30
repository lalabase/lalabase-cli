package ui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

func (m Model) View() tea.View {
	g := m.geometry()
	content := view.Render(m.screen(), g, m.focus, m.widthMethod)
	if m.dialog != nil {
		content = view.Overlay(content, m.dialogScreen(), g, m.widthMethod)
	}
	v := tea.NewView(content)
	v.AltScreen = true
	// The wheel scrolls the object pane. Buttons are reported with it, so
	// the terminal selects text only with its modifier held (Option in
	// iTerm2, Fn in Terminal.app).
	v.MouseMode = tea.MouseModeCellMotion
	// Ground and text of the whole window, the terminal's padding included.
	// Bubble Tea gives the terminal its own back at the end.
	if m.painted {
		v.BackgroundColor, v.ForegroundColor = view.GroundColor, view.TextColor
	}
	return v
}
