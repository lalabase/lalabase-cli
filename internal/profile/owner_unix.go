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
)

// lock takes an exclusive lock on a lock file (created 0600) and returns the
// function that releases it. The lock file is opened without following a
// link (that would lock, and create, a file elsewhere) and without blocking,
// and must be a regular file: a FIFO or device takes no lock.
func lock(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600) //nolint:gosec // beside the config file
	if err != nil {
		return nil, err // names the path
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, fmt.Errorf("lock file %s is no regular file", path)
	}
	// Checked before anything waits on it: a lock file others may change,
	// or that belongs to somebody else, could be held forever or swapped
	// while we hold it.
	if err := ownFile(path, info); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("lock file %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil { //nolint:gosec // a file descriptor fits an int
		_ = f.Close()
		return nil, fmt.Errorf("lock file %s: %w", path, err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:gosec // a file descriptor fits an int
		_ = f.Close()
	}, nil
}

// currentUID is the user the config must belong to (tests replace it).
var currentUID = os.Getuid

// ownerOf names the owner of the entry at path (tests replace it to make one
// entry look foreign).
var ownerOf = func(_ string, info fs.FileInfo) uint32 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return st.Uid
	}
	return uint32(currentUID()) //nolint:gosec // a uid fits
}

// checkOwner refuses a config file that belongs to another user.
func checkOwner(info fs.FileInfo) error {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	if int(st.Uid) != currentUID() {
		return errors.New("belongs to another user")
	}
	return nil
}

// ownFile: a file beside the config (its lock) is the user's alone: owned
// by the user, writable by nobody else, no access list letting others in.
func ownFile(path string, info fs.FileInfo) error {
	if uid := ownerOf(path, info); int(uid) != currentUID() {
		return fmt.Errorf("%s belongs to another user (uid %d)", path, uid)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%s may be changed by others (mode %o); run: chmod go-w %s", path, info.Mode().Perm(), path)
	}
	return checkACL(path, private)
}

// maxLinks bounds the links followed while checking a path, as Linux bounds
// them while resolving one (macOS stops at 32; the open then fails anyway).
const maxLinks = 40

// The threat checkDirs closes: another local user without root, who can
// change only what is writable for them and replace only entries they own.
// Against that user every directory on the way to the config must hold:
//
//   - a directory passed through belongs to the user or root and is not
//     writable by group or others, unless sticky (as /tmp: others may add
//     entries there, but only replace their own);
//   - a link passed through belongs to the user or root: in a sticky
//     directory its owner may point it elsewhere at any time;
//   - the config's own directory belongs to the user and is writable by
//     nobody else, sticky or not (others could add a lock file of theirs);
//   - no access list lets others in where the mode bits do not (macOS).
//
// dir is walked from / down, links resolved as the kernel resolves them. A
// missing directory ends the walk: nothing below it exists yet.
func checkDirs(dir string) error {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if err := passable("/"); err != nil {
		return err
	}
	pending := strings.Split(dir, "/")
	cur, links := "/", 0
	for len(pending) > 0 {
		name := pending[0]
		pending = pending[1:]
		switch name {
		case "", ".":
			continue
		case "..":
			cur = filepath.Dir(cur) // cur is resolved: its parent was entered before
			continue
		}
		next := filepath.Join(cur, name)
		info, err := os.Lstat(next)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			if uid := ownerOf(next, info); uid != 0 && int(uid) != currentUID() {
				return fmt.Errorf("%s is a link of another user (uid %d), who may point it elsewhere at any time", next, uid)
			}
			if links++; links > maxLinks {
				return fmt.Errorf("%s: too many links on the way", dir)
			}
			target, err := os.Readlink(next)
			if err != nil {
				return err
			}
			if filepath.IsAbs(target) {
				cur = "/"
			}
			pending = append(strings.Split(target, "/"), pending...)
			continue
		}
		if err := passableInfo(next, info); err != nil {
			return err
		}
		cur = next
	}
	// cur is the config's directory, resolved.
	info, err := os.Lstat(cur)
	if err != nil {
		return err
	}
	return privateDir(cur, info)
}

func passable(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	return passableInfo(path, info)
}

// passableInfo checks a directory passed through.
func passableInfo(path string, info fs.FileInfo) error {
	if !info.IsDir() {
		return fmt.Errorf("%s is no directory", path)
	}
	if uid := ownerOf(path, info); uid != 0 && int(uid) != currentUID() {
		return fmt.Errorf("%s belongs to another user (uid %d); the config may only lie in directories of your own or of root", path, uid)
	}
	if info.Mode().Perm()&0o022 != 0 && info.Mode()&fs.ModeSticky == 0 {
		return fmt.Errorf("others may change %s (mode %o), and with it the config; run: chmod go-w %s", path, info.Mode().Perm(), path)
	}
	return checkACL(path, passage)
}

// privateDir checks the config's own directory: the user's alone.
func privateDir(path string, info fs.FileInfo) error {
	if !info.IsDir() {
		return fmt.Errorf("%s is no directory", path)
	}
	if uid := ownerOf(path, info); int(uid) != currentUID() {
		return fmt.Errorf("%s belongs to another user (uid %d); the config's directory must be your own", path, uid)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("others may add files to %s (mode %o), the config's directory; run: chmod go-w %s", path, info.Mode().Perm(), path)
	}
	return checkACL(path, private)
}
