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
	// keepEnded is how long an agent that ended, is lost or never got past
	// starting stays after the last change of its files before Prune
	// removes it, seen or not (owner, 2026-10-28): long enough to come
	// back to it, short enough that the directory stays far below maxWalk
	// without d. The last change is the last hook, the end, the open
	// (the seen mark): the client cannot know when a lost process went,
	// so an agent that waited silently for 30 days and then went is
	// removed at the next start without having been shown as lost.
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
// first: Remove takes them in this order, so that a read that starts after
// the record went finds no agent. A read that took the record just before
// may still show the agent once, as starting; the interface drops it
// (receiveRemoved).
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

// Prune cleans dir at the start of a run: an agent that ended, is lost or
// never got past starting once its files did not change for keepEnded,
// and every leftover older than strayAfter (a hook's temporary file, an
// agent's files without its launch record, an agent whose launch record
// fails its check). An agent that works or waits stays, whatever its age,
// and so does one whose launch record is of a newer format (another
// client's); names of another form are left alone. One file that cannot
// be removed does not stop the rest: its error is returned after. A
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
	var failed []error
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
				failed = append(failed, removeGone(filepath.Join(dir, name)))
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
			failed = append(failed, Remove(dir, id))
		}
	}
	return errors.Join(failed...)
}

// gone decides Prune's question for one agent, by the last change of its
// files (newest): a leftover once that is older than strayAfter; an agent
// that ended, is lost or never got past starting once it is older than
// keepEnded.
func gone(dir, id string, record bool, newest, now time.Time, procs Procs) bool {
	if !record {
		return now.Sub(newest) > strayAfter
	}
	if newerFormat(dir, id) {
		return false
	}
	a, ok := readAgent(dir, id, procs)
	if !ok {
		// The launch record fails its check: Read shows nothing of it.
		return now.Sub(newest) > strayAfter
	}
	switch a.State {
	case Ended, Lost, Starting:
		return now.Sub(newest) > keepEnded
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
