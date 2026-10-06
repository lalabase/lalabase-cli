//go:build !unix

package profile

import "io/fs"

// checkOwner has nothing to check where there are no Unix owners.
func checkOwner(fs.FileInfo) error { return nil }

// lock has no file lock to take here; the release builds are Unix only.
func lock(string) (func(), error) { return func() {}, nil }

// checkDirs has no owners to check here.
func checkDirs(string) error { return nil }

// checkACL has no access lists to read here.
func checkACL(string, aclZone) error { return nil }
