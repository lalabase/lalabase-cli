//go:build unix

package profile

import (
	"errors"
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
