package ui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
	"github.com/gboyke/lalabase-cli/internal/worker"
)

type Model struct {
	width, height int
	focus         view.Pane
	mode          Mode
	widthMethod   ansi.Method
	content       present.Shell
	supervisor    *worker.Supervisor
}

// Option configures the root model at construction.
type Option func(*Model)

// WithSupervisor lets the model quit on the supervisor's first fatal error.
func WithSupervisor(s *worker.Supervisor) Option { return func(m *Model) { m.supervisor = s } }

func New(opts ...Option) Model {
	m := Model{focus: view.List, widthMethod: ansi.WcWidth, content: placeholder()}
	for _, opt := range opts {
		opt(&m)
	}
	return m
}

// NewWithContent uses the same display boundary as the built-in placeholder.
func NewWithContent(organisation, list, object string) Model {
	m := New()
	m.content = presentation(organisation, list, object)
	return m
}

func (m Model) Init() tea.Cmd {
	if m.supervisor == nil {
		return nil
	}
	return watchFatal(m.supervisor.Failed(), m.supervisor.Context().Done())
}

// watchFatal turns the first fatal error into a message. A fatal error also
// ends the lifetime, so an ended lifetime asks Failed once more.
func watchFatal(failed, ended <-chan struct{}) tea.Cmd {
	return func() tea.Msg {
		select {
		case <-failed:
			return worker.Fatal{}
		case <-ended:
			select {
			case <-failed:
				return worker.Fatal{}
			default:
				return nil
			}
		}
	}
}
