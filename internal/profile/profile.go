// Package profile chooses the server and token a command runs with.
//
// A profile is a server address and a token, always together: never a URL
// from one source and a token from another. Sources:
//
//   - --profile NAME: that profile of the config file, whole; the
//     environment is ignored.
//   - the interactive mode: LALABASE_URL plus LALABASE_TOKEN (both or
//     neither), else the profile "default".
//   - agent commands: LALABASE_AGENT_URL plus LALABASE_AGENT_TOKEN (both or
//     neither), else the profile "agent". Never "default", never
//     LALABASE_TOKEN: an agent that read an injected comment must not reach
//     the person's token by accident. The agent token must only read
//     (VerifyAgent) unless the person wrote allow_write = true into the file.
//
// The file is ~/.config/lalabase/config.toml (or under $XDG_CONFIG_HOME); it
// holds tokens and is refused when anybody but its owner may read it.
package profile

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"

	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/privatefs"
	"github.com/gboyke/lalabase-cli/internal/safefile"
)

// Mode is who runs the command.
type Mode int

const (
	// Interactive: a person at the full-screen interface.
	Interactive Mode = iota
	// Agent: a command for Claude Code or a script.
	Agent
)

func (m Mode) String() string {
	if m == Agent {
		return "agent"
	}
	return "interactive"
}

// Names of the two profiles the modes fall back on.
const (
	DefaultName = "default"
	AgentName   = "agent"
)

// Entry is one profile as the file stores it.
type Entry struct {
	URL        string `toml:"url"`
	Token      string `toml:"token"`
	AllowWrite bool   `toml:"allow_write,omitempty"`
}

// File is the whole config file.
type File struct {
	Profiles map[string]Entry `toml:"profiles"`
}

// Profile is the server and token a command runs with.
type Profile struct {
	Name       string
	URL        string
	Token      string
	AllowWrite bool
	// Source says where it came from, for messages ("file ...", "env ...").
	Source string
}

// Path is the config file: $XDG_CONFIG_HOME/lalabase/config.toml, else
// ~/.config/lalabase/config.toml (on macOS too: a terminal tool's config is
// looked for there).
func Path(getenv func(string) string, home string) string {
	if dir := getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "lalabase", "config.toml")
	}
	return filepath.Join(home, ".config", "lalabase", "config.toml")
}

// ErrNoFile reports a config file that does not exist yet.
var ErrNoFile = errors.New("no config file")

// maxConfigSize bounds the config file. A larger one is an error, never cut:
// a cut file written back would lose the profiles behind the cut.
const maxConfigSize = 1 << 20

// afterLoad runs inside Update between loading and saving (tests).
var afterLoad func()

// Load reads the config file. Owner and permissions are checked on the file
// that was opened, not on its name: a file swapped between a check and the
// read cannot slip through. It is opened without blocking, so a FIFO in its
// place is refused instead of hanging the open.
func Load(path string) (File, error) {
	if err := dirsOf(filepath.Dir(path)); err != nil {
		return File{}, err
	}
	// The config is never a link: what a link points to lies in a
	// directory nobody checked.
	data, info, err := safefile.ReadRegularNoLink(path, maxConfigSize)
	if errors.Is(err, fs.ErrNotExist) {
		return File{Profiles: map[string]Entry{}}, ErrNoFile
	}
	if err != nil {
		return File{}, err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return File{}, fmt.Errorf("%s holds tokens and others may read or change it (mode %o); run: chmod 600 %s", path, info.Mode().Perm(), path)
	}
	if err := privatefs.CheckOwner(info); err != nil {
		return File{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := privatefs.CheckACL(path, privatefs.Private); err != nil {
		return File{}, err
	}

	var file File
	if _, err := toml.Decode(string(data), &file); err != nil {
		return File{}, fmt.Errorf("%s: %w", path, err)
	}
	if file.Profiles == nil {
		file.Profiles = map[string]Entry{}
	}
	return file, nil
}

// Save writes the config file: into a temporary file created exclusively with
// mode 0600 in the same directory, then renamed over the old one, so no
// reader ever sees half a file and no other user ever sees a token. A config
// path that is a link is refused rather than written through.
func Save(path string, file File) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := dirsOf(dir); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("%s is no regular file; refusing to write through it", path)
	}

	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(file); err != nil {
		return err
	}
	// Load reads at most maxConfigSize: a larger file could not be read back,
	// and every profile in it would be out of reach. The old file stays.
	if buf.Len() > maxConfigSize {
		return fmt.Errorf("%s would grow too large (%d bytes, at most %d); nothing written", path, buf.Len(), maxConfigSize)
	}
	tmp, err := os.CreateTemp(dir, ".config-*.toml") // O_EXCL, mode 0600
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return rename(name, path)
}

// rename replaces the config (tests replace it to fail).
var rename = os.Rename

// dirsOf checks the directories on the way to the config (tests replace it
// to reach the checks of the file itself).
var dirsOf = privatefs.CheckDirs

// Update loads the config file, lets change modify it and saves it, all under
// an exclusive lock on a lock file beside it: two updates at the same time
// (two auth import runs) cannot both load the same state and overwrite each
// other's profile. If change returns an error, nothing is written.
func Update(path string, change func(*File) error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	// The lock file lies in the same directory: whoever could change it
	// could take the lock away.
	if err := dirsOf(filepath.Dir(path)); err != nil {
		return err
	}
	unlock, err := privatefs.Lock(path + ".lock")
	if err != nil {
		return err
	}
	defer unlock()

	file, err := Load(path)
	if err != nil && !errors.Is(err, ErrNoFile) {
		return err
	}
	if afterLoad != nil {
		afterLoad()
	}
	if err := change(&file); err != nil {
		return err
	}
	return Save(path, file)
}

// Resolve chooses the profile for a command.
func Resolve(mode Mode, explicit string, getenv func(string) string, path string) (Profile, error) {
	if explicit != "" {
		return fromFile(path, explicit, "")
	}
	urlVar, tokenVar, name, hint := "LALABASE_URL", "LALABASE_TOKEN", DefaultName, "lalabase auth import" //nolint:gosec // names of environment variables, no credentials
	if mode == Agent {
		urlVar, tokenVar, name, hint = "LALABASE_AGENT_URL", "LALABASE_AGENT_TOKEN", AgentName, "lalabase auth import --agent" //nolint:gosec // names of environment variables, no credentials
	}
	url, token := getenv(urlVar), getenv(tokenVar)
	switch {
	case url != "" && token != "":
		return Profile{Name: "env", URL: url, Token: token, Source: "env " + urlVar + " and " + tokenVar}, nil
	case url != "" || token != "":
		return Profile{}, fmt.Errorf("set %s and %s together, or neither", urlVar, tokenVar)
	}
	return fromFile(path, name, hint)
}

func fromFile(path, name, hint string) (Profile, error) {
	file, err := Load(path)
	if errors.Is(err, ErrNoFile) {
		if hint == "" {
			hint = "lalabase auth import --profile " + name
		}
		return Profile{}, fmt.Errorf("no profile %q: %s does not exist yet; run: %s", name, path, hint)
	}
	if err != nil {
		return Profile{}, err
	}
	entry, ok := file.Profiles[name]
	if !ok {
		if hint == "" {
			hint = "lalabase auth import --profile " + name
		}
		return Profile{}, fmt.Errorf("no profile %q in %s; run: %s", name, path, hint)
	}
	if entry.URL == "" || entry.Token == "" {
		return Profile{}, fmt.Errorf("profile %q in %s needs both url and token", name, path)
	}
	return Profile{Name: name, URL: entry.URL, Token: entry.Token, AllowWrite: entry.AllowWrite, Source: "file " + path}, nil
}

// VerifyAgent refuses an agent profile whose token may write, and one whose
// server does not say (before MCP 0.7.0): "unknown" is not "read-only".
//
// allow_write = true in the file lets both through, deliberately: the person
// allowed the agent to write, so whether the token may write no longer
// matters, and "unknown" is only a "maybe" of that. It comes only from the
// file, written by hand; auth import never sets it and drops it when it
// replaces a profile.
func VerifyAgent(me api.Me, p Profile) error {
	if p.AllowWrite {
		return nil
	}
	if me.Token.ReadOnly == nil {
		return errors.New("the server does not say whether this token may write; agent commands need a read-only token (or allow_write = true in the agent profile)")
	}
	if !*me.Token.ReadOnly {
		return errors.New("this token may write; agent commands need a read-only token: create one in Lalabase and run lalabase auth import --agent (or set allow_write = true in the agent profile)")
	}
	return nil
}
