package ui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/ui/view"
	"github.com/gboyke/lalabase-cli/internal/worker"
)

// Model is the one root model. It owns every piece of state; the Bubbles
// widgets, if any, keep widget state only. It stays comparable (the C1a
// tests compare models), so the session's state lives behind one pointer.
type Model struct {
	width, height int
	focus         view.Pane
	mode          Mode
	widthMethod   ansi.Method
	// shell is the placeholder content (organisation, list, object), shown
	// without a session; it becomes a screen at render time.
	shell      [3]string
	supervisor *worker.Supervisor
	// app is the session: startup, organisations, counters and the inbox.
	// nil for the shell alone (C1a, and the tests of the shell).
	app *app
}

// Option configures the root model at construction.
type Option func(*Model)

// WithSupervisor lets the model quit on the supervisor's first fatal error.
func WithSupervisor(s *worker.Supervisor) Option { return func(m *Model) { m.supervisor = s } }

// Session is what the interface needs to talk to a server: the startup
// commands, the inbox gateway, the session's mailbox and the profile the
// cache namespace is named after. The command constructs all of them.
type Session struct {
	Startup *gateway.Startup
	Gateway *gateway.Gateway
	Events  *gateway.Events
	// Profile and Server name the namespace of the cache, with the token id
	// /me confirms.
	Profile string
	Server  string
}

// WithSession gives the model a session to run.
func WithSession(s Session) Option { return func(m *Model) { m.app = newApp(s) } }

// WithClock replaces the clock (tests).
func WithClock(now func() time.Time) Option {
	return func(m *Model) {
		if m.app != nil {
			m.app.now = now
		}
	}
}

func New(opts ...Option) Model {
	m := Model{focus: view.List, widthMethod: ansi.WcWidth, shell: placeholder()}
	for _, opt := range opts {
		opt(&m)
	}
	if m.app != nil {
		m.app.base = context.Background()
		if m.supervisor != nil {
			m.app.base = m.supervisor.Context()
		}
	}
	return m
}

// NewWithContent uses the same display boundary as the built-in placeholder.
func NewWithContent(organisation, list, object string) Model {
	m := New()
	m.shell = [3]string{organisation, list, object}
	return m
}

// Init starts the watch on the supervisor, the session's mailbox and the
// first read, /me. Nothing else is asked before /me succeeded.
func (m Model) Init() tea.Cmd {
	var cmds []tea.Cmd
	if m.supervisor != nil {
		cmds = append(cmds, watchFatal(m.supervisor.Failed(), m.supervisor.Context().Done()))
	}
	if m.app != nil {
		cmds = append(cmds, m.app.wait(), m.app.startStep(stepMe))
	}
	return tea.Batch(cmds...)
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
