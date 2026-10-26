package main

import (
	"flag"
	"io"
	"time"

	"github.com/gboyke/lalabase-cli/internal/agents"
)

// runHook is `lalabase hook --dir <agents dir> --event <event> <agent id>`,
// the command a launch's settings file hands Claude Code for each event
// (C9a). It runs inside Claude Code's turn, so it never writes to stdout
// or stderr and always exits 0, on its own errors and on a panic too:
// stdout of SessionStart and UserPromptSubmit reaches the model, exit 2
// blocks a tool or drops a prompt, any other code shows the person an
// error after every answer. It reads no profile, no config and no token,
// asks no server, and of the environment CLAUDE_PID alone (a messaging
// token lies beside it). What it does is internal/agents' Hook.
func runHook(args []string, env Env) (code int) {
	defer func() {
		_ = recover()
		code = 0
	}()
	flags := flag.NewFlagSet("hook", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dir := flags.String("dir", "", "")
	event := flags.String("event", "", "")
	if flags.Parse(args) != nil || flags.NArg() != 1 {
		return 0
	}
	// Claude Code pipes the event's JSON in; a person who types the
	// command at a terminal would leave it waiting for input.
	var input io.Reader
	if !env.Interactive {
		input = env.Stdin
	}
	procs := env.procs
	if procs == nil {
		procs = agents.System
	}
	_ = agents.Hook(agents.HookCall{
		Dir: *dir, Event: *event, Agent: flags.Arg(0), Input: input,
		PID: env.Getenv("CLAUDE_PID"), Now: time.Now(), Procs: procs,
	})
	return 0
}
