package claudecfg

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// C7 asked whether an entry that cannot be imported still carries its base
// address. It does not: inspect sets BaseURL only together with a token, so
// such an entry never counts as a server Claude Code reads for the launch.
func TestAnEntryWithAProblemCarriesNoBaseURL(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, ".claude.json"), `{"mcpServers": {
	  "lalabase-var":   {"type":"http","url":"https://lalabase.com/api/mcp","headers":{"Authorization":"Bearer ${LALABASE_TOKEN}"}},
	  "lalabase-local": {"type":"stdio","command":"node"}
	}}`)
	entries, errs := Find(home, cwd)
	if len(errs) > 0 || len(entries) != 2 {
		t.Fatalf("entries %+v, errors %v", entries, errs)
	}
	for _, e := range entries {
		if e.Problem == "" || e.BaseURL != "" {
			t.Errorf("%s: problem %q, base %q; want a problem and no base", e.Name, e.Problem, e.BaseURL)
		}
	}
}

// realDir is a temporary directory under its real path (macOS keeps them
// under a link, /var -> /private/var).
func realDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func names(entries []Entry) []string {
	var out []string
	for _, e := range entries {
		out = append(out, e.Scope+":"+e.Name)
	}
	return out
}

func TestProjectKeys(t *testing.T) {
	root := realDir(t)
	plain := filepath.Join(root, "plain")
	repo := filepath.Join(root, "repo")
	sub := filepath.Join(repo, "app", "models")
	tree := filepath.Join(root, "repo-wt")
	module := filepath.Join(repo, "vendor", "lib")
	for _, dir := range []string{plain, filepath.Join(repo, ".git", "worktrees", "repo-wt"), sub, tree, filepath.Join(repo, ".git", "modules", "lib"), module} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// A worktree: .git is a file naming its git directory, which names the
	// common directory of the main checkout.
	write(t, filepath.Join(tree, ".git"), "gitdir: "+filepath.Join(repo, ".git", "worktrees", "repo-wt")+"\n")
	write(t, filepath.Join(repo, ".git", "worktrees", "repo-wt", "commondir"), "../..\n")
	// A submodule: a .git file without a common directory.
	write(t, filepath.Join(module, ".git"), "gitdir: ../../.git/modules/lib\n")
	link := filepath.Join(root, "link")
	if err := os.Symlink(repo, link); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		folder string
		want   []string
	}{
		{plain, []string{plain}},
		{repo, []string{repo}},
		{sub, []string{sub, repo}},
		{tree, []string{tree, repo}},
		{module, []string{module}},
		{link, []string{link, repo}},
	} {
		got := ProjectKeys(c.folder)
		slices.Sort(got)
		want := slices.Clone(c.want)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("%s: keys %v, want %v", c.folder, got, want)
		}
	}
}

// Visible lists every server Claude Code may use in a folder, whatever it
// is called: the user scope, the local scope of the folder's project keys
// alone, and .mcp.json in the folder and every directory above it.
func TestVisibleServersOfAFolder(t *testing.T) {
	home, root := realDir(t), realDir(t)
	repo := filepath.Join(root, "repo")
	sub := filepath.Join(repo, "app")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(home, ".claude.json"), `{
	  "mcpServers": {"lalabase": {"type":"http","url":"https://lalabase.com/api/mcp","headers":{"Authorization":"Bearer `+tokenA+`"}},
	                 "github": {"type":"http","url":"https://api.github.com/mcp"}},
	  "projects": {
	    "`+repo+`": {"mcpServers": {"tools": {"type":"stdio","command":"node"}}},
	    "`+sub+`":  {"mcpServers": {"near": {"type":"stdio","command":"node"}}},
	    "/elsewhere": {"mcpServers": {"lalabase": {"type":"http","url":"https://other.example/api/mcp","headers":{"Authorization":"Bearer `+tokenB+`"}}}}
	  }
	}`)
	write(t, filepath.Join(sub, ".mcp.json"), `{"mcpServers": {"here": {"type":"stdio","command":"x"}}}`)
	write(t, filepath.Join(root, ".mcp.json"), `{"mcpServers": {"above": {"type":"stdio","command":"x"}}}`)

	entries, errs := Visible(home, sub)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	got := names(entries)
	want := []string{"user:github", "user:lalabase", "local:tools", "local:near", "project:here", "project:above"}
	if !slices.Equal(got, want) {
		t.Fatalf("visible %v, want %v", got, want)
	}
	// The local entries name the key they were filed under, the project
	// entries their file.
	if entries[2].Where != repo || entries[5].Where != filepath.Join(root, ".mcp.json") {
		t.Errorf("where: %q, %q", entries[2].Where, entries[5].Where)
	}
	// What Visible reads, it reads like Find: the user's lalabase entry is
	// importable, github is no Lalabase server and says so.
	if entries[1].BaseURL != "https://lalabase.com" || entries[0].Problem == "" {
		t.Errorf("inspected: %+v, %+v", entries[1], entries[0])
	}
}

// A file that cannot be read is an error, never a silent absence: an
// unreadable .mcp.json might hold the server that shadows the one picked.
func TestVisibleReportsAnUnreadableFile(t *testing.T) {
	home, folder := realDir(t), realDir(t)
	write(t, filepath.Join(folder, ".mcp.json"), `{"mcpServers": `)
	if _, errs := Visible(home, folder); len(errs) == 0 {
		t.Fatal("a broken .mcp.json was not reported")
	}
}
