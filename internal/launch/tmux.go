package launch

import (
	"context"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"
	"github.com/gboyke/lalabase-cli/internal/agents"
	"github.com/gboyke/lalabase-cli/internal/worker"
)

// Prototype (tmux agents): with tmux on the PATH and the client in iTerm,
// a tracked agent runs in a tmux session of its own on the client's own
// socket, detached, and goes on when nobody looks at it. Beside the
// client stands at most one iTerm pane, the viewer: a tmux client
// attached to the agent of the ticket shown (View). The tmux server is
// the viewer's only state: the client asks it, never remembers it.

// tmuxSocket is the name of the client's tmux socket (tmux -L): apart
// from the person's own tmux server, in tmux's per-user directory.
const tmuxSocket = "lalabase"

// tmuxOptions are set on the client's tmux server with every new
// session: no status line and no prefix key (the viewer looks like a
// plain pane), Esc without delay (Claude Code needs it), the mouse for
// scrolling, and the keys and colours iTerm sends.
var tmuxOptions = [][]string{
	{"set", "-g", "status", "off"},
	{"set", "-g", "prefix", "None"},
	{"set", "-g", "escape-time", "0"},
	{"set", "-g", "mouse", "on"},
	{"set", "-g", "history-limit", "50000"},
	{"set", "-g", "default-terminal", "tmux-256color"},
	{"set", "-g", "terminal-features[90]", "xterm*:RGB:extkeys"},
	{"set", "-g", "extended-keys", "on"},
	// The viewer leaves when its agent ends, so its pane closes.
	{"set", "-g", "detach-on-destroy", "on"},
}

// viewer is what View works on: the session wanted last, and one change
// at a time.
type viewer struct {
	mu sync.Mutex
	// want is the session the newest View asked for, "" for none; a View
	// that runs late applies it, not its own.
	want string
	run  sync.Mutex
}

// tmuxArgs are the arguments of one tmux call on the client's socket.
func tmuxArgs(args ...string) []string {
	return append([]string{"-L", tmuxSocket, "-f", "/dev/null"}, args...)
}

// startInTmux starts the launch script in a detached tmux session named
// after the agent, and sets the server's options in the same call.
func (l *Launcher) startInTmux(ctx context.Context, agent, folder, scriptPath string) Kind {
	args := tmuxArgs("new-session", "-d", "-s", agent, "-c", folder, "-x", "200", "-y", "50", "/bin/sh", scriptPath)
	for _, o := range tmuxOptions {
		args = append(append(args, ";"), o...)
	}
	if _, _, err := l.runTmux(ctx, args); err != nil {
		if ctx.Err() != nil {
			return NoAnswer
		}
		return TmuxFailed
	}
	return Opened
}

// View is the command that makes the viewer show the agent's session, or
// close for "": the newest call wins, whatever order the commands run in.
func (l *Launcher) View(agent string) tea.Cmd {
	if l.tmux == "" || (agent != "" && !agents.ForeignID(agent)) {
		return nil
	}
	l.viewer.mu.Lock()
	l.viewer.want = agent
	l.viewer.mu.Unlock()
	sup := l.cfg.Supervisor
	return func() tea.Msg {
		_, ok := worker.Run(sup, "viewer", func() tea.Msg {
			l.viewer.run.Lock()
			defer l.viewer.run.Unlock()
			l.viewer.mu.Lock()
			want := l.viewer.want
			l.viewer.mu.Unlock()
			l.applyView(want)
			return nil
		})
		if !ok && sup.Err() != nil {
			return worker.Fatal{}
		}
		return nil
	}
}

// applyView brings the viewer to want: the client attached to it switched,
// detached for "" (its pane closes), or a new pane split off the client's
// session when there is none. An agent without a session closes it too.
func (l *Launcher) applyView(want string) {
	ctx, cancel := context.WithTimeout(l.cfg.Supervisor.Context(), l.deadline)
	defer cancel()
	if want != "" {
		if _, _, err := l.runTmux(ctx, tmuxArgs("has-session", "-t", "="+want)); err != nil {
			want = ""
		}
	}
	out, _, _ := l.runTmux(ctx, tmuxArgs("list-clients", "-F", "#{client_tty} #{session_name}"))
	var clients [][2]string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if tty, session, ok := strings.Cut(line, " "); ok {
			clients = append(clients, [2]string{tty, session})
		}
	}
	if want == "" {
		for _, c := range clients {
			_, _, _ = l.runTmux(ctx, tmuxArgs("detach-client", "-t", c[0]))
		}
		return
	}
	if len(clients) > 0 {
		if clients[0][1] != want {
			_, _, _ = l.runTmux(ctx, tmuxArgs("switch-client", "-c", clients[0][0], "-t", "="+want))
		}
		return
	}
	line := "exec " + quote(l.tmux) + " -L " + tmuxSocket + " attach -t " + quote("="+want)
	_, _, _ = l.osascript(ctx, appleScript, osascriptArgs(l.session, line))
}
