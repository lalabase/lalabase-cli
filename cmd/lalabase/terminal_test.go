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
	"github.com/gboyke/lalabase-cli/internal/profile"
	"github.com/gboyke/lalabase-cli/internal/ui"
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
	before             *unix.Termios
	deadline           time.Time
	readDone, waitDone chan struct{}
	cancel             context.CancelFunc
	mu                 sync.Mutex
	output             bytes.Buffer
	readErr, errorExit error
}

func startTerminal(t *testing.T, scenario string, width uint16) *terminalProcess {
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
	home := t.TempDir()
	if err := profile.Save(filepath.Join(home, ".config", "lalabase", "config.toml"), profile.File{Profiles: map[string]profile.Entry{"default": {URL: "http://127.0.0.1:1", Token: "pty-dummy"}}}); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	processCtx, processCancel := context.WithTimeout(context.Background(), caseLimit)
	t.Cleanup(processCancel)
	cmd := exec.CommandContext(processCtx, executable, "-test.run=^TestTerminalChild$", "-test.timeout=20s", "--", scenario)
	cmd.Env = []string{"C1A_PTY_CHILD=1", "HOME=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, ".config"), "TERM=xterm-256color", "LC_ALL=C.UTF-8", "RUNEWIDTH_EASTASIAN=0", "CI=1"}
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
	cmd.WaitDelay = cleanupLimit
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = ack.Close()
	ctx, cancel := context.WithCancel(processCtx)
	c := &terminalProcess{cmd: cmd, master: master, slave: slave, before: before, deadline: time.Now().Add(caseLimit), readDone: make(chan struct{}), waitDone: make(chan struct{}), cancel: cancel}
	c.release = release
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
		if condition() {
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
		return err == nil && attrs.Lflag&(unix.ECHO|unix.ICANON) == 0 && strings.Contains(c.bytes(), "No data loaded") && lastMode(modes, 1049, true) && lastMode(modes, 2004, true) && lastMode(modes, 25, false)
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
	if !reflect.DeepEqual(c.before, after) {
		t.Fatalf("termios not restored:\nbefore %+v\nafter  %+v", c.before, after)
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
	for _, tc := range []struct {
		mode int
		end  bool
	}{{1049, false}, {25, true}, {2004, false}} {
		events := terminalModes(c.bytes())[tc.mode]
		if len(events) < 2 || events[0] == tc.end || events[len(events)-1] != tc.end {
			t.Fatalf("mode %d not restored: %v", tc.mode, events)
		}
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
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	env := Env{Context: ctx, Home: os.Getenv("HOME"), Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, Getenv: os.Getenv, Interactive: isTerminal(os.Stdin), OutputTerminal: isTerminal(os.Stdout)}
	scenario := os.Args[len(os.Args)-1]
	if scenario == "panic-update" || scenario == "panic-view" || scenario == "hang" {
		env.runUI = func(in io.Reader, out io.Writer) error {
			return ui.Run(ctx, in, out, []string{"TERM=xterm-256color"}, faultModel{Model: ui.New(), scenario: scenario})
		}
	}
	if scenario == "hostile" {
		env.runUI = func(in io.Reader, out io.Writer) error {
			return ui.Run(ctx, in, out, []string{"TERM=xterm-256color"}, ui.NewWithContent("SAFE_INLINE"+terminalHostile, "No data loaded\nSAFE_LIST"+terminalHostile, "SAFE_OBJECT"+terminalHostile))
		}
	}
	code := runWith(nil, env)
	_, _ = fmt.Fprintf(os.Stdout, "C1A_RESULT=%d\n", code)
	fds := []unix.PollFd{{Fd: 3, Events: unix.POLLIN}}
	if n, err := unix.Poll(fds, int(exitLimit/time.Millisecond)); err != nil || n != 1 {
		os.Exit(99)
	}
	os.Exit(code)
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
			if !strings.Contains(c.bytes(), "C1a ") {
				t.Fatal("fault did not reach panic handler")
			}
		})
	}
}

func TestPTYTimeoutKillsAndReapsGroup(t *testing.T) {
	c := startTerminal(t, "hang", 80)
	if err := c.ready(); err != nil {
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
	if err := unix.Kill(-c.cmd.Process.Pid, 0); !errors.Is(err, unix.ESRCH) {
		t.Fatalf("process group survived cleanup: %v", err)
	}
	select {
	case <-c.waitDone:
	default:
		t.Fatal("child not reaped")
	}
	t.Logf("timeout rejected; group killed and reaped after %s", time.Since(start))
}

func TestPTYDeadlineCannotPassAfterExpiry(t *testing.T) {
	c := &terminalProcess{deadline: time.Now().Add(-time.Second)}
	if err := c.until("expired case", time.Second, func() bool { return true }); err == nil {
		t.Fatal("condition passed after the case deadline")
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
	if err := c.until("object focus", readyLimit, func() bool { return strings.Contains(c.bytes()[offset:], "\x1b[1mObject") }); err != nil {
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
		if err := c.until(fmt.Sprintf("resize to %d", width), readyLimit, func() bool { return strings.Contains(c.bytes()[offset:], marker) }); err != nil {
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
