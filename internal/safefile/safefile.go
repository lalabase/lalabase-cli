// Package safefile reads files that come from places the client does not
// control: its own config file, Claude Code's configuration, a .mcp.json in
// whatever directory the command runs in.
//
// One rule for all of them: only a regular file is read, opened without
// blocking (a FIFO in its place would make a plain open wait for a writer),
// checked on the opened descriptor (no swap between check and read) and read
// up to a limit (a link to /dev/zero would otherwise read forever). A file
// larger than the limit is an error, never cut.
package safefile

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
)

// ReadRegular reads path if it is a regular file of at most max bytes. A
// missing file reports fs.ErrNotExist. info describes the opened file.
func ReadRegular(path string, maxBytes int64) ([]byte, fs.FileInfo, error) {
	return read(path, maxBytes, true)
}

// ReadRegularNoLink is ReadRegular for a file that must not be a link: a link
// in its place is refused, not followed (ErrLink).
func ReadRegularNoLink(path string, maxBytes int64) ([]byte, fs.FileInfo, error) {
	return read(path, maxBytes, false)
}

// ErrLink: the path is a link where none may be.
var ErrLink = errors.New("is a link")

func read(path string, maxBytes int64, follow bool) ([]byte, fs.FileInfo, error) {
	f, err := openNoBlock(path, follow)
	if err != nil && !follow {
		if info, lerr := os.Lstat(path); lerr == nil && info.Mode()&fs.ModeSymlink != 0 {
			return nil, nil, fmt.Errorf("%s %w", path, ErrLink)
		}
	}
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, info, fmt.Errorf("%s is no regular file", path)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return nil, info, err
	}
	if int64(len(data)) > maxBytes {
		return nil, info, fmt.Errorf("%s is larger than %d bytes; not reading it", path, maxBytes)
	}
	return data, info, nil
}
