package diskcache

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Plan test 13: two lalabase processes on one profile. The second gets
// ErrBusy at once (no wait), the first keeps the disk; when the first is
// gone the next open succeeds. The holder is this test binary run again
// (TestDiskCacheHolder), so a real second process holds the lock. It
// reports through a marker file beside the root (no terminal output from a
// package the interface reaches, not even in a test) and leaves when its
// stdin closes.

const holderEnv = "C1E_DISKCACHE_HOLD"

func TestDiskCacheHolder(t *testing.T) {
	root := os.Getenv(holderEnv)
	if root == "" {
		return
	}
	marker := filepath.Join(filepath.Dir(root), "holder")
	c, err := (Opener{Root: root}).Open("default", time.Now())
	if err != nil {
		_ = os.WriteFile(marker, []byte("FAILED "+err.Error()), 0o600)
		os.Exit(3)
	}
	if err := os.WriteFile(marker, []byte("HELD"), 0o600); err != nil {
		os.Exit(4)
	}
	// Until the parent closes our stdin.
	_, _ = io.Copy(io.Discard, os.Stdin)
	_ = c.Close()
	os.Exit(0)
}

func TestTwoProcessesOneProfile(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cache")
	marker := filepath.Join(filepath.Dir(root), "holder")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestDiskCacheHolder$", "-test.count=1")
	cmd.Env = append(os.Environ(), holderEnv+"="+root)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// waited is closed once the holder was reaped, so every wait on it
	// returns, however many there are.
	waited := make(chan struct{})
	var exit error
	go func() { exit = cmd.Wait(); close(waited) }()
	defer func() {
		_ = stdin.Close()
		select {
		case <-waited:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-waited
		}
	}()
	// The holder says when it has the lock.
	deadline := time.Now().Add(10 * time.Second)
	for {
		if data, err := os.ReadFile(marker); err == nil { //nolint:gosec // the test's own marker
			if string(data) != "HELD" {
				t.Fatalf("the holder said %q", data)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the holder did not take the lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// The second process: busy at once.
	done := make(chan error, 1)
	start := time.Now()
	go func() {
		_, err := (Opener{Root: root}).Open("default", time.Now())
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrBusy) {
			t.Fatalf("got %v, want ErrBusy", err)
		}
		if time.Since(start) > within {
			t.Fatalf("the open took %s", time.Since(start))
		}
	case <-time.After(within):
		t.Fatal("the second open waited for the holder")
	}
	// The holder goes; the lock with it.
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-waited:
		if exit != nil {
			t.Fatalf("the holder exited with %v", exit)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the holder did not exit")
	}
	c, err := (Opener{Root: root}).Open("default", time.Now())
	if err != nil {
		t.Fatalf("after the holder left: %v", err)
	}
	_ = c.Close()
}
