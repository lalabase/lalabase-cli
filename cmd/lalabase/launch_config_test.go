package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/gboyke/lalabase-cli/internal/launch"
	"github.com/gboyke/lalabase-cli/internal/launchdir"
	"github.com/gboyke/lalabase-cli/internal/profile"
	"github.com/gboyke/lalabase-cli/internal/worker"
)

// Review round 3 of #22: the launch gets every value it reads from the
// command's Env and the profile, the variables of iTerm and of a
// multiplexer among them. Since C7b it gets the MCP address and the token,
// and the directory of its files beside the disk cache; since C9a the
// agents' directory beside those and the program's own path.
func TestTheLaunchReadsTheCommandsEnvironment(t *testing.T) {
	sup := worker.New(context.Background())
	defer sup.Stop(0)
	folders := map[string]string{"p": "~/dev/p"}
	vars := map[string]string{
		"TERM_PROGRAM": "iTerm.app", "ITERM_SESSION_ID": "w0t1p2:FCB46015-2039-451D-82C2-59CE4353910D",
		// Claude Code's own variable: not read since C7b.
		"CLAUDE_CONFIG_DIR": "/Users/anna/.claude-work",
	}
	env := Env{Home: "/Users/anna", Cwd: "/Users/anna/dev", Executable: "/opt/lalabase/bin/lalabase", Getenv: func(k string) string { return vars[k] }}
	p := profile.Profile{Name: "default", URL: "https://lalabase.com", Token: "lalab_pat_X", Folders: folders}
	const server, mcp = "https://lalabase.com", "https://lalabase.com/api/mcp"
	got := launchConfig(env, p, server, mcp, sup)
	want := launch.Config{
		Supervisor: sup, Home: "/Users/anna", Cwd: "/Users/anna/dev", MCP: mcp, Token: "lalab_pat_X", Folders: folders,
		Files: "/Users/anna/.cache/lalabase/cli/default/l",
		// The agents of the profile on its server: 16 hex digits of the
		// SHA-256 of "https://lalabase.com".
		Agents: "/Users/anna/.cache/lalabase/cli/default/a/7e64fbe1cb7bbe56", Executable: "/opt/lalabase/bin/lalabase",
		TermProgram: "iTerm.app", SessionID: "w0t1p2:FCB46015-2039-451D-82C2-59CE4353910D",
		Server: server,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("config\n got %+v\nwant %+v", got, want)
	}
	// Over SSH a browser would open on a screen the person does not see.
	vars["SSH_CONNECTION"] = "192.168.0.1 22 192.168.0.2 22"
	if got := launchConfig(env, p, server, mcp, sup); !got.Remote {
		t.Fatal("SSH_CONNECTION is not read")
	}
	delete(vars, "SSH_CONNECTION")
	// XDG_CACHE_HOME moves the files with the disk cache; a profile name
	// that cannot name a directory has none.
	vars["XDG_CACHE_HOME"] = "/Users/anna/caches"
	if got := launchConfig(env, p, server, mcp, sup); got.Files != "/Users/anna/caches/lalabase/cli/default/l" || got.Agents != "/Users/anna/caches/lalabase/cli/default/a/7e64fbe1cb7bbe56" {
		t.Errorf("files %q, agents %q", got.Files, got.Agents)
	}
	delete(vars, "XDG_CACHE_HOME")
	if got := launchConfig(env, profile.Profile{Name: "../x"}, server, mcp, sup); got.Files != "" || got.Agents != "" {
		t.Errorf("files %q, agents %q for a bad profile name", got.Files, got.Agents)
	}
	// Review round 6, F6: another terminal is passed on as it is.
	vars["TERM_PROGRAM"] = "Apple_Terminal"
	if got := launchConfig(env, p, server, mcp, sup); got.TermProgram != "Apple_Terminal" {
		t.Errorf("terminal %q", got.TermProgram)
	}
	vars["TERM_PROGRAM"] = "iTerm.app"
	for _, key := range []string{"TMUX", "STY", "ZELLIJ"} {
		vars[key] = "1"
		if got := launchConfig(env, p, server, mcp, sup); got.Multiplexer != key {
			t.Errorf("%s: multiplexer %q", key, got.Multiplexer)
		}
		delete(vars, key)
	}
}

// Review round 2 of #33, F2: when the client quits, the launch files older
// than a day go and younger ones stay. They are made while the interface
// runs, so the removal must come after it.
func TestQuittingPrunesOldLaunchFiles(t *testing.T) {
	env, errOut := interactiveEnv(t, map[string]string{"LALABASE_URL": "http://127.0.0.1:1", "LALABASE_TOKEN": "dummy"})
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	env.Home = home
	root := filepath.Join(home, ".cache", "lalabase", "cli", "env", "l")
	var old, young string
	env.runUI = func(context.Context, io.Reader, io.Writer, []string, *worker.Supervisor) error {
		for _, path := range []*string{&old, &young} {
			d, err := launchdir.Make(root, time.Now())
			if err != nil {
				return err
			}
			if _, err := d.Write(launchdir.MCPConfig, []byte("{}"), 0o600); err != nil {
				return err
			}
			*path = d.Path
		}
		then := time.Now().Add(-launchdir.MaxAge - time.Hour)
		return os.Chtimes(old, then, then)
	}
	if code := runInteractive("", env); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if _, err := os.Lstat(old); !os.IsNotExist(err) {
		t.Errorf("the old launch directory stayed: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(young, launchdir.MCPConfig)); err != nil {
		t.Errorf("the young launch directory went: %v", err)
	}
}
