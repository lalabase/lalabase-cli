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
	"sync"
	"time"

	"github.com/gboyke/lalabase-cli/internal/api"
	"github.com/gboyke/lalabase-cli/internal/claudecfg"
	"github.com/gboyke/lalabase-cli/internal/clean"
	"github.com/gboyke/lalabase-cli/internal/features"
	"github.com/gboyke/lalabase-cli/internal/profile"
	"github.com/gboyke/lalabase-cli/internal/queue"
	"github.com/gboyke/lalabase-cli/internal/transport"
)

// requestTimeout bounds the requests for one profile, waiting in a
// rate-limit pause included (tests shorten it).
var requestTimeout = 30 * time.Second

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

// requests runs auth's requests through one queue like every other request,
// so a 429 holds back what follows instead of running into the window. It
// keeps the last pause it was told of, to explain a limit that ran out in it.
type requests struct {
	q     *queue.Queue
	now   func() time.Time
	mu    sync.Mutex
	until time.Time
}

func newRequests(env Env) *requests {
	r := &requests{now: time.Now}
	r.q = queue.New(queue.WithOnPause(func(until time.Time) {
		r.mu.Lock()
		r.until = until
		r.mu.Unlock()
		fmt.Fprintf(env.Stderr, "Rate limit: waiting until %s.\n", when(until, r.now()))
	}))
	return r
}

// do runs one of the person's reads.
func (r *requests) do(ctx context.Context, run func(context.Context) transport.Answer) transport.Answer {
	return r.q.Do(ctx, queue.Job{Priority: queue.User, Kind: queue.Read, Run: run})
}

// failed says why a request brought no answer. The command's own end comes
// first: a limit that ran out (in a rate-limit pause, or waiting for an
// answer) or a cancel is no statement of the server's.
func (r *requests) failed(ctx context.Context, ans transport.Answer) string {
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		r.mu.Lock()
		until := r.until
		r.mu.Unlock()
		if now := r.now(); until.After(now) {
			return fmt.Sprintf("gave up waiting: the server's rate limit ends at %s, after this command's %s limit. Try again then", when(until, now), requestTimeout)
		}
		return fmt.Sprintf("no answer within %s", requestTimeout)
	case errors.Is(ctx.Err(), context.Canceled):
		return "canceled"
	}
	return fmt.Sprintf("%s: %s", ans.Class, show(ans.Message))
}

// tokenFailed says why a check of the token brought no answer: a rejection
// by the server is something else than a request that did not get through.
func (r *requests) tokenFailed(ctx context.Context, ans transport.Answer) string {
	if ans.Class == transport.Unauthenticated {
		return fmt.Sprintf("the server did not accept the token (%s): %s", ans.Class, show(ans.Message))
	}
	if ctx.Err() != nil {
		return "the token could not be checked: " + r.failed(ctx, ans)
	}
	return fmt.Sprintf("the token could not be checked (%s): %s", ans.Class, show(ans.Message))
}

// when names the end of a pause: the time, the date too when it is another
// day, and how long until then.
func when(until, now time.Time) string {
	u, n := until.Local(), now.Local()
	layout := "15:04:05"
	if u.YearDay() != n.YearDay() || u.Year() != n.Year() {
		layout = "2006-01-02 15:04:05"
	}
	left := max(until.Sub(now).Round(time.Second), 0)
	return fmt.Sprintf("%s (in %d s)", u.Format(layout), int(left.Seconds()))
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
	// What the person reads to answer goes to stderr: with stdout in a file
	// the list and the questions are still on the screen. The result goes to
	// stdout.
	ui := env.Stderr

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
		fmt.Fprintln(ui, "Lalabase servers in Claude Code's configuration:")
		for i, e := range usable {
			fmt.Fprintf(ui, "  %d) %-7s %s  %s  %s  token %s...\n", i+1, e.Scope, show(e.Where), show(e.Name), show(e.BaseURL), show(claudecfg.TokenPrefix(e.Token)))
			if e.Note != "" {
				fmt.Fprintf(ui, "     %s\n", show(e.Note))
			}
			if e.Approval != "" {
				fmt.Fprintf(ui, "     %s\n", show(e.Approval))
			}
		}
	}
	if len(unusable) > 0 {
		fmt.Fprintln(ui, "Cannot be imported:")
		for _, e := range unusable {
			fmt.Fprintf(ui, "     %-7s %s  %s  (%s)\n", e.Scope, show(e.Where), show(e.Name), show(e.Problem))
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

	in := asker{in: bufio.NewReader(env.Stdin), out: ui}
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
	req := newRequests(env)
	ans := req.do(ctx, func(ctx context.Context) (a transport.Answer) {
		me, a = client.Me(ctx)
		return a
	})
	if !ans.OK() {
		fmt.Fprintf(env.Stderr, "%s.\nNothing saved.\n", req.tokenFailed(ctx, ans))
		return 1
	}
	fmt.Fprintf(ui, "Token of %s (%s), may write: %s\n", show(me.User.DisplayName), show(me.User.Email), mayWrite(me))

	if forAgent && (me.Token.ReadOnly == nil || !*me.Token.ReadOnly) {
		fmt.Fprintln(ui, "Agent commands run only with a read-only token: with this one they will refuse to start.\n"+
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
		fmt.Fprintf(ui, "Profile %q exists: %s, token %s...\n", target, show(old.URL), show(claudecfg.TokenPrefix(old.Token)))
		if old.AllowWrite {
			fmt.Fprintln(ui, "It allows agent commands to write (allow_write = true). The new token will not:\n"+
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

	req := newRequests(env)
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
		if !statusOf(req, p, c.mode, env.Stdout) {
			code = 1
		}
	}
	return code
}

// statusOf asks the server about one profile; false when it cannot be used.
func statusOf(req *requests, p profile.Profile, mode profile.Mode, out io.Writer) bool {
	client, raw, err := newAPI(p.URL, p.Token)
	if err != nil {
		fmt.Fprintf(out, "  %s\n", show(err.Error()))
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	var me api.Me
	ans := req.do(ctx, func(ctx context.Context) (a transport.Answer) {
		me, a = client.Me(ctx)
		return a
	})
	if !ans.OK() {
		fmt.Fprintf(out, "  %s\n", req.tokenFailed(ctx, ans))
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
	ans = req.do(ctx, func(ctx context.Context) (a transport.Answer) {
		info, a = raw.Initialize(ctx)
		return a
	})
	if !ans.OK() {
		fmt.Fprintf(out, "  MCP: %s\n", req.failed(ctx, ans))
		return false
	}
	var tools []transport.Tool
	ans = req.do(ctx, func(ctx context.Context) (a transport.Answer) {
		tools, a = raw.ListTools(ctx)
		return a
	})
	if !ans.OK() {
		fmt.Fprintf(out, "  MCP tools: %s\n", req.failed(ctx, ans))
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
