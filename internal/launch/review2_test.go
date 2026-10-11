package launch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/gboyke/lalabase-cli/internal/claudecfg"
)

// Review round 2 of #22: the reviewer's regressions through the launcher,
// each red before its fix.

// localOverride is ~/.claude.json with the profile's server in the user
// scope and another one of the same name filed under key.
func localOverride(t *testing.T, key string) string {
	t.Helper()
	good := map[string]any{"type": "http", "url": profileServer + "/api/mcp", "headers": map[string]string{"Authorization": "Bearer lalab_pat_good"}}
	other := map[string]any{"type": "http", "url": "https://other.example/api/mcp", "headers": map[string]string{"Authorization": "Bearer lalab_pat_other"}}
	raw, err := json.Marshal(map[string]any{"mcpServers": map[string]any{"lalabase": good}, "projects": map[string]any{key: map[string]any{"mcpServers": map[string]any{"lalabase": other}}}})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestReviewTwoALoneSurrogateDoesNotHideAServer(t *testing.T) {
	r := newRig(t, true, nil)
	writeFile(t, filepath.Join(r.cwd, ".mcp.json"), `{"mcpServers":{"lalabase\ud800":{"type":"http","url":"https://evil.example/api/mcp","headers":{"Authorization":"Bearer lalab_pat_FAKE"}},"lalabase\ufffd":{"type":"http","url":"https://lalabase.com/api/mcp","headers":{"Authorization":"Bearer lalab_pat_FAKE"}},"lalabase_":{"type":"http","url":"https://lalabase.com/api/mcp","headers":{"Authorization":"Bearer lalab_pat_FAKE"}}}}`)
	if d := r.done(shown); d.Kind != ConfigUnreadable {
		t.Fatalf("done %+v", d)
	}
}

func TestReviewTwoAnEncodedEndpointIsNotTheProfilesServer(t *testing.T) {
	r := newRig(t, true, nil)
	r.l.cfg.Server = profileServer + "/tenant"
	writeFile(t, filepath.Join(r.home, ".claude.json"), userServer("lalabase", r.l.cfg.Server))
	writeFile(t, filepath.Join(r.cwd, ".mcp.json"), `{"mcpServers":{"lalabase":{"type":"http","url":"https://lalabase.com/tenant%2Fapi/mcp","headers":{"Authorization":"Bearer lalab_pat_FAKE"}}}}`)
	if d := r.done(shown); d.Kind != Conflict {
		t.Fatalf("done %+v", d)
	}
	// The profile itself with an escape in its path names no server.
	r.l.cfg.Server = profileServer + "/t%65nant"
	if sameServer(r.l.cfg.Server, r.l.cfg.Server) {
		t.Fatal("an escaped profile path compared equal")
	}
}

func TestReviewTwoTheLocalKeyOfABareWorktree(t *testing.T) {
	r := newRig(t, true, nil)
	root := realTemp(t)
	key, folder := filepath.Join(root, "repo.git"), filepath.Join(root, "tree")
	admin := filepath.Join(key, "worktrees", "tree")
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(folder, ".git"), "gitdir: "+admin+"\n")
	writeFile(t, filepath.Join(admin, "commondir"), "../..\n")
	writeFile(t, filepath.Join(admin, "gitdir"), filepath.Join(folder, ".git")+"\n")
	writeFile(t, filepath.Join(r.home, ".claude.json"), localOverride(t, key))
	r.l.cfg.Cwd = folder
	if d := r.done(shown); d.Kind != Conflict {
		t.Fatalf("done %+v, keys %v", d, claudecfg.ProjectKeys(folder))
	}
}

func TestReviewTwoTheLocalKeyOfALinkedGit(t *testing.T) {
	r := newRig(t, true, nil)
	key := filepath.Join(realTemp(t), "repo")
	folder := filepath.Join(key, "sub")
	for _, dir := range []string{folder, filepath.Join(key, "git-data")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("git-data", filepath.Join(key, ".git")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(r.home, ".claude.json"), localOverride(t, key))
	r.l.cfg.Cwd = folder
	if d := r.done(shown); d.Kind != Conflict {
		t.Fatalf("done %+v, keys %v", d, claudecfg.ProjectKeys(folder))
	}
}

// P02: local before project, directly.
func TestReviewTwoLocalBeforeProject(t *testing.T) {
	got, kind, _ := pickServer([]claudecfg.Entry{entry("project", "a-project", profileServer), entry("local", "z-local", profileServer)}, profileServer)
	if got != "z-local" || kind != OK {
		t.Fatalf("got %q, %v", got, kind)
	}
}

// P06: in a tool-name group the name in the pattern is passed, whatever
// order the entries come in.
func TestReviewTwoTheValidNameOfAGroupWhateverItsOrder(t *testing.T) {
	for _, entries := range [][]claudecfg.Entry{
		{entry("user", "lalabase prod", profileServer), entry("project", "lalabase_prod", profileServer)},
		{entry("project", "lalabase_prod", profileServer), entry("user", "lalabase prod", profileServer)},
	} {
		if got, kind, _ := pickServer(entries, profileServer); got != "lalabase_prod" || kind != OK {
			t.Errorf("%v: got %q, %v", names(entries), got, kind)
		}
	}
}

func names(entries []claudecfg.Entry) []string {
	var out []string
	for _, e := range entries {
		out = append(out, e.Name)
	}
	return out
}

// P22 and P23: the exact key wins; without one, the first in sort order,
// on every run (a map's order is random).
func TestReviewTwoFolderKeysInSeveralCases(t *testing.T) {
	const lower = "abcdef00-0000-4000-8000-0000000000ff"
	exact, upper, mixed := realTemp(t), realTemp(t), realTemp(t)
	r := newRig(t, true, nil)
	r.l.cfg.Folders = map[string]string{lower: exact, "ABCDEF00-0000-4000-8000-0000000000FF": upper}
	if got, kind, _ := r.l.folder(lower); kind != OK || got != exact {
		t.Errorf("exact key: %q, %v", got, kind)
	}
	r.l.cfg.Folders = map[string]string{"Abcdef00-0000-4000-8000-0000000000ff": mixed, "ABCDEF00-0000-4000-8000-0000000000FF": upper}
	for range 50 {
		if got, kind, _ := r.l.folder(lower); kind != OK || got != upper {
			t.Fatalf("no exact key: %q, %v", got, kind)
		}
	}
}
