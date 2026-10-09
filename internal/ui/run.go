package ui

import (
	"context"
	"io"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/worker"
)

// Run runs the interface. A panic in the model's Init, Update or View is
// recovered here, under the supervisor: it becomes the session's fatal
// error (the command prints its one fixed line and exits 1), never a panic
// value and a stack on the terminal, which Bubble Tea's own recovery would
// print and which may carry foreign text. Without a supervisor the model
// runs as it is.
func Run(ctx context.Context, in io.Reader, out io.Writer, environment []string, model tea.Model, sup *worker.Supervisor) error {
	_, err := runProgram(ctx, in, out, environment, guarded{model: model, sup: sup})
	return err
}

func runProgram(ctx context.Context, in io.Reader, out io.Writer, environment []string, model tea.Model) (tea.Model, error) {
	return tea.NewProgram(model, tea.WithContext(ctx), tea.WithInput(in), tea.WithOutput(out), tea.WithEnvironment(environment)).Run()
}

// guarded recovers a panic of the model it wraps where it happens.
type guarded struct {
	model tea.Model
	sup   *worker.Supervisor
}

// Init starts the model and, under a supervisor, the watch on its first
// fatal error: a panic in View has no command to quit with, the watch
// quits for it.
func (g guarded) Init() tea.Cmd {
	if g.sup == nil {
		return g.model.Init()
	}
	cmd, ok := worker.Run(g.sup, "ui: init", g.model.Init)
	if !ok {
		return tea.Quit
	}
	return tea.Batch(g.guard(cmd), watchFatal(g.sup.Failed(), g.sup.Context().Done()))
}

// guard runs a command of the model under the supervisor as well: Bubble
// Tea runs it on a goroutine of its own, outside Update, and would print
// its panic. A batch is guarded command by command.
func (g guarded) guard(cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		msg, ok := worker.Run(g.sup, "ui: command", cmd)
		if !ok {
			return nil
		}
		if batch, isBatch := msg.(tea.BatchMsg); isBatch {
			for i := range batch {
				batch[i] = g.guard(batch[i])
			}
		}
		return msg
	}
}

// stepped is one Update's outcome.
type stepped struct {
	model tea.Model
	cmd   tea.Cmd
}

func (g guarded) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(worker.Fatal); ok {
		return g, tea.Quit
	}
	if g.sup == nil {
		next, cmd := g.model.Update(msg)
		g.model = next
		return g, cmd
	}
	s, ok := worker.Run(g.sup, "ui: update", func() stepped {
		next, cmd := g.model.Update(msg)
		return stepped{model: next, cmd: cmd}
	})
	if !ok {
		return g, tea.Quit
	}
	g.model = s.model
	return g, g.guard(s.cmd)
}

func (g guarded) View() tea.View {
	if g.sup == nil {
		return g.model.View()
	}
	v, ok := worker.Run(g.sup, "ui: view", g.model.View)
	if !ok {
		// The fatal error is recorded; the watch on the supervisor quits.
		return tea.NewView("")
	}
	return v
}
