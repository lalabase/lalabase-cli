package agents

import (
	"errors"
	"path/filepath"
	"time"
)

// A starting agent has no state and no pid (C9a): before its first hook
// the reader cannot tell one that is about to start from one that never
// will. The client can, in two cases (C9c): the jump finds its pane gone
// (iTerm's 4201), or claude ran in this terminal and ended without any
// hook (Esc on the folder's trust dialog). It then writes the state lost
// itself (MarkLost), so the agent stops showing as starting. The file
// stays format 1: a client from before reads the new name as a state that
// fails its check, so as starting, which is what it showed before.

// MarkLost records the agent id in dir as lost at at, under the agent's
// lock, when it has a launch record and no state yet; lost says whether
// it wrote. A state that is there, written by a hook before the lock was
// taken, stays: the session started after all. A hook after the mark
// replaces it, as it replaces every state.
func MarkLost(dir, id string, at time.Time) (lost bool, err error) {
	if !ValidID(id) {
		return false, errors.New("no agent id")
	}
	if at.IsZero() {
		return false, errors.New("no time")
	}
	if err := existing(dir); err != nil {
		return false, err
	}
	unlock, err := lock(filepath.Join(dir, id+lockSuffix), lockFor)
	if err != nil {
		return false, err
	}
	defer unlock()
	if !recorded(dir, id) {
		// No agent to mark: a state alone would be a leftover.
		return false, nil
	}
	path := filepath.Join(dir, id+stateSuffix)
	if _, err := readState(path); err == nil {
		return false, nil
	}
	rec := stateRecord{Format: FormatVersion, State: stateNames[Lost], Changed: at.UTC()}
	if err := replace(dir, path, rec); err != nil {
		return false, err
	}
	return true, nil
}

// recorded says whether the agent id has a launch record that passes its
// checks, as Read would list it.
func recorded(dir, id string) bool {
	var r launchRecord
	if err := readOwn(filepath.Join(dir, id+launchSuffix), &r); err != nil || r.Format != FormatVersion || r.Agent != id {
		return false
	}
	return r.launch().valid()
}

// After is what became of a start the client made, for Settle: the agent
// it is about (the new one, or the one whose pane the jump did not find),
// the agent a start again replaces ("" for a first start or a jump), and
// whether a pane opened for the new one (iTerm), so the start went
// through whatever the agent's state is.
type After struct {
	Agent, Replaces string
	Opened          bool
}

// Settle does what follows a start, as the agents' files are now:
//   - unless a pane opened, an agent without a state is lost (MarkLost): its
//     pane is gone, or its claude ended here before any hook;
//   - a start again replaces the old agent once it went through: a pane
//     opened, or a hook of the new agent came. One that never started
//     leaves the old agent and goes itself, so nothing half is left.
//
// lost says whether the agent was marked lost. Each removal decides under
// the agent's lock again (Remove): an old agent that runs again stays,
// and the error says so (ErrRunning).
func Settle(dir string, s After, at time.Time, procs Procs) (lost bool, err error) {
	if !s.Opened {
		if lost, err = MarkLost(dir, s.Agent, at); err != nil {
			return false, err
		}
	}
	switch {
	case s.Replaces == "":
		return lost, nil
	case lost:
		// The start again never began: the new agent goes, the old one
		// keeps the conversation it had.
		return true, Remove(dir, s.Agent, at, procs)
	}
	return false, Remove(dir, s.Replaces, at, procs)
}
