package claudecfg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	tokenA = "lalab_pat_AAAAAAAAAAAAAAAAAAAA"
	tokenB = "lalab_pat_BBBBBBBBBBBBBBBBBBBB"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestEntriesFromAllThreeScopes(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, ".claude.json"), `{
	  "mcpServers": {"lalabase": {"type":"http","url":"https://lalabase.com/api/mcp","headers":{"Authorization":"Bearer `+tokenA+`"}}},
	  "projects": {
	    "/work/core": {"mcpServers": {"lalabase-staging": {"type":"http","url":"https://staging.example/sub/api/mcp/","headers":{"authorization":"Bearer `+tokenB+`"}}}},
	    "/work/other": {"mcpServers": {"github": {"type":"http","url":"https://api.github.com/mcp","headers":{"Authorization":"Bearer ghp_x"}}}}
	  }
	}`)
	write(t, filepath.Join(cwd, ".mcp.json"), `{"mcpServers": {"lb": {"url":"http://localhost:3000/api/mcp","headers":{"Authorization":"Bearer `+tokenA+`"}}}}`)

	entries, errs := Find(home, cwd)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries: %+v", len(entries), entries)
	}
	want := []struct{ scope, name, base, token string }{
		{"user", "lalabase", "https://lalabase.com", tokenA},
		{"local", "lalabase-staging", "https://staging.example/sub", tokenB},
		{"project", "lb", "http://localhost:3000", tokenA},
	}
	for i, w := range want {
		e := entries[i]
		if e.Scope != w.scope || e.Name != w.name || e.BaseURL != w.base || e.Token != w.token || e.Problem != "" {
			t.Errorf("entry %d: %+v, want %+v", i, e, w)
		}
	}
	if entries[1].Where != "/work/core" {
		t.Errorf("local entry names %q", entries[1].Where)
	}
}

// What cannot be imported is listed with the reason, never guessed at and
// never run: a stdio server is a program, a ${VAR} is a value elsewhere.
func TestEntriesThatCannotBeImportedSayWhy(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, ".claude.json"), `{"projects": {"/p": {"mcpServers": {
	  "lalabase-local": {"type":"stdio","command":"node","args":["dist/index.js"]},
	  "lalabase-var":   {"type":"http","url":"https://lalabase.com/api/mcp","headers":{"Authorization":"Bearer ${LALABASE_TOKEN}"}},
	  "lalabase-two":   {"type":"http","url":"https://lalabase.com/api/mcp","headers":{"Authorization":"Bearer `+tokenA+`","authorization":"Bearer `+tokenB+`"}},
	  "lalabase-path":  {"type":"http","url":"https://lalabase.com/mcp","headers":{"Authorization":"Bearer `+tokenA+`"}},
	  "lalabase-basic": {"type":"http","url":"https://lalabase.com/api/mcp","headers":{"Authorization":"Basic dXNlcjpwdw=="}},
	  "lalabase-query": {"type":"http","url":"https://lalabase.com/api/mcp?x=1","headers":{"Authorization":"Bearer `+tokenA+`"}},
	  "lalabase-user":  {"type":"http","url":"https://u:p@lalabase.com/api/mcp","headers":{"Authorization":"Bearer `+tokenA+`"}}
	}}}}`)
	entries, _ := Find(home, cwd)
	reasons := map[string]string{
		"lalabase-local": "program", "lalabase-var": "variable", "lalabase-two": "more than one",
		"lalabase-path": "address", "lalabase-basic": "token", "lalabase-query": "address", "lalabase-user": "address",
	}
	if len(entries) != len(reasons) {
		t.Fatalf("got %d entries: %+v", len(entries), entries)
	}
	for _, e := range entries {
		if e.Problem == "" || !strings.Contains(e.Problem, reasons[e.Name]) || e.Usable() {
			t.Errorf("%s: problem %q", e.Name, e.Problem)
		}
		if e.Token != "" {
			t.Errorf("%s: a token was kept from an entry that cannot be imported", e.Name)
		}
	}
}

func TestMissingOrBrokenFilesAreReportedNotFatal(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	entries, errs := Find(home, cwd)
	if len(entries) != 0 || len(errs) != 0 {
		t.Fatalf("no files: %v %v", entries, errs)
	}
	write(t, filepath.Join(home, ".claude.json"), `{not json`)
	write(t, filepath.Join(cwd, ".mcp.json"), `{"mcpServers": {"lalabase": {"url":"https://lalabase.com/api/mcp","headers":{"Authorization":"Bearer `+tokenA+`"}}}}`)
	entries, errs = Find(home, cwd)
	if len(entries) != 1 || len(errs) != 1 {
		t.Fatalf("broken user file: %v %v", entries, errs)
	}
}

func TestOnlyLalabaseEntriesAreListed(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, ".claude.json"), `{"mcpServers": {
	  "github": {"type":"http","url":"https://api.github.com/mcp","headers":{"Authorization":"Bearer ghp_x"}},
	  "work": {"type":"http","url":"https://lalabase.example/api/mcp","headers":{"Authorization":"Bearer `+tokenA+`"}}
	}}`)
	entries, _ := Find(home, cwd)
	if len(entries) != 1 || entries[0].Name != "work" || !entries[0].Usable() {
		t.Fatalf("got %+v", entries)
	}
}

func TestTokenPrefixShowsNoMoreThanTheServerDoes(t *testing.T) {
	if got := TokenPrefix(tokenA); got != "lalab_pat_AA" {
		t.Fatalf("got %q", got)
	}
	if got := TokenPrefix("short"); got != "short" {
		t.Fatalf("got %q", got)
	}
}
