//go:build darwin

package profile

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/privatefs"
)

// setACL adds an access control entry, as a person or an admin would.
func setACL(t *testing.T, path, entry string) {
	t.Helper()
	if out, err := exec.CommandContext(t.Context(), "chmod", "+a", entry, path).CombinedOutput(); err != nil { //nolint:gosec // test input
		t.Fatalf("chmod +a %q: %v %s", entry, err, out)
	}
	// A deny entry would keep the test's directory from being removed.
	t.Cleanup(func() { _ = exec.CommandContext(context.Background(), "chmod", "-N", path).Run() }) //nolint:gosec // test input
}

// On macOS an access control list can open what the mode bits close: a
// 0600 file in a 0700 directory with "everyone allow read" is readable by
// everyone. The mode bits do not show it.
func TestAccessListsAreChecked(t *testing.T) {
	type setup func(t *testing.T, home, dir, path string)
	cases := []struct {
		name    string
		setup   setup
		refused bool
	}{
		{"a deny entry on the config's directory (as on every home)", func(t *testing.T, _, dir, _ string) {
			setACL(t, dir, "everyone deny delete")
		}, false},
		{"an allow entry on the config's directory", func(t *testing.T, _, dir, _ string) {
			setACL(t, dir, "everyone allow list")
		}, true},
		{"a deny entry, then an allow entry on the config's directory", func(t *testing.T, _, dir, _ string) {
			setACL(t, dir, "everyone deny delete")
			setACL(t, dir, "everyone allow list")
		}, true},
		{"an inherited allow entry on the config", func(t *testing.T, _, dir, path string) {
			setACL(t, dir, "everyone allow read,file_inherit")
			_ = os.Remove(path)
			if err := os.WriteFile(path, []byte(twoProfiles), 0o600); err != nil {
				t.Fatal(err)
			}
			if out, _ := exec.CommandContext(t.Context(), "chmod", "-a", "everyone allow read,file_inherit", dir).CombinedOutput(); len(out) > 0 { //nolint:gosec // test input
				t.Fatalf("%s", out)
			}
		}, true},
		{"an allow entry to read on a directory above", func(t *testing.T, home, _, _ string) {
			setACL(t, home, "everyone allow list,search")
		}, false},
		{"an allow entry to add on a directory above", func(t *testing.T, home, _, _ string) {
			setACL(t, home, "everyone allow add_subdirectory")
		}, true},
		{"an allow entry to delete inside a directory above", func(t *testing.T, home, _, _ string) {
			setACL(t, home, "everyone allow delete_child")
		}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "home")
			dir := filepath.Join(home, "lalabase")
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "config.toml")
			if err := os.WriteFile(path, []byte(twoProfiles), 0o600); err != nil {
				t.Fatal(err)
			}
			tc.setup(t, home, dir, path)
			_, err := Load(path)
			if tc.refused != (err != nil) {
				t.Fatalf("refused %v, got %v", tc.refused, err)
			}
			if tc.refused && !strings.Contains(err.Error(), "access list") {
				t.Fatalf("got %v", err)
			}
		})
	}
}

// The lock file is checked like the config: any allow entry lets others in,
// one to read included (reading is enough to hold a lock).
func TestALockFileWithAnAccessListIsRefused(t *testing.T) {
	for _, entry := range []string{"everyone allow write", "everyone allow read"} {
		t.Run(entry, func(t *testing.T) {
			path := writeConfig(t, twoProfiles, 0o600)
			if err := os.WriteFile(path+".lock", nil, 0o600); err != nil {
				t.Fatal(err)
			}
			setACL(t, path+".lock", entry)
			if err := Update(path, func(*File) error { return nil }); err == nil || !strings.Contains(err.Error(), "access list") {
				t.Fatalf("got %v", err)
			}
		})
	}
}

// An access list that cannot be read is no access list: refused.
func TestAnAccessListThatCannotBeReadIsRefused(t *testing.T) {
	path := writeConfig(t, twoProfiles, 0o600)
	old := privatefs.ReadACL
	privatefs.ReadACL = func(string) ([]privatefs.ACE, error) { return nil, errors.New("ENOSYS") }
	t.Cleanup(func() { privatefs.ReadACL = old })
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "reading its access list") {
		t.Fatalf("got %v", err)
	}
}
