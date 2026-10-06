//go:build unix

package safefile

import (
	"os"
	"syscall"
)

// openNoBlock opens for reading without blocking: opening a FIFO for reading
// would otherwise wait for a writer.
func openNoBlock(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0) //nolint:gosec // reading the path is the point
}
