//go:build unix

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gboyke/lalabase-cli/internal/agents"
)

const (
	hookAgent   = "0f8fad5b-d9cb-469f-a165-70867728950e"
	hookSession = "7c9e6679-7425-40de-944b-e07fc1f90ae7"
	// messagingToken stands for the token Claude Code puts into a hook's
	// environment, which the hook must never read.
	messagingToken = "MESSAGING-TOKEN-SECRET"
)

// hookProcs is a process table where every pid lives with start time 7,
// or panics when asked.
type hookProcs struct{ panics bool }

func (p hookProcs) Start(int) (int64, bool, error) {
	if p.panics {
		panic("inside the hook")
	}
	return 7, true, nil
}

// hookEnv is an Env for `lalabase hook`: stdin the input, the environment
// a map whose every read is recorded.
type hookEnv struct {
	mu   sync.Mutex
	read []string
	vars map[string]string
}

func (h *hookEnv) getenv(key string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.read = append(h.read, key)
	return h.vars[key]
}

func runHookCase(t *testing.T, input string, procs agents.Procs, pid string, args ...string) (stdout, stderr string, code int, read []string) {
	t.Helper()
	h := &hookEnv{vars: map[string]string{
		"CLAUDE_PID": pid, "CLAUDE_CODE_MESSAGING_TOKEN": messagingToken, "LALABASE_TOKEN": messagingToken,
		"HOME": t.TempDir(), "XDG_CACHE_HOME": t.TempDir(), "XDG_CONFIG_HOME": t.TempDir(),
	}}
	var out, errOut bytes.Buffer
	env := Env{Stdin: strings.NewReader(input), Stdout: &out, Stderr: &errOut, Getenv: h.getenv, Home: t.TempDir(), procs: procs}
	code = runWith(append([]string{"hook"}, args...), env)
	return out.String(), errOut.String(), code, h.read
}

func hookDir(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "default", agents.Dir)
	if err := agents.Prepare(dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

func stateFile(dir string) string { return filepath.Join(dir, hookAgent+".state.json") }

// The hook writes nothing to either stream and exits 0 in every case, its
// own failures and a panic included; what it records is checked beside.
func TestTheHookIsSilentAndExitsZero(t *testing.T) {
	good := `{"session_id":"` + hookSession + `","hook_event_name":"Stop","background_tasks":[]}`
	for _, c := range []struct {
		name   string
		input  string
		procs  agents.Procs
		pid    string
		args   func(dir string) []string
		setup  func(t *testing.T, dir string)
		wantOK string // the state recorded, "" for no state file
	}{
		{name: "good input", input: good, wantOK: "waiting"},
		{name: "broken JSON", input: `{"session_id":`, wantOK: "waiting"},
		{name: "too large", input: `{"background_tasks":[{"status":"running"}],"x":"` + strings.Repeat("x", agents.MaxInput) + `"}`, wantOK: "waiting"},
		{name: "no input", wantOK: "waiting"},
		{name: "missing directory", args: func(dir string) []string {
			return []string{"--dir", filepath.Join(dir, "missing"), "--event", "Stop", hookAgent}
		}},
		{name: "relative directory", args: func(string) []string { return []string{"--dir", "a", "--event", "Stop", hookAgent} }},
		{name: "directory open to others", setup: func(t *testing.T, dir string) {
			if err := os.Chmod(dir, 0o777); err != nil { //nolint:gosec // the test opens it on purpose
				t.Fatal(err)
			}
		}},
		{name: "a link in the state's place", setup: func(t *testing.T, dir string) {
			if err := os.Symlink(filepath.Join(t.TempDir(), "elsewhere"), stateFile(dir)); err != nil {
				t.Fatal(err)
			}
		}, wantOK: "waiting"},
		{name: "a link in the lock's place", setup: func(t *testing.T, dir string) {
			if err := os.Symlink(filepath.Join(t.TempDir(), "elsewhere"), filepath.Join(dir, hookAgent+".lock")); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "invalid agent id", args: func(dir string) []string { return []string{"--dir", dir, "--event", "Stop", "../" + hookAgent} }},
		{name: "network permission notification", input: `{"notification_type":"permission_prompt"}`, wantOK: "approval", args: func(dir string) []string {
			return []string{"--dir", dir, "--event", "Notification", hookAgent}
		}},
		{name: "unrelated notification", input: `{"notification_type":"idle_prompt"}`, args: func(dir string) []string {
			return []string{"--dir", dir, "--event", "Notification", hookAgent}
		}},
		{name: "unknown event", args: func(dir string) []string { return []string{"--dir", dir, "--event", "UnknownEvent", hookAgent} }},
		{name: "unknown flag", args: func(dir string) []string { return []string{"--dir", dir, "--event", "Stop", "--x", hookAgent} }},
		{name: "two agents", args: func(dir string) []string { return []string{"--dir", dir, "--event", "Stop", hookAgent, hookAgent} }},
		{name: "no arguments", args: func(string) []string { return nil }},
		{name: "invalid CLAUDE_PID", pid: "-1", wantOK: "waiting"},
		{name: "panic inside", procs: hookProcs{panics: true}},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := hookDir(t)
			if c.setup != nil {
				c.setup(t, dir)
			}
			args := []string{"--dir", dir, "--event", "Stop", hookAgent}
			if c.args != nil {
				args = c.args(dir)
			}
			procs, pid := c.procs, c.pid
			if procs == nil {
				procs = hookProcs{}
			}
			if pid == "" {
				pid = "4242"
			}
			stdout, stderr, code, _ := runHookCase(t, c.input, procs, pid, args...)
			if stdout != "" || stderr != "" || code != 0 {
				t.Fatalf("stdout %q, stderr %q, exit %d", stdout, stderr, code)
			}
			data, err := os.ReadFile(stateFile(dir)) //nolint:gosec // the test's file
			if c.wantOK == "" {
				if err == nil {
					t.Fatalf("a state was recorded: %s", data)
				}
				return
			}
			var rec struct {
				State string `json:"state"`
				PID   int    `json:"pid"`
			}
			if err != nil || json.Unmarshal(data, &rec) != nil || rec.State != c.wantOK {
				t.Fatalf("%s %v", data, err)
			}
			if wantPID := 4242; c.pid == "-1" {
				if rec.PID != 0 {
					t.Fatalf("pid %d recorded from %q", rec.PID, c.pid)
				}
			} else if rec.PID != wantPID {
				t.Fatalf("pid %d", rec.PID)
			}
		})
	}
}

// The hook reads CLAUDE_PID and nothing else of the environment: not the
// messaging token beside it, no profile, no token, no cache root (the
// directory comes from the command line). Nothing of the input or the
// environment reaches a file but the six facts and the pid.
func TestTheHookReadsOnlyCLAUDEPID(t *testing.T) {
	dir := hookDir(t)
	input, err := json.Marshal(map[string]any{
		"session_id": hookSession, "prompt": "PROMPT-SECRET", "cwd": "/home/someone/private",
		"tool_name": "\x1b]52;c;cGFzdGU=\x07", "token": messagingToken,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range agents.Events {
		stdout, stderr, code, read := runHookCase(t, string(input), hookProcs{}, "4242", "--dir", dir, "--event", event, hookAgent)
		if stdout != "" || stderr != "" || code != 0 {
			t.Fatalf("%s: stdout %q, stderr %q, exit %d", event, stdout, stderr, code)
		}
		if !slices.Equal(read, []string{"CLAUDE_PID"}) {
			t.Fatalf("%s read the environment's %v", event, read)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(dir, e.Name())) //nolint:gosec // the test's file
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range []string{"PROMPT-SECRET", "someone", "cGFzdGU", messagingToken} {
			if strings.Contains(string(data), s) {
				t.Errorf("%s holds %q", e.Name(), s)
			}
		}
	}
}

// A person typing the command at a terminal is not left waiting for
// input: an interactive stdin is not read.
func TestTheHookDoesNotReadATerminal(t *testing.T) {
	dir := hookDir(t)
	h := &hookEnv{vars: map[string]string{"CLAUDE_PID": "4242"}}
	var out, errOut bytes.Buffer
	env := Env{Stdin: blockingReader{t}, Stdout: &out, Stderr: &errOut, Getenv: h.getenv, Interactive: true, procs: hookProcs{}}
	if code := runWith([]string{"hook", "--dir", dir, "--event", "Stop", hookAgent}, env); code != 0 || out.Len() != 0 || errOut.Len() != 0 {
		t.Fatalf("exit %d, %q, %q", code, out.String(), errOut.String())
	}
}

type blockingReader struct{ t *testing.T }

func (b blockingReader) Read([]byte) (int, error) {
	b.t.Error("a terminal was read")
	return 0, errors.New("no")
}

// hookChildEnv makes the test binary run main() as `lalabase hook`.
const hookChildEnv = "C9A_HOOK_CHILD"

func TestHookChild(t *testing.T) {
	if os.Getenv(hookChildEnv) == "" {
		return
	}
	os.Args = append([]string{"lalabase"}, strings.Fields(os.Getenv(hookChildEnv))...)
	main()
}

// The real program: main() as Claude Code runs it, with pipes for the
// streams. It writes no byte to either and exits 0 whatever it is given,
// and records the state.
func TestTheProgramIsSilent(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := hookDir(t)
	for _, c := range []struct{ args, input string }{
		{"hook --dir " + dir + " --event Stop " + hookAgent, `{"session_id":"` + hookSession + `"}`},
		{"hook --dir " + dir + " --event Stop " + hookAgent, `not json`},
		{"hook --dir " + dir + "/missing --event Stop " + hookAgent, `{}`},
		{"hook --dir " + dir + " --event Bogus " + hookAgent, `{}`},
		{"hook --bogus", ``},
		{"hook", ``},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		cmd := exec.CommandContext(ctx, executable, "-test.run=^TestHookChild$", "-test.count=1")
		cmd.Env = append(os.Environ(), hookChildEnv+"="+c.args, "CLAUDE_PID=1", "CLAUDE_CODE_MESSAGING_TOKEN="+messagingToken)
		cmd.Stdin = strings.NewReader(c.input)
		var out, errOut bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errOut
		start := time.Now()
		err := cmd.Run()
		cancel()
		if err != nil || out.Len() != 0 || errOut.Len() != 0 {
			t.Fatalf("%q: %v, stdout %q, stderr %q", c.args, err, out.String(), errOut.String())
		}
		if took := time.Since(start); took > 5*time.Second {
			t.Errorf("%q took %v", c.args, took)
		}
	}
	data, err := os.ReadFile(stateFile(dir)) //nolint:gosec // the test's file
	if err != nil || !strings.Contains(string(data), `"state":"waiting"`) || !strings.Contains(string(data), hookSession) {
		t.Fatalf("%s %v", data, err)
	}
}
