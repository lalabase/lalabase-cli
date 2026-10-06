package safefile

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func within(t *testing.T, f func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- f() }()
	select {
	case err := <-done:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("reading did not return: it blocked or read without end")
	}
	return nil
}

func TestReadsARegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.json")
	if err := os.WriteFile(path, []byte(`{"a":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	data, info, err := ReadRegular(path, 100)
	if err != nil || string(data) != `{"a":1}` || info.Size() != 7 {
		t.Fatalf("%q %v %v", data, info, err)
	}
}

func TestAMissingFileIsNotExist(t *testing.T) {
	_, _, err := ReadRegular(filepath.Join(t.TempDir(), "none"), 100)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("got %v", err)
	}
}

func TestALargerFileIsAnErrorNotACut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.json")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 101)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadRegular(path, 100); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("got %v", err)
	}
}

// A link to an endless device and a FIFO are refused at once: neither the
// open nor the read may hang.
func TestSpecialFilesAreRefusedWithoutHanging(t *testing.T) {
	dir := t.TempDir()
	zero := filepath.Join(dir, "zero.json")
	if err := os.Symlink("/dev/zero", zero); err != nil {
		t.Fatal(err)
	}
	if err := within(t, func() error { _, _, err := ReadRegular(zero, 1<<20); return err }); err == nil || !strings.Contains(err.Error(), "no regular file") {
		t.Fatalf("/dev/zero: %v", err)
	}
	fifo := filepath.Join(dir, "fifo.json")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skip("no FIFO here:", err)
	}
	if err := within(t, func() error { _, _, err := ReadRegular(fifo, 1<<20); return err }); err == nil || !strings.Contains(err.Error(), "no regular file") {
		t.Fatalf("FIFO: %v", err)
	}
}
