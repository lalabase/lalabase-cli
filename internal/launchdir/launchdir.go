// Package launchdir keeps the files of one launch of Claude Code beside a
// ticket (C7b): one directory per press, <cache root>/<profile>/l/<16 hex>/,
// and in it the MCP configuration that hands Claude Code the profile's
// server (MCPConfig) and the script the new iTerm pane runs (Script).
//
// Threat model: internal/privatefs's, the one of the config (another local
// user without root). The token in MCPConfig is the one config.toml holds
// in plain text already; it never goes into a command line, where ps shows
// it to every local user. The way to the root is checked from / down
// (privatefs.CheckDirs), the launch directory is new (Mkdir, 0700, a random
// name) and checked the same way, and every file in it is created
// exclusively (O_EXCL: nothing of that name, a link neither, may be there
// before), given its mode, synced and checked as a private file. The path
// is handed over only once the file is whole, so no rename is needed.
//
// Lifetime: Claude Code reads its MCP configuration once, at start
// (checked with Claude Code 2.1.310, the plan's C7b section). A launch
// directory stays MaxAge after its last change and is then removed by
// Prune, which runs before every launch and when the client quits; a
// younger one is never touched, its pane may still be starting. Prune
// removes only names of its own: in the root a directory of the id's form
// (a name of that form that is no directory is removed by its name, never
// followed), in it the files of this package. A directory that still holds
// anything else stays.
package launchdir

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/gboyke/lalabase-cli/internal/privatefs"
)

// The files of a launch directory.
const (
	// MCPConfig is the file of claude's --mcp-config: the one server.
	MCPConfig = "m"
	// Script is what the iTerm pane runs: cd and exec claude.
	Script = "run"
)

// files are every name Prune removes from a launch directory.
var files = []string{MCPConfig, Script}

// MaxAge is how long a launch directory stays after its last change.
const MaxAge = 24 * time.Hour

// maxWalk bounds the entries Prune visits in the root.
const maxWalk = 4096

// idForm is the name of a launch directory: 8 random bytes in hex.
var idForm = regexp.MustCompile(`^[0-9a-f]{16}$`)

// Dir is one launch directory: its path, and the random id that names it.
type Dir struct {
	Path, ID string
}

// Make prunes root and makes a new launch directory in it. root is made
// (0700) when missing; it and the way to it must pass privatefs.
func Make(root string, now time.Time) (Dir, error) {
	if !filepath.IsAbs(root) {
		return Dir{}, errors.New("no directory for the launch files: the cache directory is unknown")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return Dir{}, err
	}
	if err := privatefs.CheckDirs(root); err != nil {
		return Dir{}, err
	}
	if err := Prune(root, now); err != nil {
		return Dir{}, err
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return Dir{}, err
	}
	id := hex.EncodeToString(b[:])
	path := filepath.Join(root, id)
	if err := os.Mkdir(path, 0o700); err != nil {
		return Dir{}, err
	}
	if err := privatefs.CheckDirs(path); err != nil {
		return Dir{}, err
	}
	return Dir{Path: path, ID: id}, nil
}

// Write creates the file name in the directory with data and mode (the
// owner's bits alone count) and returns its path. A file that cannot be
// written whole is removed.
func (d Dir) Write(name string, data []byte, mode fs.FileMode) (path string, err error) {
	path = filepath.Join(d.Path, name)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // a new file in the launch directory, checked by privatefs
	if err != nil {
		return "", err
	}
	// Capture the created path: an error return clears the named result.
	defer func(createdPath string) {
		if err != nil {
			_ = f.Close()
			_ = os.Remove(createdPath)
		}
	}(path)
	// Set, not left to the umask: the script must be executable.
	if err := f.Chmod(mode & 0o700); err != nil {
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		return "", err
	}
	if err := f.Sync(); err != nil {
		return "", err
	}
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if err := privatefs.OwnFile(path, info); err != nil {
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return path, nil
}

// Prune removes the launch directories in root older than MaxAge, or
// stamped more than a minute in the future (an implausible time keeps
// nothing). A missing root has nothing to prune.
func Prune(root string, now time.Time) error {
	if !filepath.IsAbs(root) {
		return nil
	}
	if err := privatefs.CheckDirs(root); err != nil {
		return err
	}
	d, err := os.Open(root) //nolint:gosec // the launch root, checked by privatefs
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	visited := 0
	for {
		entries, err := d.ReadDir(256)
		for _, e := range entries {
			if visited++; visited > maxWalk {
				return fmt.Errorf("%s holds too many entries", root)
			}
			if err := prune1(root, e, now); err != nil {
				return err
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// prune1 removes one entry of the root when it is an expired launch
// directory, or a name of that form that is no directory.
func prune1(root string, e fs.DirEntry, now time.Time) error {
	if !idForm.MatchString(e.Name()) {
		return nil // not ours: left alone
	}
	path := filepath.Join(root, e.Name())
	info, err := e.Info()
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return remove(path)
	}
	if age := now.Sub(info.ModTime()); age <= MaxAge && age >= -time.Minute {
		return nil
	}
	for _, name := range files {
		if err := remove(filepath.Join(path, name)); err != nil {
			return err
		}
	}
	return remove(path)
}

// remove removes path by its name, never following it. A directory that
// still holds something stays (ErrExist is ENOTEMPTY too).
func remove(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, fs.ErrExist) {
		return err
	}
	return nil
}
