package agents

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gboyke/lalabase-cli/internal/privatefs"
)

// The agents directory is cleaned in two ways (C9c): the person removes an
// ended or lost agent with d (Remove), and every run of the client
// removes what nobody needs any more before it reads the agents (Prune).
// Without the second the directory only grows, about four files per
// agent, until Read refuses it (maxWalk) and no agent shows at all.

const (
	// keepEnded is how long an ended or lost agent stays after its last
	// state before Prune removes it, seen or not (owner, 2026-10-28):
	// long enough to come back to it, short enough that the directory
	// stays far below maxWalk without d.
	keepEnded = 30 * 24 * time.Hour
	// strayAfter is the age at which a leftover goes: a temporary file of
	// a hook, the files of an agent without its launch record, a launch
	// record that fails its check. Younger ones may still be written: a
	// hook has 5 s, the launch writes its record before claude starts.
	strayAfter = time.Hour
	// maxPruneWalk bounds the entries Prune visits: far more than Read's
	// maxWalk, so that a directory Read refuses can still be cleaned, and
	// still a bound, so that a directory another process fills cannot
	// keep the client from starting.
	maxPruneWalk = 1 << 16
)

// agentSuffixes are the endings of an agent's files, its launch record
// first: Remove takes them in this order, so that a reader coming between
// finds no agent rather than half of one.
var agentSuffixes = []string{launchSuffix, stateSuffix, seenSuffix, lockSuffix}

// Remove removes the agent id's files from dir: its launch record, its
// state, its seen mark, its lock. A file already gone is no error. It does
// not ask the agent's state: that is the caller's decision (d on an ended
// or lost agent, Prune on an old one). A hook of the agent that still
// runs may write its state again; Prune takes that leftover later.
func Remove(dir, id string) error {
	if !ValidID(id) {
		return errors.New("no agent id")
	}
	if err := existing(dir); err != nil {
		return err
	}
	for _, suffix := range agentSuffixes {
		if err := removeGone(filepath.Join(dir, id+suffix)); err != nil {
			return err
		}
	}
	return nil
}

// Prune cleans dir at the start of a run: an ended or lost agent whose
// last state is older than keepEnded, and every leftover older than
// strayAfter (a hook's temporary file, an agent's files without its launch
// record, an agent whose launch record fails its check). Every other
// agent stays, whatever its age; names of another form are left alone. A
// missing directory has nothing to prune; procs tells a lost agent
// (System when nil).
func Prune(dir string, now time.Time, procs Procs) error {
	if !filepath.IsAbs(dir) {
		return nil
	}
	if procs == nil {
		procs = System
	}
	if err := privatefs.CheckDirs(dir); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	// The files of each agent by its id: whether its launch record is
	// there, and the newest of their times.
	type agentFiles struct {
		record bool
		newest time.Time
	}
	found := map[string]*agentFiles{}
	err := privatefs.Walk(dir, maxPruneWalk, func(e fs.DirEntry) error {
		name := e.Name()
		info, err := e.Info()
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if strings.HasPrefix(name, tempPrefix) {
			if now.Sub(info.ModTime()) > strayAfter {
				return removeGone(filepath.Join(dir, name))
			}
			return nil
		}
		id, suffix, ok := agentFile(name)
		if !ok {
			return nil
		}
		f := found[id]
		if f == nil {
			f = &agentFiles{}
			found[id] = f
		}
		f.record = f.record || suffix == launchSuffix
		if info.ModTime().After(f.newest) {
			f.newest = info.ModTime()
		}
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for id, f := range found {
		if gone(dir, id, f.record, f.newest, now, procs) {
			if err := Remove(dir, id); err != nil {
				return err
			}
		}
	}
	return nil
}

// gone decides Prune's question for one agent: a leftover once all its
// files are older than strayAfter; an agent once it ended or was lost
// longer than keepEnded ago.
func gone(dir, id string, record bool, newest, now time.Time, procs Procs) bool {
	if !record {
		return now.Sub(newest) > strayAfter
	}
	a, ok := readAgent(dir, id, procs)
	if !ok {
		// The launch record fails its check: Read shows nothing of it.
		return now.Sub(newest) > strayAfter
	}
	return (a.State == Ended || a.State == Lost) && now.Sub(a.Since) > keepEnded
}

// agentFile splits name into an agent's id and one of agentSuffixes; ok is
// false for every other name.
func agentFile(name string) (id, suffix string, ok bool) {
	for _, suffix := range agentSuffixes {
		if id, found := strings.CutSuffix(name, suffix); found && ValidID(id) {
			return id, suffix, true
		}
	}
	return "", "", false
}

// removeGone removes path; one already gone is no error.
func removeGone(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
