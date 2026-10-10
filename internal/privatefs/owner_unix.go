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
)

// ErrLocked: another process holds the lock (TryLock).
var ErrLocked = errors.New("held by another process")

// Lock takes an exclusive lock on a lock file (created 0600) and returns the
// function that releases it. It waits for a holder to let go. The lock file
// is opened without following a link (that would lock, and create, a file
// elsewhere) and without blocking, and must be a regular file: a FIFO or
// device takes no lock.
func Lock(path string) (func(), error) { return lock(path, true) }

// TryLock is Lock without the wait: a lock somebody else holds is ErrLocked
// at once. The disk cache uses it: a second lalabase on the same profile
// falls back to memory instead of waiting for the first to quit.
func TryLock(path string) (func(), error) { return lock(path, false) }

func lock(path string, wait bool) (func(), error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600) //nolint:gosec // beside the file it guards
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
	if err := OwnFile(path, info); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("lock file %w", err)
	}
	how := syscall.LOCK_EX
	if !wait {
		how |= syscall.LOCK_NB
	}
	if err := syscall.Flock(int(f.Fd()), how); err != nil { //nolint:gosec // a file descriptor fits an int
		_ = f.Close()
		if !wait && (errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)) {
			return nil, fmt.Errorf("lock file %s: %w", path, ErrLocked)
		}
		return nil, fmt.Errorf("lock file %s: %w", path, err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:gosec // a file descriptor fits an int
		_ = f.Close()
	}, nil
}

// CurrentUID is the user the files must belong to (tests replace it).
var CurrentUID = os.Getuid

// OwnerOf names the owner of the entry at path (tests replace it to make
// one entry look foreign).
var OwnerOf = func(_ string, info fs.FileInfo) uint32 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return st.Uid
	}
	return uint32(CurrentUID()) //nolint:gosec // a uid fits
}

// CheckOwner refuses a file that belongs to another user, by the metadata
// of the opened file.
func CheckOwner(info fs.FileInfo) error {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	if int(st.Uid) != CurrentUID() {
		return errors.New("belongs to another user")
	}
	return nil
}

// OwnFile: a private file (the config's lock, a cache entry) is the user's
// alone: owned by the user, no right at all for anybody else (reading is
// enough to hold a lock: flock with LOCK_SH on a descriptor opened for
// reading), no access list letting others in.
func OwnFile(path string, info fs.FileInfo) error {
	if uid := OwnerOf(path, info); int(uid) != CurrentUID() {
		return fmt.Errorf("%s belongs to another user (uid %d)", path, uid)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s is open to others (mode %o), who could hold it and block every save; run: chmod 600 %s", path, info.Mode().Perm(), path)
	}
	return CheckACL(path, Private)
}

// maxLinks bounds the links followed while checking a path, as Linux bounds
// them while resolving one (macOS stops at 32; the open then fails anyway).
const maxLinks = 40

// CheckDirs checks every directory on the way to dir against the package's
// threat model, and dir itself as a private directory. dir is walked from /
// down, links resolved as the kernel resolves them. A missing directory ends
// the walk: nothing below it exists yet.
func CheckDirs(dir string) error {
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
			if uid := OwnerOf(next, info); uid != 0 && int(uid) != CurrentUID() {
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
	// cur is the private directory, resolved.
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
	if uid := OwnerOf(path, info); uid != 0 && int(uid) != CurrentUID() {
		return fmt.Errorf("%s belongs to another user (uid %d); a private file may only lie in directories of your own or of root", path, uid)
	}
	if info.Mode().Perm()&0o022 != 0 && info.Mode()&fs.ModeSticky == 0 {
		return fmt.Errorf("others may change %s (mode %o), and with it what lies below; run: chmod go-w %s", path, info.Mode().Perm(), path)
	}
	return CheckACL(path, Passage)
}

// privateDir checks the file's own directory: the user's alone.
func privateDir(path string, info fs.FileInfo) error {
	if !info.IsDir() {
		return fmt.Errorf("%s is no directory", path)
	}
	if uid := OwnerOf(path, info); int(uid) != CurrentUID() {
		return fmt.Errorf("%s belongs to another user (uid %d); a private directory must be your own", path, uid)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("others may add files to %s (mode %o), a private directory; run: chmod go-w %s", path, info.Mode().Perm(), path)
	}
	return CheckACL(path, Private)
}
