package ui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
)

type Model struct {
	width, height int
	focus         view.Pane
	mode          Mode
	widthMethod   ansi.Method
	content       present.Shell
}

func New() Model { return Model{focus: view.List, widthMethod: ansi.WcWidth, content: placeholder()} }

// NewWithContent uses the same display boundary as the built-in placeholder.
func NewWithContent(organisation, list, object string) Model {
	m := New()
	m.content = presentation(organisation, list, object)
	return m
}

func (Model) Init() tea.Cmd { return nil }
