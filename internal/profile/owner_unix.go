//go:build unix

package profile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
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
