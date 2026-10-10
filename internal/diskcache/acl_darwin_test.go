//go:build darwin

package diskcache

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// setACL adds an access control entry, as a person or an admin would.
func setACL(t *testing.T, path, entry string) {
	t.Helper()
	if out, err := exec.CommandContext(t.Context(), "chmod", "+a", entry, path).CombinedOutput(); err != nil { //nolint:gosec // test input
		t.Fatalf("chmod +a %q: %v %s", entry, err, out)
	}
	t.Cleanup(func() { _ = exec.CommandContext(context.Background(), "chmod", "-N", path).Run() }) //nolint:gosec // test input
}

// Plan test 7, macOS: an access list that lets others into the cache
// directory, or add to a directory above, refuses the disk cache; a deny
// entry (every home has one) and a read entry above do not.
func TestAccessListsOnTheWayAreChecked(t *testing.T) {
	for _, tc := range []struct {
		name    string
		where   string // "dir" or "above"
		entry   string
		refused bool
	}{
		{"deny on the directory", "dir", "everyone deny delete", false},
		{"allow list on the directory", "dir", "everyone allow list", true},
		{"allow read above", "above", "everyone allow list,search", false},
		{"allow add above", "above", "everyone allow add_subdirectory", true},
		{"allow delete inside above", "above", "everyone allow delete_child", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			above := filepath.Join(t.TempDir(), "cache")
			dir := filepath.Join(above, "default")
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			target := dir
			if tc.where == "above" {
				target = above
			}
			setACL(t, target, tc.entry)
			c, err := (Opener{Root: above}).Open("default", t0)
			if tc.refused != (err != nil) {
				t.Fatalf("refused %v, got %v", tc.refused, err)
			}
			if err != nil && !strings.Contains(err.Error(), "access list") {
				t.Fatalf("got %v", err)
			}
			if c != nil {
				_ = c.Close()
			}
		})
	}
}
