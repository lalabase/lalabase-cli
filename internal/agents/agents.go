// Package agents keeps what the client knows about the Claude Code sessions
// it started beside a ticket (C9a): one agent per press of x, keyed by an id
// of the client's own (Claude's session id changes with /clear), and its
// state as the session's hooks report it.
//
// Where: <cache root>/<profile>/a/, beside the launch directories (l/,
// internal/launchdir) and apart from them: an agent outlives a launch
// directory's day. Neither launchdir.Prune nor the disk cache touches it.
// Per agent:
//
//   - <agent id>.launch.json, written once by the launch (created
//     exclusively): the ticket's address, the session id it started with,
//     the pane, the time;
//   - <agent id>.state.json, written by `lalabase hook` (Hook): the state,
//     the time it changed, Claude's latest session id, the pid and its start
//     time; replaced whole (a temporary file in the directory, synced,
//     renamed), so a reader sees the old state or the new one;
//   - <agent id>.lock, the hook's lock: hooks of one agent may overlap (a
//     Stop and the tools of a background agent), and a read, change and
//     write back would otherwise lose one of them.
//
// Each file has one writer. The title of a ticket is never in them: it
// comes from the cache.
//
// Threat model: internal/privatefs's, the one of the config (another local
// user without root must not forge a state, a launch or a hook). The
// directory is the user's alone (0700, privatefs.CheckDirs on the whole
// way), the files are 0600 and read as private files (privatefs.OwnFile)
// through internal/safefile, at most maxFile bytes. The hook never makes the
// directory: the launch does, and a hook that finds none, or one that fails
// a check, writes nothing.
//
// A hook's input is foreign: it carries the person's prompt and paths. Hook
// takes four values of it, each compared to a fixed text or checked
// against a pattern, and nothing of it is shown: the session id (a UUID),
// whether SessionEnd's reason is "clear", whether a PermissionRequest's
// tool is AskUserQuestion, and how many background tasks run. The event
// comes from the hook's own command line (the settings file the launch
// wrote), the pid from CLAUDE_PID, checked as a positive number. The
// environment also holds a messaging token, which is never read.
//
// Reading (Read) is bounded: at most maxWalk directory entries, at most
// maxFile bytes a file. A file that fails a check is the failure of its
// agent alone, never of the others.
package agents

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"regexp"

	"github.com/gboyke/lalabase-cli/internal/privatefs"
)

// Dir is the name of the agents directory in a profile's directory.
const Dir = "a"

// The names of an agent's files: its id, then one of these.
const (
	launchSuffix = ".launch.json"
	stateSuffix  = ".state.json"
	lockSuffix   = ".lock"
	tempPrefix   = ".tmp-"
)

// FormatVersion is the version of both files' JSON.
const FormatVersion = 1

// maxFile bounds a file of an agent: both are a few hundred bytes.
const maxFile = 4 << 10

// maxWalk bounds the directory entries a read visits.
const maxWalk = 4096

// idForm is an agent's id and a session id: a UUID, written by the client
// in lower case.
var idForm = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// serverID is an id from the server or from Claude Code: a UUID in either
// case.
var serverID = regexp.MustCompile(`^[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}$`)

// NewID is a random UUID (version 4): an agent's id, or the session id a
// launch hands Claude Code.
func NewID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32], nil
}

// ValidID says whether s has the form of an agent's id.
func ValidID(s string) bool { return idForm.MatchString(s) }

// Prepare makes the agents directory (0700) when it is missing and checks
// it and the way to it. The launch calls it before it hands Claude Code a
// hook that writes there.
func Prepare(dir string) error {
	if !filepath.IsAbs(dir) {
		return errors.New("no directory for the agents: the cache directory is unknown")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return privatefs.CheckDirs(dir)
}

// existing checks a directory that must be there already: the hook never
// makes it, and privatefs.CheckDirs passes a missing one.
func existing(dir string) error {
	if !filepath.IsAbs(dir) {
		return errors.New("the agents directory is no absolute path")
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("the agents directory is no directory")
	}
	return privatefs.CheckDirs(dir)
}
