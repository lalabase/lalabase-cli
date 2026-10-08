package ui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

func (m Model) View() tea.View {
	v := tea.NewView(view.Render(m.content, layout(m.width, m.height), m.focus, m.widthMethod))
	v.AltScreen = true
	return v
}
