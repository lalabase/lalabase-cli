//go:build unix

package agents

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gboyke/lalabase-cli/internal/privatefs"
)

const (
	agentID   = "0f8fad5b-d9cb-469f-a165-70867728950e"
	sessionID = "7c9e6679-7425-40de-944b-e07fc1f90ae7"
	cleared   = "16fd2706-8baf-433b-82eb-8c7fada847da"
	org       = "6ba7b810-9dad-11d1-80b4-00c04fd430c8"
	project   = "6ba7b811-9dad-11d1-80b4-00c04fd430c8"
	ticket    = "6ba7b812-9dad-11d1-80b4-00c04fd430c8"
	pane      = "18C12925-0000-4000-8000-000000000001"
)

var t0 = time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)

func realTemp(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// agentsDir is a prepared agents directory under a fresh profile dir.
func agentsDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(realTemp(t), "cli", "default", Dir)
	if err := Prepare(dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

// procs is a fake process table: pid to start time; a missing pid is gone.
type procs struct {
	mu    sync.Mutex
	start map[int]int64
	err   error
	delay time.Duration
	calls int
}

func (p *procs) Start(pid int) (int64, bool, error) {
	if p.delay > 0 {
		time.Sleep(p.delay)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.err != nil {
		return 0, false, p.err
	}
	s, ok := p.start[pid]
	return s, ok, nil
}

func live(pid int, start int64) *procs { return &procs{start: map[int]int64{pid: start}} }

func launchOf(at time.Time) Launch {
	return Launch{Agent: agentID, Organisation: org, Project: project, Ticket: ticket, Number: 42, Session: sessionID, Pane: pane, At: at}
}

func hook(t *testing.T, dir, event, input string, p Procs) {
	t.Helper()
	c := HookCall{Dir: dir, Event: event, Agent: agentID, PID: "4242", Now: t0, Procs: p}
	if input != "" {
		c.Input = strings.NewReader(input)
	}
	if err := Hook(c); err != nil {
		t.Fatalf("%s: %v", event, err)
	}
}

func stateOf(t *testing.T, dir string) stateRecord {
	t.Helper()
	r, err := readState(filepath.Join(dir, agentID+stateSuffix))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func TestNewIDIsARandomUUIDVersion4(t *testing.T) {
	seen := map[string]bool{}
	for range 64 {
		id, err := NewID()
		if err != nil {
			t.Fatal(err)
		}
		if !ValidID(id) || id[14] != '4' || !strings.ContainsRune("89ab", rune(id[19])) {
			t.Fatalf("id %q", id)
		}
		if seen[id] {
			t.Fatalf("id %q twice", id)
		}
		seen[id] = true
	}
	for _, id := range []string{"", "0F8FAD5B-D9CB-469F-A165-70867728950E", agentID + "x", "../" + agentID[3:]} {
		if ValidID(id) {
			t.Errorf("%q passed", id)
		}
	}
}

func TestPrepareMakesAPrivateDirectory(t *testing.T) {
	dir := filepath.Join(realTemp(t), "cli", "default", Dir)
	if err := Prepare(dir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("%v %v", info.Mode(), err)
	}
	if err := Prepare("relative/a"); err == nil {
		t.Error("a relative directory passed")
	}
	if err := os.Chmod(dir, 0o770); err != nil { //nolint:gosec // the test opens it on purpose
		t.Fatal(err)
	}
	if err := Prepare(dir); err == nil {
		t.Error("a directory others may change passed")
	}
}

func TestALaunchRecordIsWrittenOnceAndPrivately(t *testing.T) {
	dir := agentsDir(t)
	if err := WriteLaunch(dir, launchOf(t0)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, agentID+launchSuffix)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("%v %v", info.Mode(), err)
	}
	if err := WriteLaunch(dir, launchOf(t0.Add(time.Minute))); err == nil {
		t.Error("a second record of the same agent passed")
	}
	got, err := Read(dir, &procs{})
	if err != nil || len(got) != 1 {
		t.Fatalf("%v %v", got, err)
	}
	want := Agent{ID: agentID, Organisation: org, Project: project, Ticket: ticket, Number: 42, Pane: pane, Session: sessionID, State: Starting, Since: t0}
	if got[0] != want {
		t.Fatalf("%+v", got[0])
	}
	// Every field is checked before anything is written.
	for name, change := range map[string]func(*Launch){
		"agent":   func(l *Launch) { l.Agent = "x" },
		"upper":   func(l *Launch) { l.Agent = strings.ToUpper(agentID) },
		"session": func(l *Launch) { l.Session = "" },
		"org":     func(l *Launch) { l.Organisation = "" },
		"project": func(l *Launch) { l.Project = "../x" },
		"ticket":  func(l *Launch) { l.Ticket = "x" },
		"number":  func(l *Launch) { l.Number = 0 },
		"big":     func(l *Launch) { l.Number = 1 << 31 },
		"pane":    func(l *Launch) { l.Pane = "w0t0p0:" + pane },
		"time":    func(l *Launch) { l.At = time.Time{} },
	} {
		l := launchOf(t0)
		l.Agent = "1f8fad5b-d9cb-469f-a165-70867728950e"
		change(&l)
		if err := WriteLaunch(dir, l); err == nil {
			t.Errorf("%s passed", name)
		}
	}
	if got := names(t, dir); len(got) != 1 {
		t.Fatalf("a refused record left %v", got)
	}
	// The directory is never made here.
	missing := filepath.Join(realTemp(t), Dir)
	if err := WriteLaunch(missing, launchOf(t0)); err == nil {
		t.Error("a missing directory passed")
	}
	if _, err := os.Lstat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the launch made the directory: %v", err)
	}
}

// Every row of the event table.
func TestEveryEventLeadsToItsState(t *testing.T) {
	for _, c := range []struct {
		name, event, input string
		want               string
	}{
		{"start", SessionStart, `{"session_id":"` + sessionID + `","source":"startup"}`, "working"},
		// The owner, after the manual check: after /clear and /compact
		// Claude Code waits for the person, and no Stop follows.
		{"start after clear", SessionStart, `{"session_id":"` + cleared + `","source":"clear"}`, "waiting"},
		{"compact", SessionStart, `{"source":"compact"}`, "waiting"},
		{"resume", SessionStart, `{"source":"resume"}`, "working"},
		{"start without a source", SessionStart, `{}`, "working"},
		{"a source in another case", SessionStart, `{"source":"Clear"}`, "working"},
		{"prompt", UserPromptSubmit, `{"prompt":"hi"}`, "working"},
		{"pre", PreToolUse, `{"tool_name":"Bash"}`, "working"},
		{"post", PostToolUse, `{"tool_name":"Write"}`, "working"},
		{"approval", PermissionRequest, `{"tool_name":"Write"}`, "approval"},
		{"question", PermissionRequest, `{"tool_name":"AskUserQuestion"}`, "waiting"},
		{"question in other case", PermissionRequest, `{"tool_name":"askuserquestion"}`, "approval"},
		{"stop", Stop, `{"background_tasks":[]}`, "waiting"},
		{"stop without the field", Stop, `{}`, "waiting"},
		{"stop with a running task", Stop, `{"background_tasks":[{"type":"subagent","status":"running","agent_type":"Explore"}]}`, "working"},
		{"stop with a finished task", Stop, `{"background_tasks":[{"type":"subagent","status":"completed"}]}`, "waiting"},
		{"end", SessionEnd, `{"reason":"other"}`, "ended"},
		{"exit", SessionEnd, `{"reason":"prompt_input_exit"}`, "ended"},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := agentsDir(t)
			hook(t, dir, c.event, c.input, live(4242, 7))
			if got := stateOf(t, dir); got.State != c.want || !got.Changed.Equal(t0) || got.PID != 4242 || got.Start != 7 {
				t.Fatalf("%+v, want %s", got, c.want)
			}
		})
	}
}

// /clear: SessionEnd with reason clear changes nothing, the SessionStart
// after it takes the new session id; the agent stays the same.
func TestClearKeepsTheAgentAndTakesTheNewSession(t *testing.T) {
	dir := agentsDir(t)
	p := live(4242, 7)
	hook(t, dir, SessionStart, `{"session_id":"`+sessionID+`"}`, p)
	hook(t, dir, Stop, `{"session_id":"`+sessionID+`"}`, p)
	before := stateOf(t, dir)
	if before.State != "waiting" || before.Session != sessionID {
		t.Fatalf("%+v", before)
	}
	hook(t, dir, SessionEnd, `{"session_id":"`+sessionID+`","reason":"clear"}`, p)
	if got := stateOf(t, dir); got != before {
		t.Fatalf("clear changed %+v to %+v", before, got)
	}
	hook(t, dir, SessionStart, `{"session_id":"`+cleared+`","source":"clear"}`, p)
	if got := stateOf(t, dir); got.State != "waiting" || got.Session != cleared {
		t.Fatalf("%+v", got)
	}
}

// Without its facts (too large, no JSON, no input) only the event
// decides, and the session id stays.
func TestAnUnreadableInputLeavesTheEventAlone(t *testing.T) {
	big := `{"session_id":"` + cleared + `","reason":"clear","tool_name":"AskUserQuestion","background_tasks":[{"status":"running"}],"pad":"` + strings.Repeat("x", MaxInput) + `"}`
	for name, input := range map[string]string{
		"too large": big,
		"broken":    `{"session_id":"` + cleared + `","reason":"clear","tool_name":"AskUserQuestion","background_tasks":[{"status":"running"}]`,
		"array":     `[1,2]`,
		"wrong":     `{"session_id":42,"reason":"clear"}`,
	} {
		t.Run(name, func(t *testing.T) {
			for event, want := range map[string]string{Stop: "waiting", PermissionRequest: "approval", SessionEnd: "ended"} {
				dir := agentsDir(t)
				hook(t, dir, SessionStart, `{"session_id":"`+sessionID+`"}`, live(4242, 7))
				hook(t, dir, event, input, live(4242, 7))
				if got := stateOf(t, dir); got.State != want || got.Session != sessionID {
					t.Errorf("%s: %+v, want %s", event, got, want)
				}
			}
		})
	}
	// No input at all.
	dir := agentsDir(t)
	if err := Hook(HookCall{Dir: dir, Event: Stop, Agent: agentID, Now: t0}); err != nil {
		t.Fatal(err)
	}
	if got := stateOf(t, dir); got.State != "waiting" {
		t.Fatalf("%+v", got)
	}
}

// The same state goes on: its age counts from when it began.
func TestTheTimeIsTheChangeOfTheState(t *testing.T) {
	dir := agentsDir(t)
	c := HookCall{Dir: dir, Agent: agentID, Now: t0}
	for i, e := range []string{UserPromptSubmit, PreToolUse, PostToolUse, Stop} {
		c.Event, c.Now = e, t0.Add(time.Duration(i)*time.Minute)
		if err := Hook(c); err != nil {
			t.Fatal(err)
		}
		want := t0
		if e == Stop {
			want = t0.Add(3 * time.Minute)
		}
		if got := stateOf(t, dir); !got.Changed.Equal(want) {
			t.Fatalf("%s: changed %v, want %v", e, got.Changed, want)
		}
	}
}

// Nothing but the six facts and the pid reaches a file: the prompt, the
// paths, terminal escapes and a messaging token in the input stay out.
func TestNothingElseOfTheInputIsKept(t *testing.T) {
	dir := agentsDir(t)
	secrets := []string{"PROMPT-SECRET", "/home/someone/private", "\x1b]52;c;cGFzdGU=\x07", "TOKEN-SECRET", "\u202e"}
	input, err := json.Marshal(map[string]any{
		"session_id": sessionID, "agent_id": secrets[0], "source": secrets[1], "prompt": secrets[0], "cwd": secrets[1], "transcript_path": secrets[1] + "/t.jsonl",
		"tool_name": secrets[2], "tool_input": map[string]string{"command": secrets[2]}, "reason": secrets[4],
		"background_tasks":            []map[string]string{{"status": "running", "description": secrets[0]}},
		"CLAUDE_CODE_MESSAGING_TOKEN": secrets[3],
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range Events {
		hook(t, dir, e, string(input), live(4242, 7))
	}
	for _, name := range names(t, dir) {
		data, err := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // the test's file
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range append(secrets, "cGFzdGU", "\\u001b", "description") {
			if strings.Contains(string(data), s) {
				t.Errorf("%s holds %q: %s", name, s, data)
			}
		}
	}
}

// A call that fails a check writes nothing and makes nothing.
func TestARefusedCallWritesNothing(t *testing.T) {
	dir := agentsDir(t)
	for name, c := range map[string]HookCall{
		"agent":        {Dir: dir, Event: Stop, Agent: "not-an-id"},
		"agent upper":  {Dir: dir, Event: Stop, Agent: strings.ToUpper(agentID)},
		"agent path":   {Dir: dir, Event: Stop, Agent: "../" + agentID},
		"event":        {Dir: dir, Event: "Notification", Agent: agentID},
		"subagent":     {Dir: dir, Event: "SubagentStop", Agent: agentID},
		"no event":     {Dir: dir, Agent: agentID},
		"relative dir": {Dir: "a", Event: Stop, Agent: agentID},
		"missing dir":  {Dir: filepath.Join(dir, "missing"), Event: Stop, Agent: agentID},
	} {
		c.Now = t0
		if err := Hook(c); err == nil {
			t.Errorf("%s passed", name)
		}
	}
	if got := names(t, dir); len(got) != 0 {
		t.Fatalf("a refused call left %v", got)
	}
}

func TestADirectoryOthersMayChangeOrAFileInsteadIsRefused(t *testing.T) {
	root := realTemp(t)
	open := filepath.Join(root, "open")
	if err := os.Mkdir(open, 0o777); err != nil { //nolint:gosec // the test opens it on purpose
		t.Fatal(err)
	}
	if err := os.Chmod(open, 0o777); err != nil { //nolint:gosec // past the umask
		t.Fatal(err)
	}
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	private := filepath.Join(root, "private")
	if err := os.Mkdir(private, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(private, link); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{open, file, link} {
		if err := Hook(HookCall{Dir: dir, Event: Stop, Agent: agentID, Now: t0}); err == nil {
			t.Errorf("%s passed", dir)
		}
	}
	if got := names(t, open); len(got) != 0 {
		t.Errorf("the open directory got %v", got)
	}
	if got := names(t, private); len(got) != 0 {
		t.Errorf("the link's target got %v", got)
	}
}

// A link in the state file's place is not followed: its target stays as
// it was, the state file becomes a file of the hook's own.
func TestALinkInPlaceOfTheStateIsNotFollowed(t *testing.T) {
	dir := agentsDir(t)
	target := filepath.Join(realTemp(t), "target")
	if err := os.WriteFile(target, []byte("theirs"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, agentID+stateSuffix)); err != nil {
		t.Fatal(err)
	}
	hook(t, dir, Stop, `{}`, nil)
	if got, _ := os.ReadFile(target); string(got) != "theirs" { //nolint:gosec // the test's file
		t.Fatalf("the target became %q", got)
	}
	info, err := os.Lstat(filepath.Join(dir, agentID+stateSuffix))
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("%v %v", info.Mode(), err)
	}
	// A link in the lock's place takes no lock: nothing is written.
	dir = agentsDir(t)
	if err := os.Symlink(target, filepath.Join(dir, agentID+lockSuffix)); err != nil {
		t.Fatal(err)
	}
	if err := Hook(HookCall{Dir: dir, Event: Stop, Agent: agentID, Now: t0}); err == nil {
		t.Error("a link as the lock passed")
	}
	if _, err := os.Lstat(filepath.Join(dir, agentID+stateSuffix)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a state was written: %v", err)
	}
}

// CLAUDE_PID is a positive decimal number; anything else keeps the pid the
// state had.
func TestOnlyAValidPIDIsRecorded(t *testing.T) {
	dir := agentsDir(t)
	p := live(4242, 7)
	p.start[99] = 9
	hook(t, dir, SessionStart, `{}`, p)
	for _, pid := range []string{"", "0", "-1", "+99", "099", "99 ", "9x", "4294967296", "99999999999"} {
		c := HookCall{Dir: dir, Event: PreToolUse, Agent: agentID, PID: pid, Now: t0, Procs: p}
		if err := Hook(c); err != nil {
			t.Fatal(err)
		}
		if got := stateOf(t, dir); got.PID != 4242 || got.Start != 7 {
			t.Fatalf("pid %q: %+v", pid, got)
		}
	}
	// A pid whose process cannot be read keeps the old one too.
	if err := Hook(HookCall{Dir: dir, Event: PreToolUse, Agent: agentID, PID: "123", Now: t0, Procs: p}); err != nil {
		t.Fatal(err)
	}
	if got := stateOf(t, dir); got.PID != 4242 {
		t.Fatalf("%+v", got)
	}
	if err := Hook(HookCall{Dir: dir, Event: PreToolUse, Agent: agentID, PID: "99", Now: t0, Procs: p}); err != nil {
		t.Fatal(err)
	}
	if got := stateOf(t, dir); got.PID != 99 || got.Start != 9 {
		t.Fatalf("%+v", got)
	}
}

// Two hooks of one agent at once: each reads, changes and writes back
// under the lock, so neither loses what the other wrote (the pid of one,
// the session of the other).
func TestOverlappingHooksLoseNothing(t *testing.T) {
	// A slow disk under a loaded test run must not make a hook give up:
	// this is about the lock, the wait's bound is the next test's.
	saved := lockFor
	lockFor = 10 * time.Second
	t.Cleanup(func() { lockFor = saved })
	for round := range 8 {
		dir := agentsDir(t)
		p := live(4242, 7)
		p.delay = 20 * time.Millisecond // inside the lock: widens the window
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		wg.Add(2)
		go func() {
			defer wg.Done()
			errs <- Hook(HookCall{Dir: dir, Event: PreToolUse, Agent: agentID, PID: "4242", Now: t0, Procs: p})
		}()
		go func() {
			defer wg.Done()
			errs <- Hook(HookCall{Dir: dir, Event: UserPromptSubmit, Agent: agentID, Input: strings.NewReader(`{"session_id":"` + sessionID + `"}`), Now: t0, Procs: p})
		}()
		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("the hooks hang")
		}
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		if got := stateOf(t, dir); got.PID != 4242 || got.Session != sessionID {
			t.Fatalf("round %d lost an update: %+v", round, got)
		}
	}
}

// A lock another holds is waited for a short while, then the hook gives
// up and writes nothing.
func TestAHeldLockIsWaitedForBriefly(t *testing.T) {
	dir := agentsDir(t)
	unlock, err := privatefs.Lock(filepath.Join(dir, agentID+lockSuffix))
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	start := time.Now()
	if err := Hook(HookCall{Dir: dir, Event: Stop, Agent: agentID, Now: t0}); !errors.Is(err, privatefs.ErrLocked) {
		t.Fatalf("err %v", err)
	}
	if took := time.Since(start); took < lockFor || took > 3*time.Second {
		t.Fatalf("took %v", took)
	}
	if _, err := os.Lstat(filepath.Join(dir, agentID+stateSuffix)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a state was written: %v", err)
	}
}

func TestReadDerivesStartingAndLost(t *testing.T) {
	for _, c := range []struct {
		name  string
		hooks []string
		procs *procs
		want  State
	}{
		{"no hook yet", nil, live(4242, 7), Starting},
		{"working", []string{PreToolUse}, live(4242, 7), Working},
		{"waiting", []string{Stop}, live(4242, 7), WaitingForYou},
		{"approval", []string{PermissionRequest}, live(4242, 7), WaitingForApproval},
		{"ended", []string{SessionEnd}, &procs{}, Ended},
		{"gone", []string{PreToolUse}, &procs{start: map[int]int64{}}, Lost},
		{"pid reused", []string{Stop}, live(4242, 8), Lost},
		{"unknown", []string{PreToolUse}, &procs{err: errors.New("no sysctl")}, Working},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := agentsDir(t)
			if err := WriteLaunch(dir, launchOf(t0.Add(-time.Hour))); err != nil {
				t.Fatal(err)
			}
			for _, e := range c.hooks {
				hook(t, dir, e, `{}`, live(4242, 7))
			}
			got, err := Read(dir, c.procs)
			if err != nil || len(got) != 1 || got[0].State != c.want {
				t.Fatalf("%+v %v, want %d", got, err, c.want)
			}
			since := t0
			if c.hooks == nil {
				since = t0.Add(-time.Hour)
			}
			if !got[0].Since.Equal(since) {
				t.Fatalf("since %v", got[0].Since)
			}
		})
	}
}

// The session's latest id is the state's; a state without one keeps the
// launch's.
func TestReadTakesTheLatestSession(t *testing.T) {
	dir := agentsDir(t)
	if err := WriteLaunch(dir, launchOf(t0)); err != nil {
		t.Fatal(err)
	}
	hook(t, dir, Stop, `{}`, nil)
	if got, _ := Read(dir, nil); len(got) != 1 || got[0].Session != sessionID {
		t.Fatalf("%+v", got)
	}
	hook(t, dir, SessionStart, `{"session_id":"`+cleared+`"}`, nil)
	if got, _ := Read(dir, nil); len(got) != 1 || got[0].Session != cleared {
		t.Fatalf("%+v", got)
	}
}

// A file that fails a check is its agent's failure alone.
func TestABrokenFileFailsItsAgentAlone(t *testing.T) {
	dir := agentsDir(t)
	good := launchOf(t0)
	if err := WriteLaunch(dir, good); err != nil {
		t.Fatal(err)
	}
	others := map[string]string{
		"1f8fad5b-d9cb-469f-a165-70867728950e": "not json",
		"2f8fad5b-d9cb-469f-a165-70867728950e": `{"format":2}`,
		// The record of another agent under this name.
		"3f8fad5b-d9cb-469f-a165-70867728950e": `{"format":1,"agent":"` + agentID + `"}`,
		"4f8fad5b-d9cb-469f-a165-70867728950e": strings.Repeat(" ", maxFile+1),
	}
	for id, data := range others {
		if err := os.WriteFile(filepath.Join(dir, id+launchSuffix), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Open to others, a link, a directory.
	open := launchOf(t0)
	open.Agent = "5f8fad5b-d9cb-469f-a165-70867728950e"
	if err := WriteLaunch(dir, open); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dir, open.Agent+launchSuffix), 0o644); err != nil { //nolint:gosec // the test opens it on purpose
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, agentID+launchSuffix), filepath.Join(dir, "6f8fad5b-d9cb-469f-a165-70867728950e"+launchSuffix)); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "7f8fad5b-d9cb-469f-a165-70867728950e"+launchSuffix), 0o700); err != nil {
		t.Fatal(err)
	}
	// A broken state of the good agent: it is starting.
	if err := os.WriteFile(filepath.Join(dir, agentID+stateSuffix), []byte(`{"format":1,"state":"sleeping"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Read(dir, nil)
	if err != nil || len(got) != 1 || got[0].ID != agentID || got[0].State != Starting {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestReadIsBounded(t *testing.T) {
	dir := agentsDir(t)
	for i := range maxWalk + 1 {
		if err := os.WriteFile(filepath.Join(dir, "x"+strconv.Itoa(i)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Read(dir, nil); err == nil {
		t.Fatal("a directory beyond the walk passed")
	}
}

func TestReadWithoutADirectory(t *testing.T) {
	for _, dir := range []string{"", "relative", filepath.Join(realTemp(t), "missing")} {
		if got, err := Read(dir, nil); err != nil || got != nil {
			t.Errorf("%q: %v %v", dir, got, err)
		}
	}
	root := realTemp(t)
	open := filepath.Join(root, Dir)
	if err := os.Mkdir(open, 0o777); err != nil { //nolint:gosec // the test opens it on purpose
		t.Fatal(err)
	}
	if err := os.Chmod(open, 0o777); err != nil { //nolint:gosec // past the umask
		t.Fatal(err)
	}
	if _, err := Read(open, nil); err == nil {
		t.Error("a directory others may change was read")
	}
}

// The urgency is the order of the states.
func TestUrgency(t *testing.T) {
	order := []State{WaitingForApproval, WaitingForYou, Lost, Working, Starting, Ended}
	for i := range order {
		for j := range order {
			if order[i].Urgent(order[j]) != (i < j) {
				t.Errorf("%d before %d: %v", order[i], order[j], order[i].Urgent(order[j]))
			}
		}
	}
}

// A background subagent's tools come with its agent_id while the main
// session may wait for the person: an approval or a question still open
// stays, the subagent's tool events change nothing then. Its tools while
// the session works keep it working; the main session's own tool after an
// approval works again.
func TestASubagentsToolsKeepAnOpenWait(t *testing.T) {
	const sub = `{"agent_id":"a1b2c3","agent_type":"Explore","tool_name":"Read"}`
	for _, c := range []struct {
		name         string
		before       string
		event, input string
		want         string
	}{
		{"approval, subagent's post", PermissionRequest, PostToolUse, sub, "approval"},
		{"approval, subagent's pre", PermissionRequest, PreToolUse, sub, "approval"},
		{"question, subagent's post", PermissionRequest, PostToolUse, sub, "approval"},
		{"waiting, subagent's pre", Stop, PreToolUse, sub, "waiting"},
		{"approval, the session's own post", PermissionRequest, PostToolUse, `{"tool_name":"Write"}`, "working"},
		{"approval, an empty agent_id", PermissionRequest, PostToolUse, `{"agent_id":""}`, "working"},
		{"working, subagent's post", UserPromptSubmit, PostToolUse, sub, "working"},
		{"approval, a new prompt", PermissionRequest, UserPromptSubmit, `{}`, "working"},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := agentsDir(t)
			hook(t, dir, c.before, `{}`, live(4242, 7))
			before := stateOf(t, dir)
			hook(t, dir, c.event, c.input, live(4242, 7))
			got := stateOf(t, dir)
			if got.State != c.want {
				t.Fatalf("%s after %s: %+v, want %s", c.event, c.before, got, c.want)
			}
			if got.State == before.State && !got.Changed.Equal(before.Changed) {
				t.Fatalf("the wait's time moved: %v to %v", before.Changed, got.Changed)
			}
		})
	}
}
