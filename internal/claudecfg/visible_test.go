package claudecfg

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
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

// Review round 3, F1: bytes that are not UTF-8 decode to one U+FFFD each in
// Go and to one per broken sequence in JavaScript, so two names distinct
// for Claude Code could become one here. Such a file is refused.
func TestAFileThatIsNotUTF8IsUnreadable(t *testing.T) {
	home, folder := realDir(t), realDir(t)
	write(t, filepath.Join(folder, ".mcp.json"), "{\"mcpServers\":{\"lalabase\xf0\x9f\x98prod\":{\"type\":\"http\",\"url\":\"https://evil.example/api/mcp\"},"+
		`"lalabase\ufffd\ufffd\ufffdprod":{"type":"http","url":"https://good.example/api/mcp"}}}`)
	if _, errs := Visible(home, "", folder); len(errs) == 0 {
		t.Fatal("a file with broken UTF-8 was read")
	}
}

// Review round 3, C4b: two high surrogates in a row are two lone ones.
func TestTwoHighSurrogatesAreLone(t *testing.T) {
	if !loneSurrogate([]byte(`{"x\ud800\ud800":1}`)) {
		t.Fatal("not refused")
	}
}

// Review round 4: a server whose url and token are fine but one field has
// another type than Claude Code takes never counts; and a high surrogate
// followed by an escape above the low surrogates is lone.
func TestAWrongTypeAloneMakesTheServerAProblem(t *testing.T) {
	home, folder := realDir(t), realDir(t)
	for _, field := range []string{`"type":5`, `"command":["node"]`, `"headers":"x"`} {
		write(t, filepath.Join(folder, ".mcp.json"), `{"mcpServers":{"lalabase":{`+field+`,"url":"https://lalabase.com/api/mcp","headers":{"Authorization":"Bearer `+tokenA+`"}}}}`)
		if field == `"headers":"x"` {
			write(t, filepath.Join(folder, ".mcp.json"), `{"mcpServers":{"lalabase":{"type":"http","url":"https://lalabase.com/api/mcp","headers":"x"}}}`)
		}
		entries, _ := Visible(home, "", folder)
		if len(entries) != 1 || entries[0].Problem == "" || entries[0].BaseURL != "" {
			t.Errorf("%s: %+v", field, entries)
		}
	}
	if !loneSurrogate([]byte(`{"x\ud800\ue000":1}`)) {
		t.Error("a high surrogate before U+E000 was not refused")
	}
}

// Review round 5, F1: Claude Code takes a .git as a root only when it is a
// directory or a file, or a link to one whose target it accepts; past
// anything else it goes on upwards to the next root, and keys the local
// scope there. A .git of another kind keeps its directory as a key and the
// walk goes on to the next directory or file named .git.
func TestProjectKeysGoOnPastAGitClaudeCodeSkips(t *testing.T) {
	root := realDir(t)
	outer := filepath.Join(root, "outer")
	for _, dir := range []string{filepath.Join(outer, ".git"), filepath.Join(outer, "dangling", "deep"), filepath.Join(outer, "fifo"), filepath.Join(outer, "linked"), filepath.Join(outer, "data")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("nowhere", filepath.Join(outer, "dangling", ".git")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(outer, "fifo", ".git"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A link to a directory: Claude Code may refuse its target by checks
	// of its own, so the walk reads on past it as well.
	if err := os.Symlink(filepath.Join(outer, "data"), filepath.Join(outer, "linked", ".git")); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		folder string
		want   []string
	}{
		{filepath.Join(outer, "dangling", "deep"), []string{filepath.Join(outer, "dangling", "deep"), filepath.Join(outer, "dangling"), outer}},
		{filepath.Join(outer, "fifo"), []string{filepath.Join(outer, "fifo"), outer}},
		{filepath.Join(outer, "linked"), []string{filepath.Join(outer, "linked"), outer}},
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

// Review round 5, F1, through Visible: the outer root's local server is
// seen from below a broken .git link.
func TestVisibleSeesTheOuterRootPastABrokenGitLink(t *testing.T) {
	home, root := realDir(t), realDir(t)
	sub := filepath.Join(root, "sub")
	for _, dir := range []string{filepath.Join(root, ".git"), sub} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("nowhere", filepath.Join(sub, ".git")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(home, ".claude.json"), `{"projects":{"`+root+`":{"mcpServers":{"lalabase":{"type":"http","url":"https://evil.example/api/mcp","headers":{"Authorization":"Bearer `+tokenA+`"}}}}}}`)
	entries, errs := Visible(home, "", sub)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	if len(entries) != 1 || entries[0].Scope != "local" || entries[0].BaseURL != "https://evil.example" {
		t.Fatalf("entries %+v", entries)
	}
}

// Review round 5, F2: a high surrogate escape at the very end of the data
// is lone, and reading past the end must not panic.
func TestAHighSurrogateAtTheEndIsLone(t *testing.T) {
	for _, data := range []string{`"\ud800`, `"\ud800\`, `"\ud800\u`} {
		if !loneSurrogate([]byte(data)) {
			t.Errorf("%q: not refused", data)
		}
	}
}

// Review round 6, F5: a folder reached through a link is walked on both
// paths, so the git root on the physical path and the one on the path as
// given are both keys.
func TestProjectKeysWalkTheGivenAndThePhysicalPath(t *testing.T) {
	root := realDir(t)
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(repo, link); err != nil {
		t.Fatal(err)
	}
	got := ProjectKeys(filepath.Join(link, "sub"))
	slices.Sort(got)
	want := []string{link, filepath.Join(link, "sub"), repo, filepath.Join(repo, "sub")}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("keys %v, want %v", got, want)
	}
}

// Review round 6, F7: a worktree whose gitdir is named through a link. Git
// resolves the common directory from the git directory's real path; the
// path as written is kept as a key as well.
func TestTheMainCheckoutOfAGitdirReachedThroughALink(t *testing.T) {
	root := realDir(t)
	main := filepath.Join(root, "main")
	wt := filepath.Join(root, "wt")
	gitdir := filepath.Join(main, ".git", "worktrees", "wt")
	for _, dir := range []string{gitdir, wt} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(gitdir, filepath.Join(root, "gitdirlink")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(wt, ".git"), "gitdir: "+filepath.Join(root, "gitdirlink")+"\n")
	write(t, filepath.Join(gitdir, "commondir"), "../..\n")
	got := ProjectKeys(wt)
	if !slices.Contains(got, main) {
		t.Errorf("keys %v lack the main checkout %s", got, main)
	}
	if !slices.Contains(got, filepath.Dir(root)) {
		t.Errorf("keys %v lack the lexical reading %s", got, filepath.Dir(root))
	}
}
