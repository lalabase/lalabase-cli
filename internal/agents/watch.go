package agents

import (
	"errors"
	"io/fs"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/worker"
)

// Watcher reads the agents of one profile for the interface, under the
// session's supervisor.
type Watcher struct {
	sup   *worker.Supervisor
	dir   string
	procs Procs
}

// NewWatcher reads dir ("" for none: no agents) with the system's
// processes.
func NewWatcher(sup *worker.Supervisor, dir string) *Watcher {
	return &Watcher{sup: sup, dir: dir, procs: System}
}

// Snapshot is the message of one read: the agents, or why there are none.
type Snapshot struct {
	Ref    uint64
	Agents []Agent
	Err    error
}

// Read is the command of one read. It runs under the supervisor on the
// command's own goroutine: a panic is the session's fatal error. It ends
// without a message when the session ended first.
func (w *Watcher) Read(ref uint64) tea.Cmd {
	return func() tea.Msg {
		msg, ok := worker.Run(w.sup, "agents: read", func() tea.Msg {
			agents, err := Read(w.dir, w.procs)
			return Snapshot{Ref: ref, Agents: agents, Err: err}
		})
		if !ok {
			if w.sup.Err() != nil {
				return worker.Fatal{}
			}
			return nil
		}
		return msg
	}
}

// Marked is the message of one MarkSeen: the agent, and why it is not
// recorded (nil when it is).
type Marked struct {
	ID  string
	Err error
}

// See is the command that records the agent id as seen (MarkSeen), under
// the supervisor like Read; at is the model's time of the open.
func (w *Watcher) See(id string, at time.Time) tea.Cmd {
	return func() tea.Msg {
		msg, ok := worker.Run(w.sup, "agents: seen", func() tea.Msg {
			return Marked{ID: id, Err: withoutPath(MarkSeen(w.dir, id, at))}
		})
		if !ok {
			if w.sup.Err() != nil {
				return worker.Fatal{}
			}
			return nil
		}
		return msg
	}
}

// Removed is the message of one Remove: the agent, and why it is still
// there (nil when it is gone).
type Removed struct {
	ID  string
	Err error
}

// Remove is the command that removes the agent id's files (d on an ended
// or lost agent), under the supervisor like Read.
func (w *Watcher) Remove(id string) tea.Cmd {
	return func() tea.Msg {
		msg, ok := worker.Run(w.sup, "agents: remove", func() tea.Msg {
			return Removed{ID: id, Err: withoutPath(Remove(w.dir, id))}
		})
		if !ok {
			if w.sup.Err() != nil {
				return worker.Fatal{}
			}
			return nil
		}
		return msg
	}
}

// withoutPath is err for the status line: of a file operation the reason
// alone (permission denied), not the file's path, which would push the
// reason out of a narrow line; any other error as it is.
func withoutPath(err error) error {
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err
	}
	return err
}
