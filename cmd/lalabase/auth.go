package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/claudecfg"
	"github.com/gboyke/lalabase-cli/internal/clean"
	"github.com/gboyke/lalabase-cli/internal/features"
	"github.com/gboyke/lalabase-cli/internal/profile"
	"github.com/gboyke/lalabase-cli/internal/queue"
	"github.com/gboyke/lalabase-cli/internal/transport"
)

// requestTimeout bounds the few requests auth makes.
const requestTimeout = 30 * time.Second

// errChangedMeanwhile: the profile is no longer what the person was asked
// about.
var errChangedMeanwhile = errors.New("changed meanwhile")

func runAuth(args []string, globalProfile string, env Env) int {
	if len(args) == 0 {
		fmt.Fprint(env.Stderr, usage)
		return 2
	}
	switch args[0] {
	case "import":
		return authImport(args[1:], globalProfile, env)
	case "status":
		return authStatus(args[1:], globalProfile, env)
	}
	fmt.Fprintf(env.Stderr, "lalabase: unknown auth command %q\n\n%s", args[0], usage)
	return 2
}

// show cleans foreign text for one line of output.
func show(s string) string { return clean.Inline(s) }

// maxAnswer bounds one answer: a number or a yes. A longer line is no
// answer, and is not read to its end (which may never come).
const maxAnswer = 64

// asker reads answers from stdin, one line each.
type asker struct {
	in  *bufio.Reader
	out io.Writer
}

func (a asker) ask(question string) string {
	fmt.Fprint(a.out, question)
	var line []byte
	for len(line) <= maxAnswer {
		b, err := a.in.ReadByte()
		if err != nil || b == '\n' {
			return strings.TrimSpace(string(line))
		}
		line = append(line, b)
	}
	return ""
}

func (a asker) yes(question string) bool {
	answer := strings.ToLower(a.ask(question + " [y/N] "))
	return answer == "y" || answer == "yes"
}

// newQueue: auth's requests go through the queue like every other request,
// so a 429 holds back what follows instead of running into the window.
func newQueue(env Env) *queue.Queue {
	return queue.New(queue.WithOnPause(func(until time.Time) {
		fmt.Fprintf(env.Stderr, "The server asks to wait; continuing at %s.\n", until.Local().Format("15:04:05"))
	}))
}

// through runs one of the person's reads through the queue.
func through(ctx context.Context, q *queue.Queue, run func(context.Context) transport.Answer) transport.Answer {
	return q.Do(ctx, queue.Job{Priority: queue.User, Kind: queue.Read, Run: run})
}

// refusal says why a check of the token brought no answer: a rejection by the
// server is something else than a request that did not get through.
func refusal(ans transport.Answer) string {
	if ans.Class == transport.Unauthenticated {
		return fmt.Sprintf("the server did not accept the token (%s): %s", ans.Class, show(ans.Message))
	}
	return fmt.Sprintf("the token could not be checked (%s): %s", ans.Class, show(ans.Message))
}

func newAPI(baseURL, token string) (*api.API, *transport.Client, error) {
	c, err := transport.New(baseURL, token, transport.WithVersion(buildVersion()))
	if err != nil {
		return nil, nil, err
	}
	return api.New(c), c, nil
}

func mayWrite(me api.Me) string {
	switch {
	case me.Token.ReadOnly == nil:
		return "unknown (the server does not say)"
	case *me.Token.ReadOnly:
		return "no (read-only)"
	}
	return "yes"
}

// authImport takes a server and token from Claude Code's MCP configuration
// into a profile. It always asks which entry, checks the token with the
// server before saving, and never sets allow_write.
func authImport(args []string, globalProfile string, env Env) int {
	fs := flag.NewFlagSet("auth import", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	agent := fs.Bool("agent", false, "save as the profile for agent commands")
	name := fs.String("profile", globalProfile, "profile name to save as")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	target := *name
	if target == "" {
		target = profile.DefaultName
		if *agent {
			target = profile.AgentName
		}
	}
	// The profile "agent" is the agent's, whichever flag named it.
	forAgent := *agent || target == profile.AgentName

	entries, errs := claudecfg.Find(env.Home, env.Cwd)
	for _, err := range errs {
		fmt.Fprintf(env.Stderr, "note: %s\n", show(err.Error()))
	}
	var usable []claudecfg.Entry
	var unusable []claudecfg.Entry
	for _, e := range entries {
		if e.Usable() {
			usable = append(usable, e)
		} else {
			unusable = append(unusable, e)
		}
	}
	if len(entries) == 0 {
		fmt.Fprint(env.Stderr, "No Lalabase server in Claude Code's MCP configuration.\n"+
			"Create an access token in Lalabase, then register the server:\n"+
			"  claude mcp add --transport http lalabase https://<server>/api/mcp --header \"Authorization: Bearer <token>\"\n")
		return 1
	}

	if len(usable) > 0 {
		fmt.Fprintln(env.Stdout, "Lalabase servers in Claude Code's configuration:")
		for i, e := range usable {
			fmt.Fprintf(env.Stdout, "  %d) %-7s %s  %s  %s  token %s...\n", i+1, e.Scope, show(e.Where), show(e.Name), show(e.BaseURL), show(claudecfg.TokenPrefix(e.Token)))
			if e.Note != "" {
				fmt.Fprintf(env.Stdout, "     %s\n", show(e.Note))
			}
		}
	}
	if len(unusable) > 0 {
		fmt.Fprintln(env.Stdout, "Cannot be imported:")
		for _, e := range unusable {
			fmt.Fprintf(env.Stdout, "     %-7s %s  %s  (%s)\n", e.Scope, show(e.Where), show(e.Name), show(e.Problem))
		}
	}
	if len(usable) == 0 {
		fmt.Fprintln(env.Stderr, "Nothing to import; see the reasons above.")
		return 1
	}
	if !env.Interactive {
		fmt.Fprintln(env.Stderr, "auth import asks which entry to take; run it in a terminal.")
		return 2
	}

	in := asker{in: bufio.NewReader(env.Stdin), out: env.Stdout}
	n, err := strconv.Atoi(in.ask(fmt.Sprintf("Which one? [1-%d] ", len(usable))))
	if err != nil || n < 1 || n > len(usable) {
		fmt.Fprintln(env.Stderr, "No such entry; nothing saved.")
		return 1
	}
	chosen := usable[n-1]

	client, _, err := newAPI(chosen.BaseURL, chosen.Token)
	if err != nil {
		fmt.Fprintf(env.Stderr, "Cannot use this entry: %s\n", show(err.Error()))
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	var me api.Me
	ans := through(ctx, newQueue(env), func(ctx context.Context) (a transport.Answer) {
		me, a = client.Me(ctx)
		return a
	})
	if !ans.OK() {
		fmt.Fprintf(env.Stderr, "%s\nNothing saved.\n", refusal(ans))
		return 1
	}
	fmt.Fprintf(env.Stdout, "Token of %s (%s), may write: %s\n", show(me.User.DisplayName), show(me.User.Email), mayWrite(me))

	if forAgent && (me.Token.ReadOnly == nil || !*me.Token.ReadOnly) {
		fmt.Fprintln(env.Stdout, "Agent commands run only with a read-only token: with this one they will refuse to start.\n"+
			"Create a token without write access for Claude Code and import that one.")
		if !in.yes(fmt.Sprintf("Save it as profile %q anyway?", target)) {
			fmt.Fprintln(env.Stderr, "Nothing saved.")
			return 1
		}
	}

	path := profile.Path(env.Getenv, env.Home)
	file, err := profile.Load(path)
	if err != nil && !errors.Is(err, profile.ErrNoFile) {
		fmt.Fprintf(env.Stderr, "%s\nNothing saved.\n", show(err.Error()))
		return 1
	}
	old, exists := file.Profiles[target]
	if exists {
		fmt.Fprintf(env.Stdout, "Profile %q exists: %s, token %s...\n", target, show(old.URL), show(claudecfg.TokenPrefix(old.Token)))
		if old.AllowWrite {
			fmt.Fprintln(env.Stdout, "It allows agent commands to write (allow_write = true). The new token will not:\n"+
				"set allow_write again by hand if you want that for this token.")
		}
		if !in.yes("Replace it?") {
			fmt.Fprintln(env.Stderr, "Nothing saved.")
			return 1
		}
	}
	// Load, merge and replace under one lock; and only if the profile is
	// still what the person was asked about.
	err = profile.Update(path, func(current *profile.File) error {
		now, stillExists := current.Profiles[target]
		if stillExists != exists || now != old {
			return errChangedMeanwhile
		}
		current.Profiles[target] = profile.Entry{URL: chosen.BaseURL, Token: chosen.Token}
		file = *current
		return nil
	})
	if errors.Is(err, errChangedMeanwhile) {
		fmt.Fprintf(env.Stderr, "Profile %q changed while you were asked; nothing saved. Run auth import again.\n", target)
		return 1
	}
	if err != nil {
		fmt.Fprintf(env.Stderr, "Saving failed: %s\n", show(err.Error()))
		return 1
	}
	fmt.Fprintf(env.Stdout, "Saved profile %q in %s\n", target, show(path))
	if !forAgent {
		if _, ok := file.Profiles[profile.AgentName]; !ok {
			fmt.Fprintln(env.Stdout, "For Claude Code, add a read-only token as the agent profile: lalabase auth import --agent")
		}
	}
	return 0
}

// authStatus shows, per profile: where it comes from, who the token belongs
// to, whether it may write, and what the server supports. Exit 1 when a
// profile cannot be used.
func authStatus(args []string, globalProfile string, env Env) int {
	fs := flag.NewFlagSet("auth status", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	name := fs.String("profile", globalProfile, "show only this profile")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	path := profile.Path(env.Getenv, env.Home)

	type check struct {
		label string
		mode  profile.Mode
	}
	checks := []check{{"default (interactive)", profile.Interactive}, {"agent", profile.Agent}}
	if *name != "" {
		// The profile "agent" is checked as the agent commands will use it.
		mode := profile.Interactive
		if *name == profile.AgentName {
			mode = profile.Agent
		}
		checks = []check{{*name, mode}}
	}

	q := newQueue(env)
	code := 0
	for i, c := range checks {
		if i > 0 {
			fmt.Fprintln(env.Stdout)
		}
		fmt.Fprintf(env.Stdout, "%s:\n", show(c.label))
		p, err := profile.Resolve(c.mode, *name, env.Getenv, path)
		if err != nil {
			fmt.Fprintf(env.Stdout, "  %s\n", show(err.Error()))
			code = 1
			continue
		}
		fmt.Fprintf(env.Stdout, "  %s, token %s... (%s)\n", show(p.URL), show(claudecfg.TokenPrefix(p.Token)), show(p.Source))
		if !statusOf(q, p, c.mode, env.Stdout) {
			code = 1
		}
	}
	return code
}

// statusOf asks the server about one profile; false when it cannot be used.
func statusOf(q *queue.Queue, p profile.Profile, mode profile.Mode, out io.Writer) bool {
	client, raw, err := newAPI(p.URL, p.Token)
	if err != nil {
		fmt.Fprintf(out, "  %s\n", show(err.Error()))
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	var me api.Me
	ans := through(ctx, q, func(ctx context.Context) (a transport.Answer) {
		me, a = client.Me(ctx)
		return a
	})
	if !ans.OK() {
		fmt.Fprintf(out, "  %s\n", refusal(ans))
		return false
	}
	fmt.Fprintf(out, "  token of %s (%s), may write: %s\n", show(me.User.DisplayName), show(me.User.Email), mayWrite(me))
	usable := true
	if mode == profile.Agent {
		if err := profile.VerifyAgent(me, p); err != nil {
			fmt.Fprintf(out, "  agent commands refuse this profile: %s\n", show(err.Error()))
			usable = false
		} else {
			fmt.Fprintln(out, "  agent commands accept this profile")
		}
	}

	var info transport.ServerInfo
	ans = through(ctx, q, func(ctx context.Context) (a transport.Answer) {
		info, a = raw.Initialize(ctx)
		return a
	})
	if !ans.OK() {
		fmt.Fprintf(out, "  MCP: %s: %s\n", ans.Class, show(ans.Message))
		return false
	}
	var tools []transport.Tool
	ans = through(ctx, q, func(ctx context.Context) (a transport.Answer) {
		tools, a = raw.ListTools(ctx)
		return a
	})
	if !ans.OK() {
		fmt.Fprintf(out, "  MCP tools: %s: %s\n", ans.Class, show(ans.Message))
		return false
	}
	report := features.Detect(info, tools)
	fmt.Fprintf(out, "  server %s %s", show(info.Name), show(info.Version))
	if report.BelowTested {
		fmt.Fprintf(out, " (older than %s, the version this client was tested with)", features.MinTestedServerVersion)
	}
	fmt.Fprintln(out)
	for _, f := range features.All {
		if report.Enabled[f.Name] {
			fmt.Fprintf(out, "  on   %s\n", f.Name)
		} else {
			fmt.Fprintf(out, "  off  %s (missing %s)\n", f.Name, show(strings.Join(report.Missing[f.Name], ", ")))
		}
	}
	return usable
}
