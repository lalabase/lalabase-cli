package ui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/gboyke/lalabase-cli/internal/diskcache"
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
	// agentsFrom is the pane the focus was on when A opened the agents
	// (C9b): esc and a second A go back to it.
	agentsFrom  view.Pane
	mode        Mode
	widthMethod ansi.Method
	// shell is the placeholder content (organisation, list, object), shown
	// without a session; it becomes a screen at render time.
	shell      [3]string
	supervisor *worker.Supervisor
	// app is the session: startup, organisations, counters and the inbox.
	// nil for the shell alone (C1a, and the tests of the shell).
	app *app
	// dialog is the box over the screen, nil without one (M1); dialogs the
	// number of the last one opened.
	dialog  *dialog
	dialogs uint64
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
	// Disk opens the disk cache of the profile after /me confirmed the
	// identity (C1e); nil for memory alone.
	Disk *diskcache.Opener
	// Launcher starts Claude Code beside the ticket shown (C7); nil
	// offers no x.
	Launcher Launcher
	// Agents reads what the sessions x started do (C9a); nil shows none.
	Agents AgentWatcher
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

// WithLocation replaces the zone times are shown in (tests).
func WithLocation(loc *time.Location) Option {
	return func(m *Model) {
		if m.app != nil {
			m.app.loc = loc
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
		// A session starts in the chooser: the first thing to do is to pick
		// an organisation, and in two columns the chooser shows only then.
		m.focus = view.Navigation
	}
	return m
}

// NewWithContent uses the same display boundary as the built-in placeholder.
func NewWithContent(organisation, list, object string, opts ...Option) Model {
	m := New(opts...)
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
		// The agents are the profile's, not the server's: read at once,
		// whatever /me answers.
		cmds = append(cmds, m.app.wait(), m.app.startStep(stepMe), m.app.readAgents())
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
