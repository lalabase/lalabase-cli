package main

import (
	"context"
	"reflect"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/launch"
	"github.com/gboyke/lalabase-cli/internal/profile"
	"github.com/gboyke/lalabase-cli/internal/worker"
)

// Review round 3 of #22: the launch gets every value it reads from the
// command's Env and the profile, the variables of iTerm, of Claude Code's
// configuration and of a multiplexer among them.
func TestTheLaunchReadsTheCommandsEnvironment(t *testing.T) {
	sup := worker.New(context.Background())
	defer sup.Stop(0)
	folders := map[string]string{"p": "~/dev/p"}
	vars := map[string]string{
		"TERM_PROGRAM": "iTerm.app", "ITERM_SESSION_ID": "w0t1p2:FCB46015-2039-451D-82C2-59CE4353910D",
		"CLAUDE_CONFIG_DIR": "/Users/anna/.claude-work",
	}
	env := Env{Home: "/Users/anna", Cwd: "/Users/anna/dev", Getenv: func(k string) string { return vars[k] }}
	p := profile.Profile{URL: "https://lalabase.com", Folders: folders}
	got := launchConfig(env, p, sup)
	want := launch.Config{
		Supervisor: sup, Home: "/Users/anna", Cwd: "/Users/anna/dev", Server: "https://lalabase.com", Folders: folders,
		TermProgram: "iTerm.app", SessionID: "w0t1p2:FCB46015-2039-451D-82C2-59CE4353910D",
		ClaudeConfigDir: "/Users/anna/.claude-work",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("config\n got %+v\nwant %+v", got, want)
	}
	for _, key := range []string{"TMUX", "STY", "ZELLIJ"} {
		vars[key] = "1"
		if got := launchConfig(env, p, sup); got.Multiplexer != key {
			t.Errorf("%s: multiplexer %q", key, got.Multiplexer)
		}
		delete(vars, key)
	}
}
