package launch

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/launchdir"
	"github.com/gboyke/lalabase-cli/internal/worker"
)

// deadline is how long iTerm has to split the session and type the line.
const deadline = 5 * time.Second

// osascriptPath is macOS's own: not looked up on the PATH.
const osascriptPath = "/usr/bin/osascript"

// maxStderr is how much of osascript's error output is kept: enough for
// the error number at its end.
const maxStderr = 4 << 10

// Config is what a launcher needs. The command builds it from its Env and
// the profile; the interface never reads the environment itself.
type Config struct {
	Supervisor *worker.Supervisor
	// Home is the person's home (~/ in a folder), Cwd the folder the
	// client was started in.
	Home, Cwd string
	// MCP is the profile's MCP address as the transport resolves it, Token
	// the profile's token: the one server Claude Code gets (C7b). Folders
	// are the profile's project folders.
	MCP, Token string
	Folders    map[string]string
	// Files is the directory the launch keeps its files in
	// (<cache root>/<profile>/l, internal/launchdir); "" when the client
	// has no cache directory.
	Files string
	// TermProgram and SessionID are TERM_PROGRAM and ITERM_SESSION_ID as
	// the environment has them; their form is checked here.
	TermProgram, SessionID string
	// Multiplexer is set when the client runs inside tmux, GNU screen or
	// zellij (TMUX, STY, ZELLIJ): ITERM_SESSION_ID may then name the
	// session the multiplexer was started in, not the one it shows in.
	Multiplexer string
}

// Launcher starts Claude Code beside a ticket, one launch per call of
// Launch. The interface keeps a second press from starting a second one.
type Launcher struct {
	cfg Config
	// session is the unique id of the client's iTerm session, "" when the
	// launch runs claude in this terminal.
	session  string
	deadline time.Duration
	// The outside world, replaced in tests.
	osascript   func(ctx context.Context, script string, args []string) (stderr string, err error)
	lookPath    func(string) (string, error)
	execProcess func(*exec.Cmd, tea.ExecCallback) tea.Cmd
	stat        func(string) (os.FileInfo, error)
	makeDir     func(root string, now time.Time) (launchdir.Dir, error)
	now         func() time.Time
}

// New returns a launcher. iTerm is used on macOS when TERM_PROGRAM says
// iTerm.app, ITERM_SESSION_ID has its form and no multiplexer runs between
// the client and iTerm; otherwise claude runs in this terminal while the
// interface waits.
func New(cfg Config) *Launcher {
	l := &Launcher{
		cfg: cfg, deadline: deadline,
		osascript: runOSAScript, lookPath: exec.LookPath, execProcess: tea.ExecProcess,
		stat: os.Stat, makeDir: launchdir.Make, now: time.Now,
	}
	if iTermPossible && cfg.TermProgram == "iTerm.app" && cfg.Multiplexer == "" {
		l.session = iTermSession(cfg.SessionID)
	}
	return l
}

// Launch is the command of one launch for the ticket shown; its message is
// a Done carrying ref. It runs under the supervisor on the command's own
// goroutine: a panic is the session's fatal error. In iTerm the message
// comes when iTerm answered or the deadline passed; otherwise it is
// Bubble Tea's exec of claude, and the Done follows when claude ended.
func (l *Launcher) Launch(ref uint64, t Ticket) tea.Cmd {
	sup := l.cfg.Supervisor
	return func() tea.Msg {
		msg, ok := worker.Run(sup, "launch", func() tea.Msg { return l.launch(ref, t) })
		if !ok {
			if sup.Err() != nil {
				return worker.Fatal{}
			}
			// Not run: the session ended before this command came.
			return Done{Ref: ref, Ticket: t, Kind: Canceled}
		}
		return msg
	}
}

func (l *Launcher) launch(ref uint64, t Ticket) tea.Msg {
	done := func(k Kind, detail string) Done { return Done{Ref: ref, Ticket: t, Kind: k, Detail: detail} }
	if !checkTicket(t) {
		return done(NotHandedOver, "")
	}
	folder, kind, detail := l.folder(t.Project)
	if kind != OK {
		return done(kind, detail)
	}
	if l.cfg.MCP == "" || l.cfg.Token == "" {
		return done(NoFiles, "the profile has no server address or token")
	}
	// Claude Code is handed the profile's server in a file of its own,
	// under a name nothing else has: it reads no server of its own
	// configuration (--strict-mcp-config), so none can stand in for it.
	dir, err := l.makeDir(l.cfg.Files, l.now())
	if err != nil {
		return done(NoFiles, err.Error())
	}
	server := serverPrefix + dir.ID[:8]
	mcp, err := dir.Write(launchdir.MCPConfig, mcpConfig(server, l.cfg.MCP, l.cfg.Token), 0o600)
	if err != nil {
		return done(NoFiles, err.Error())
	}
	args := claudeArgs(t, server, mcp)
	if l.session == "" {
		return l.here(folder, args, done)
	}
	line, ok := typedLine(filepath.Join(dir.Path, launchdir.Script))
	if !ok {
		return done(NoFiles, "this path cannot be typed into a shell: "+dir.Path)
	}
	if _, err := dir.Write(launchdir.Script, script(folder, args), 0o700); err != nil {
		return done(NoFiles, err.Error())
	}
	ctx, cancel := context.WithTimeout(l.cfg.Supervisor.Context(), l.deadline)
	defer cancel()
	stderr, err := l.osascript(ctx, appleScript, osascriptArgs(l.session, line))
	return done(classify(ctx, stderr, err), "")
}

// here runs claude in this terminal: Bubble Tea gives the terminal up,
// runs it and takes the terminal back when it ends. No shell: the
// arguments go to the program as they are.
func (l *Launcher) here(folder string, args []string, done func(Kind, string) Done) tea.Msg {
	path, err := l.lookPath("claude")
	if err != nil {
		return done(NoClaude, "")
	}
	// No context: claude lives as long as the person works in it; the
	// interface waits meanwhile and has nothing to cancel it for.
	c := exec.Command(path, args...) //nolint:noctx // see above
	c.Dir = folder
	return l.execProcess(c, func(err error) tea.Msg {
		if err != nil {
			return done(EndedWithError, err.Error())
		}
		return done(Ended, "")
	})()
}

// folder is the folder Claude Code starts in: the project's entry in the
// profile, an absolute path or one under ~/, else the folder the client was
// started in. It must exist, be a directory and pass as typed.
func (l *Launcher) folder(project string) (string, Kind, string) {
	path, ok := l.folderEntry(project)
	switch {
	case !ok && l.cfg.Cwd == "":
		return "", NoFolder, "the folder lalabase was started in is unknown"
	case !ok:
		path = l.cfg.Cwd
	case strings.HasPrefix(path, "~/"):
		if l.cfg.Home == "" {
			return "", NoFolder, path
		}
		path = filepath.Join(l.cfg.Home, path[2:])
	}
	if !filepath.IsAbs(path) {
		return "", NoFolder, path
	}
	path = filepath.Clean(path)
	if !passable(path) {
		return "", BadFolder, path
	}
	info, err := l.stat(path)
	if err != nil {
		return "", NoFolder, path
	}
	if !info.IsDir() {
		return "", NotADirectory, path
	}
	return path, OK, ""
}

// folderEntry is the project's entry in the profile; a UUID written in
// another case is the same id. Of two entries differing in case only, the
// exact one wins.
func (l *Launcher) folderEntry(project string) (string, bool) {
	if path, ok := l.cfg.Folders[project]; ok {
		return path, true
	}
	keys := make([]string, 0, len(l.cfg.Folders))
	for k := range l.cfg.Folders {
		if strings.EqualFold(k, project) {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return "", false
	}
	slices.Sort(keys)
	return l.cfg.Folders[keys[0]], true
}

// errorNumber is the number osascript ends an execution error with.
var errorNumber = regexp.MustCompile(`\((-?[0-9]+)\)\s*$`)

// classify reads what iTerm answered: nothing in time, a refusal of macOS
// (-1743: this terminal may not control iTerm), the script's own "session
// not found" (4201), or any other error.
func classify(ctx context.Context, stderr string, err error) Kind {
	if err == nil {
		return Opened
	}
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return NoAnswer
	case ctx.Err() != nil:
		return Canceled
	}
	switch m := errorNumber.FindStringSubmatch(stderr); {
	case m == nil:
	case m[1] == "-1743":
		return NoPermission
	case m[1] == "4201":
		return SessionGone
	}
	return ITermFailed
}

// runOSAScript runs the script with osascript, the script on stdin, the
// values as arguments.
func runOSAScript(ctx context.Context, script string, args []string) (string, error) {
	return runChild(ctx, osascriptPath, script, args)
}

// runChild runs path with args, script on stdin, and keeps the start of its
// error output. os/exec copies stdin and stderr on goroutines of its own;
// they end with the child, and WaitDelay bounds the wait for them after
// the deadline killed it (a grandchild may hold the output open).
func runChild(ctx context.Context, path, script string, args []string) (string, error) {
	c := exec.CommandContext(ctx, path, args...)
	c.Stdin = strings.NewReader(script)
	var stderr capped
	c.Stderr = &stderr
	c.WaitDelay = time.Second
	err := c.Run()
	return string(stderr.b), err
}

// capped keeps the first maxStderr bytes written to it.
type capped struct{ b []byte }

func (c *capped) Write(p []byte) (int, error) {
	if room := maxStderr - len(c.b); room > 0 {
		c.b = append(c.b, p[:min(room, len(p))]...)
	}
	return len(p), nil
}
