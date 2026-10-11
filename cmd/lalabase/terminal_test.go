//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/creack/pty"
	"github.com/gboyke/lalabase-cli/internal/inboxfixture"
	"github.com/gboyke/lalabase-cli/internal/profile"
	"github.com/gboyke/lalabase-cli/internal/ui"
	"github.com/gboyke/lalabase-cli/internal/worker"
	"golang.org/x/sys/unix"
)

const readyLimit = 5 * time.Second
const exitLimit = 5 * time.Second
const caseLimit = 15 * time.Second
const cleanupLimit = 2 * time.Second

type terminalProcess struct {
	cmd                *exec.Cmd
	master, slave      *os.File
	release            *os.File
	descendantFD       int
	before             *unix.Termios
	deadline           time.Time
	readDone, waitDone chan struct{}
	cancel             context.CancelFunc
	cancelProcess      context.CancelFunc
	marker             string
	mu                 sync.Mutex
	output             bytes.Buffer
	readErr, errorExit error
}

// terminalOptions: the server the child's profile points at, the text that
// says the interface is up, and the case limit.
type terminalOptions struct {
	url, marker string
	limit       time.Duration
	// home is the child's home, when a run is to find what an earlier one
	// left there (the disk cache, C1e); empty for a fresh one. cacheHome,
	// when set, is the child's XDG_CACHE_HOME.
	home, cacheHome string
	// env is added to the child's environment (NO_COLOR=1).
	env []string
	// folders are the default profile's project folders (C7).
	folders map[string]string
}

func startTerminal(t *testing.T, scenario string, width uint16) *terminalProcess {
	t.Helper()
	// The header is the one line every interface shows first, the shell and
	// the session alike.
	return startTerminalWith(t, scenario, width, terminalOptions{url: "http://127.0.0.1:1", marker: "lalabase | default", limit: caseLimit})
}

func startTerminalWith(t *testing.T, scenario string, width uint16, opts terminalOptions) *terminalProcess {
	t.Helper()
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = master.Close(); _ = slave.Close() })
	before, err := terminalAttributes(int(slave.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	if before.Lflag&(unix.ECHO|unix.ICANON) != unix.ECHO|unix.ICANON {
		t.Fatal("PTY did not start in cooked mode")
	}
	if err := pty.Setsize(master, &pty.Winsize{Rows: 24, Cols: width}); err != nil {
		t.Fatal(err)
	}
	if err := unix.SetNonblock(int(master.Fd()), true); err != nil {
		t.Fatal(err)
	}
	home := opts.home
	if home == "" {
		home = t.TempDir()
	}
	token := "pty-dummy"
	if opts.url != "http://127.0.0.1:1" {
		token = inboxfixture.Token
	}
	if err := profile.Save(filepath.Join(home, ".config", "lalabase", "config.toml"), profile.File{Profiles: map[string]profile.Entry{"default": {URL: opts.url, Token: token, Folders: opts.folders}}}); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	processCtx, processCancel := context.WithTimeout(context.Background(), opts.limit)
	t.Cleanup(processCancel)
	cmd := exec.CommandContext(processCtx, executable, "-test.run=^TestTerminalChild$", "-test.timeout="+(opts.limit+10*time.Second).String(), "--", scenario)
	cmd.Env = []string{"C1A_PTY_CHILD=1", "HOME=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, ".config"), "TERM=xterm-256color", "LC_ALL=C.UTF-8", "RUNEWIDTH_EASTASIAN=0", "CI=1", "TZ=UTC"}
	if opts.cacheHome != "" {
		cmd.Env = append(cmd.Env, "XDG_CACHE_HOME="+opts.cacheHome)
	}
	cmd.Env = append(cmd.Env, opts.env...)
	cmd.Dir = home
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	cmd.Cancel = func() error {
		err := unix.Kill(-cmd.Process.Pid, unix.SIGKILL)
		if errors.Is(err, unix.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	ack, release, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ack.Close(); _ = release.Close() })
	cmd.ExtraFiles = []*os.File{ack}
	var descendants, alive *os.File
	descendantFD := -1
	if scenario == "hang" {
		descendants, alive, err = os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = descendants.Close(); _ = alive.Close() })
		// File.Fd can restore blocking mode on a pipe. Capture it once,
		// then use the descriptor directly in every bounded probe.
		descendantFD = int(descendants.Fd())
		if err := unix.SetNonblock(descendantFD, true); err != nil {
			t.Fatal(err)
		}
		cmd.ExtraFiles = append(cmd.ExtraFiles, alive)
	}
	cmd.WaitDelay = cleanupLimit
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = ack.Close()
	if alive != nil {
		_ = alive.Close()
		// Keep mutation failures contained even if the cleanup under test
		// kills only the leader. This independent fallback runs last.
		t.Cleanup(func() { _ = unix.Kill(-cmd.Process.Pid, unix.SIGKILL) })
	}
	ctx, cancel := context.WithCancel(processCtx)
	c := &terminalProcess{cmd: cmd, master: master, slave: slave, before: before, deadline: time.Now().Add(opts.limit), readDone: make(chan struct{}), waitDone: make(chan struct{}), cancel: cancel, marker: opts.marker}
	c.release = release
	c.descendantFD, c.cancelProcess = descendantFD, processCancel
	if d, ok := t.Deadline(); ok && d.Add(-cleanupLimit).Before(c.deadline) {
		c.deadline = d.Add(-cleanupLimit)
	}
	go func() { c.errorExit = cmd.Wait(); close(c.waitDone) }()
	go c.read(ctx)
	t.Cleanup(func() {
		if err := c.stop(); err != nil {
			t.Error(err)
		}
	})
	return c
}

func (c *terminalProcess) read(ctx context.Context) {
	defer close(c.readDone)
	fd := int(c.master.Fd())
	buf := make([]byte, 4096)
	for ctx.Err() == nil {
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		_, err := unix.Poll(fds, 50)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			c.setReadError(err)
			return
		}
		if fds[0].Revents == 0 {
			continue
		}
		n, err := unix.Read(fd, buf)
		if n > 0 {
			c.mu.Lock()
			if c.output.Len()+n > 2<<20 {
				c.readErr = errors.New("PTY output exceeded 2 MiB")
				c.mu.Unlock()
				return
			}
			_, _ = c.output.Write(buf[:n])
			c.mu.Unlock()
		}
		if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EINTR) {
			continue
		}
		if errors.Is(err, unix.EIO) || (n == 0 && err == nil) {
			return
		}
		if err != nil {
			c.setReadError(err)
			return
		}
	}
}

func (c *terminalProcess) setReadError(err error) { c.mu.Lock(); defer c.mu.Unlock(); c.readErr = err }
func (c *terminalProcess) bytes() string          { c.mu.Lock(); defer c.mu.Unlock(); return c.output.String() }

func (c *terminalProcess) until(label string, limit time.Duration, condition func() bool) error {
	deadline := time.Now().Add(limit)
	if c.deadline.Before(deadline) {
		deadline = c.deadline
	}
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if !time.Now().Before(deadline) {
			return fmt.Errorf("%s timeout after %s", label, limit)
		}
		passed := condition()
		// A late observation must never turn an expired case into success.
		if !time.Now().Before(deadline) {
			return fmt.Errorf("%s timeout after %s", label, limit)
		}
		if passed {
			return nil
		}
		c.mu.Lock()
		err := c.readErr
		c.mu.Unlock()
		if err != nil {
			return err
		}
		select {
		case <-timer.C:
			return fmt.Errorf("%s timeout after %s", label, limit)
		case <-ticker.C:
		}
	}
}

func terminalModes(output string) map[int][]bool {
	modes := map[int][]bool{}
	p := ansi.NewParser()
	p.SetHandler(ansi.Handler{HandleCsi: func(cmd ansi.Cmd, params ansi.Params) {
		if cmd.Prefix() != '?' || (cmd.Final() != 'h' && cmd.Final() != 'l') {
			return
		}
		for _, param := range params {
			n := param.Param(0)
			modes[n] = append(modes[n], cmd.Final() == 'h')
		}
	}})
	p.Parse([]byte(output))
	return modes
}

func lastMode(modes map[int][]bool, mode int, value bool) bool {
	events := modes[mode]
	return len(events) > 0 && events[len(events)-1] == value
}

func (c *terminalProcess) ready() error {
	return c.until("readiness", readyLimit, func() bool {
		modes := terminalModes(c.bytes())
		attrs, err := terminalAttributes(int(c.slave.Fd()))
		return err == nil && attrs.Lflag&(unix.ECHO|unix.ICANON) == 0 && strings.Contains(c.bytes(), c.marker) && lastMode(modes, 1049, true) && lastMode(modes, 2004, true) && lastMode(modes, 25, false)
	})
}

func (c *terminalProcess) send(s string) error {
	return c.until("input write", time.Second, func() bool {
		n, err := unix.Write(int(c.master.Fd()), []byte(s))
		s = s[max(0, n):]
		if err != nil && !errors.Is(err, unix.EAGAIN) && !errors.Is(err, unix.EINTR) {
			c.setReadError(err)
		}
		return len(s) == 0
	})
}

func (c *terminalProcess) finish(t *testing.T, code int) {
	t.Helper()
	end := time.Now().Add(exitLimit)
	if err := c.until("program return", exitLimit, func() bool { return strings.Contains(c.bytes(), "C1A_RESULT=") }); err != nil {
		t.Fatal(err)
	}
	// Darwin revokes the slave when the session leader exits. Inspect it after
	// Program.Run returns, while the child waits for this acknowledgement.
	after, err := terminalAttributes(int(c.slave.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	if err := termiosRestorationError(c.before, after); err != nil {
		t.Fatal(err)
	}
	if _, err := c.release.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	if err := c.until("exit", time.Until(end), func() bool {
		select {
		case <-c.waitDone:
			return true
		default:
			return false
		}
	}); err != nil {
		t.Fatal(err)
	}
	if got := c.cmd.ProcessState.ExitCode(); got != code {
		t.Fatalf("exit %d, want %d: %v\n%s", got, code, c.errorExit, c.bytes())
	}
	_ = c.slave.Close()
	if err := c.until("output drain", cleanupLimit, func() bool {
		select {
		case <-c.readDone:
			return true
		default:
			return false
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := terminalRestorationError(c.bytes()); err != nil {
		t.Fatal(err)
	}
}

func (c *terminalProcess) stop() error {
	defer c.cancel()
	deadline := time.Now().Add(cleanupLimit)
	select {
	case <-c.waitDone:
	default:
		if err := unix.Kill(-c.cmd.Process.Pid, unix.SIGKILL); err != nil && !errors.Is(err, unix.ESRCH) {
			return err
		}
	}
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case <-c.waitDone:
	case <-timer.C:
		return errors.New("child was not reaped within cleanup deadline")
	}
	c.cancel()
	_ = c.slave.Close()
	select {
	case <-c.readDone:
	case <-timer.C:
		return errors.New("PTY reader exceeded cleanup deadline")
	}
	err := c.master.Close()
	if errors.Is(err, os.ErrClosed) {
		return nil
	}
	return err
}

func TestTerminalChild(t *testing.T) {
	if os.Getenv("C1A_PTY_CHILD") != "1" {
		return
	}
	scenario := os.Args[len(os.Args)-1]
	limit := 12 * time.Second
	if strings.HasPrefix(scenario, "inbox-") {
		limit = inboxLimit + 10*time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	cwd, _ := os.Getwd()
	env := Env{Context: ctx, Home: os.Getenv("HOME"), Cwd: cwd, Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, Getenv: os.Getenv, Interactive: isTerminal(os.Stdin), OutputTerminal: isTerminal(os.Stdout)}
	if scenario == "hang" {
		// The inherited pipe stays open while this sleeping descendant lives.
		// EOF proves group termination without relying on zombie reaping by
		// the host's init process, which differs across CI containers.
		// Ignore the session leader's hangup, otherwise killing only the
		// leader could appear to be a successful group kill. Signal readiness
		// only after the ignored disposition has been installed.
		grandchild := exec.CommandContext(ctx, "/bin/sh", "-c", "trap '' HUP; printf R >&3; exec /bin/sleep 60")
		grandchild.Env = []string{}
		grandchild.ExtraFiles = []*os.File{os.NewFile(4, "descendant-liveness")}
		if err := grandchild.Start(); err != nil {
			t.Fatal(err)
		}
		go func() { _ = grandchild.Wait() }()
		_, _ = fmt.Fprintln(os.Stdout, "C1A_DESCENDANT_STARTED")
	}
	if scenario == "resize" {
		// The shell's geometry alone, as C1a tested it.
		env.runUI = func(ctx context.Context, in io.Reader, out io.Writer, environment []string, sup *worker.Supervisor) error {
			return ui.Run(ctx, in, out, environment, ui.New(), sup)
		}
	}
	if scenario == "panic-update" || scenario == "panic-view" || scenario == "hang" {
		env.runUI = func(ctx context.Context, in io.Reader, out io.Writer, environment []string, sup *worker.Supervisor) error {
			return ui.Run(ctx, in, out, environment, faultModel{Model: ui.New(), scenario: scenario}, sup)
		}
	}
	if _, ok := fatalScenarios[scenario]; ok || scenario == "worker-hang" {
		env.runUI = fatalRunner(scenario)
	}
	if scenario == "hostile" {
		env.runUI = func(ctx context.Context, in io.Reader, out io.Writer, environment []string, sup *worker.Supervisor) error {
			return ui.Run(ctx, in, out, environment, ui.NewWithContent("SAFE_INLINE"+terminalHostile, "No data loaded\nSAFE_LIST"+terminalHostile, "SAFE_OBJECT"+terminalHostile), sup)
		}
	}
	if scenario == "environment" {
		env.runUI = func(ctx context.Context, in io.Reader, out io.Writer, environment []string, sup *worker.Supervisor) error {
			return ui.Run(ctx, in, out, environment, environmentModel{Model: ui.New()}, sup)
		}
	}
	code := runWith(nil, env)
	_, _ = fmt.Fprintf(os.Stdout, "C1A_RESULT=%d\n", code)
	// Wait for the parent's acknowledgement. The runtime's preemption signals
	// interrupt poll under load; leaving on EINTR would end the session
	// leader early, and Darwin then revokes the terminal the parent inspects.
	deadline := time.Now().Add(exitLimit)
	for {
		fds := []unix.PollFd{{Fd: 3, Events: unix.POLLIN}}
		n, err := unix.Poll(fds, max(0, int(time.Until(deadline)/time.Millisecond)))
		if errors.Is(err, unix.EINTR) && time.Now().Before(deadline) {
			continue
		}
		if err != nil || n != 1 {
			os.Exit(99)
		}
		break
	}
	os.Exit(code)
}

type environmentModel struct{ tea.Model }

func (m environmentModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if environment, ok := msg.(tea.EnvMsg); ok {
		if !reflect.DeepEqual([]string(environment), []string{"TERM=xterm-256color"}) {
			panic("runner did not use the explicit environment")
		}
		return m, tea.Quit
	}
	next, cmd := m.Model.Update(msg)
	m.Model = next
	return m, cmd
}

func TestPTYRunnerUsesExplicitEnvironment(t *testing.T) {
	c := startTerminal(t, "environment", 80)
	c.finish(t, 0)
}

// Faults are test-only model decorators, triggered after terminal readiness.
type faultModel struct {
	tea.Model
	scenario string
	armed    bool
}

func (m faultModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok && key.String() == "q" && m.scenario == "hang" {
		return m, nil
	}
	if key, ok := msg.(tea.KeyPressMsg); ok && key.String() == "!" {
		if m.scenario == "panic-update" {
			panic("C1a update fault")
		}
		m.armed = true
		return m, nil
	}
	next, cmd := m.Model.Update(msg)
	m.Model = next
	return m, cmd
}
func (m faultModel) View() tea.View {
	if m.armed {
		panic("C1a view fault")
	}
	return m.Model.View()
}

func TestPTYRestoresAfterPanic(t *testing.T) {
	for _, scenario := range []string{"panic-update", "panic-view"} {
		t.Run(scenario, func(t *testing.T) {
			c := startTerminal(t, scenario, 80)
			if err := c.ready(); err != nil {
				t.Fatal(err)
			}
			if err := c.send("!"); err != nil {
				t.Fatal(err)
			}
			c.finish(t, 1)
			// Round 1 of #10: the panic is recovered under the supervisor,
			// its value (foreign text, perhaps) never reaches the terminal;
			// the one fixed line does.
			out := c.bytes()
			if strings.Contains(out, "C1a ") || strings.Contains(out, "Caught panic") || strings.Contains(out, "goroutine ") {
				t.Fatalf("the panic reached the terminal:\n%q", out)
			}
			if !strings.Contains(out, "lalabase: terminal interface failed.") {
				t.Fatal("the safe text is missing")
			}
		})
	}
}

func TestPTYTimeoutKillsAndReapsGroup(t *testing.T) {
	c := startTerminal(t, "hang", 80)
	if err := c.ready(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c.bytes(), "C1A_DESCENDANT_STARTED") {
		t.Fatal("fixture omitted the descendant")
	}
	if err := c.descendantReady(); err != nil {
		t.Fatal(err)
	}
	if err := c.send("q"); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	err := c.until("deliberately hanging child", exitLimit, func() bool { return strings.Contains(c.bytes(), "C1A_RESULT=") })
	if err == nil {
		t.Fatalf("timeout counted as success after %s", time.Since(start))
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Fatal(err)
	}
	if err := c.stop(); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed < exitLimit || elapsed > exitLimit+cleanupLimit {
		t.Fatalf("unexpected deadline duration: %s", elapsed)
	}
	if err := c.descendantsExited(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.waitDone:
	default:
		t.Fatal("child not reaped")
	}
	t.Logf("timeout rejected; group killed and reaped after %s", time.Since(start))
}

func (c *terminalProcess) descendantReady() error {
	probe := &terminalProcess{deadline: c.deadline}
	return probe.until("descendant readiness", readyLimit, func() bool {
		var b [1]byte
		n, err := unix.Read(c.descendantFD, b[:])
		if err != nil && !errors.Is(err, unix.EAGAIN) && !errors.Is(err, unix.EINTR) {
			probe.setReadError(err)
		}
		if n == 0 && err == nil {
			probe.setReadError(errors.New("descendant exited before readiness"))
		}
		return n == 1 && b[0] == 'R'
	})
}

func (c *terminalProcess) descendantsExited() error {
	// Descendant liveness comes from a separate pipe and must not depend
	// on PTY reads after process cancellation.
	probe := &terminalProcess{deadline: c.deadline}
	return probe.until("descendant exit", cleanupLimit, func() bool {
		var b [1]byte
		n, err := unix.Read(c.descendantFD, b[:])
		if err != nil && !errors.Is(err, unix.EAGAIN) && !errors.Is(err, unix.EINTR) {
			probe.setReadError(err)
		}
		return n == 0 && err == nil
	})
}

func TestPTYCancellationKillsDescendants(t *testing.T) {
	c := startTerminal(t, "hang", 80)
	if err := c.ready(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c.bytes(), "C1A_DESCENDANT_STARTED") {
		t.Fatal("fixture omitted the descendant")
	}
	if err := c.descendantReady(); err != nil {
		t.Fatal(err)
	}
	c.cancelProcess()
	// Check before stop(), so its group kill cannot hide a broken Cancel.
	if err := c.descendantsExited(); err != nil {
		t.Fatal(err)
	}
	probe := &terminalProcess{deadline: c.deadline}
	if err := probe.until("canceled child reap", cleanupLimit, func() bool {
		select {
		case <-c.waitDone:
			return true
		default:
			return false
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := c.stop(); err != nil {
		t.Fatal(err)
	}
}

func TestPTYDeadlineCannotPassAfterExpiry(t *testing.T) {
	c := &terminalProcess{deadline: time.Now().Add(-time.Second)}
	if err := c.until("expired case", time.Second, func() bool { return true }); err == nil {
		t.Fatal("condition passed after the case deadline")
	}
}

func TestPTYDeadlineCannotPassDuringCondition(t *testing.T) {
	c := &terminalProcess{deadline: time.Now().Add(caseLimit)}
	err := c.until("slow condition", time.Millisecond, func() bool {
		time.Sleep(20 * time.Millisecond)
		return true
	})
	if err == nil || !strings.Contains(err.Error(), "slow condition timeout") {
		t.Fatalf("late success escaped the deadline: %v", err)
	}
}

func TestPTYResizePreservesFocus(t *testing.T) {
	c := startTerminal(t, "resize", 120)
	if err := c.ready(); err != nil {
		t.Fatal(err)
	}
	offset := len(c.bytes())
	if err := c.send("\x1b[C"); err != nil {
		t.Fatal(err)
	}
	// The title in bold; the renderer may join the bold with the end of
	// the separator's faint ("\x1b[22;1m").
	if err := c.until("object focus", readyLimit, func() bool {
		out := c.bytes()[offset:]
		return strings.Contains(out, "\x1b[1mObject") || strings.Contains(out, ";1mObject")
	}); err != nil {
		t.Fatalf("%v: %q", err, c.bytes()[offset:])
	}
	for _, width := range []uint16{79, 80, 119, 120, 79} {
		offset = len(c.bytes())
		if err := pty.Setsize(c.master, &pty.Winsize{Rows: 24, Cols: width}); err != nil {
			t.Fatal(err)
		}
		if err := c.cmd.Process.Signal(syscall.SIGWINCH); err != nil {
			t.Fatal(err)
		}
		marker := "No item opened"
		if width == 79 {
			marker = "Organisation > List > Object"
		}
		// Read the screen, not the byte stream: the renderer writes only
		// the cells that changed, so a marker can arrive in fragments
		// (" > List > Object" after an "Organisation" that was already
		// there), and whether it does depends on the frames before.
		if err := c.until(fmt.Sprintf("resize to %d", width), readyLimit, func() bool { return c.screenShows(width, marker) }); err != nil {
			t.Fatalf("%v: %q", err, c.bytes()[offset:])
		}
	}
	if err := c.send("q"); err != nil {
		t.Fatal(err)
	}
	c.finish(t, 0)
	if err := terminalOutputError(c.bytes()); err != nil {
		t.Fatal(err)
	}
}

func TestPTYRestoresOnQuitAndInterrupt(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		code        int
	}{{"quit", "q", 0}, {"ctrl-c", "\x03", 130}, {"sigint", "", 130}} {
		t.Run(tc.name, func(t *testing.T) {
			c := startTerminal(t, tc.name, 80)
			if err := c.ready(); err != nil {
				t.Fatalf("%v\n%s", err, c.bytes())
			}
			if tc.name == "sigint" {
				if err := c.cmd.Process.Signal(os.Interrupt); err != nil {
					t.Fatal(err)
				}
			} else if err := c.send(tc.input); err != nil {
				t.Fatal(err)
			}
			c.finish(t, tc.code)
		})
	}
}
