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
	"github.com/gboyke/lalabase-cli/internal/claudecfg"
	"github.com/gboyke/lalabase-cli/internal/worker"
)

const (
	profileServer = "https://lalabase.com"
	sessionUUID   = "FCB46015-2039-451D-82C2-59CE4353910D"
	// testLimit bounds every wait of these tests.
	testLimit = 10 * time.Second
)

// osascriptCall is what the fake osascript was given.
type osascriptCall struct {
	script string
	args   []string
}

// rig is one launcher under test: a home with Claude Code's configuration,
// a start folder, a fake osascript, LookPath and ExecProcess.
type rig struct {
	t      *testing.T
	home   string
	cwd    string
	sup    *worker.Supervisor
	l      *Launcher
	mu     sync.Mutex
	calls  []osascriptCall
	answer func(ctx context.Context) (string, error)
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

// userServer is ~/.claude.json with one user-scope server.
func userServer(name, url string) string {
	return `{"mcpServers": {"` + name + `": {"type":"http","url":"` + url + `/api/mcp","headers":{"Authorization":"Bearer lalab_pat_AAAAAAAAAAAA"}}}}`
}

func newRig(t *testing.T, iterm bool, folders map[string]string) *rig {
	t.Helper()
	r := &rig{t: t, home: realTemp(t), cwd: realTemp(t)}
	writeFile(t, filepath.Join(r.home, ".claude.json"), userServer("lalabase", profileServer))
	r.sup = worker.New(context.Background())
	t.Cleanup(func() { r.sup.Stop(testLimit) })
	cfg := Config{Supervisor: r.sup, Home: r.home, Cwd: r.cwd, Server: profileServer, Folders: folders}
	r.l = New(cfg)
	r.l.session = ""
	if iterm {
		r.l.session = sessionUUID
	}
	r.answer = func(context.Context) (string, error) { return "", nil }
	r.l.osascript = func(ctx context.Context, script string, args []string) (string, error) {
		r.mu.Lock()
		r.calls = append(r.calls, osascriptCall{script, args})
		answer := r.answer
		r.mu.Unlock()
		return answer(ctx)
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

func TestITermSplitsTheSessionWithTheExactLine(t *testing.T) {
	r := newRig(t, true, nil)
	if d := r.done(shown); d.Kind != Opened || d.Detail != "" {
		t.Fatalf("done %+v", d)
	}
	calls := r.osascriptCalls()
	if len(calls) != 1 {
		t.Fatalf("osascript ran %d times", len(calls))
	}
	want := "cd '" + r.cwd + "' && claude -n 'lalabase #42' '" + wantPrompt + "'"
	if calls[0].script != appleScript {
		t.Error("the script is not the constant")
	}
	if got := calls[0].args; len(got) != 3 || got[0] != "-" || got[1] != sessionUUID || got[2] != want {
		t.Errorf("arguments\n got %q\nwant %q", got, []string{"-", sessionUUID, want})
	}
}

// Every refusal comes before anything runs: no osascript, no claude.
func TestARefusalRunsNothing(t *testing.T) {
	for name, c := range map[string]struct {
		ticket  Ticket
		folders map[string]string
		config  string
		kind    Kind
	}{
		"bad project id": {ticket: Ticket{Project: "x'; touch pwned #", ID: ticketID, Number: 1}, kind: NotHandedOver},
		"bad ticket id":  {ticket: Ticket{Project: projectID, ID: ticketID + "\n", Number: 1}, kind: NotHandedOver},
		"no number":      {ticket: Ticket{Project: projectID, ID: ticketID}, kind: NotHandedOver},
		"no server":      {ticket: shown, config: userServer("lalabase", "https://staging.example"), kind: NoServer},
		"bad name":       {ticket: shown, config: userServer("lala base", profileServer), kind: BadName},
		"broken config":  {ticket: shown, config: `{"mcpServers": `, kind: ConfigUnreadable},
		"missing folder": {ticket: shown, folders: map[string]string{projectID: "/does/not/exist"}, kind: NoFolder},
		"relative":       {ticket: shown, folders: map[string]string{projectID: "dev/website"}, kind: NoFolder},
		"newline":        {ticket: shown, folders: map[string]string{projectID: "/tmp/a\nb"}, kind: BadFolder},
	} {
		t.Run(name, func(t *testing.T) {
			for _, iterm := range []bool{true, false} {
				r := newRig(t, iterm, c.folders)
				if c.config != "" {
					writeFile(t, filepath.Join(r.home, ".claude.json"), c.config)
				}
				if d := r.done(c.ticket); d.Kind != c.kind {
					t.Errorf("iTerm %v: kind %v, want %v (%q)", iterm, d.Kind, c.kind, d.Detail)
				}
				if n := len(r.osascriptCalls()); n != 0 || len(r.execd) != 0 {
					t.Errorf("iTerm %v: ran osascript %d, claude %d times", iterm, n, len(r.execd))
				}
			}
		})
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

// The folder decides which servers Claude Code sees: a project's .mcp.json
// there that names the same server elsewhere stops the launch.
func TestTheProjectFileOfTheFolderCounts(t *testing.T) {
	r := newRig(t, true, nil)
	writeFile(t, filepath.Join(r.cwd, ".mcp.json"), `{"mcpServers": {"lalabase": {"type":"http","url":"https://evil.example/api/mcp"}}}`)
	if d := r.done(shown); d.Kind != Conflict || !strings.Contains(d.Detail, filepath.Join(r.cwd, ".mcp.json")) {
		t.Fatalf("done %+v", d)
	}
	if len(r.osascriptCalls()) != 0 {
		t.Fatal("osascript ran")
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
	want := []string{"/opt/bin/claude", "-n", "lalabase #42", wantPrompt}
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
	r.l.visible = func(string, string, string) ([]claudecfg.Entry, []error) { panic("boom") }
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

// Review round 1, F6: CLAUDE_CONFIG_DIR reaches the reading of Claude
// Code's configuration, and only in its absolute form.
func TestTheConfigDirOfClaudeCode(t *testing.T) {
	r := newRig(t, true, nil)
	var asked []string
	r.l.visible = func(home, dir, folder string) ([]claudecfg.Entry, []error) {
		asked = append(asked, dir)
		return claudecfg.Visible(home, dir, folder)
	}
	dir := realTemp(t)
	r.l.cfg.ClaudeConfigDir = dir
	if d := r.done(shown); d.Kind != Opened || len(asked) != 1 || asked[0] != dir {
		t.Fatalf("done %+v, asked %q", d, asked)
	}
	for _, bad := range []string{"relative/dir", "/tmp/a\nb"} {
		r.l.cfg.ClaudeConfigDir = bad
		if d := r.done(shown); d.Kind != ConfigUnreadable {
			t.Errorf("%q: done %+v", bad, d)
		}
	}
}

// Review round 1, P41: after the deadline killed osascript, a child of it
// that keeps its error output open does not hold the launch: the wait for
// the output ends after WaitDelay.
func TestAKilledChildDoesNotHoldTheLaunch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := runChild(ctx, "/bin/sh", "sleep 20 >&2 &\nsleep 20\n", []string{"-s"})
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
	if len(c.b) != maxStderr {
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
