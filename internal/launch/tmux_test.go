package launch

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// The tmux agents: with tmux, a tracked launch starts the script in a
// detached session named after the agent, on the client's socket, and
// opens no pane; View brings the one viewer pane to the agent wanted.

const tmuxPath = "/opt/homebrew/bin/tmux"

// fakeTmux answers tmux calls: the sessions that exist and the clients
// attached (tty and session), as a tmux server would; every call is kept.
type fakeTmux struct {
	mu       sync.Mutex
	calls    [][]string
	sessions []string
	clients  [][2]string
	fail     bool
}

func (f *fakeTmux) run(_ context.Context, args []string) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, append([]string(nil), args...))
	if f.fail {
		return "", "boom", errors.New("exit status 1")
	}
	if len(args) < 5 || args[0] != "-L" || args[1] != tmuxSocket || args[2] != "-f" || args[3] != "/dev/null" {
		return "", "bad socket", errors.New("exit status 1")
	}
	switch cmd := args[4:]; cmd[0] {
	case "new-session":
		f.sessions = append(f.sessions, cmd[3])
	case "has-session":
		if !slices.Contains(f.sessions, strings.TrimPrefix(cmd[2], "=")) {
			return "", "can't find session", errors.New("exit status 1")
		}
	case "list-clients":
		var b strings.Builder
		for _, c := range f.clients {
			b.WriteString(c[0] + " " + c[1] + "\n")
		}
		return b.String(), "", nil
	case "switch-client":
		for i := range f.clients {
			if f.clients[i][0] == cmd[2] {
				f.clients[i][1] = strings.TrimPrefix(cmd[4], "=")
			}
		}
	case "detach-client":
		f.clients = slices.DeleteFunc(f.clients, func(c [2]string) bool { return c[0] == cmd[2] })
	}
	return "", "", nil
}

// commands are the tmux commands the fake got, each without the socket.
func (f *fakeTmux) commands() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		out = append(out, strings.Join(c[4:], " "))
	}
	return out
}

func (r *rig) withTmux() *fakeTmux {
	f := &fakeTmux{}
	r.l.tmux = tmuxPath
	r.l.runTmux = f.run
	return f
}

// view runs View's command for agent and waits for it.
func (r *rig) view(agent string) {
	r.t.Helper()
	cmd := r.l.View(agent)
	if cmd == nil {
		r.t.Fatalf("no command for %q", agent)
	}
	r.runCmd(cmd)
}

func (r *rig) runCmd(cmd tea.Cmd) {
	r.t.Helper()
	out := make(chan tea.Msg, 1)
	if !r.sup.Start("test: view", func(context.Context) { out <- cmd() }) {
		r.t.Fatal("the lifetime is over")
	}
	select {
	case <-out:
	case <-time.After(testLimit):
		r.t.Fatal("no answer")
	}
}

func TestATrackedLaunchRunsInATmuxSession(t *testing.T) {
	r := newRig(t, true, nil)
	f := r.withTmux()
	if d := r.done(shown); d.Kind != Opened || d.Untracked != "" {
		t.Fatalf("done %+v", d)
	}
	if calls := r.osascriptCalls(); len(calls) != 0 {
		t.Fatalf("a pane opened: %v", calls)
	}
	dir, _ := r.theLaunch()
	cmds := f.commands()
	if len(cmds) != 1 {
		t.Fatalf("tmux calls %q", cmds)
	}
	want := "new-session -d -s " + agentID + " -c " + r.cwd + " -x 200 -y 50 /bin/sh " + filepath.Join(dir, "run")
	if !strings.HasPrefix(cmds[0], want+" ; ") {
		t.Fatalf("tmux\n got %q\nwant %q", cmds[0], want)
	}
	for _, o := range []string{"set -g status off", "set -g prefix None", "set -g escape-time 0", "set -g detach-on-destroy on"} {
		if !strings.Contains(cmds[0], " ; "+o) {
			t.Errorf("option %q not set", o)
		}
	}
	// The token stays in the server file.
	if strings.Contains(cmds[0], profileToken) {
		t.Fatal("the token is in tmux's arguments")
	}
	// The agent is recorded, without a pane.
	if got := r.agentsOf(); len(got) != 1 || got[0].ID != agentID || got[0].Pane != "" {
		t.Fatalf("agents %+v", got)
	}
}

func TestTmuxThatFailsIsAProblem(t *testing.T) {
	r := newRig(t, true, nil)
	r.withTmux().fail = true
	if d := r.done(shown); d.Kind != TmuxFailed {
		t.Fatalf("done %+v", d)
	}
	if got := r.agentsOf(); len(got) != 0 {
		t.Fatalf("an agent that never started was recorded: %+v", got)
	}
}

// Without tmux the launch splits the pane as before.
func TestWithoutTmuxThePaneSplits(t *testing.T) {
	r := newRig(t, true, nil)
	if d := r.done(shown); d.Kind != Opened || len(r.osascriptCalls()) != 1 {
		t.Fatalf("done %+v, osascript %d", d, len(r.osascriptCalls()))
	}
	if r.l.View(agentID) != nil {
		t.Fatal("a viewer without tmux")
	}
}

func TestTheViewerFollowsWhatIsWanted(t *testing.T) {
	const other = "7c9e6679-7425-40de-944b-e07fc1f90ae8"
	r := newRig(t, true, nil)
	f := r.withTmux()
	f.sessions = []string{agentID, other}
	// No viewer yet: the client's session is split, the pane attaches.
	r.view(agentID)
	calls := r.osascriptCalls()
	if len(calls) != 1 || calls[0].script != appleScript {
		t.Fatalf("osascript %v", calls)
	}
	if got, want := calls[0].args[2], "exec '"+tmuxPath+"' -L lalabase attach -t '="+agentID+"'"; got != want {
		t.Fatalf("typed %q, want %q", got, want)
	}
	// The pane's client attached: another agent switches it, no new pane.
	f.clients = [][2]string{{"/dev/ttys009", agentID}}
	r.view(other)
	if len(r.osascriptCalls()) != 1 || f.clients[0][1] != other {
		t.Fatalf("panes %d, client %v", len(r.osascriptCalls()), f.clients)
	}
	// The same agent again changes nothing.
	before := len(f.commands())
	r.view(other)
	if cmds := f.commands()[before:]; slices.ContainsFunc(cmds, func(c string) bool { return strings.HasPrefix(c, "switch-client") }) {
		t.Fatalf("switched again: %q", cmds)
	}
	// None: the client detaches, its pane closes.
	r.view("")
	if len(f.clients) != 0 {
		t.Fatalf("clients %v", f.clients)
	}
	// An agent without a session: nothing opens.
	r.view("7c9e6679-7425-40de-944b-e07fc1f90ae9")
	if len(r.osascriptCalls()) != 1 {
		t.Fatal("a pane opened for an agent without a session")
	}
}

// The newest View wins: a command that runs late applies the session
// wanted last, not its own.
func TestTheNewestViewWins(t *testing.T) {
	r := newRig(t, true, nil)
	f := r.withTmux()
	f.sessions = []string{agentID}
	f.clients = [][2]string{{"/dev/ttys009", agentID}}
	early := r.l.View(agentID)
	late := r.l.View("")
	r.runCmd(late)
	r.runCmd(early)
	if len(f.clients) != 0 || len(r.osascriptCalls()) != 0 {
		t.Fatalf("clients %v, panes %d", f.clients, len(r.osascriptCalls()))
	}
}

// Only an agent's id reaches tmux.
func TestViewTakesAnAgentsIDOnly(t *testing.T) {
	r := newRig(t, true, nil)
	r.withTmux()
	for _, bad := range []string{"x; rm -rf /", "=" + agentID, agentID + ":0"} {
		if r.l.View(bad) != nil {
			t.Fatalf("%q passed", bad)
		}
	}
}
