package launch

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/agents"
	"github.com/gboyke/lalabase-cli/internal/launchdir"
	"github.com/gboyke/lalabase-cli/internal/worker"
)

const (
	profileMCP   = "https://lalabase.com/api/mcp"
	profileToken = "lalab_pat_TESTTOKENTESTTOKEN"
	sessionUUID  = "FCB46015-2039-451D-82C2-59CE4353910D"
	// The new pane's unique id as osascript prints it, and the two ids
	// the rig's launches get (C9a).
	paneUUID     = "18C12925-0000-4000-8000-000000000001"
	agentID      = "0f8fad5b-d9cb-469f-a165-70867728950e"
	agentSession = "7c9e6679-7425-40de-944b-e07fc1f90ae7"
	executable   = "/opt/lalabase/bin/lalabase"
	// testLimit bounds every wait of these tests.
	testLimit = 10 * time.Second
)

// osascriptCall is what the fake osascript was given.
type osascriptCall struct {
	script string
	args   []string
}

// rig is one launcher under test: a home, a start folder, a directory for
// the launch files, a fake osascript, LookPath and ExecProcess.
type rig struct {
	t      *testing.T
	home   string
	cwd    string
	files  string
	agents string
	sup    *worker.Supervisor
	l      *Launcher
	mu     sync.Mutex
	calls  []osascriptCall
	answer func(ctx context.Context) (string, error)
	stdout string
	execd  []*exec.Cmd
}

func realTemp(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func newRig(t *testing.T, iterm bool, folders map[string]string) *rig {
	t.Helper()
	r := &rig{t: t, home: realTemp(t), cwd: realTemp(t)}
	r.files = filepath.Join(r.home, ".cache", "lalabase", "cli", "default", "l")
	r.agents = filepath.Join(r.home, ".cache", "lalabase", "cli", "default", "a")
	r.sup = worker.New(context.Background())
	t.Cleanup(func() { r.sup.Stop(testLimit) })
	cfg := Config{Supervisor: r.sup, Home: r.home, Cwd: r.cwd, MCP: profileMCP, Token: profileToken, Folders: folders, Files: r.files,
		Agents: r.agents, Executable: executable}
	r.l = New(cfg)
	r.l.session = ""
	if iterm {
		r.l.session = sessionUUID
	}
	r.answer = func(context.Context) (string, error) { return "", nil }
	r.stdout = paneUUID + "\n"
	r.l.osascript = func(ctx context.Context, script string, args []string) (string, string, error) {
		r.mu.Lock()
		r.calls = append(r.calls, osascriptCall{script, args})
		answer, stdout := r.answer, r.stdout
		r.mu.Unlock()
		stderr, err := answer(ctx)
		if err != nil {
			stdout = ""
		}
		return stdout, stderr, err
	}
	// The agent's id first, then the session's, on every launch.
	ids := 0
	r.l.newID = func() (string, error) {
		ids++
		if ids%2 == 1 {
			return agentID, nil
		}
		return agentSession, nil
	}
	r.l.lookPath = func(string) (string, error) { return "/opt/bin/claude", nil }
	r.l.execProcess = func(c *exec.Cmd, fn tea.ExecCallback) tea.Cmd {
		r.mu.Lock()
		r.execd = append(r.execd, c)
		r.mu.Unlock()
		return func() tea.Msg { return execRan{fn: fn} }
	}
	return r
}

// execRan stands in for Bubble Tea's message of ExecProcess: the program
// runs the command and calls fn with its error.
type execRan struct{ fn tea.ExecCallback }

func (r *rig) launch(t Ticket) tea.Msg {
	r.t.Helper()
	out := make(chan tea.Msg, 1)
	if !r.sup.Start("test: launch", func(context.Context) { out <- r.l.Launch(7, t)() }) {
		r.t.Fatal("the lifetime is over")
	}
	select {
	case msg := <-out:
		return msg
	case <-time.After(testLimit):
		r.t.Fatal("no message")
	}
	return nil
}

func (r *rig) done(t Ticket) Done {
	r.t.Helper()
	msg := r.launch(t)
	d, ok := msg.(Done)
	if !ok {
		r.t.Fatalf("message %#v, want Done", msg)
	}
	if d.Ref != 7 || d.Ticket != t {
		r.t.Fatalf("done for %d %+v", d.Ref, d.Ticket)
	}
	return d
}

func (r *rig) osascriptCalls() []osascriptCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]osascriptCall(nil), r.calls...)
}

// launchDirs are the launch directories the rig's launches made.
func (r *rig) launchDirs() []string {
	r.t.Helper()
	entries, err := os.ReadDir(r.files)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		r.t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, filepath.Join(r.files, e.Name()))
	}
	return out
}

// theLaunch is the one launch directory, its id and the server name the
// prompt names.
func (r *rig) theLaunch() (dir, server string) {
	r.t.Helper()
	dirs := r.launchDirs()
	if len(dirs) != 1 {
		r.t.Fatalf("launch directories %q, want one", dirs)
	}
	return dirs[0], "lalabase_c7_" + filepath.Base(dirs[0])[:8]
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // the test's file
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// promptFor is wantPrompt with another server's name.
func promptFor(server string) string { return strings.ReplaceAll(wantPrompt, "lalabase", server) }

// wantMCP is the server file of a launch, byte for byte.
func wantMCP(server string) string {
	return `{"mcpServers":{"` + server + `":{"type":"http","url":"` + profileMCP + `","headers":{"Authorization":"Bearer ` + profileToken + `"}}}}`
}

func TestITermSplitsTheSessionAndTypesTheScriptsPath(t *testing.T) {
	r := newRig(t, true, nil)
	if d := r.done(shown); d.Kind != Opened || d.Detail != "" {
		t.Fatalf("done %+v", d)
	}
	calls := r.osascriptCalls()
	if len(calls) != 1 {
		t.Fatalf("osascript ran %d times", len(calls))
	}
	dir, server := r.theLaunch()
	want := "'" + filepath.Join(dir, "run") + "'"
	if calls[0].script != appleScript {
		t.Error("the script is not the constant")
	}
	if got := calls[0].args; len(got) != 3 || got[0] != "-" || got[1] != sessionUUID || got[2] != want {
		t.Errorf("arguments\n got %q\nwant %q", got, []string{"-", sessionUUID, want})
	}
	if got := readFile(t, filepath.Join(dir, "m")); got != wantMCP(server) {
		t.Errorf("server file\n got %s\nwant %s", got, wantMCP(server))
	}
	wantScript := "#!/bin/sh\n# Claude Code beside a Lalabase ticket: one launch, written by lalabase.\n" +
		"cd '" + r.cwd + "' || exit\n" +
		"exec claude '--mcp-config' '" + filepath.Join(dir, "m") + "' '--strict-mcp-config' '--session-id' '" + agentSession + "' '--settings' '" + filepath.Join(dir, "s") + "' '-n' 'lalabase #42' '" + promptFor(server) + "'\n"
	if got := readFile(t, filepath.Join(dir, "run")); got != wantScript {
		t.Errorf("script\n got %q\nwant %q", got, wantScript)
	}
	for name, perm := range map[string]os.FileMode{"m": 0o600, "run": 0o700, "s": 0o600} {
		if info, err := os.Lstat(filepath.Join(dir, name)); err != nil || info.Mode() != perm {
			t.Errorf("%s: mode %v, %v", name, info.Mode(), err)
		}
	}
}

// The token is in the server file alone: never in osascript's or claude's
// arguments, the typed line or the script.
func TestTheTokenIsInTheServerFileAlone(t *testing.T) {
	for _, iterm := range []bool{true, false} {
		r := newRig(t, iterm, nil)
		r.launch(shown)
		var seen []string
		for _, c := range r.osascriptCalls() {
			seen = append(seen, c.args...)
		}
		for _, c := range r.execd {
			seen = append(seen, c.Args...)
			seen = append(seen, c.Env...)
		}
		dir, server := r.theLaunch()
		if iterm {
			seen = append(seen, readFile(t, filepath.Join(dir, "run")))
		}
		seen = append(seen, readFile(t, filepath.Join(dir, "s")))
		seen = append(seen, readFile(t, filepath.Join(r.agents, agentID+".launch.json")))
		if len(seen) == 0 {
			t.Fatalf("iTerm %v: nothing ran", iterm)
		}
		for _, s := range seen {
			if strings.Contains(s, profileToken) || strings.Contains(s, "TESTTOKEN") {
				t.Errorf("iTerm %v: the token in %q", iterm, s)
			}
		}
		if got := readFile(t, filepath.Join(dir, "m")); got != wantMCP(server) {
			t.Errorf("iTerm %v: server file %s", iterm, got)
		}
	}
}

// Every press has a directory, a server name and a script of its own.
func TestEveryPressHasItsOwnFiles(t *testing.T) {
	r := newRig(t, true, nil)
	r.done(shown)
	r.done(shown)
	dirs := r.launchDirs()
	calls := r.osascriptCalls()
	if len(dirs) != 2 || len(calls) != 2 || calls[0].args[2] == calls[1].args[2] {
		t.Fatalf("directories %q, lines %v", dirs, calls)
	}
	if readFile(t, filepath.Join(dirs[0], "m")) == readFile(t, filepath.Join(dirs[1], "m")) {
		t.Error("two launches share a server name")
	}
}

// Every refusal comes before anything runs: no osascript, no claude, and
// before a refusal of the ticket or the folder no file is written.
func TestARefusalRunsNothing(t *testing.T) {
	for name, c := range map[string]struct {
		ticket  Ticket
		folders map[string]string
		cfg     func(*Config)
		kind    Kind
	}{
		"bad project id": {ticket: Ticket{Project: "x'; touch pwned #", ID: ticketID, Number: 1}, kind: NotHandedOver},
		"bad ticket id":  {ticket: Ticket{Project: projectID, ID: ticketID + "\n", Number: 1}, kind: NotHandedOver},
		"no number":      {ticket: Ticket{Project: projectID, ID: ticketID}, kind: NotHandedOver},
		"missing folder": {ticket: shown, folders: map[string]string{projectID: "/does/not/exist"}, kind: NoFolder},
		"relative":       {ticket: shown, folders: map[string]string{projectID: "dev/website"}, kind: NoFolder},
		"newline":        {ticket: shown, folders: map[string]string{projectID: "/tmp/a\nb"}, kind: BadFolder},
		"no files":       {ticket: shown, cfg: func(c *Config) { c.Files = "" }, kind: NoFiles},
		"no token":       {ticket: shown, cfg: func(c *Config) { c.Token = "" }, kind: NoFiles},
		"no server":      {ticket: shown, cfg: func(c *Config) { c.MCP = "" }, kind: NoFiles},
	} {
		t.Run(name, func(t *testing.T) {
			for _, iterm := range []bool{true, false} {
				r := newRig(t, iterm, c.folders)
				if c.cfg != nil {
					c.cfg(&r.l.cfg)
				}
				if d := r.done(c.ticket); d.Kind != c.kind {
					t.Errorf("iTerm %v: kind %v, want %v (%q)", iterm, d.Kind, c.kind, d.Detail)
				}
				if n := len(r.osascriptCalls()); n != 0 || len(r.execd) != 0 {
					t.Errorf("iTerm %v: ran osascript %d, claude %d times", iterm, n, len(r.execd))
				}
				if dirs := r.launchDirs(); len(dirs) != 0 {
					t.Errorf("iTerm %v: launch files written: %q", iterm, dirs)
				}
			}
		})
	}
}

// A launch directory that cannot be made, or a script path no shell takes
// as typed, stops the launch and says why.
func TestLaunchFilesThatCannotBeWritten(t *testing.T) {
	for _, iterm := range []bool{true, false} {
		r := newRig(t, iterm, nil)
		open := filepath.Join(r.home, "open")
		if err := os.Mkdir(open, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(open, 0o777); err != nil { //nolint:gosec // the case under test
			t.Fatal(err)
		}
		r.l.cfg.Files = filepath.Join(open, "l")
		if d := r.done(shown); d.Kind != NoFiles || !strings.Contains(d.Detail, open) {
			t.Errorf("iTerm %v: a root open to others: %+v", iterm, d)
		}
		if n := len(r.osascriptCalls()); n != 0 || len(r.execd) != 0 {
			t.Errorf("iTerm %v: ran osascript %d, claude %d times", iterm, n, len(r.execd))
		}
	}
	// Review round 2 of #33, F5: the path is checked before the token is
	// written, so a launch that cannot go on leaves no server file.
	r := newRig(t, true, nil)
	r.files = filepath.Join(realTemp(t), "a\\b", "l")
	r.l.cfg.Files = r.files
	if d := r.done(shown); d.Kind != NoFiles || len(r.osascriptCalls()) != 0 || !strings.Contains(d.Detail, "cannot be typed") {
		t.Errorf("a path with a backslash: %+v", d)
	}
	for _, dir := range r.launchDirs() {
		if entries, _ := os.ReadDir(dir); len(entries) != 0 {
			t.Errorf("a path with a backslash left %s: %v", dir, entries)
		}
	}
}

// Claude Code's own configuration is not read: a project's .mcp.json that
// names another server, or a broken ~/.claude.json, changes nothing. That
// the launch reads no file at all is held by the source guard
// (launchImports names the client packages it may import).
func TestClaudeCodesConfigurationIsNotRead(t *testing.T) {
	r := newRig(t, true, nil)
	writeFile(t, filepath.Join(r.cwd, ".mcp.json"), `{"mcpServers": {"lalabase": {"type":"http","url":"https://evil.example/api/mcp"}}}`)
	writeFile(t, filepath.Join(r.home, ".claude.json"), `{"mcpServers": `)
	if d := r.done(shown); d.Kind != Opened {
		t.Fatalf("done %+v", d)
	}
	dir, server := r.theLaunch()
	if got := readFile(t, filepath.Join(dir, "m")); got != wantMCP(server) {
		t.Errorf("server file %s", got)
	}
}

func TestTheFolderOfAProject(t *testing.T) {
	r := newRig(t, true, nil)
	inHome := filepath.Join(r.home, "dev", "website")
	elsewhere := filepath.Join(realTemp(t), "intern")
	file := filepath.Join(r.home, "notes.txt")
	for _, dir := range []string{inHome, elsewhere} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, file, "x")
	for _, c := range []struct {
		name    string
		folders map[string]string
		want    string
		kind    Kind
	}{
		{"no entry: the start folder", nil, r.cwd, OK},
		{"another project's entry", map[string]string{"00000000-0000-4000-8000-000000000099": elsewhere}, r.cwd, OK},
		{"absolute", map[string]string{projectID: elsewhere}, elsewhere, OK},
		{"under home", map[string]string{projectID: "~/dev/website"}, inHome, OK},
		{"cleaned", map[string]string{projectID: elsewhere + "/../intern/"}, elsewhere, OK},
		{"missing", map[string]string{projectID: "~/dev/gone"}, filepath.Join(r.home, "dev", "gone"), NoFolder},
		{"a file", map[string]string{projectID: file}, file, NotADirectory},
		{"tilde alone", map[string]string{projectID: "~"}, "~", NoFolder},
		{"another user", map[string]string{projectID: "~anna/dev"}, "~anna/dev", NoFolder},
	} {
		r.l.cfg.Folders = c.folders
		got, kind, detail := r.l.folder(projectID)
		if kind != c.kind || (kind == OK && got != c.want) || (kind != OK && detail != c.want) {
			t.Errorf("%s: %q, kind %v, detail %q; want %q, kind %v", c.name, got, kind, detail, c.want, c.kind)
		}
	}
	// Without a home, ~/ means nothing; without a start folder, there is
	// no folder.
	r.l.cfg.Home, r.l.cfg.Folders = "", map[string]string{projectID: "~/dev/website"}
	if _, kind, _ := r.l.folder(projectID); kind != NoFolder {
		t.Errorf("~/ without a home: %v", kind)
	}
	r.l.cfg.Cwd, r.l.cfg.Folders = "", nil
	if _, kind, _ := r.l.folder(projectID); kind != NoFolder {
		t.Errorf("no start folder: %v", kind)
	}
}

func TestWhatITermAnswers(t *testing.T) {
	for _, c := range []struct {
		name   string
		answer func(ctx context.Context) (string, error)
		kind   Kind
	}{
		{"permission", func(context.Context) (string, error) {
			return "36:120: execution error: Not authorized to send Apple events to iTerm. (-1743)\n", errors.New("exit status 1")
		}, NoPermission},
		{"no session", func(context.Context) (string, error) {
			return "18:37: execution error: session not found (4201)\n", errors.New("exit status 1")
		}, SessionGone},
		{"other", func(context.Context) (string, error) {
			return "execution error: iTerm got an error (-1708)\n", errors.New("exit status 1")
		}, ITermFailed},
		{"a number inside the text is no code", func(context.Context) (string, error) {
			return "execution error: 4201 (-1708)\n", errors.New("exit status 1")
		}, ITermFailed},
		{"no answer", func(ctx context.Context) (string, error) {
			<-ctx.Done()
			return "", ctx.Err()
		}, NoAnswer},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t, true, nil)
			r.l.deadline = 50 * time.Millisecond
			r.answer = c.answer
			if d := r.done(shown); d.Kind != c.kind {
				t.Fatalf("kind %v, want %v", d.Kind, c.kind)
			}
		})
	}
}

// The deadline is iTerm's: 5 s, from the plan.
func TestTheDeadlineIsFiveSeconds(t *testing.T) {
	if l := New(Config{}); l.deadline != 5*time.Second {
		t.Fatalf("deadline %v", l.deadline)
	}
}

func TestWithoutITermClaudeRunsHereWithTheSameArguments(t *testing.T) {
	r := newRig(t, false, nil)
	msg := r.launch(shown)
	ran, ok := msg.(execRan)
	if !ok {
		t.Fatalf("message %#v, want the exec of claude", msg)
	}
	if len(r.execd) != 1 {
		t.Fatalf("claude ran %d times", len(r.execd))
	}
	c := r.execd[0]
	dir, server := r.theLaunch()
	want := []string{"/opt/bin/claude", "--mcp-config", filepath.Join(dir, "m"), "--strict-mcp-config",
		"--session-id", agentSession, "--settings", filepath.Join(dir, "s"), "-n", "lalabase #42", promptFor(server)}
	if c.Path != "/opt/bin/claude" || len(c.Args) != len(want) || c.Dir != r.cwd {
		t.Fatalf("command %q %q in %q", c.Path, c.Args, c.Dir)
	}
	for i := range want {
		if c.Args[i] != want[i] {
			t.Errorf("argument %d: %q, want %q", i, c.Args[i], want[i])
		}
	}
	if len(r.osascriptCalls()) != 0 {
		t.Error("osascript ran without iTerm")
	}
	// No script without iTerm: claude runs directly.
	if _, err := os.Lstat(filepath.Join(dir, "run")); !os.IsNotExist(err) {
		t.Errorf("a script without iTerm: %v", err)
	}
	if d, ok := ran.fn(nil).(Done); !ok || d.Kind != Ended || d.Ref != 7 || d.Ticket != shown {
		t.Errorf("after a clean end: %#v", ran.fn(nil))
	}
	if d, ok := ran.fn(errors.New("exit status 2")).(Done); !ok || d.Kind != EndedWithError || d.Detail != "exit status 2" {
		t.Errorf("after an error: %#v", ran.fn(errors.New("exit status 2")))
	}
}

func TestWithoutClaudeOnThePath(t *testing.T) {
	r := newRig(t, false, nil)
	r.l.lookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	if d := r.done(shown); d.Kind != NoClaude {
		t.Fatalf("done %+v", d)
	}
	if len(r.execd) != 0 {
		t.Fatal("claude ran")
	}
	// Review round 2 of #33, F5: no server file for a launch that cannot
	// run.
	if dirs := r.launchDirs(); len(dirs) != 0 {
		t.Errorf("launch files written: %q", dirs)
	}
}

// iTerm is used when both variables say so and the session id has its
// form, and only on macOS; anything else runs claude here.
func TestWhenITermIsUsed(t *testing.T) {
	for _, c := range []struct {
		program, session string
		iterm            bool
	}{
		{"iTerm.app", "w0t1p2:" + sessionUUID, true},
		{"iTerm.app", sessionUUID, false},
		{"iTerm.app", "w0t1p2:" + sessionUUID + "\n", false},
		{"iTerm.app", "", false},
		{"Apple_Terminal", "w0t1p2:" + sessionUUID, false},
		{"WezTerm", "", false},
		{"", "w0t1p2:" + sessionUUID, false},
		{"iterm.app", "w0t1p2:" + sessionUUID, false},
	} {
		l := New(Config{TermProgram: c.program, SessionID: c.session})
		want := c.iterm && iTermPossible
		if got := l.session != ""; got != want {
			t.Errorf("%q %q: iTerm %v, want %v", c.program, c.session, got, want)
		}
		if want && l.session != sessionUUID {
			t.Errorf("session %q", l.session)
		}
	}
}

// A launch after the session ended runs nothing and says so; a panic in it
// is the session's fatal error, never a message with half a result.
func TestALaunchUnderTheSupervisor(t *testing.T) {
	r := newRig(t, true, nil)
	r.sup.Stop(testLimit)
	if msg := r.l.Launch(7, shown)(); msg != (Done{Ref: 7, Ticket: shown, Kind: Canceled}) {
		t.Fatalf("after the end: %#v", msg)
	}
	if len(r.osascriptCalls()) != 0 {
		t.Fatal("osascript ran after the end")
	}

	r = newRig(t, true, nil)
	r.l.makeDir = func(string, time.Time) (launchdir.Dir, error) { panic("boom") }
	if msg := r.launch(shown); msg != (worker.Fatal{}) {
		t.Fatalf("after a panic: %#v", msg)
	}
	if r.sup.Err() == nil {
		t.Fatal("no fatal error recorded")
	}
}

// Review round 1, P32: the error number is the one at the end of the
// output, not a number in parentheses before it.
func TestTheErrorNumberIsTheLastOne(t *testing.T) {
	ctx := context.Background()
	if got := classify(ctx, "execution error: (4201) in a name (-1708)\n", errors.New("exit status 1")); got != ITermFailed {
		t.Fatalf("kind %v", got)
	}
	if got := classify(ctx, "execution error: x (-1743)\n", errors.New("exit status 1")); got != NoPermission {
		t.Fatalf("kind %v", got)
	}
}

// Review round 1, P35 and F10: a relative entry is no folder, even one that
// exists from here; a project id in another case finds its entry.
func TestAFolderEntryOfAnotherForm(t *testing.T) {
	r := newRig(t, true, nil)
	r.l.cfg.Folders = map[string]string{projectID: "."}
	if _, kind, _ := r.l.folder(projectID); kind != NoFolder {
		t.Errorf("a relative entry: %v", kind)
	}
	const lettered = "abcdef00-0000-4000-8000-0000000000ff"
	dir := realTemp(t)
	r.l.cfg.Folders = map[string]string{strings.ToUpper(lettered): dir}
	if got, kind, _ := r.l.folder(lettered); kind != OK || got != dir {
		t.Errorf("an entry in upper case: %q, %v", got, kind)
	}
}

// Review round 1, P66: the session's end cancels a launch waiting on iTerm.
func TestTheSessionsEndCancelsTheWaitOnITerm(t *testing.T) {
	r := newRig(t, true, nil)
	waiting := make(chan struct{})
	r.answer = func(ctx context.Context) (string, error) {
		close(waiting)
		<-ctx.Done()
		return "", ctx.Err()
	}
	out := make(chan tea.Msg, 1)
	if !r.sup.Start("test: launch", func(context.Context) { out <- r.l.Launch(7, shown)() }) {
		t.Fatal("the lifetime is over")
	}
	<-waiting
	go r.sup.Stop(testLimit)
	select {
	case msg := <-out:
		if d, ok := msg.(Done); !ok || d.Kind != Canceled {
			t.Fatalf("message %#v", msg)
		}
	case <-time.After(testLimit):
		t.Fatal("no message")
	}
}

// Review round 1, P41: after the deadline killed osascript, a child of it
// that keeps its error output open does not hold the launch: the wait for
// the output ends after WaitDelay.
func TestAKilledChildDoesNotHoldTheLaunch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, err := runChild(ctx, "/bin/sh", "sleep 20 >&2 &\nsleep 20\n", []string{"-s"})
	if err == nil {
		t.Fatal("no error after the deadline")
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("the launch waited %v", took)
	}
}

// Review round 3, P57: of osascript's error output the first 4 KiB are
// kept, whatever comes after.
func TestTheErrorOutputIsCapped(t *testing.T) {
	var c capped
	for range 3 {
		if n, err := c.Write(make([]byte, 3000)); n != 3000 || err != nil {
			t.Fatalf("write %d, %v", n, err)
		}
	}
	if len(c.b) != maxOutput {
		t.Fatalf("kept %d bytes", len(c.b))
	}
}

// Review round 3, F2: inside a terminal multiplexer ITERM_SESSION_ID may
// name the session the multiplexer was started in, not the one it shows in
// now; the launch runs claude here instead of splitting a session.
func TestNoITermInsideAMultiplexer(t *testing.T) {
	l := New(Config{TermProgram: "iTerm.app", SessionID: "w0t1p2:" + sessionUUID, Multiplexer: "screen"})
	if l.session != "" {
		t.Fatalf("session %q inside a multiplexer", l.session)
	}
	if l := New(Config{TermProgram: "iTerm.app", SessionID: "w0t1p2:" + sessionUUID}); iTermPossible && l.session == "" {
		t.Fatal("no session without a multiplexer")
	}
}

// Review round 6, F2: the iTerm path exists on macOS and nowhere else; the
// tests of New follow the constant, so it is pinned here.
func TestITermIsPossibleOnMacOSAlone(t *testing.T) {
	if iTermPossible != (runtime.GOOS == "darwin") {
		t.Fatalf("iTermPossible is %v on %s", iTermPossible, runtime.GOOS)
	}
}

// Review round 2 of #22, P22 and P23: the exact key wins; without one, the
// first in sort order, on every run (a map's order is random).
func TestFolderKeysInSeveralCases(t *testing.T) {
	const lower = "abcdef00-0000-4000-8000-0000000000ff"
	exact, upper, mixed := realTemp(t), realTemp(t), realTemp(t)
	r := newRig(t, true, nil)
	r.l.cfg.Folders = map[string]string{lower: exact, "ABCDEF00-0000-4000-8000-0000000000FF": upper}
	if got, kind, _ := r.l.folder(lower); kind != OK || got != exact {
		t.Errorf("exact key: %q, %v", got, kind)
	}
	r.l.cfg.Folders = map[string]string{"Abcdef00-0000-4000-8000-0000000000ff": mixed, "ABCDEF00-0000-4000-8000-0000000000FF": upper}
	for range 50 {
		if got, kind, _ := r.l.folder(lower); kind != OK || got != upper {
			t.Fatalf("no exact key: %q, %v", got, kind)
		}
	}
}

// The launch prunes before it makes its directory: one older than a day
// goes, a younger one stays.
func TestALaunchPrunesOldLaunchFiles(t *testing.T) {
	r := newRig(t, true, nil)
	r.done(shown)
	old, _ := r.theLaunch()
	if err := os.Chtimes(old, time.Now().Add(-25*time.Hour), time.Now().Add(-25*time.Hour)); err != nil {
		t.Fatal(err)
	}
	r.done(shown)
	young, _ := r.theLaunch()
	r.done(shown)
	if dirs := r.launchDirs(); len(dirs) != 2 || (dirs[0] != young && dirs[1] != young) {
		t.Fatalf("after the prune %q, want %s and a new one", dirs, young)
	}
}

// agentsOf reads the rig's agents directory as the interface does.
func (r *rig) agentsOf() []agents.Agent {
	r.t.Helper()
	got, err := agents.Read(r.agents, nil)
	if err != nil {
		r.t.Fatal(err)
	}
	return got
}

// C9a: a launch iTerm opened records the agent with the pane's id the
// AppleScript returned, after the pane opened; the settings file is the
// launch directory's.
func TestAnOpenedLaunchRecordsTheAgent(t *testing.T) {
	r := newRig(t, true, nil)
	if d := r.done(shown); d.Kind != Opened || d.Untracked != "" {
		t.Fatalf("done %+v", d)
	}
	got := r.agentsOf()
	want := agents.Agent{ID: agentID, Organisation: orgID, Project: projectID, Ticket: ticketID, Number: 42, Pane: paneUUID, Session: agentSession, State: agents.Starting}
	if len(got) != 1 || got[0].Since.IsZero() {
		t.Fatalf("agents %+v", got)
	}
	got[0].Since = time.Time{}
	if got[0] != want {
		t.Fatalf("agent\n got %+v\nwant %+v", got[0], want)
	}
	dir, _ := r.theLaunch()
	if s := readFile(t, filepath.Join(dir, "s")); string(settings(executable, r.agents, agentID)) != s {
		t.Errorf("settings %s", s)
	}
	if info, err := os.Lstat(r.agents); err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("agents directory %v %v", info.Mode(), err)
	}
}

// What the AppleScript printed is taken as the pane's id only in that
// form; a launch that got no answer in time is recorded without one (the
// pane may still open); a launch iTerm refused records nothing.
func TestThePaneAndWhichLaunchesAreRecorded(t *testing.T) {
	for _, c := range []struct {
		name   string
		stdout string
		answer func(ctx context.Context) (string, error)
		kind   Kind
		pane   string
		agent  bool
	}{
		{"pane", paneUUID + "\n", nil, Opened, paneUUID, true},
		{"lower case", strings.ToLower(paneUUID), nil, Opened, strings.ToLower(paneUUID), true},
		{"no id", "", nil, Opened, "", true},
		{"another form", "session " + paneUUID + "\n", nil, Opened, "", true},
		{"an escape", "\x1b]52;c;x\x07", nil, Opened, "", true},
		{"no answer", "", func(ctx context.Context) (string, error) { <-ctx.Done(); return "", ctx.Err() }, NoAnswer, "", true},
		{"refused", "", func(context.Context) (string, error) {
			return "execution error: Not authorized (-1743)\n", errors.New("exit status 1")
		}, NoPermission, "", false},
		{"no session", "", func(context.Context) (string, error) {
			return "execution error: session not found (4201)\n", errors.New("exit status 1")
		}, SessionGone, "", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t, true, nil)
			r.l.deadline = 50 * time.Millisecond
			r.stdout = c.stdout
			if c.answer != nil {
				r.answer = c.answer
			}
			if d := r.done(shown); d.Kind != c.kind || d.Untracked != "" {
				t.Fatalf("done %+v", d)
			}
			got := r.agentsOf()
			if !c.agent {
				if len(got) != 0 {
					t.Fatalf("a refused launch recorded %+v", got)
				}
				return
			}
			if len(got) != 1 || got[0].Pane != c.pane {
				t.Fatalf("agents %+v, want pane %q", got, c.pane)
			}
		})
	}
}

// Without iTerm the agent is recorded before claude runs here: the
// interface waits until it ends.
func TestHereTheAgentIsRecordedBeforeClaudeRuns(t *testing.T) {
	r := newRig(t, false, nil)
	recorded := false
	r.l.execProcess = func(c *exec.Cmd, fn tea.ExecCallback) tea.Cmd {
		recorded = len(r.agentsOf()) == 1
		r.execd = append(r.execd, c)
		return func() tea.Msg { return execRan{fn: fn} }
	}
	r.launch(shown)
	if !recorded {
		t.Fatal("claude ran before the agent was recorded")
	}
	if got := r.agentsOf(); got[0].Pane != "" {
		t.Fatalf("a pane without iTerm: %+v", got[0])
	}
}

// A launch whose hooks cannot be handed over goes on without them, as
// before C9a, and says why: no settings file, no session id, no record.
func TestALaunchWithoutHooks(t *testing.T) {
	for name, c := range map[string]struct {
		change func(r *rig)
		why    string
	}{
		"no program":          {func(r *rig) { r.l.cfg.Executable = "" }, "unknown"},
		"no agents dir":       {func(r *rig) { r.l.cfg.Agents = "" }, "unknown"},
		"relative program":    {func(r *rig) { r.l.cfg.Executable = "bin/lalabase" }, "program's path"},
		"program newline":     {func(r *rig) { r.l.cfg.Executable = "/opt/la\nlabase" }, "program's path"},
		"program backslash":   {func(r *rig) { r.l.cfg.Executable = `/opt/la\labase` }, "program's path"},
		"agents dir escape":   {func(r *rig) { r.l.cfg.Agents = filepath.Join(r.home, "a\x1b[2Jb") }, "agents' directory"},
		"agents dir relative": {func(r *rig) { r.l.cfg.Agents = "cache/a" }, "agents' directory"},
		"no organisation":     {func(*rig) {}, "organisation"},
		"agents dir open":     {func(r *rig) { r.l.cfg.Agents = openDir(r.t, r.home) }, "others may"},
		"no id":               {func(r *rig) { r.l.newID = func() (string, error) { return "", errors.New("no randomness") } }, "no randomness"},
	} {
		t.Run(name, func(t *testing.T) {
			for _, iterm := range []bool{true, false} {
				r := newRig(t, iterm, nil)
				c.change(r)
				ticket := shown
				if name == "no organisation" {
					ticket.Org = ""
				}
				msg := r.launch(ticket)
				if d, ok := msg.(Done); iterm && (!ok || d.Kind != Opened || !strings.Contains(d.Untracked, c.why)) {
					t.Fatalf("iTerm: %#v", msg)
				}
				// Review round 1 of #34, F3: without iTerm the end says
				// it too.
				if ran, ok := msg.(execRan); !iterm && (!ok || !strings.Contains(ran.fn(nil).(Done).Untracked, c.why)) {
					t.Fatalf("here: %#v", msg)
				}
				dir, _ := r.theLaunch()
				if _, err := os.Lstat(filepath.Join(dir, "s")); !os.IsNotExist(err) {
					t.Errorf("iTerm %v: a settings file: %v", iterm, err)
				}
				var args []string
				if iterm {
					args = []string{readFile(t, filepath.Join(dir, "run"))}
				} else {
					args = r.execd[0].Args
				}
				for _, a := range args {
					if strings.Contains(a, "--session-id") || strings.Contains(a, "--settings") {
						t.Errorf("iTerm %v: %q", iterm, a)
					}
				}
				if got, _ := agents.Read(r.agents, nil); len(got) != 0 {
					t.Errorf("iTerm %v: recorded %+v", iterm, got)
				}
			}
		})
	}
}

// openDir is a directory others may change, under root.
func openDir(t *testing.T, root string) string {
	t.Helper()
	dir := filepath.Join(root, "open")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o777); err != nil { //nolint:gosec // the case under test
		t.Fatal(err)
	}
	return filepath.Join(dir, "a")
}

// A record that cannot be written leaves the pane open and says why.
func TestARecordThatCannotBeWritten(t *testing.T) {
	r := newRig(t, true, nil)
	r.l.writeLaunch = func(string, agents.Launch) error { return errors.New("disk full") }
	if d := r.done(shown); d.Kind != Opened || d.Untracked != "disk full" {
		t.Fatalf("done %+v", d)
	}
	// Review round 2 of #34, F1: without iTerm claude runs anyway, and its
	// end says why the state was not shown.
	r = newRig(t, false, nil)
	r.l.writeLaunch = func(string, agents.Launch) error { return errors.New("disk full") }
	ran, ok := r.launch(shown).(execRan)
	if !ok {
		t.Fatal("claude did not run")
	}
	if d := ran.fn(nil).(Done); d.Kind != Ended || d.Untracked != "disk full" {
		t.Fatalf("done %+v", d)
	}
}
