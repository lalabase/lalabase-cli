package agents

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/gboyke/lalabase-cli/internal/privatefs"
)

// The agents directory is cleaned in two ways (C9c): the person removes an
// agent whose session is over with d (Remove), and every run of the client
// removes what nobody needs any more before it reads the agents (Prune).
// Without the second the directory only grows, about four files per
// agent, until Read refuses it (maxWalk) and no agent shows at all.
//
// Both decide and remove under the agent's lock, the one every hook takes
// (removeIf): the agent is read again there, its files' times too, so a
// hook that wrote since the interface's read or the directory's walk (the
// agent works again, it ended a moment ago) changes the decision instead
// of losing its agent.

const (
	// keepEnded is how long an agent that ended, is lost or never got past
	// starting stays after the last change of its files before Prune
	// removes it, seen or not (owner, 2026-10-28): long enough to come
	// back to it. The last change is the last hook, the end, the open
	// (the seen mark): the client cannot know when a lost process went,
	// so an agent that waited silently for 30 days and then went is
	// removed at the next start without having been shown as lost.
	keepEnded = 30 * 24 * time.Hour
	// stuckStarting is how long an agent may be starting before d may
	// remove it (owner, 2026-10-28): claude's trust dialog may take a
	// while, but one an hour old never got past it (closed, or no pane
	// opened, so no hook ever came).
	stuckStarting = time.Hour
	// strayAfter is the age at which a leftover goes: a temporary file of
	// a hook, the files of an agent without its launch record, a launch
	// record that fails its check. Younger ones may still be written: a
	// hook has 5 s, the launch writes its record before claude starts.
	strayAfter = time.Hour
	// maxOver is how many agents whose session is over (Removable: ended,
	// lost, stuck starting) Prune keeps, whatever their age (owner,
	// 2026-10-28): of more, those with the oldest last change go first.
	// With about four files each this keeps the directory well below
	// what Read visits (maxWalk), also for someone who starts dozens of
	// agents a day and never presses d.
	maxOver = 500
	// maxPruneWalk bounds the entries Prune visits: far more than Read's
	// maxWalk, so that a directory Read refuses can still be cleaned, and
	// still a bound, so that a directory another process fills cannot
	// keep the client from starting.
	maxPruneWalk = 1 << 16
)

// removeOrder are the endings of an agent's files in the order removeIf
// takes them, its launch record last: a removal that fails part way
// leaves the agent listed, as the error it reports says. A read between
// may show it once as starting (its state gone, its record not yet); the
// interface drops it after a removal that succeeded (withoutGone). The
// lock goes after the lock is let go.
var removeOrder = []string{seenSuffix, stateSuffix, launchSuffix}

// ErrTooManyFiles is Read's error for a directory with more entries than
// a read visits (maxWalk): the interface says so instead of showing no
// agent without a word; the next start's Prune brings it below.
var ErrTooManyFiles = privatefs.ErrTooManyEntries

// ErrRunning is Remove's answer for an agent that is no longer removable:
// a hook wrote since the interface read it, and it works or waits again.
var ErrRunning = errors.New("it is running again")

// Removable says whether d may remove a by now: its session is over
// (ended, lost) or never began (starting for more than stuckStarting).
// The interface offers d by it, and Remove asks it again under the
// agent's lock.
func Removable(a Agent, now time.Time) bool {
	switch a.State {
	case Ended, Lost:
		return true
	case Starting:
		return now.Sub(a.Since) > stuckStarting
	}
	return false
}

// Remove removes the agent id's files from dir (d) if it is still
// Removable by now when its lock is held; ErrRunning when it is not any
// more. An agent already gone is no error.
func Remove(dir, id string, now time.Time, procs Procs) error {
	running := false
	err := removeIf(dir, id, procs, func(c current) bool {
		if !c.listed {
			return c.record // a broken record goes; a missing one is gone
		}
		running = !Removable(c.agent, now)
		return !running
	})
	if err == nil && running {
		return ErrRunning
	}
	return err
}

// current is an agent as removeIf finds it under its lock: whether its
// launch record is there, whether Read would list it (and as what), and
// the last change of any of its files.
type current struct {
	record, listed bool
	agent          Agent
	newest         time.Time
}

// removeIf takes the agent id's lock, finds it as it is now (current) and
// removes its files if remove says so: the hook that would change it waits
// for the lock meanwhile. Taking the lock makes its file where there was
// none; an agent that stays keeps it, as every agent with a hook has, and
// it goes with the agent. procs tells a lost agent (System when nil).
func removeIf(dir, id string, procs Procs, remove func(current) bool) error {
	if !ValidID(id) {
		return errors.New("no agent id")
	}
	if procs == nil {
		procs = System
	}
	if err := existing(dir); err != nil {
		return err
	}
	lockPath := filepath.Join(dir, id+lockSuffix)
	unlock, err := lock(lockPath, lockFor)
	if err != nil {
		return err
	}
	c, err := find(dir, id, procs)
	if err != nil || !remove(c) {
		unlock()
		if err == nil && c.newest.IsZero() {
			// Nothing of the agent is left but the lock, which taking
			// it may just have made.
			return removeGone(lockPath)
		}
		return err
	}
	for _, suffix := range removeOrder {
		if err := removeGone(filepath.Join(dir, id+suffix)); err != nil {
			unlock()
			return err
		}
	}
	unlock()
	// A hook that waited for the lock writes a state without a record;
	// Prune takes that leftover after an hour.
	return removeGone(lockPath)
}

// find is the agent id as it is now: its files' last change (the lock
// file, which removeIf just opened, not counted) and what Read makes of it.
func find(dir, id string, procs Procs) (current, error) {
	var c current
	for _, suffix := range []string{launchSuffix, stateSuffix, seenSuffix} {
		info, err := os.Lstat(filepath.Join(dir, id+suffix))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return current{}, err
		}
		c.record = c.record || suffix == launchSuffix
		if info.ModTime().After(c.newest) {
			c.newest = info.ModTime()
		}
	}
	if c.record {
		c.agent, c.listed = readAgent(dir, id, procs)
	}
	return c, nil
}

// Prune cleans dir at the start of a run: an agent that ended, is lost or
// never got past starting once its files did not change for keepEnded,
// and every leftover older than strayAfter (a hook's temporary file, an
// agent's files without its launch record, an agent whose launch record
// fails its check). An agent that works or waits stays, whatever its age,
// and so does one whose launch record is of a newer format (another
// client's); names of another form are left alone. Of the agents whose
// session is over at most maxOver stay, the newest. Each agent is decided
// under its lock, as it is then (removeIf), not as the walk found it. One
// file that cannot be removed does not stop the rest: its error is
// returned after. A missing directory has nothing to prune; procs tells a
// lost agent (System when nil).
func Prune(dir string, now time.Time, procs Procs) error {
	if !filepath.IsAbs(dir) {
		return nil
	}
	if err := privatefs.CheckDirs(dir); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	ids := map[string]bool{}
	var failed []error
	err := privatefs.Walk(dir, maxPruneWalk, func(e fs.DirEntry) error {
		name := e.Name()
		if strings.HasPrefix(name, tempPrefix) {
			info, err := e.Info()
			if err == nil && now.Sub(info.ModTime()) > strayAfter {
				failed = append(failed, removeGone(filepath.Join(dir, name)))
			}
			return nil
		}
		if id, ok := agentFile(name); ok {
			ids[id] = true
		}
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	// The agents whose session is over and that stay by their age, with
	// their last change, for the bound on their number.
	type overAgent struct {
		id     string
		newest time.Time
	}
	var over []overAgent
	for id := range ids {
		failed = append(failed, removeIf(dir, id, procs, func(c current) bool {
			if pruned(dir, id, c, now) {
				return true
			}
			if c.listed && Removable(c.agent, now) {
				over = append(over, overAgent{id, c.newest})
			}
			return false
		}))
	}
	if len(over) > maxOver {
		slices.SortFunc(over, func(x, y overAgent) int {
			if c := x.newest.Compare(y.newest); c != 0 {
				return c
			}
			return strings.Compare(x.id, y.id)
		})
		for _, o := range over[:len(over)-maxOver] {
			failed = append(failed, removeIf(dir, o.id, procs, func(c current) bool { return c.listed && Removable(c.agent, now) }))
		}
	}
	return errors.Join(failed...)
}

// pruned decides Prune's question for one agent as it is now, by the last
// change of its files: a leftover once that is older than strayAfter; an
// agent that ended, is lost or never got past starting once it is older
// than keepEnded.
func pruned(dir, id string, c current, now time.Time) bool {
	age := now.Sub(c.newest)
	switch {
	case !c.record:
		return age > strayAfter
	case newerFormat(dir, id):
		return false
	case !c.listed:
		// The launch record fails its check: Read shows nothing of it.
		return age > strayAfter
	}
	switch c.agent.State {
	case Ended, Lost, Starting:
		return age > keepEnded
	}
	return false
}

// newerFormat says whether the agent's launch record is of a format newer
// than this client's: a newer client on the same profile wrote it, and
// this one cannot tell whether it still runs.
func newerFormat(dir, id string) bool {
	var r struct {
		Format int `json:"format"`
	}
	return readOwn(filepath.Join(dir, id+launchSuffix), &r) == nil && r.Format > FormatVersion
}

// agentFile is the agent's id of one of its files' names; ok is false for
// every other name.
func agentFile(name string) (id string, ok bool) {
	for _, suffix := range []string{launchSuffix, stateSuffix, seenSuffix, lockSuffix} {
		if id, found := strings.CutSuffix(name, suffix); found && ValidID(id) {
			return id, true
		}
	}
	return "", false
}

// removeGone removes path; one already gone is no error.
func removeGone(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
