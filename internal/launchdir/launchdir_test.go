//go:build unix

package launchdir

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func realTemp(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode()
}

// now is the real clock: a new directory carries its time.
var now = time.Now()

func TestALaunchDirectoryAndItsFiles(t *testing.T) {
	root := filepath.Join(realTemp(t), "cli", "default", "l")
	d, err := Make(root, now)
	if err != nil {
		t.Fatal(err)
	}
	if !idForm.MatchString(d.ID) || d.Path != filepath.Join(root, d.ID) {
		t.Fatalf("dir %+v", d)
	}
	for _, dir := range []string{root, d.Path} {
		if m := mode(t, dir); !m.IsDir() || m.Perm() != 0o700 {
			t.Errorf("%s: mode %v", dir, m)
		}
	}
	for _, c := range []struct {
		name string
		data string
		mode os.FileMode
	}{
		{MCPConfig, `{"mcpServers":{}}`, 0o600},
		{Script, "#!/bin/sh\nexit 0\n", 0o700},
	} {
		path, err := d.Write(c.name, []byte(c.data), c.mode)
		if err != nil || path != filepath.Join(d.Path, c.name) {
			t.Fatalf("%s: %q, %v", c.name, path, err)
		}
		if m := mode(t, path); !m.IsRegular() || m.Perm() != c.mode {
			t.Errorf("%s: mode %v, want %v", c.name, m, c.mode)
		}
		if got, err := os.ReadFile(path); err != nil || string(got) != c.data { //nolint:gosec // the test's file
			t.Errorf("%s: %q, %v", c.name, got, err)
		}
	}
	// A file is written once: a second write of the name fails and leaves
	// the first.
	if _, err := d.Write(MCPConfig, []byte("other"), 0o600); err == nil {
		t.Error("a second write of the same name passed")
	}
	if got, _ := os.ReadFile(filepath.Join(d.Path, MCPConfig)); string(got) != `{"mcpServers":{}}` { //nolint:gosec // the test's file
		t.Errorf("after a second write: %q", got)
	}
	// Others get no right, whatever was asked for.
	path, err := d.Write("x", nil, 0o777)
	if err != nil || mode(t, path).Perm() != 0o700 {
		t.Errorf("mode 0777 asked: %v, %v", mode(t, path), err)
	}
}

// Every press gets a directory of its own.
func TestEveryLaunchHasItsOwnDirectory(t *testing.T) {
	root := filepath.Join(realTemp(t), "l")
	seen := map[string]bool{}
	for range 20 {
		d, err := Make(root, now)
		if err != nil {
			t.Fatal(err)
		}
		if seen[d.ID] {
			t.Fatalf("id %s twice", d.ID)
		}
		seen[d.ID] = true
	}
}

// The threat model of privatefs holds for the way and the root: a
// directory others may write to is refused, and nothing is made in it.
func TestARootOthersMayChangeIsRefused(t *testing.T) {
	if _, err := Make("relative/l", now); err == nil {
		t.Error("a relative root passed")
	}
	if _, err := Make("", now); err == nil {
		t.Error("no root passed")
	}
	open := filepath.Join(realTemp(t), "open")
	if err := os.Mkdir(open, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(open, 0o777); err != nil { //nolint:gosec // the case under test
		t.Fatal(err)
	}
	root := filepath.Join(open, "l")
	if _, err := Make(root, now); err == nil {
		t.Fatal("a root below a directory open to others passed")
	}
	if err := os.Chmod(root, 0o777); err != nil { //nolint:gosec // the case under test
		t.Fatal(err)
	}
	if err := os.Chmod(open, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Make(root, now); err == nil {
		t.Fatal("a root open to others passed")
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Errorf("made in a refused root: %v", entries)
	}
}

func TestPruneRemovesOldLaunchDirectoriesAlone(t *testing.T) {
	root := filepath.Join(realTemp(t), "l")
	outside := realTemp(t)
	// Made first, aged after: every Make prunes.
	ages := map[string]time.Duration{}
	launch := func(age time.Duration, extra ...string) string {
		t.Helper()
		d, err := Make(root, now)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range append([]string{MCPConfig, Script}, extra...) {
			if _, err := d.Write(name, []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		ages[d.Path] = age
		return d.Path
	}
	old := launch(25 * time.Hour)
	young := launch(23 * time.Hour)
	future := launch(-2 * time.Hour)
	soon := launch(-30 * time.Second)
	foreign := launch(25*time.Hour, "notes")
	for path, age := range ages {
		if err := os.Chtimes(path, now.Add(-age), now.Add(-age)); err != nil {
			t.Fatal(err)
		}
	}
	// A name of the id's form that is no directory: removed by its name,
	// never followed.
	link := filepath.Join(root, "00000000000000aa")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "00000000000000bb")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// Names of another form are not ours, however old.
	other := filepath.Join(root, "0000000000000cc")
	if err := os.Mkdir(other, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(other, now.Add(-48*time.Hour), now.Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, MCPConfig), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Prune(root, now); err != nil {
		t.Fatal(err)
	}
	gone := func(path string) bool { _, err := os.Lstat(path); return os.IsNotExist(err) }
	for _, path := range []string{old, future, link, file} {
		if !gone(path) {
			t.Errorf("%s stayed", path)
		}
	}
	for _, path := range []string{young, soon, other, filepath.Join(young, MCPConfig), filepath.Join(outside, MCPConfig)} {
		if gone(path) {
			t.Errorf("%s was removed", path)
		}
	}
	// An old directory with something else in it loses our files and stays.
	if gone(filepath.Join(foreign, "notes")) || !gone(filepath.Join(foreign, MCPConfig)) || !gone(filepath.Join(foreign, Script)) {
		t.Errorf("the directory with a foreign file: %v", entries(t, foreign))
	}
}

func TestPruneWithoutARoot(t *testing.T) {
	if err := Prune(filepath.Join(realTemp(t), "missing"), now); err != nil {
		t.Fatalf("a missing root: %v", err)
	}
	if err := Prune("", now); err != nil {
		t.Fatalf("no root: %v", err)
	}
}

func entries(t *testing.T, dir string) []string {
	t.Helper()
	list, err := os.ReadDir(dir)
	if err != nil {
		return []string{err.Error()}
	}
	var out []string
	for _, e := range list {
		out = append(out, e.Name())
	}
	return out
}
