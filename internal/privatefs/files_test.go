//go:build unix

package privatefs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// A new file is created with its data and the owner's bits of its mode
// alone; anything of that name, a link to elsewhere too, is left as it is
// and is an error.
func TestCreateExclusiveCreatesOnlyANewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "script")
	if err := CreateExclusive(path, []byte("data"), 0o755); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("mode %o, want the owner's bits alone", got)
	}
	if data, _ := os.ReadFile(path); string(data) != "data" { //nolint:gosec // the test's file
		t.Fatalf("data %q", data)
	}
	if err := CreateExclusive(path, []byte("other"), 0o600); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("an existing file: %v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "data" { //nolint:gosec // the test's file
		t.Fatalf("the existing file was changed: %q", data)
	}
	target := filepath.Join(dir, "target")
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := CreateExclusive(link, []byte("x"), 0o600); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("a dangling link: %v", err)
	}
	if _, err := os.Lstat(target); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the link's target was created: %v", err)
	}
}

// The group's and others' bits of mode are dropped, not refused, so no
// file is created readable by others; without its directory nothing is
// created.
func TestCreateExclusiveDropsOthersBitsAndNeedsItsDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	if err := CreateExclusive(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("%v, %v", info.Mode(), err)
	}
	if err := CreateExclusive(filepath.Join(t.TempDir(), "missing", "f"), nil, 0o600); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("no directory: %v", err)
	}
}

// Walk visits every entry up to the limit and refuses a directory with
// more, whole: nothing of a directory too large is visited past the limit.
func TestWalkIsBounded(t *testing.T) {
	dir := t.TempDir()
	for i := range 300 {
		if err := os.WriteFile(filepath.Join(dir, strconv.Itoa(i)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		limit int
		err   error
	}{{300, nil}, {299, ErrTooManyEntries}} {
		visited := 0
		err := Walk(dir, c.limit, func(fs.DirEntry) error { visited++; return nil })
		if !errors.Is(err, c.err) || visited > c.limit {
			t.Fatalf("limit %d: %v, %d visited", c.limit, err, visited)
		}
		if c.err == nil && visited != 300 {
			t.Fatalf("limit %d: %d visited", c.limit, visited)
		}
	}
}

// The first error of visit ends the walk and is Walk's; a missing
// directory is fs.ErrNotExist, for the caller to take as empty.
func TestWalkStopsAtVisitsError(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a", "b", "c"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	stop := errors.New("stop")
	visited := 0
	if err := Walk(dir, 10, func(fs.DirEntry) error { visited++; return stop }); !errors.Is(err, stop) || visited != 1 {
		t.Fatalf("%v after %d", err, visited)
	}
	if err := Walk(filepath.Join(dir, "missing"), 10, func(fs.DirEntry) error { return nil }); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing: %v", err)
	}
}
