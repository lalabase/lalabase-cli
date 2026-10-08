package main

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/worker"
)

// The fatal error is read after the interface returned: a panic whose message
// lost the race against "q" or Ctrl-C still ends with exit 1, with the one
// safe text and nothing of the panic.
func TestAFatalErrorWinsOverHowTheInterfaceEnded(t *testing.T) {
	for name, ended := range map[string]error{"quit": nil, "interrupt": tea.ErrInterrupted, "killed": tea.ErrProgramKilled} {
		env, errOut := interactiveEnv(t, map[string]string{"LALABASE_URL": "http://127.0.0.1:1", "LALABASE_TOKEN": "synthetic"})
		env.runUI = func(_ context.Context, _ io.Reader, _ io.Writer, _ []string, sup *worker.Supervisor) error {
			worker.Run(sup, "gateway: inbox", func() int { panic("lalab_pat_secret") })
			return ended
		}
		if code := runInteractive("", env); code != 1 {
			t.Errorf("%s: exit %d", name, code)
		}
		if got := errOut.String(); got != "lalabase: terminal interface failed.\n" {
			t.Errorf("%s: printed %q", name, got)
		}
	}
	for name, c := range map[string]struct {
		ended error
		code  int
	}{"quit": {nil, 0}, "interrupt": {tea.ErrInterrupted, 130}, "failed": {errors.New("x"), 1}} {
		env, _ := interactiveEnv(t, map[string]string{"LALABASE_URL": "http://127.0.0.1:1", "LALABASE_TOKEN": "synthetic"})
		env.runUI = func(context.Context, io.Reader, io.Writer, []string, *worker.Supervisor) error { return c.ended }
		if code := runInteractive("", env); code != c.code {
			t.Errorf("%s without a fatal error: exit %d", name, code)
		}
	}
}

// The session's work ends with the interface; work that ignores that does
// not hold the exit longer than the cleanup limit.
func TestTheSessionsWorkEndsWithTheInterface(t *testing.T) {
	env, _ := interactiveEnv(t, map[string]string{"LALABASE_URL": "http://127.0.0.1:1", "LALABASE_TOKEN": "synthetic"})
	ended, block := make(chan struct{}), make(chan struct{})
	defer close(block)
	env.runUI = func(_ context.Context, _ io.Reader, _ io.Writer, _ []string, sup *worker.Supervisor) error {
		sup.Start("follows", func(ctx context.Context) { <-ctx.Done(); close(ended) })
		sup.Start("ignores", func(context.Context) { <-block })
		return nil
	}
	start := time.Now()
	if code := runInteractive("", env); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if took := time.Since(start); took < workerCleanup || took > workerCleanup+time.Second {
		t.Fatalf("returned after %s", took)
	}
	select {
	case <-ended:
	default:
		t.Fatal("the session's work was not canceled")
	}
}
