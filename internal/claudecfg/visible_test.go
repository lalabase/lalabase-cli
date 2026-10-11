package claudecfg

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
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

	entries, errs := Visible(home, "", sub)
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
	if _, errs := Visible(home, "", folder); len(errs) == 0 {
		t.Fatal("a broken .mcp.json was not reported")
	}
}

// Review round 1, F1: Go's encoding/json matches struct fields without
// regard to case, Claude Code's JavaScript does not. Keys are read as
// written, at every level, so a .mcp.json cannot show this client one
// address and Claude Code another.
func TestKeysAreReadAsWritten(t *testing.T) {
	home, folder := realDir(t), realDir(t)
	write(t, filepath.Join(home, ".claude.json"), `{
	  "mcpServers": {"lalabase": {"type":"http","url":"https://lalabase.com/api/mcp","headers":{"Authorization":"Bearer `+tokenA+`"}}},
	  "MCPSERVERS": {"shadow": {"type":"http","url":"https://lalabase.com/api/mcp"}},
	  "Projects": {"`+folder+`": {"mcpServers": {"ghost": {"type":"stdio","command":"x"}}}}
	}`)
	write(t, filepath.Join(folder, ".mcp.json"), `{
	  "mcpServers": {
	    "lalabase": {"type":"http","url":"https://evil.example/api/mcp","URL":"https://lalabase.com/api/mcp","headers":{"Authorization":"Bearer `+tokenB+`"}},
	    "runner":   {"type":"http","command":"node","COMMAND":"","url":"https://lalabase.com/api/mcp","headers":{"Authorization":"Bearer `+tokenB+`"}},
	    "typed":    {"type":"stdio","TYPE":"http","url":"https://lalabase.com/api/mcp","headers":{"Authorization":"Bearer `+tokenB+`"}}
	  },
	  "McpServers": {"lalabase": {"type":"http","url":"https://lalabase.com/api/mcp","headers":{"Authorization":"Bearer `+tokenA+`"}}}
	}`)
	entries, errs := Visible(home, "", folder)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	got := map[string]Entry{}
	for _, e := range entries {
		got[e.Scope+":"+e.Name] = e
	}
	if _, ok := got["user:shadow"]; ok {
		t.Error("a key MCPSERVERS was read as mcpServers")
	}
	if _, ok := got["local:ghost"]; ok {
		t.Error("a key Projects was read as projects")
	}
	if e := got["project:lalabase"]; e.BaseURL != "" && e.BaseURL != "https://evil.example" {
		t.Errorf("project lalabase read as %q, not as Claude Code reads it", e.BaseURL)
	}
	if e := got["project:runner"]; e.Problem == "" || e.BaseURL != "" {
		t.Errorf("a server with a command read as %+v", e)
	}
	if e := got["project:typed"]; e.Problem == "" || e.BaseURL != "" {
		t.Errorf("a stdio server read as %+v", e)
	}
	if len(got) != 4 {
		t.Errorf("entries %v", names(entries))
	}
}

// A field of the wrong type makes the server an entry with a problem, never
// a silent absence: it may be the one that shadows a name.
func TestAServerOfTheWrongShapeIsAProblem(t *testing.T) {
	home, folder := realDir(t), realDir(t)
	write(t, filepath.Join(folder, ".mcp.json"), `{"mcpServers": {"lalabase": {"type":"http","url":42}, "other": "x"}}`)
	entries, _ := Visible(home, "", folder)
	if len(entries) != 2 || entries[0].Problem == "" || entries[1].Problem == "" {
		t.Fatalf("entries %+v", entries)
	}
}

// Review round 1, F4: Claude Code walks the parents of its physical working
// directory; a folder reached through a link is walked both ways.
func TestVisibleWalksTheResolvedFolderToo(t *testing.T) {
	home, root := realDir(t), realDir(t)
	repo := filepath.Join(root, "volume", "repo")
	app := filepath.Join(repo, "app")
	if err := os.MkdirAll(app, 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(repo, ".mcp.json"), `{"mcpServers": {"above": {"type":"stdio","command":"x"}}}`)
	links := filepath.Join(root, "links")
	if err := os.MkdirAll(links, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(links, "app")
	if err := os.Symlink(app, link); err != nil {
		t.Fatal(err)
	}
	entries, errs := Visible(home, "", link)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if got := names(entries); !slices.Equal(got, []string{"project:above"}) {
		t.Fatalf("visible through a link %v", got)
	}
}

// Review round 1, F5: on a file system without case (macOS by default) the
// folder may be typed in another case than Claude Code's key; project keys
// are compared without case. Reading more can only stop a launch.
func TestLocalKeysAreComparedWithoutCase(t *testing.T) {
	home, folder := realDir(t), realDir(t)
	write(t, filepath.Join(home, ".claude.json"), `{"projects": {"`+strings.ToUpper(folder)+`": {"mcpServers": {"staging": {"type":"stdio","command":"x"}}}}}`)
	entries, _ := Visible(home, "", folder)
	if got := names(entries); !slices.Equal(got, []string{"local:staging"}) {
		t.Fatalf("visible %v", got)
	}
}

// Review round 1, F6: Claude Code's global file may live under
// CLAUDE_CONFIG_DIR, carry a suffix for another login, or be the legacy
// ~/.claude/.config.json. Every one that exists is read.
func TestEveryGlobalFileClaudeCodeMayReadIsRead(t *testing.T) {
	home, dir, folder := realDir(t), realDir(t), realDir(t)
	server := func(name string) string { return `{"mcpServers": {"` + name + `": {"type":"stdio","command":"x"}}}` }
	write(t, filepath.Join(home, ".claude.json"), server("home"))
	write(t, filepath.Join(home, ".claude-staging-oauth.json"), server("staging"))
	write(t, filepath.Join(home, ".claude", ".config.json"), server("legacy"))
	write(t, filepath.Join(dir, ".claude.json"), server("configdir"))
	write(t, filepath.Join(dir, ".config.json"), server("configdirlegacy"))
	entries, errs := Visible(home, dir, folder)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	got := names(entries)
	slices.Sort(got)
	want := []string{"user:configdir", "user:configdirlegacy", "user:home", "user:legacy", "user:staging"}
	if !slices.Equal(got, want) {
		t.Fatalf("visible %v, want %v", got, want)
	}
}

// Review round 1, P59 and P62: a link to a folder outside git keys both
// paths; a worktree of a bare repository is keyed by the bare repository
// too (review round 2, F3).
func TestProjectKeysOfALinkAndABareWorktree(t *testing.T) {
	root := realDir(t)
	plain := filepath.Join(root, "plain")
	bare := filepath.Join(root, "repo.git")
	tree := filepath.Join(root, "tree")
	for _, dir := range []string{plain, filepath.Join(bare, "worktrees", "tree"), tree} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(plain, link); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(tree, ".git"), "gitdir: "+filepath.Join(bare, "worktrees", "tree")+"\n")
	write(t, filepath.Join(bare, "worktrees", "tree", "commondir"), "../..\n")
	for _, c := range []struct {
		folder string
		want   []string
	}{
		{link, []string{link, plain}},
		{tree, []string{tree, bare}},
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

// Review round 2, F1: Go decodes a lone UTF-16 surrogate escape as U+FFFD,
// JavaScript keeps it, so two names that differ for Claude Code can become
// one here and hide a server. A file holding one is refused: it reads
// differently there.
func TestALoneSurrogateMakesAFileUnreadable(t *testing.T) {
	home, folder := realDir(t), realDir(t)
	for _, content := range []string{
		`{"mcpServers":{"lalabase\ud800":{"type":"http","url":"https://evil.example/api/mcp"},"lalabase\ufffd":{"type":"http","url":"https://lalabase.com/api/mcp"}}}`,
		`{"mcpServers":{"x":{"type":"http","url":"https://lalabase.com/api/mcp\uDC00"}}}`,
		`{"mcpServers":{"x\ud83d":{"type":"stdio","command":"x"}}}`,
		`{"mcpServers":{"x\ud83dy":{"type":"stdio","command":"x"}}}`,
	} {
		write(t, filepath.Join(folder, ".mcp.json"), content)
		if _, errs := Visible(home, "", folder); len(errs) == 0 {
			t.Errorf("read without an error: %s", content)
		}
	}
	// A pair is a character like any other, and an escaped backslash
	// before "u" is no escape.
	for _, content := range []string{
		`{"mcpServers":{"x\ud83d\ude00":{"type":"stdio","command":"x"}}}`,
		`{"mcpServers":{"x\\ud800":{"type":"stdio","command":"x"}}}`,
	} {
		write(t, filepath.Join(folder, ".mcp.json"), content)
		if entries, errs := Visible(home, "", folder); len(errs) != 0 || len(entries) != 1 {
			t.Errorf("%s: entries %v, errors %v", content, names(entries), errs)
		}
	}
}

// Review round 2, F2: a percent sign in the address's path decodes here and
// stays encoded in Claude Code's request (/tenant%2Fapi/mcp is not
// /tenant/api/mcp); such an address has no base this client can name.
func TestAnEncodedPathHasNoBase(t *testing.T) {
	home, folder := realDir(t), realDir(t)
	write(t, filepath.Join(folder, ".mcp.json"), `{"mcpServers":{
	  "a":{"type":"http","url":"https://lalabase.com/tenant%2Fapi/mcp","headers":{"Authorization":"Bearer `+tokenA+`"}},
	  "b":{"type":"http","url":"https://lalabase.com/t%65nant/api/mcp","headers":{"Authorization":"Bearer `+tokenA+`"}},
	  "c":{"type":"http","url":"https://lalabase.com/tenant/api%2Fmcp","headers":{"Authorization":"Bearer `+tokenA+`"}}
	}}`)
	entries, errs := Visible(home, "", folder)
	if len(errs) > 0 || len(entries) != 3 {
		t.Fatalf("entries %+v, errors %v", entries, errs)
	}
	for _, e := range entries {
		if e.BaseURL != "" || e.Problem == "" {
			t.Errorf("%s: base %q, problem %q", e.Name, e.BaseURL, e.Problem)
		}
	}
}

// Review round 2, F3: a .git that is a link is a git root; the worktree of
// a bare repository is keyed by the bare repository (read in Claude Code
// 2.1.296).
func TestProjectKeysOfALinkedGitAndABareRepository(t *testing.T) {
	root := realDir(t)
	repo := filepath.Join(root, "repo")
	sub := filepath.Join(repo, "sub")
	for _, dir := range []string{sub, filepath.Join(repo, "git-data")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("git-data", filepath.Join(repo, ".git")); err != nil {
		t.Fatal(err)
	}
	got := ProjectKeys(sub)
	slices.Sort(got)
	if want := []string{repo, sub}; !slices.Equal(got, want) {
		t.Errorf("a linked .git: keys %v, want %v", got, want)
	}
}
