//go:build unix

package privatefs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fakeInfo is file metadata with a chosen owner, so the owner check can be
// tested without a file of another user.
type fakeInfo struct {
	fs.FileInfo
	uid uint32
}

func (f fakeInfo) Sys() any { return &syscall.Stat_t{Uid: f.uid} }

func TestAFileOfAnotherUserIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	real, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	me := uint32(os.Getuid()) //nolint:gosec // a uid fits
	if err := CheckOwner(fakeInfo{real, me}); err != nil {
		t.Errorf("own file: %v", err)
	}
	if err := CheckOwner(fakeInfo{real, me + 1}); err == nil || !strings.Contains(err.Error(), "another user") {
		t.Errorf("another user's file: %v", err)
	}
}

// OwnFile: the user's alone, no right at all for anybody else.
func TestOwnFileRefusesEveryRightOfOthers(t *testing.T) {
	for _, mode := range []os.FileMode{0o640, 0o604, 0o620, 0o602, 0o666, 0o700} {
		path := filepath.Join(t.TempDir(), "f")
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		err = OwnFile(path, info)
		if mode == 0o700 {
			if err != nil {
				t.Errorf("mode %o: %v", mode, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), "chmod 600") {
			t.Errorf("mode %o: got %v", mode, err)
		}
	}
	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	foreignAt(t, path)
	if err := OwnFile(path, info); err == nil || !strings.Contains(err.Error(), "another user") {
		t.Fatalf("a foreign file: %v", err)
	}
}

// foreignAt makes the entry at path look owned by another user.
func foreignAt(t *testing.T, path string) {
	t.Helper()
	old := OwnerOf
	OwnerOf = func(p string, info fs.FileInfo) uint32 {
		if p == path {
			return uint32(CurrentUID()) + 1 //nolint:gosec // a uid fits
		}
		return old(p, info)
	}
	t.Cleanup(func() { OwnerOf = old })
}

// Links are resolved as the kernel resolves them: a ".." after a link goes
// up from where the link led, not from where it lies.
func TestDotDotAfterALinkGoesUpFromItsTarget(t *testing.T) {
	root := t.TempDir()
	mk := func(rel string, mode os.FileMode) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
	}
	mk("far/deep", 0o700)
	mk("far/leaf", 0o777) // where the kernel arrives
	mk("leaf", 0o700)     // where a lexical shortcut would arrive
	if err := os.Symlink("far/deep", filepath.Join(root, "hop")); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "L")
	if err := os.Symlink("hop/../leaf", dir); err != nil {
		t.Fatal(err)
	}
	got, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.Stat(filepath.Join(root, "far", "leaf"))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(got, want) {
		t.Fatal("the kernel resolves the link elsewhere; the test's premise is wrong")
	}
	if err := CheckDirs(dir); err == nil || !strings.Contains(err.Error(), "far/leaf") {
		t.Fatalf("got %v", err)
	}
}

// At most 40 links on the way, as Linux allows (macOS stops at 32, and then
// the open fails on its own).
func TestTheLinkLimitIsForty(t *testing.T) {
	chain := func(n int) string {
		root := realTempDir(t)
		end := filepath.Join(root, "end")
		if err := os.Mkdir(end, 0o700); err != nil {
			t.Fatal(err)
		}
		prev := end
		for i := range n {
			l := filepath.Join(root, fmt.Sprintf("l%d", i))
			if err := os.Symlink(prev, l); err != nil {
				t.Fatal(err)
			}
			prev = l
		}
		return prev
	}
	if err := CheckDirs(chain(40)); err != nil {
		t.Fatalf("40 links: %v", err)
	}
	if err := CheckDirs(chain(41)); err == nil || !strings.Contains(err.Error(), "too many links") {
		t.Fatalf("41 links: %v", err)
	}
}

// The private directory itself: the user's alone, sticky or not; a
// directory above may be sticky and open (as /tmp).
func TestThePrivateDirectoryIsNeverSharedAStickyParentIs(t *testing.T) {
	sticky := filepath.Join(t.TempDir(), "shared")
	if err := os.Mkdir(sticky, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sticky, 0o777|os.ModeSticky); err != nil { //nolint:gosec // like /tmp
		t.Fatal(err)
	}
	dir := filepath.Join(sticky, "own")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := CheckDirs(dir); err != nil {
		t.Fatalf("a private directory below a sticky one: %v", err)
	}
	if err := os.Chmod(dir, 0o777|os.ModeSticky); err != nil { //nolint:gosec // the test opens it on purpose
		t.Fatal(err)
	}
	if err := CheckDirs(dir); err == nil || !strings.Contains(err.Error(), "chmod go-w") {
		t.Fatalf("a sticky private directory: %v", err)
	}
}

// realTempDir is t.TempDir with its links resolved (on macOS /var is one), so
// a test counts and names exactly the links it made.
func realTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// TryLock answers at once: a lock held elsewhere is ErrLocked, never a
// wait. Two descriptors of one process conflict under flock like two
// processes do, so the holder can be this test (the two-process case is
// the disk cache's, internal/diskcache).
func TestTryLockNeverWaits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	unlock, err := TryLock(path)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := TryLock(path); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, ErrLocked) {
			t.Fatalf("got %v, want ErrLocked", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("TryLock waited for the holder")
	}
	unlock()
	again, err := TryLock(path)
	if err != nil {
		t.Fatalf("after the release: %v", err)
	}
	again()
}

// The lock file is opened without following a link and must be a regular
// file.
func TestTheLockFileIsNoLinkAndNoFIFO(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "link.lock")
	if err := os.Symlink(filepath.Join(t.TempDir(), "elsewhere"), link); err != nil {
		t.Fatal(err)
	}
	if _, err := TryLock(link); err == nil || !strings.Contains(err.Error(), link) {
		t.Fatalf("a link: %v", err)
	}
	fifo := filepath.Join(dir, "fifo.lock")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := TryLock(fifo); done <- err }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "no regular file") {
			t.Fatalf("a FIFO: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("TryLock hangs on a FIFO")
	}
}
