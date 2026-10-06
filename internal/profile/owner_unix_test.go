//go:build unix

package profile

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fakeInfo is file metadata with a chosen owner, so the owner check can be
// tested without a file of another user.
type fakeInfo struct {
	fs.FileInfo
	uid uint32
}

func (f fakeInfo) Sys() any { return &syscall.Stat_t{Uid: f.uid} }

func TestAFileOfAnotherUserIsRefused(t *testing.T) {
	path := writeConfig(t, twoProfiles, 0o600)
	real, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	me := uint32(os.Getuid()) //nolint:gosec // a uid fits
	if err := checkOwner(fakeInfo{real, me}); err != nil {
		t.Errorf("own file: %v", err)
	}
	if err := checkOwner(fakeInfo{real, me + 1}); err == nil || !strings.Contains(err.Error(), "another user") {
		t.Errorf("another user's file: %v", err)
	}
}

// A FIFO in place of the config must not block the open: a size limit on the
// read comes too late for that.
func TestAFIFOInPlaceOfTheConfigDoesNotBlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skip("no FIFO here:", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := Load(path)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "no regular file") {
			t.Fatalf("got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("opening a FIFO blocked")
	}
}
