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
	"fmt"
	"io"
	"io/fs"
)

// ReadRegular reads path if it is a regular file of at most max bytes. A
// missing file reports fs.ErrNotExist. info describes the opened file.
func ReadRegular(path string, maxBytes int64) ([]byte, fs.FileInfo, error) {
	f, err := openNoBlock(path)
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
