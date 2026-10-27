// Package agents keeps what the client knows about the Claude Code sessions
// it started beside a ticket (C9a): one agent per press of x, keyed by an id
// of the client's own (Claude's session id changes with /clear), and its
// state as the session's hooks report it.
//
// Where: <cache root>/<profile>/a/<server>/, beside the launch directories
// (l/, internal/launchdir) and apart from them: an agent outlives a launch
// directory's day. Neither launchdir.Prune nor the disk cache touches it.
// <server> (ServerDir) keeps the agents of one server apart from
// another's: a profile pointed at a staging copy of a database must not
// show the first server's agents at tickets with the same ids.
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
//     write back would otherwise lose one of them;
//   - <agent id>.seen, written once by the client (C9b, MarkSeen, created
//     exclusively) after the person opened the agent: it is seen. The
//     hook never reads or writes it.
//
// Each file has one writer. The title of a ticket is never in them: the
// interface takes it from the session (the cache, or a read of the
// ticket).
//
// Threat model: internal/privatefs's, the one of the config (another local
// user without root must not forge a state, a launch, a seen mark or a
// hook). The directory is the user's alone (0700, privatefs.CheckDirs on
// the whole way), the files are 0600 and read as private files
// (privatefs.OwnFile) through internal/safefile, at most maxFile bytes.
// The hook never makes the directory: the launch does, and a hook that
// finds none, or one that fails a check, writes nothing.
//
// A hook's input is foreign: it carries the person's prompt and paths. Hook
// takes six values of it, each compared to a fixed text or checked
// against a pattern, and nothing of it is shown: the session id (a UUID),
// whether a tool event carries a subagent's agent_id (not empty),
// whether SessionStart's source is "clear", "resume", "fork" or "compact",
// whether SessionEnd's reason is one of the first three, whether a
// PermissionRequest's
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
	"crypto/sha256"
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
	seenSuffix   = ".seen"
	tempPrefix   = ".tmp-"
)

// FormatVersion is the version of the files' JSON.
const FormatVersion = 1

// maxFile bounds a file of an agent: each is a few hundred bytes.
const maxFile = 4 << 10

// maxWalk bounds the directory entries a read visits.
const maxWalk = 4096

// idForm is an agent's id and a session id: a UUID, written by the client
// in lower case.
var idForm = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// serverID is an id from the server or from Claude Code: a UUID in either
// case.
var serverID = regexp.MustCompile(`^[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}$`)

// subagentID is a subagent's agent_id from Claude Code ("a" and 16 hex
// digits in 2.1.310), taken only in this form: it is compared, never shown.
var subagentID = regexp.MustCompile(`^[0-9A-Za-z_-]{1,64}$`)

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

// ServerDir is the directory of one server's agents in dir, the
// profile's agents directory: 16 hex digits of the SHA-256 of the
// server's normalised address (cache.NormalServer), so the address itself
// is no name on disk.
func ServerDir(dir, server string) string {
	sum := sha256.Sum256([]byte(server))
	return filepath.Join(dir, hex.EncodeToString(sum[:8]))
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
