//go:build unix

package profile

import (
	"errors"
	"io/fs"
	"os"
	"syscall"
)

// lock takes an exclusive lock on a lock file (created 0600) and returns the
// function that releases it.
func lock(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600) //nolint:gosec // beside the config file
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil { //nolint:gosec // a file descriptor fits an int
		_ = f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:gosec // a file descriptor fits an int
		_ = f.Close()
	}, nil
}

// checkOwner refuses a config file that belongs to another user.
func checkOwner(info fs.FileInfo) error {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	if int(st.Uid) != os.Getuid() {
		return errors.New("belongs to another user")
	}
	return nil
}
