package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/diskcache"
	"github.com/gboyke/lalabase-cli/internal/gateway"
	"github.com/gboyke/lalabase-cli/internal/profile"
	"github.com/gboyke/lalabase-cli/internal/queue"
	"github.com/gboyke/lalabase-cli/internal/terminal"
	"github.com/gboyke/lalabase-cli/internal/transport"
	"github.com/gboyke/lalabase-cli/internal/ui"
	"github.com/gboyke/lalabase-cli/internal/uitext"
	"github.com/gboyke/lalabase-cli/internal/worker"
)

// workerCleanup bounds the wait for the session's work after the terminal
// is restored: work that ignores its cancellation is left behind.
const workerCleanup = time.Second

func runInteractive(name string, env Env) int {
	if name != "" && name != profile.DefaultName {
		fmt.Fprintln(env.Stderr, uitext.Inline(uitext.ProfileFlagError).String())
		return 2
	}
	if !env.Interactive || !env.OutputTerminal {
		fmt.Fprintln(env.Stderr, uitext.Inline(uitext.TTYError).String())
		return 2
	}
	if !terminal.UTF8(env.Getenv) {
		fmt.Fprintln(env.Stderr, uitext.Inline(uitext.LocaleError).String())
		return 2
	}
	p, err := profile.Resolve(profile.Interactive, name, env.Getenv, profile.Path(env.Getenv, env.Home))
	if err != nil {
		fmt.Fprintln(env.Stderr, uitext.Inline(uitext.ProfileError).String())
		return 1
	}
	// The transport checks the address and the token before any terminal
	// mode is set: a profile the client cannot use ends here, in plain text.
	client, err := transport.New(p.URL, p.Token, transport.WithVersion(buildVersion()))
	if err != nil {
		fmt.Fprintln(env.Stderr, uitext.Inline(uitext.ProfileError).String())
		return 1
	}
	ctx := env.Context
	if ctx == nil {
		ctx = context.Background()
	}
	environment := []string{}
	for _, key := range []string{"TERM", "TERM_PROGRAM", "COLORTERM", "NO_COLOR", "CLICOLOR", "CLICOLOR_FORCE", "SSH_TTY", "WT_SESSION"} {
		if value := env.Getenv(key); value != "" {
			environment = append(environment, key+"="+value)
		}
	}
	run := env.runUI
	if run == nil {
		run = func(ctx context.Context, in io.Reader, out io.Writer, environment []string, sup *worker.Supervisor) error {
			// The session: one mailbox, one queue under the session's lifetime
			// (every 429 reaches the interface as a message, never stderr),
			// the adapters, the gateway and the startup commands, all
			// observing the mailbox.
			ev := gateway.NewEvents(sup)
			q := queue.New(queue.WithLifetime(sup), queue.WithOnRateLimit(ev.RateLimit))
			a := api.New(client)
			return ui.Run(ctx, in, out, environment, ui.New(ui.WithSupervisor(sup), ui.WithSession(ui.Session{
				Startup: gateway.NewStartup(sup, q, client, a, ev),
				Gateway: gateway.New(sup, q, a, gateway.WithEvents(ev)),
				Events:  ev,
				Profile: p.Name,
				Server:  p.URL,
				// The disk cache (C1e): under the user's cache directory,
				// opened only after /me confirmed the identity.
				Disk: &diskcache.Opener{Root: diskcache.Root(env.Getenv, env.Home)},
			})), sup)
		}
	}
	sup := worker.New(ctx)
	err = run(ctx, env.Stdin, env.Stdout, environment, sup)
	// The terminal is restored here. A fatal error counts even when its
	// message lost the race against the person quitting.
	sup.Stop(workerCleanup)
	if sup.Err() != nil {
		fmt.Fprintln(env.Stderr, uitext.Inline(uitext.RunError).String())
		return 1
	}
	if err != nil {
		if errors.Is(err, tea.ErrInterrupted) {
			return 130
		}
		fmt.Fprintln(env.Stderr, uitext.Inline(uitext.RunError).String())
		return 1
	}
	return 0
}
