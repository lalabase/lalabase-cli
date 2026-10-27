package agents

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/gboyke/lalabase-cli/internal/privatefs"
	"github.com/gboyke/lalabase-cli/internal/safefile"
)

// An agent the person opened once from the client is seen (C9b): an ended
// one is then drawn faint. The client alone writes <agent id>.seen, after
// the open, and never replaces it; the hook neither reads nor writes it.
// It survives a restart of the client.

// seenRecord is <agent id>.seen: the format and when it was first seen.
type seenRecord struct {
	Format int       `json:"format"`
	At     time.Time `json:"at"`
}

// MarkSeen records that the person opened the agent id in dir, which must
// exist (Prepare). The file is created exclusively; one that is there
// already is left as it is and is no error, as long as it is a private
// file of the user's own (a link, a directory or a file others may change
// in its place is).
func MarkSeen(dir, id string, at time.Time) error {
	if !ValidID(id) {
		return errors.New("no agent's id")
	}
	if at.IsZero() {
		return errors.New("no time")
	}
	if err := existing(dir); err != nil {
		return err
	}
	data, err := json.Marshal(seenRecord{Format: FormatVersion, At: at.UTC()})
	if err != nil {
		return err
	}
	path := filepath.Join(dir, id+seenSuffix)
	err = createOwn(path, data)
	if !errors.Is(err, fs.ErrExist) {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("the seen file is no regular file")
	}
	return privatefs.OwnFile(path, info)
}

// seen reads <agent id>.seen: true only for a private file of the user's
// own in its format. Anything else says nothing, and the agent is not
// seen.
func seen(dir, id string) bool {
	path := filepath.Join(dir, id+seenSuffix)
	data, info, err := safefile.ReadRegularNoLink(path, maxFile)
	if err != nil || privatefs.OwnFile(path, info) != nil {
		return false
	}
	var r seenRecord
	return json.Unmarshal(data, &r) == nil && r.Format == FormatVersion && !r.At.IsZero()
}
