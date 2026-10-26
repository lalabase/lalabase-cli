package diskcache

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// C9a: the agents' directory <profile>/a/ lies beside the cache's entries.
// Opening and pruning the cache leave it and everything in it as it was,
// a long while later too, and it counts against none of the cache's
// bounds.
func TestTheAgentsDirectoryIsNoPartOfTheCache(t *testing.T) {
	root := t.TempDir()
	agents := filepath.Join(root, "default", "a")
	if err := os.MkdirAll(agents, 0o700); err != nil {
		t.Fatal(err)
	}
	files := []string{
		"0f8fad5b-d9cb-469f-a165-70867728950e.launch.json",
		"0f8fad5b-d9cb-469f-a165-70867728950e.state.json",
		"0f8fad5b-d9cb-469f-a165-70867728950e.lock",
		".tmp-1234",
	}
	for _, name := range files {
		if err := os.WriteFile(filepath.Join(agents, name), make([]byte, 1000), 0o600); err != nil {
			t.Fatal(err)
		}
		old := t0.Add(-30 * 24 * time.Hour)
		if err := os.Chtimes(filepath.Join(agents, name), old, old); err != nil {
			t.Fatal(err)
		}
	}
	for _, now := range []time.Time{t0, t0.Add(48 * time.Hour)} {
		c := open(t, root, now)
		if c.Files() != 0 || c.Bytes() != 0 {
			t.Fatalf("the cache counts %d files, %d bytes", c.Files(), c.Bytes())
		}
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
		got := names(t, agents)
		slices.Sort(got)
		want := slices.Clone(files)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Fatalf("the agents' directory holds %v", got)
		}
	}
}
