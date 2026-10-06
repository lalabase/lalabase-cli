//go:build unix

package profile

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
	path := writeConfig(t, twoProfiles, 0o600)
	real, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	me := uint32(os.Getuid()) //nolint:gosec // a uid fits
	if err := checkOwner(fakeInfo{real, me}); err != nil {
		t.Errorf("own file: %v", err)
	}
	if err := checkOwner(fakeInfo{real, me + 1}); err == nil || !strings.Contains(err.Error(), "another user") {
		t.Errorf("another user's file: %v", err)
	}
}

// A FIFO in place of the config must not block the open: a size limit on the
// read comes too late for that.
func TestAFIFOInPlaceOfTheConfigDoesNotBlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skip("no FIFO here:", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := Load(path)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "no regular file") {
			t.Fatalf("got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("opening a FIFO blocked")
	}
}

// Load itself refuses the file of another user, not only checkOwner.
func TestLoadRefusesAConfigOfAnotherUser(t *testing.T) {
	path := writeConfig(t, twoProfiles, 0o600)
	me := currentUID
	currentUID = func() int { return me() + 1 }
	dirsOf = func(string) error { return nil } // the file's own check, not its directory's
	t.Cleanup(func() { currentUID, dirsOf = me, checkDirs })
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), path+": belongs to another user") {
		t.Fatalf("got %v", err)
	}
}

// The lock file is opened without following a link and must be a regular
// file: a link would take the lock (and create a file) somewhere else, a FIFO
// would never lock.
func TestTheLockFileIsNoLinkAndNoFIFO(t *testing.T) {
	type prepared struct {
		make func(t *testing.T, lockPath string)
		want string
	}
	cases := map[string]prepared{
		"link": {want: "", make: func(t *testing.T, lockPath string) {
			if err := os.Symlink(filepath.Join(t.TempDir(), "elsewhere"), lockPath); err != nil {
				t.Fatal(err)
			}
		}},
		// Whether flock refuses a FIFO depends on the system (macOS does,
		// Linux does not): the type check must say so itself.
		"fifo": {want: "no regular file", make: func(t *testing.T, lockPath string) {
			if err := syscall.Mkfifo(lockPath, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			path := writeConfig(t, twoProfiles, 0o600)
			tc.make(t, path+".lock")
			done := make(chan error, 1)
			go func() { done <- Update(path, func(*File) error { return nil }) }()
			select {
			case err := <-done:
				if err == nil || !strings.Contains(err.Error(), path+".lock") || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("got %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Update hangs on the lock file")
			}
		})
	}
}

// The config is only as safe as every directory on its way: whoever can
// change one of them can swap the file or its lock file. Checked from / down,
// through links (the directories holding a link and the ones it leads to).
func TestEveryDirectoryOnTheWayMustBeOurs(t *testing.T) {
	type layout func(t *testing.T, root string) (configDir string)
	open := func(t *testing.T, dir string) {
		t.Helper()
		if err := os.Chmod(dir, 0o777); err != nil { //nolint:gosec // the test opens it on purpose
			t.Fatal(err)
		}
	}
	mkdir := func(t *testing.T, dir string) string {
		t.Helper()
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	refused := map[string]layout{
		"the config directory": func(t *testing.T, root string) string {
			dir := mkdir(t, filepath.Join(root, "home", ".config", "lalabase"))
			open(t, dir)
			return dir
		},
		"a directory above it": func(t *testing.T, root string) string {
			open(t, mkdir(t, filepath.Join(root, "home", ".config")))
			return mkdir(t, filepath.Join(root, "home", ".config", "lalabase"))
		},
		"the target of a link": func(t *testing.T, root string) string {
			target := mkdir(t, filepath.Join(root, "elsewhere", "lalabase"))
			open(t, target)
			mkdir(t, filepath.Join(root, "home", ".config"))
			link := filepath.Join(root, "home", ".config", "lalabase")
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			return link
		},
		"a directory above the target of a link": func(t *testing.T, root string) string {
			open(t, mkdir(t, filepath.Join(root, "elsewhere")))
			target := mkdir(t, filepath.Join(root, "elsewhere", "lalabase"))
			mkdir(t, filepath.Join(root, "home", ".config"))
			link := filepath.Join(root, "home", ".config", "lalabase")
			if err := os.Symlink("../../elsewhere/lalabase", link); err != nil {
				t.Fatal(err)
			}
			_ = target
			return link
		},
		"a directory holding a link": func(t *testing.T, root string) string {
			target := mkdir(t, filepath.Join(root, "elsewhere", "lalabase"))
			dir := mkdir(t, filepath.Join(root, "home", ".config"))
			if err := os.Symlink(target, filepath.Join(dir, "lalabase")); err != nil {
				t.Fatal(err)
			}
			open(t, dir)
			return filepath.Join(dir, "lalabase")
		},
	}
	for name, build := range refused {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			dir := build(t, root)
			path := filepath.Join(dir, "config.toml")
			if err := os.WriteFile(path, []byte(twoProfiles), 0o600); err != nil {
				t.Fatal(err)
			}
			for op, run := range map[string]func() error{
				"Load":   func() error { _, err := Load(path); return err },
				"Save":   func() error { return Save(path, File{Profiles: map[string]Entry{}}) },
				"Update": func() error { return Update(path, func(*File) error { return nil }) },
			} {
				err := run()
				if err == nil || !strings.Contains(err.Error(), "chmod go-w") {
					t.Errorf("%s: got %v", op, err)
				}
			}
			if data, _ := os.ReadFile(path); string(data) != twoProfiles {
				t.Fatal("the config was written")
			}
			// Update checks before it locks: no lock file is made in a
			// directory others may change.
			if _, err := os.Lstat(path + ".lock"); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("a lock file was made: %v", err)
			}
		})
	}
}

// A directory others may write into but not take from (the sticky bit, as on
// /tmp) is no way in.
func TestAStickyDirectoryOnTheWayIsAccepted(t *testing.T) {
	sticky := filepath.Join(t.TempDir(), "shared")
	if err := os.Mkdir(sticky, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sticky, 0o777|os.ModeSticky); err != nil { //nolint:gosec // like /tmp
		t.Fatal(err)
	}
	path := filepath.Join(sticky, "lalabase", "config.toml")
	if err := Save(path, File{Profiles: map[string]Entry{"default": {URL: "https://lalabase.com", Token: "lalab_pat_x0000000"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatal(err)
	}
}

// A directory of another user is no chmod away: the message says so.
func TestADirectoryOfAnotherUserIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lalabase", "config.toml")
	me := currentUID
	currentUID = func() int { return me() + 1 }
	t.Cleanup(func() { currentUID = me })
	err := Save(path, File{Profiles: map[string]Entry{}})
	if err == nil || !strings.Contains(err.Error(), "belongs to another user") || strings.Contains(err.Error(), "chmod") {
		t.Fatalf("got %v", err)
	}
}

// Links that lead in a circle end the check, they do not hang it.
func TestALinkLoopIsRefused(t *testing.T) {
	root := t.TempDir()
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	if err := os.Symlink(b, a); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(a, b); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := Load(filepath.Join(a, "config.toml")); done <- err }()
	select {
	case err := <-done:
		if err == nil || errors.Is(err, ErrNoFile) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Load hangs on a link loop")
	}
}

// The config itself is never a link: what it points to lies in a directory
// nobody checked.
func TestTheConfigIsNoLink(t *testing.T) {
	root := t.TempDir()
	safe := filepath.Join(root, "safe")
	bad := filepath.Join(root, "bad")
	for _, d := range []string{safe, bad} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(bad, 0o777); err != nil { //nolint:gosec // the test opens it on purpose
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, "config.toml"), []byte(twoProfiles), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(safe, "config.toml")
	if err := os.Symlink("../bad/config.toml", link); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(link); err == nil || !strings.Contains(err.Error(), "is a link") {
		t.Fatalf("got %v", err)
	}
}

// The config's own directory is the user's alone: a sticky bit lets others
// add entries there (a lock file of theirs, say), so it is no excuse here.
func TestTheConfigDirectoryIsNeverShared(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "lalabase")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(twoProfiles), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o777|os.ModeSticky); err != nil { //nolint:gosec // the test opens it on purpose
		t.Fatal(err)
	}
	for op, run := range map[string]func() error{
		"Load":   func() error { _, err := Load(path); return err },
		"Save":   func() error { return Save(path, File{Profiles: map[string]Entry{}}) },
		"Update": func() error { return Update(path, func(*File) error { return nil }) },
	} {
		if err := run(); err == nil || !strings.Contains(err.Error(), "chmod go-w") {
			t.Errorf("%s: got %v", op, err)
		}
	}
}

// A lock file that others may change, or that belongs to somebody else, is
// refused before anything waits on it: its owner could hold it forever, or
// swap it while we hold it.
func TestALockFileOfOthersIsRefused(t *testing.T) {
	// Reading is enough to hold a lock (flock with LOCK_SH on a descriptor
	// opened for reading): no right at all for anybody else.
	for _, mode := range []os.FileMode{0o640, 0o604, 0o620, 0o602, 0o666} {
		t.Run(fmt.Sprintf("mode %o", mode), func(t *testing.T) {
			path := writeConfig(t, twoProfiles, 0o600)
			if err := os.WriteFile(path+".lock", nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path+".lock", mode); err != nil {
				t.Fatal(err)
			}
			if err := Update(path, func(*File) error { return nil }); err == nil || !strings.Contains(err.Error(), path+".lock") || !strings.Contains(err.Error(), "chmod 600") {
				t.Fatalf("got %v", err)
			}
		})
	}
	t.Run("of another user, held", func(t *testing.T) {
		path := writeConfig(t, twoProfiles, 0o600)
		held, err := os.OpenFile(path+".lock", os.O_RDWR|os.O_CREATE, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = held.Close() }()
		if err := syscall.Flock(int(held.Fd()), syscall.LOCK_EX); err != nil { //nolint:gosec // a file descriptor fits an int
			t.Fatal(err)
		}
		foreignAt(t, path+".lock")
		done := make(chan error, 1)
		go func() { done <- Update(path, func(*File) error { return nil }) }()
		select {
		case err := <-done:
			if err == nil || !strings.Contains(err.Error(), "another user") {
				t.Fatalf("got %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Update waits on a lock file of another user")
		}
	})
}

// foreignAt makes the entry at path look owned by another user.
func foreignAt(t *testing.T, path string) {
	t.Helper()
	old := ownerOf
	ownerOf = func(p string, info fs.FileInfo) uint32 {
		if p == path {
			return uint32(currentUID()) + 1 //nolint:gosec // a uid fits
		}
		return old(p, info)
	}
	t.Cleanup(func() { ownerOf = old })
}

// A link on the way that belongs to somebody else can be pointed elsewhere
// by its owner at any time, even in a sticky directory: refused, wherever it
// points now.
func TestALinkOfAnotherUserIsRefused(t *testing.T) {
	root := realTempDir(t)
	target := filepath.Join(root, "real")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	foreignAt(t, link)
	path := filepath.Join(link, "config.toml")
	if err := Save(path, File{Profiles: map[string]Entry{}}); err == nil || !strings.Contains(err.Error(), "link of another user") {
		t.Fatalf("got %v", err)
	}
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
	if err := checkDirs(dir); err == nil || !strings.Contains(err.Error(), "far/leaf") {
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
	if err := checkDirs(chain(40)); err != nil {
		t.Fatalf("40 links: %v", err)
	}
	if err := checkDirs(chain(41)); err == nil || !strings.Contains(err.Error(), "too many links") {
		t.Fatalf("41 links: %v", err)
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
