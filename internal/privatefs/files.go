package privatefs

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
)

// CreateExclusive creates the file path with data: exclusively (anything
// of that name, a link too, is an error and stays as it is), with the
// owner's bits of mode alone (set, not left to the umask: a launch script
// must be executable), synced, and checked as a private file of the user's
// own (OwnFile). A file that cannot be written whole is removed, so a
// reader never finds half of one. The directory is the caller's to check
// first (CheckDirs). Every file the launch and the agents' records create
// once and never replace comes this way (the launch files, the launch
// record, the seen mark); a file that is replaced, as an agent's state,
// goes through a temporary file and a rename instead.
func CreateExclusive(path string, data []byte, mode fs.FileMode) (err error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // a new file of the client's own, checked below
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = f.Close()
			_ = os.Remove(path)
		}
	}()
	if err := f.Chmod(mode & 0o700); err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if err := OwnFile(path, info); err != nil {
		return err
	}
	return f.Close()
}

// ErrTooManyEntries is Walk's answer to a directory with more entries than
// it may visit.
var ErrTooManyEntries = errors.New("too many entries")

// Walk calls visit for every entry of the directory dir, in the order the
// directory gives them, and stops at the first error visit returns. It
// visits at most limit entries: a directory with more is refused
// (ErrTooManyEntries), never read in part, so that a directory another
// process fills cannot keep the client reading. Entries are read in
// batches, never all at once. A missing directory is the error of
// os.Open (fs.ErrNotExist); the directory is the caller's to check first
// (CheckDirs).
func Walk(dir string, limit int, visit func(fs.DirEntry) error) error {
	d, err := os.Open(dir) //nolint:gosec // a directory of the client's own, checked by the caller
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	visited := 0
	for {
		entries, err := d.ReadDir(256)
		for _, e := range entries {
			if visited++; visited > limit {
				return fmt.Errorf("%s: %w", dir, ErrTooManyEntries)
			}
			if err := visit(e); err != nil {
				return err
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
