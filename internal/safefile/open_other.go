//go:build !unix

package safefile

import "os"

func openNoBlock(path string) (*os.File, error) { return os.Open(path) } //nolint:gosec // reading the path is the point
