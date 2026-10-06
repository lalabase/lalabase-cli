//go:build linux

package profile

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// On Linux an access control list shows in the mode bits: the group bits
// hold the mask, the most any named entry may grant. The mode checks
// therefore see an entry that lets another user write or read.
func TestAccessListsShowInTheModeBits(t *testing.T) {
	if _, err := exec.LookPath("setfacl"); err != nil {
		skipOrFail(t, "no setfacl here: %v", err)
	}
	set := func(t *testing.T, entry, path string) {
		t.Helper()
		if out, err := exec.CommandContext(t.Context(), "setfacl", "-m", entry, path).CombinedOutput(); err != nil { //nolint:gosec // test input
			skipOrFail(t, "setfacl -m %s: %v %s", entry, err, out)
		}
	}
	t.Run("a named user may write a directory above", func(t *testing.T) {
		home := filepath.Join(t.TempDir(), "home")
		path := filepath.Join(home, "lalabase", "config.toml")
		if err := Save(path, File{Profiles: map[string]Entry{}}); err != nil {
			t.Fatal(err)
		}
		set(t, "u:nobody:rwx", home)
		if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "chmod go-w") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("a named user may read the config", func(t *testing.T) {
		path := writeConfig(t, twoProfiles, 0o600)
		set(t, "u:nobody:r", path)
		if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "others may read") {
			t.Fatalf("got %v", err)
		}
	})
}

// skipOrFail: on the CI a test that cannot set up its case fails instead of
// skipping, or it would pass there without testing anything.
func skipOrFail(t *testing.T, format string, args ...any) {
	t.Helper()
	if os.Getenv("CI") != "" {
		t.Fatalf(format, args...)
	}
	t.Skipf(format, args...)
}
