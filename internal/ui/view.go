package ui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

func (m Model) View() tea.View {
	v := tea.NewView(view.Render(m.screen(), m.geometry(), m.focus, m.widthMethod))
	v.AltScreen = true
	// The wheel scrolls the object pane. Buttons are reported with it, so
	// the terminal selects text only with its modifier held (Option in
	// iTerm2, Fn in Terminal.app).
	v.MouseMode = tea.MouseModeCellMotion
	return v
}
