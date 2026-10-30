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
	"github.com/gboyke/lalabase-cli/internal/agents"
	"github.com/gboyke/lalabase-cli/internal/launchdir"
	"github.com/gboyke/lalabase-cli/internal/worker"
)

// deadline is how long iTerm has to split the session and type the line.
const deadline = 5 * time.Second

// osascriptPath is macOS's own: not looked up on the PATH.
const osascriptPath = "/usr/bin/osascript"

// maxOutput is how much of osascript's output and error output is kept:
// enough for the new pane's id and for the error number at its end.
const maxOutput = 4 << 10

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
	// Agents is the directory the agents' files live in
	// (<cache root>/<profile>/a/<server>, internal/agents), Executable
	// the client's own program, links resolved: the hooks a launch hands
	// Claude Code run it to record the agent's state there (C9a). "" for
	// either: the launch goes on without hooks.
	Agents, Executable string
	// TermProgram and SessionID are TERM_PROGRAM and ITERM_SESSION_ID as
	// the environment has them; their form is checked here.
	TermProgram, SessionID string
	// Multiplexer is set when the client runs inside tmux, GNU screen or
	// zellij (TMUX, STY, ZELLIJ): ITERM_SESSION_ID may then name the
	// session the multiplexer was started in, not the one it shows in.
	Multiplexer string
	// Server is the profile's server, which a file's address starts with
	// (C10). Remote is set when the client runs over SSH: a browser would
	// open on a screen the person does not see.
	Server string
	Remote bool
}

// Launcher starts Claude Code beside a ticket, one launch per call of
// Launch. The interface keeps a second press from starting a second one.
type Launcher struct {
	cfg Config
	// session is the unique id of the client's iTerm session, "" when the
	// launch runs claude in this terminal.
	session  string
	deadline time.Duration
	// tmux is the path of tmux when agents run in tmux sessions
	// (tmux.go): in iTerm, with tmux on the PATH; "" otherwise.
	tmux   string
	viewer viewer
	// The outside world, replaced in tests.
	osascript   func(ctx context.Context, script string, args []string) (stdout, stderr string, err error)
	runTmux     func(ctx context.Context, args []string) (stdout, stderr string, err error)
	lookPath    func(string) (string, error)
	execProcess func(*exec.Cmd, tea.ExecCallback) tea.Cmd
	stat        func(string) (os.FileInfo, error)
	makeDir     func(root string, now time.Time) (launchdir.Dir, error)
	now         func() time.Time
	prepare     func(dir string) error
	newID       func() (string, error)
	writeLaunch func(dir string, l agents.Launch) error
	opener      func() (string, bool)
	runOpener   func(ctx context.Context, path string, args []string) error
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
		prepare: agents.Prepare, newID: agents.NewID, writeLaunch: agents.WriteLaunch,
	}
	if iTermPossible && cfg.TermProgram == "iTerm.app" && cfg.Multiplexer == "" {
		l.session = iTermSession(cfg.SessionID)
	}
	if l.session != "" {
		if path, err := l.lookPath("tmux"); err == nil && filepath.IsAbs(path) && passable(path) {
			l.tmux = path
		}
	}
	l.runTmux = func(ctx context.Context, args []string) (string, string, error) {
		return runChild(ctx, l.tmux, "", args)
	}
	l.opener = l.findOpener
	l.runOpener = func(ctx context.Context, path string, args []string) error {
		_, _, err := runChild(ctx, path, "", args)
		return err
	}
	return l
}

// Launch is the command of one launch for the ticket shown; its message is
// a Done carrying ref. It runs under the supervisor on the command's own
// goroutine: a panic is the session's fatal error. In iTerm the message
// comes when iTerm answered or the deadline passed; otherwise it is
// Bubble Tea's exec of claude, and the Done follows when claude ended.
func (l *Launcher) Launch(ref uint64, t Ticket) tea.Cmd {
	return l.command(ref, t, StartAction, func() tea.Msg { return l.launch(ref, t, Again{}) })
}

// Again is the agent a start again replaces (C9c): its id, and Claude's
// latest session id when a hook recorded one (agents.Agent.Session with
// Resumable). Without one the session never began (Esc on the trust
// dialog) and there is no conversation to resume: the start is a fresh
// one with a new session id.
type Again struct {
	Agent, Session string
}

// Again is the command of a start again for the agent a replaces, at its
// ticket t (F9): a new agent with a launch of its own, which resumes a's
// latest session, never its launch's id after a /clear. The old agent's
// files are not touched: the interface removes it once the new one went
// through (agents.Settle), so a start that fails leaves it.
func (l *Launcher) Again(ref uint64, t Ticket, a Again) tea.Cmd {
	return l.command(ref, t, AgainAction, func() tea.Msg { return l.launch(ref, t, a) })
}

// Jump is the command that selects the pane of the agent at t (F8); its
// message is a Done with the action JumpAction, under the supervisor like
// Launch.
func (l *Launcher) Jump(ref uint64, t Ticket, agent, pane string) tea.Cmd {
	return l.command(ref, t, JumpAction, func() tea.Msg { return l.jump(ref, t, agent, pane) })
}

// OpenFile is the command that opens a file of the ticket t in the browser
// (C10); its message is a Done with the action OpenFileAction, under the
// supervisor like Launch.
func (l *Launcher) OpenFile(ref uint64, t Ticket, f File) tea.Cmd {
	return l.command(ref, t, OpenFileAction, func() tea.Msg { return l.openFile(ref, t, f) })
}

// Jumps says whether a jump can find a pane: the client runs in iTerm.
func (l *Launcher) Jumps() bool { return l.session != "" }

// command runs do under the supervisor as the command of one action.
func (l *Launcher) command(ref uint64, t Ticket, action Action, do func() tea.Msg) tea.Cmd {
	sup := l.cfg.Supervisor
	return func() tea.Msg {
		msg, ok := worker.Run(sup, "launch", do)
		if !ok {
			if sup.Err() != nil {
				return worker.Fatal{}
			}
			// Not run: the session ended before this command came.
			return Done{Ref: ref, Ticket: t, Kind: Canceled, Action: action}
		}
		return msg
	}
}

// jump asks iTerm to select the pane; NoPane without iTerm or without a
// pane id of its form. The outcome is read from the error's number alone
// (classify): 4201, the script's own, is a pane closed.
func (l *Launcher) jump(ref uint64, t Ticket, agent, pane string) Done {
	d := Done{Ref: ref, Ticket: t, Action: JumpAction, Agent: agent, Kind: NoPane}
	if l.session == "" || !agents.ForeignID(pane) {
		return d
	}
	ctx, cancel := context.WithTimeout(l.cfg.Supervisor.Context(), l.deadline)
	defer cancel()
	_, stderr, err := l.osascript(ctx, jumpScript, []string{"-", pane})
	switch d.Kind = classify(ctx, stderr, err); d.Kind {
	case Opened:
		d.Kind = Jumped
	case SessionGone:
		d.Kind = PaneGone
	}
	return d
}

// openFile hands the file's address to the system's opener: one argument,
// no shell, a deadline. What the browser then finds is the server's to
// answer: this says only that the address was handed over.
func (l *Launcher) openFile(ref uint64, t Ticket, f File) Done {
	d := Done{Ref: ref, Ticket: t, Action: OpenFileAction, Detail: f.Name}
	address, ok := fileAddress(l.cfg.Server, t.Org, f.ID)
	if !ok {
		d.Kind = NotHandedOver
		return d
	}
	path, ok := l.opener()
	if !ok || l.cfg.Remote {
		d.Kind = NoOpener
		return d
	}
	ctx, cancel := context.WithTimeout(l.cfg.Supervisor.Context(), l.deadline)
	defer cancel()
	err := l.runOpener(ctx, path, []string{address})
	switch {
	case err == nil:
		d.Kind = FileOpened
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		d.Kind = NoAnswer
	case ctx.Err() != nil:
		d.Kind = Canceled
	default:
		d.Kind = OpenFailed
	}
	return d
}

// launch starts claude for t: a first start, or a start again of the
// agent a names (a.Agent set).
func (l *Launcher) launch(ref uint64, t Ticket, a Again) tea.Msg {
	action := StartAction
	if a.Agent != "" {
		action = AgainAction
	}
	done := func(k Kind, detail string) Done {
		return Done{Ref: ref, Ticket: t, Kind: k, Detail: detail, Action: action, Replaces: a.Agent}
	}
	if !checkTicket(t) || !resumable(a.Session) {
		return done(NotHandedOver, "")
	}
	folder, kind, detail := l.folder(t.Project)
	if kind != OK {
		return done(kind, detail)
	}
	if l.cfg.MCP == "" || l.cfg.Token == "" {
		return done(NoFiles, "the profile has no server address or token")
	}
	// What can stop the launch is checked before the token is written: a
	// launch that cannot go on leaves no server file behind.
	var claude string
	if l.session == "" {
		path, err := l.lookPath("claude")
		if err != nil {
			return done(NoClaude, "")
		}
		claude = path
	}
	dir, err := l.makeDir(l.cfg.Files, l.now())
	if err != nil {
		return done(NoFiles, err.Error())
	}
	line, ok := typedLine(filepath.Join(dir.Path, launchdir.Script))
	if l.session != "" && !ok {
		return done(NoFiles, "the launch directory's path cannot be typed into a shell: "+dir.Path)
	}
	// Claude Code is handed the profile's server in a file of its own,
	// under a name nothing else has: it reads no server of its own
	// configuration (--strict-mcp-config), so none can stand in for it.
	server := serverPrefix + dir.ID[:8]
	mcp, err := dir.Write(launchdir.MCPConfig, mcpConfig(server, l.cfg.MCP, l.cfg.Token), 0o600)
	if err != nil {
		return done(NoFiles, err.Error())
	}
	tr, agent, untracked, err := l.track(t, dir, a.Session)
	if err != nil {
		return done(NoFiles, err.Error())
	}
	args := claudeArgs(t, server, mcp, tr)
	if l.session == "" {
		// Recorded before claude runs: the interface waits until it ends.
		recorded := ""
		if why := l.record(t, agent, tr, ""); why != "" {
			untracked = why
		} else {
			recorded = agent
		}
		return l.here(claude, folder, args, func(k Kind, detail string) Done {
			d := done(k, detail)
			d.Untracked, d.Agent = untracked, recorded
			return d
		})
	}
	scriptPath, err := dir.Write(launchdir.Script, script(folder, args), 0o700)
	if err != nil {
		return done(NoFiles, err.Error())
	}
	ctx, cancel := context.WithTimeout(l.cfg.Supervisor.Context(), l.deadline)
	defer cancel()
	if l.tmux != "" && agent != "" {
		// The agent runs in a tmux session of its own; the
		// viewer shows it while its ticket is shown (View).
		d := done(l.startInTmux(ctx, agent, folder, scriptPath), "")
		if d.Kind == Opened || d.Kind == NoAnswer {
			if why := l.record(t, agent, tr, ""); why != "" {
				untracked = why
			} else {
				d.Agent = agent
			}
			d.Untracked = untracked
		}
		return d
	}
	stdout, stderr, err := l.osascript(ctx, appleScript, osascriptArgs(l.session, line))
	d := done(classify(ctx, stderr, err), "")
	// The agent is recorded once a pane opened, or may still open; a
	// launch iTerm refused has none.
	if d.Kind == Opened || d.Kind == NoAnswer {
		pane := ""
		if d.Kind == Opened {
			pane = paneID(stdout)
		}
		if why := l.record(t, agent, tr, pane); why != "" {
			untracked = why
		} else {
			d.Agent = agent
		}
		d.Untracked = untracked
	}
	return d
}

// resumable: a session to resume is "" (a fresh start) or a UUID in the
// lower case the client's records take (agents.Launch): one in capitals
// would resume, but its new agent could not be recorded (review round 1
// of #43).
func resumable(session string) bool {
	return session == "" || agents.ForeignID(session) && session == strings.ToLower(session)
}

// track prepares the hooks of a new agent (C9a): its id and the session
// id, the agents directory made and checked, and the settings file in the
// launch directory. resume is the session a start again resumes (C9c):
// it is the new agent's session, with or without hooks. A launch it
// cannot track goes on without hooks, as before C9a, and why says what
// stopped it; err is a failure to write the launch directory, which stops
// the launch.
func (l *Launcher) track(t Ticket, dir launchdir.Dir, resume string) (tr tracking, agent, why string, err error) {
	tr = tracking{session: resume, resume: resume != ""}
	exe, agentsDir := l.cfg.Executable, l.cfg.Agents
	switch {
	case exe == "" || agentsDir == "":
		return tr, "", "the program or the cache directory is unknown", nil
	case !filepath.IsAbs(exe) || !passable(exe):
		return tr, "", "the program's path cannot be put into a hook: " + exe, nil
	case !filepath.IsAbs(agentsDir) || !passable(agentsDir):
		return tr, "", "the agents' directory cannot be put into a hook: " + agentsDir, nil
	case !agents.ForeignID(t.Org):
		return tr, "", "the ticket's organisation is unknown", nil
	}
	agent, session, why := l.newAgent(!tr.resume)
	if why != "" {
		return tr, "", why, nil
	}
	if !tr.resume {
		tr.session = session
	}
	path, err := dir.Write(launchdir.Settings, settings(exe, agentsDir, agent), 0o600)
	if err != nil {
		return tracking{}, "", "", err
	}
	tr.settings = path
	return tr, agent, "", nil
}

// newAgent makes the agents directory ready and draws the agent's id and,
// for a new session, the session's; why says what failed.
func (l *Launcher) newAgent(withSession bool) (agent, session, why string) {
	if err := l.prepare(l.cfg.Agents); err != nil {
		return "", "", err.Error()
	}
	agent, err := l.newID()
	if err != nil {
		return "", "", err.Error()
	}
	if !withSession {
		return agent, "", ""
	}
	session, err = l.newID()
	if err != nil {
		return "", "", err.Error()
	}
	return agent, session, ""
}

// record writes the launch record of an agent whose hooks were handed
// over, and says why it could not; nothing for a launch without hooks.
func (l *Launcher) record(t Ticket, agent string, tr tracking, pane string) (why string) {
	if agent == "" {
		return ""
	}
	err := l.writeLaunch(l.cfg.Agents, agents.Launch{
		Agent: agent, Organisation: t.Org, Project: t.Project, Ticket: t.ID, Number: t.Number,
		Session: tr.session, Pane: pane, At: l.now(),
	})
	if err != nil {
		return err.Error()
	}
	return ""
}

// paneID is the new pane's unique id as the AppleScript returned it, ""
// when the output has another form.
func paneID(stdout string) string {
	id := strings.TrimSpace(stdout)
	if !agents.ForeignID(id) {
		return ""
	}
	return id
}

// here runs claude, found at path, in this terminal: Bubble Tea gives the
// terminal up, runs it and takes the terminal back when it ends. No shell:
// the arguments go to the program as they are.
func (l *Launcher) here(path, folder string, args []string, done func(Kind, string) Done) tea.Msg {
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
func runOSAScript(ctx context.Context, script string, args []string) (string, string, error) {
	return runChild(ctx, osascriptPath, script, args)
}

// runChild runs path with args, script on stdin, and keeps the start of its
// output and of its error output. os/exec copies stdin, stdout and stderr
// on goroutines of its own; they end with the child, and WaitDelay bounds
// the wait for them after the deadline killed it (a grandchild may hold
// the output open).
func runChild(ctx context.Context, path, script string, args []string) (stdout, stderr string, err error) {
	c := exec.CommandContext(ctx, path, args...)
	c.Stdin = strings.NewReader(script)
	var out, errOut capped
	c.Stdout, c.Stderr = &out, &errOut
	c.WaitDelay = time.Second
	err = c.Run()
	return string(out.b), string(errOut.b), err
}

// capped keeps the first maxOutput bytes written to it.
type capped struct{ b []byte }

func (c *capped) Write(p []byte) (int, error) {
	if room := maxOutput - len(c.b); room > 0 {
		c.b = append(c.b, p[:min(room, len(p))]...)
	}
	return len(p), nil
}
