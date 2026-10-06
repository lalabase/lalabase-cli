package claudecfg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func findWithin(t *testing.T, home, cwd string) ([]Entry, []error) {
	t.Helper()
	type result struct {
		entries []Entry
		errs    []error
	}
	done := make(chan result, 1)
	go func() {
		e, errs := Find(home, cwd)
		done <- result{e, errs}
	}()
	select {
	case r := <-done:
		return r.entries, r.errs
	case <-time.After(2 * time.Second):
		t.Fatal("Find did not return: it blocked or read without end")
	}
	return nil, nil
}

// A cloned repository can carry a .mcp.json that is a link to an endless
// device: auth import in that directory must not read forever.
func TestAnEndlessProjectFileIsRefusedAtOnce(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	if err := os.Symlink("/dev/zero", filepath.Join(cwd, ".mcp.json")); err != nil {
		t.Fatal(err)
	}
	entries, errs := findWithin(t, home, cwd)
	if len(entries) != 0 || len(errs) != 1 {
		t.Fatalf("%v %v", entries, errs)
	}
}

func TestAnOversizedProjectFileIsRefused(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	write(t, filepath.Join(cwd, ".mcp.json"), `{"x":"`+strings.Repeat("a", maxProjectFile)+`"}`)
	if _, errs := findWithin(t, home, cwd); len(errs) != 1 || !strings.Contains(errs[0].Error(), "larger than") {
		t.Fatalf("%v", errs)
	}
}

// The project scope comes from whatever directory auth import runs in, a
// cloned repository included: such entries are marked, with whether Claude
// Code was allowed to use them.
func TestProjectEntriesAreMarkedWithTheirApproval(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, ".claude.json"), `{"projects": {"`+cwd+`": {"enabledMcpjsonServers": ["approved"]}}}`)
	write(t, filepath.Join(cwd, ".mcp.json"), `{"mcpServers": {
	  "approved": {"url":"https://lalabase.com/api/mcp","headers":{"Authorization":"Bearer `+tokenA+`"}},
	  "stranger": {"url":"https://evil.example/api/mcp","headers":{"Authorization":"Bearer `+tokenB+`"}}
	}}`)
	entries, _ := findWithin(t, home, cwd)
	if len(entries) != 2 {
		t.Fatalf("%+v", entries)
	}
	for _, e := range entries {
		want := map[string]string{"approved": "approved in Claude Code", "stranger": "not approved in Claude Code"}[e.Name]
		if !strings.Contains(e.Note, want) || !strings.Contains(e.Note, ".mcp.json") {
			t.Errorf("%s: note %q", e.Name, e.Note)
		}
	}
}

func TestTheTokenMustBeTheWholeHeader(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, ".claude.json"), `{"mcpServers": {
	  "lalabase": {"url":"https://lalabase.com/api/mcp","headers":{"Authorization":"Bearer `+tokenA+` extra"}},
	  "lalabase-fragment": {"url":"https://lalabase.com/api/mcp#x","headers":{"Authorization":"Bearer `+tokenA+`"}},
	  "lalabase-stdio-type": {"type":"stdio"}
	}}`)
	entries, _ := findWithin(t, home, cwd)
	if len(entries) != 3 {
		t.Fatalf("%+v", entries)
	}
	reasons := map[string]string{"lalabase": "token", "lalabase-fragment": "address", "lalabase-stdio-type": "program"}
	for _, e := range entries {
		if e.Usable() || !strings.Contains(e.Problem, reasons[e.Name]) {
			t.Errorf("%s: %+v", e.Name, e)
		}
	}
}

// An entry with any name is found by its Lalabase token alone.
func TestAnEntryIsFoundByItsTokenAlone(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, ".claude.json"), `{"mcpServers": {
	  "acme": {"url":"https://proxy.example/tools","headers":{"Authorization":"Bearer `+tokenA+`"}}
	}}`)
	entries, _ := findWithin(t, home, cwd)
	if len(entries) != 1 || entries[0].Name != "acme" || entries[0].Usable() {
		t.Fatalf("%+v", entries)
	}
}

// The approval Claude Code keeps per project: all of a project's servers at
// once, a rejection that wins over that, and the project found under its real
// path when the directory was entered through a link.
func TestTheApprovalOfProjectEntries(t *testing.T) {
	servers := `{"mcpServers": {
	  "one": {"url":"https://lalabase.com/api/mcp","headers":{"Authorization":"Bearer ` + tokenA + `"}},
	  "two": {"url":"https://lalabase.com/api/mcp","headers":{"Authorization":"Bearer ` + tokenB + `"}}
	}}`
	cases := []struct {
		name     string
		settings string
		viaLink  bool
		want     map[string]string
	}{
		{"all approved", `"enableAllProjectMcpServers": true`, false,
			map[string]string{"one": "approved in Claude Code", "two": "approved in Claude Code"}},
		{"a rejection wins", `"enableAllProjectMcpServers": true, "disabledMcpjsonServers": ["two"]`, false,
			map[string]string{"one": "approved in Claude Code", "two": "rejected in Claude Code"}},
		{"entered through a link", `"enabledMcpjsonServers": ["one"]`, true,
			map[string]string{"one": "approved in Claude Code", "two": "not approved in Claude Code"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home, real := t.TempDir(), t.TempDir()
			real, err := filepath.EvalSymlinks(real)
			if err != nil {
				t.Fatal(err)
			}
			write(t, filepath.Join(real, ".mcp.json"), servers)
			write(t, filepath.Join(home, ".claude.json"), `{"projects": {"`+real+`": {`+tc.settings+`}}}`)
			cwd := real
			if tc.viaLink {
				cwd = filepath.Join(t.TempDir(), "link")
				if err := os.Symlink(real, cwd); err != nil {
					t.Fatal(err)
				}
			}
			entries, _ := findWithin(t, home, cwd)
			if len(entries) != 2 {
				t.Fatalf("%+v", entries)
			}
			for _, e := range entries {
				if !strings.HasSuffix(e.Note, ", "+tc.want[e.Name]) {
					t.Errorf("%s: note %q, want %q", e.Name, e.Note, tc.want[e.Name])
				}
			}
		})
	}
}
