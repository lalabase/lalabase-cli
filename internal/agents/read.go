package agents

import (
	"errors"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/gboyke/lalabase-cli/internal/privatefs"
)

// Agent is one agent as the interface shows it: its address, and what it
// does since when. The values passed their checks; none of them is text
// to show but the number.
type Agent struct {
	ID                            string
	Organisation, Project, Ticket string
	Number                        int
	Pane, Session                 string
	State                         State
	// Since is when the state began: the hook's time of the change, or
	// the launch's time while the agent starts. For a lost agent it is the
	// last state its hooks recorded: when the process went is not known.
	Since time.Time
	// Seen: the person opened the agent from the client once (C9b).
	Seen bool
	// Resumable: a hook recorded the session, so Session names a
	// conversation claude --resume finds (C9c). Before the first hook the
	// launch's id may never have been used (Esc on the trust dialog).
	Resumable bool
}

// Read reads the agents in dir: every launch record that passes its
// checks, with its state. A missing directory holds none. An agent whose
// state file is missing, or fails a check, is starting; one whose process
// is gone, or whose pid now names a process with another start time, is
// lost. One with a seen file of its own is seen. The agents are ordered by
// when their state began, the oldest first.
func Read(dir string, procs Procs) ([]Agent, error) {
	if !filepath.IsAbs(dir) {
		return nil, nil
	}
	if err := privatefs.CheckDirs(dir); err != nil {
		return nil, err
	}
	var ids []string
	err := privatefs.Walk(dir, maxWalk, func(e fs.DirEntry) error {
		if id, ok := strings.CutSuffix(e.Name(), launchSuffix); ok && ValidID(id) {
			ids = append(ids, id)
		}
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Agent
	for _, id := range ids {
		if a, ok := readAgent(dir, id, procs); ok {
			out = append(out, a)
		}
	}
	slices.SortStableFunc(out, func(a, b Agent) int { return a.Since.Compare(b.Since) })
	return out, nil
}

// readAgent reads one agent's launch record and state; ok is false when
// the launch record fails a check.
func readAgent(dir, id string, procs Procs) (Agent, bool) {
	path := filepath.Join(dir, id+launchSuffix)
	var r launchRecord
	if err := readOwn(path, &r); err != nil || r.Format != FormatVersion || r.Agent != id {
		return Agent{}, false
	}
	l := r.launch()
	if !l.valid() {
		return Agent{}, false
	}
	a := Agent{ID: id, Organisation: l.Organisation, Project: l.Project, Ticket: l.Ticket, Number: l.Number,
		Pane: l.Pane, Session: l.Session, State: Starting, Since: l.At, Seen: seen(dir, id)}
	s, err := readState(filepath.Join(dir, id+stateSuffix))
	if err != nil {
		return a, true
	}
	a.State, _ = stateNamed(s.State)
	a.Since = s.Changed
	if s.Session != "" {
		a.Session = s.Session
	}
	// A hook ran in the session: the one of its input, else the one
	// before, else the launch's. Of the states only the client's own lost
	// mark (MarkLost) comes without one.
	a.Resumable = a.State != Lost
	if a.State != Ended && s.PID > 0 && procs != nil {
		start, alive, err := procs.Start(s.PID)
		if err == nil && (!alive || start != s.Start) {
			a.State = Lost
		}
	}
	return a, true
}
