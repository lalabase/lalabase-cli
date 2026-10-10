//go:build !unix

package privatefs

import (
	"errors"
	"io/fs"
)

// ErrLocked: another process holds the lock (TryLock).
var ErrLocked = errors.New("held by another process")

// CheckOwner has nothing to check where there are no Unix owners.
func CheckOwner(fs.FileInfo) error { return nil }

// OwnFile has no owner or mode to check here.
func OwnFile(string, fs.FileInfo) error { return nil }

// Lock and TryLock have no file lock to take here; the release builds are
// Unix only.
func Lock(string) (func(), error)    { return func() {}, nil }
func TryLock(string) (func(), error) { return func() {}, nil }

// CheckDirs has no owners to check here.
func CheckDirs(string) error { return nil }

// CheckACL has no access lists to read here.
func CheckACL(string, Zone) error { return nil }
