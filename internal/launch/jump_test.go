package launch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/agents"
)

// C9c: x jumps to the pane of an agent that runs or starts, and starts
// an agent whose session is over again, as a new agent.

const (
	// The agent a start again replaces, and the session a /clear gave it
	// after its launch's (agentSession).
	oldAgent = "16fd2706-8baf-433b-82eb-8c7fada84700"
	cleared  = "16fd2706-8baf-433b-82eb-8c7fada847da"
)

// run starts cmd under the rig's supervisor and returns its message.
func (r *rig) run(cmd func() tea.Cmd) tea.Msg {
	r.t.Helper()
	out := make(chan tea.Msg, 1)
	if !r.sup.Start("test: run", func(context.Context) { out <- cmd()() }) {
		r.t.Fatal("the lifetime is over")
	}
	select {
	case msg := <-out:
		return msg
	case <-time.After(testLimit):
		r.t.Fatal("no message")
	}
	return nil
}

func (r *rig) jump(pane string) Done {
	r.t.Helper()
	d, ok := r.run(func() tea.Cmd { return r.l.Jump(7, shown, agentID, pane) }).(Done)
	if !ok {
		r.t.Fatal("no Done")
	}
	return d
}

// The jump runs the constant script with the pane's id as its one
// argument, and makes nothing: no launch directory, no agent.
func TestTheJumpSelectsTheAgentsPane(t *testing.T) {
	r := newRig(t, true, nil)
	d := r.jump(paneUUID)
	if d.Kind != Jumped || d.Action != JumpAction || d.Agent != agentID || d.Ticket != shown || d.Ref != 7 || d.Replaces != "" {
		t.Fatalf("done %+v", d)
	}
	calls := r.osascriptCalls()
	if len(calls) != 1 || calls[0].script != jumpScript || !slices.Equal(calls[0].args, []string{"-", paneUUID}) {
		t.Fatalf("osascript %+v", calls)
	}
	if dirs := r.launchDirs(); len(dirs) != 0 {
		t.Fatalf("a jump made launch files: %q", dirs)
	}
	if _, err := os.Lstat(r.agents); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a jump touched the agents: %v", err)
	}
	if !r.l.Jumps() {
		t.Fatal("a launcher in iTerm does not jump")
	}
}

// The script finds the pane in every window and tab and selects its tab,
// then the session, then the window (F8: the window first breaks the
// loop's references, the session before the tab leaves the tab's old
// pane in front). Its one value arrives as an argument.
func TestTheJumpScript(t *testing.T) {
	order := []string{
		"on run argv",
		"if (count of argv) is not 1 then",
		"set pid to item 1 of argv",
		"repeat with w in windows",
		"repeat with t in tabs of w",
		"repeat with s in sessions of t",
		"if (unique id of s) is pid then",
		"tell t to select",
		"tell s to select",
		"select w",
		`error "session not found" number 4201`,
	}
	at := 0
	for _, must := range order {
		i := strings.Index(jumpScript[at:], must)
		if i < 0 {
			t.Fatalf("the script lacks %q after byte %d", must, at)
		}
		at += i + len(must)
	}
	for _, mustNot := range []string{"%", "do shell script", "current window", "current session", "run script", "write text", "split"} {
		if strings.Contains(jumpScript, mustNot) {
			t.Errorf("the script holds %q", mustNot)
		}
	}
}

// The outcome is read from the error's number alone: iTerm's texts are
// localised. 4201 is the script's own: the loop found no such pane.
func TestWhatTheJumpFinds(t *testing.T) {
	fail := func(stderr string) func(context.Context) (string, error) {
		return func(context.Context) (string, error) { return stderr, errors.New("exit status 1") }
	}
	for _, c := range []struct {
		name   string
		answer func(context.Context) (string, error)
		want   Kind
	}{
		{"gone", fail("execution error: session not found (4201)\n"), PaneGone},
		{"gone, another language", fail("Ausführungsfehler: Sitzung nicht gefunden (4201)\n"), PaneGone},
		{"a text that names 4201", fail("execution error: session not found (4201) (-1728)\n"), ITermFailed},
		{"refused", fail("execution error: Not authorized (-1743)\n"), NoPermission},
		{"a broken reference", fail("execution error: Invalid index. (-1719)\n"), ITermFailed},
		{"no number", fail("session not found\n"), ITermFailed},
		{"no answer", func(ctx context.Context) (string, error) { <-ctx.Done(); return "", ctx.Err() }, NoAnswer},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t, true, nil)
			r.l.deadline = 50 * time.Millisecond
			r.answer = c.answer
			if d := r.jump(paneUUID); d.Kind != c.want || d.Agent != agentID {
				t.Fatalf("done %+v, want %v", d, c.want)
			}
		})
	}
}

// Without iTerm, or without a pane in its form, there is nothing to jump
// to: no osascript.
func TestNoJumpWithoutITermOrAPane(t *testing.T) {
	r := newRig(t, false, nil)
	if d := r.jump(paneUUID); d.Kind != NoPane || d.Action != JumpAction {
		t.Fatalf("without iTerm %+v", d)
	}
	if r.l.Jumps() {
		t.Fatal("a launcher without iTerm jumps")
	}
	r = newRig(t, true, nil)
	for _, pane := range []string{"", "w0t0p0:" + paneUUID, paneUUID + `" & do shell script "x`, paneUUID + "\n"} {
		if d := r.jump(pane); d.Kind != NoPane {
			t.Fatalf("%q: %+v", pane, d)
		}
	}
	if len(r.osascriptCalls()) != 0 {
		t.Fatal("osascript ran")
	}
}

func (r *rig) again(t Ticket, a Again) tea.Msg {
	r.t.Helper()
	return r.run(func() tea.Cmd { return r.l.Again(7, t, a) })
}

// Start again (F9): a new agent in a new launch directory, with a new
// settings file and server file, and claude --resume with the latest
// session id, the one a /clear gave, never the launch's. No prompt: the
// conversation is there. The new record keeps that session and the new
// pane; the old agent's files are not touched.
func TestStartAgainResumesTheLatestSession(t *testing.T) {
	r := newRig(t, true, nil)
	d, ok := r.again(shown, Again{Agent: oldAgent, Session: cleared}).(Done)
	if !ok || d.Kind != Opened || d.Action != AgainAction || d.Agent != agentID || d.Replaces != oldAgent || d.Untracked != "" {
		t.Fatalf("done %+v", d)
	}
	dir, server := r.theLaunch()
	wantScript := "#!/bin/sh\n# Claude Code beside a Lalabase ticket: one launch, written by lalabase.\n" +
		"cd '" + r.cwd + "' || exit\n" +
		"exec claude '--mcp-config' '" + filepath.Join(dir, "m") + "' '--strict-mcp-config' '--resume' '" + cleared + "' '--settings' '" + filepath.Join(dir, "s") + "' '-n' 'lalabase #42'\n"
	if got := readFile(t, filepath.Join(dir, "run")); got != wantScript {
		t.Errorf("script\n got %q\nwant %q", got, wantScript)
	}
	if got := readFile(t, filepath.Join(dir, "m")); got != wantMCP(server) {
		t.Errorf("server file %s", got)
	}
	if got := readFile(t, filepath.Join(dir, "s")); got != string(settings(executable, r.agents, agentID)) {
		t.Errorf("settings %s", got)
	}
	got := r.agentsOf()
	if len(got) != 1 || got[0].ID != agentID || got[0].Session != cleared || got[0].Pane != paneUUID || got[0].Ticket != ticketID {
		t.Fatalf("agents %+v", got)
	}
	calls := r.osascriptCalls()
	if len(calls) != 1 || calls[0].script != appleScript {
		t.Fatalf("osascript %+v", calls)
	}
}

// An agent whose hooks never recorded a session has no conversation to
// resume (its launch's id may never have been used): it starts afresh
// with a new session id and the prompt, as a new agent all the same.
func TestStartAgainWithoutASessionStartsAfresh(t *testing.T) {
	r := newRig(t, true, nil)
	d, ok := r.again(shown, Again{Agent: oldAgent}).(Done)
	if !ok || d.Kind != Opened || d.Action != AgainAction || d.Agent != agentID || d.Replaces != oldAgent {
		t.Fatalf("done %+v", d)
	}
	dir, server := r.theLaunch()
	script := readFile(t, filepath.Join(dir, "run"))
	if !strings.Contains(script, "'--session-id' '"+agentSession+"'") || !strings.Contains(script, promptFor(server)) || strings.Contains(script, "--resume") {
		t.Fatalf("script %q", script)
	}
}

// Without iTerm claude resumes here, its agent recorded before it runs.
func TestStartAgainHere(t *testing.T) {
	r := newRig(t, false, nil)
	ran, ok := r.again(shown, Again{Agent: oldAgent, Session: cleared}).(execRan)
	if !ok || len(r.execd) != 1 {
		t.Fatal("claude did not run here")
	}
	dir, _ := r.theLaunch()
	want := []string{"/opt/bin/claude", "--mcp-config", filepath.Join(dir, "m"), "--strict-mcp-config",
		"--resume", cleared, "--settings", filepath.Join(dir, "s"), "-n", "lalabase #42"}
	if !slices.Equal(r.execd[0].Args, want) {
		t.Fatalf("arguments\n got %q\nwant %q", r.execd[0].Args, want)
	}
	if got := r.agentsOf(); len(got) != 1 || got[0].Session != cleared || got[0].Pane != "" {
		t.Fatalf("agents %+v", got)
	}
	for _, err := range []error{nil, errors.New("exit status 1")} {
		d, ok := ran.fn(err).(Done)
		if !ok || d.Action != AgainAction || d.Agent != agentID || d.Replaces != oldAgent {
			t.Fatalf("after %v: %+v", err, d)
		}
	}
}

// A session id of another form never reaches a script or claude.
func TestStartAgainRefusesASessionOfAnotherForm(t *testing.T) {
	// Review round 1 of #43: one in capitals would pass here but not the
	// new record's check, and the resumed session would run untracked.
	for _, s := range []string{"x", cleared + "' ; touch pwned '", cleared + "\n", strings.ToUpper(cleared)} {
		r := newRig(t, true, nil)
		d, ok := r.again(shown, Again{Agent: oldAgent, Session: s}).(Done)
		if !ok || d.Kind != NotHandedOver || d.Agent != "" {
			t.Fatalf("%q: %+v", s, d)
		}
		if len(r.launchDirs()) != 0 || len(r.osascriptCalls()) != 0 {
			t.Fatalf("%q: files or osascript", s)
		}
	}
}

// A start again iTerm refused records no new agent and says no agent, so
// nothing replaces the old one.
func TestARefusedStartAgainLeavesNoAgent(t *testing.T) {
	r := newRig(t, true, nil)
	r.answer = func(context.Context) (string, error) {
		return "execution error: Not authorized (-1743)\n", errors.New("exit status 1")
	}
	d, ok := r.again(shown, Again{Agent: oldAgent, Session: cleared}).(Done)
	if !ok || d.Kind != NoPermission || d.Agent != "" || d.Replaces != oldAgent {
		t.Fatalf("done %+v", d)
	}
	if got := r.agentsOf(); len(got) != 0 {
		t.Fatalf("agents %+v", got)
	}
}

// A first start says which agent it recorded; one it could not record
// says none.
func TestTheDoneOfAStartNamesItsAgent(t *testing.T) {
	r := newRig(t, true, nil)
	if d := r.done(shown); d.Action != StartAction || d.Agent != agentID || d.Replaces != "" {
		t.Fatalf("done %+v", d)
	}
	r = newRig(t, true, nil)
	r.l.writeLaunch = func(string, agents.Launch) error { return errors.New("disk full") }
	if d := r.done(shown); d.Agent != "" || d.Untracked == "" {
		t.Fatalf("an unrecorded agent: %+v", d)
	}
}
