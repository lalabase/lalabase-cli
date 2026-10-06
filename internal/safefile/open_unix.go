//go:build unix

package safefile

import (
	"os"
	"syscall"
)

// openNoBlock opens for reading without blocking: opening a FIFO for reading
// would otherwise wait for a writer. Without follow, a link as the last part
// of the path is refused by the open itself.
func openNoBlock(path string, follow bool) (*os.File, error) {
	flags := os.O_RDONLY | syscall.O_NONBLOCK
	if !follow {
		flags |= syscall.O_NOFOLLOW
	}
	return os.OpenFile(path, flags, 0) //nolint:gosec // reading the path is the point
}
