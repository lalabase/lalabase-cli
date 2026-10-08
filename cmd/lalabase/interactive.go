package main

import (
	"context"
	"errors"
	"fmt"
	"io"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/profile"
	"github.com/gboyke/lalabase-cli/internal/terminal"
	"github.com/gboyke/lalabase-cli/internal/ui"
	"github.com/gboyke/lalabase-cli/internal/uitext"
)

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
	if _, err := profile.Resolve(profile.Interactive, name, env.Getenv, profile.Path(env.Getenv, env.Home)); err != nil {
		fmt.Fprintln(env.Stderr, uitext.Inline(uitext.ProfileError).String())
		return 1
	}
	run := env.runUI
	if run == nil {
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
		run = func(in io.Reader, out io.Writer) error { return ui.Run(ctx, in, out, environment, ui.New()) }
	}
	if err := run(env.Stdin, env.Stdout); err != nil {
		if errors.Is(err, tea.ErrInterrupted) {
			return 130
		}
		fmt.Fprintln(env.Stderr, uitext.Inline(uitext.RunError).String())
		return 1
	}
	return 0
}
