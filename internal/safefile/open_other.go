//go:build !unix

package safefile

import "os"

// openNoBlock: no FIFOs and no link flag to pass here; a link is caught by
// the caller's Lstat.
func openNoBlock(path string, follow bool) (*os.File, error) {
	if !follow {
		if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return nil, &os.PathError{Op: "open", Path: path, Err: os.ErrInvalid}
		}
	}
	return os.Open(path) //nolint:gosec // reading the path is the point
}
