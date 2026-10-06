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
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, fmt.Errorf("lock file %s is no regular file", path)
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

// maxLinks bounds the links followed while checking a path, as the kernel
// bounds them while resolving one.
const maxLinks = 40

// checkDirs walks dir from / down, resolving links as the kernel would, and
// refuses it when a directory on the way could be changed by another user:
// every directory entered, which includes the ones holding a link and the
// ones a link leads through. Such a directory is owned by this user or root,
// and not writable by group or others unless sticky (as /tmp is: others
// may add entries there, but not take ours). A missing directory ends the
// walk: nothing below it exists yet.
func checkDirs(dir string) error {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if err := trusted("/"); err != nil {
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
		if err := trustedInfo(next, info); err != nil {
			return err
		}
		cur = next
	}
	return nil
}

func trusted(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	return trustedInfo(path, info)
}

func trustedInfo(path string, info fs.FileInfo) error {
	if !info.IsDir() {
		return fmt.Errorf("%s is no directory", path)
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && st.Uid != 0 && int(st.Uid) != currentUID() {
		return fmt.Errorf("%s belongs to another user (uid %d); the config may only lie in directories of your own or of root", path, st.Uid)
	}
	if info.Mode().Perm()&0o022 != 0 && info.Mode()&fs.ModeSticky == 0 {
		return fmt.Errorf("others may change %s (mode %o), and with it the config; run: chmod go-w %s", path, info.Mode().Perm(), path)
	}
	return nil
}
