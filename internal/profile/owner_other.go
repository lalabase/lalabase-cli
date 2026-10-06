//go:build !unix

package profile

import "io/fs"

// checkOwner has nothing to check where there are no Unix owners.
func checkOwner(fs.FileInfo) error { return nil }
