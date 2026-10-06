package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/gboyke/lalabase-cli/internal/contract"
	"github.com/gboyke/lalabase-cli/internal/profile"
)

const (
	readToken  = "lalab_pat_READREADREADREAD"
	writeToken = "lalab_pat_WRITEWRITEWRITE"
)

// fakeServer answers /api/v1/me per token and the MCP initialize and
// tools/list from the contract copy.
func fakeServer(t *testing.T, displayName string) *httptest.Server {
	t.Helper()
	file, err := contract.Load()
	if err != nil {
		t.Fatal(err)
	}
	readOnly := map[string]bool{readToken: true, writeToken: false}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		ro, known := readOnly[token]
		if !known {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		switch r.URL.Path {
		case "/api/v1/me":
			body, _ := json.Marshal(map[string]any{"data": map[string]any{
				"user":                 map[string]any{"id": "u-1", "email": "anna@example.com", "display_name": displayName},
				"token":                map[string]any{"id": "t-1", "name": "n", "prefix": token[:12], "read_only": ro},
				"scoped_organisations": []any{map[string]any{"id": "o-1", "name": "Nordlicht GmbH"}},
			}})
			_, _ = w.Write(body)
		case "/api/mcp":
			var req struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			var recorded json.RawMessage
			switch req.Method {
			case "initialize":
				recorded = file.Contract.MCP.Initialize.Response.Body
			case "tools/list":
				recorded = file.Contract.MCP.ToolsList.Response.Body
			}
			var body map[string]any
			_ = json.Unmarshal(recorded, &body)
			body["id"] = req.ID
			out, _ := json.Marshal(body)
			_, _ = w.Write(out)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

type harness struct {
	home, cwd string
	out, err  bytes.Buffer
	vars      map[string]string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	return &harness{home: t.TempDir(), cwd: t.TempDir(), vars: map[string]string{}}
}

func (h *harness) claudeConfig(t *testing.T, servers string) {
	t.Helper()
	content := `{"projects": {"/work/project": {"mcpServers": {` + servers + `}}}}`
	if err := os.WriteFile(filepath.Join(h.home, ".claude.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func httpEntry(name, base, token string) string {
	return `"` + name + `": {"type":"http","url":"` + base + `/api/mcp","headers":{"Authorization":"Bearer ` + token + `"}}`
}

func (h *harness) run(input string, interactive bool, args ...string) int {
	h.out.Reset()
	h.err.Reset()
	return runWith(args, Env{
		Stdin: strings.NewReader(input), Stdout: &h.out, Stderr: &h.err,
		Getenv: func(k string) string { return h.vars[k] }, Home: h.home, Cwd: h.cwd, Interactive: interactive,
	})
}

func (h *harness) configPath() string {
	return filepath.Join(h.home, ".config", "lalabase", "config.toml")
}

func (h *harness) config(t *testing.T) profile.File {
	t.Helper()
	var file profile.File
	if _, err := toml.DecodeFile(h.configPath(), &file); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestImportSavesTheChosenEntryAsTheDefaultProfile(t *testing.T) {
	srv := fakeServer(t, "Anna Albers")
	h := newHarness(t)
	h.claudeConfig(t, httpEntry("lalabase", srv.URL, writeToken))

	if code := h.run("1\n", true, "auth", "import"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.err.String())
	}
	entry := h.config(t).Profiles["default"]
	if entry.URL != srv.URL || entry.Token != writeToken || entry.AllowWrite {
		t.Fatalf("saved %+v", entry)
	}
	info, err := os.Stat(h.configPath())
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode %v %v", info.Mode(), err)
	}
	if !strings.Contains(h.out.String(), "Anna Albers") || strings.Contains(h.out.String()+h.err.String(), writeToken) {
		t.Fatalf("output shows the whole token or not the owner:\n%s", h.out.String())
	}
}

func TestImportAsksEvenForOneEntryAndNeedsATerminal(t *testing.T) {
	srv := fakeServer(t, "Anna Albers")
	h := newHarness(t)
	h.claudeConfig(t, httpEntry("lalabase", srv.URL, writeToken))
	if code := h.run("1\n", false, "auth", "import"); code != 2 {
		t.Fatalf("exit %d", code)
	}
	if _, err := os.Stat(h.configPath()); err == nil {
		t.Fatal("saved without asking")
	}
}

func TestImportWithNothingToImportSaysWhatToDo(t *testing.T) {
	h := newHarness(t)
	if code := h.run("", true, "auth", "import"); code != 1 || !strings.Contains(h.err.String(), "claude mcp add") {
		t.Fatalf("exit %d: %s", code, h.err.String())
	}
}

func TestImportListsWhatCannotBeImportedButOffersOnlyTheRest(t *testing.T) {
	srv := fakeServer(t, "Anna Albers")
	h := newHarness(t)
	h.claudeConfig(t, httpEntry("lalabase", srv.URL, writeToken)+`, "lalabase-local": {"type":"stdio","command":"node"}`)
	if code := h.run("2\n", true, "auth", "import"); code != 1 {
		t.Fatalf("an entry that cannot be imported was offered: exit %d", code)
	}
	if !strings.Contains(h.out.String(), "lalabase-local") || !strings.Contains(h.out.String(), "local program") {
		t.Fatalf("the stdio entry is not listed with its reason:\n%s", h.out.String())
	}
}

func TestImportRefusesATokenTheServerRejects(t *testing.T) {
	srv := fakeServer(t, "Anna Albers")
	h := newHarness(t)
	h.claudeConfig(t, httpEntry("lalabase", srv.URL, "lalab_pat_UNKNOWNUNKNOWN"))
	if code := h.run("1\n", true, "auth", "import"); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if _, err := os.Stat(h.configPath()); err == nil {
		t.Fatal("a rejected token was saved")
	}
}

// --agent explains that the agent needs a read-only token, and a writing one
// is saved only on an explicit yes, never with allow_write.
func TestImportAsAgentWarnsAboutAWritingToken(t *testing.T) {
	srv := fakeServer(t, "Anna Albers")
	h := newHarness(t)
	h.claudeConfig(t, httpEntry("lalabase", srv.URL, writeToken))

	if code := h.run("1\nn\n", true, "auth", "import", "--agent"); code != 1 {
		t.Fatalf("declined: exit %d", code)
	}
	if !strings.Contains(h.out.String(), "read-only") {
		t.Fatalf("no warning:\n%s", h.out.String())
	}
	if _, err := os.Stat(h.configPath()); err == nil {
		t.Fatal("saved although declined")
	}

	if code := h.run("1\ny\n", true, "auth", "import", "--agent"); code != 0 {
		t.Fatalf("confirmed: exit %d %s", code, h.err.String())
	}
	if entry := h.config(t).Profiles["agent"]; entry.Token != writeToken || entry.AllowWrite {
		t.Fatalf("saved %+v", entry)
	}
}

// A write release the old agent profile had does not pass to another token
// unnoticed: the person is told it is dropped.
func TestReplacingAProfileDropsItsWriteReleaseAndSaysSo(t *testing.T) {
	srv := fakeServer(t, "Anna Albers")
	h := newHarness(t)
	h.claudeConfig(t, httpEntry("lalabase", srv.URL, readToken))
	if err := profile.Save(h.configPath(), profile.File{Profiles: map[string]profile.Entry{
		"agent": {URL: "https://old.example", Token: "lalab_pat_OLDOLDOLDOLD", AllowWrite: true},
	}}); err != nil {
		t.Fatal(err)
	}

	if code := h.run("1\nn\n", true, "auth", "import", "--agent"); code != 1 {
		t.Fatalf("declined: exit %d", code)
	}
	if entry := h.config(t).Profiles["agent"]; entry.Token != "lalab_pat_OLDOLDOLDOLD" || !entry.AllowWrite {
		t.Fatalf("declined, but changed: %+v", entry)
	}

	if code := h.run("1\ny\n", true, "auth", "import", "--agent"); code != 0 {
		t.Fatalf("replaced: exit %d %s", code, h.err.String())
	}
	if !strings.Contains(h.out.String(), "allow_write") {
		t.Fatalf("the dropped release was not mentioned:\n%s", h.out.String())
	}
	if entry := h.config(t).Profiles["agent"]; entry.Token != readToken || entry.AllowWrite {
		t.Fatalf("saved %+v", entry)
	}
}

func TestImportUnderAChosenName(t *testing.T) {
	srv := fakeServer(t, "Anna Albers")
	h := newHarness(t)
	h.claudeConfig(t, httpEntry("lalabase", srv.URL, writeToken))
	if code := h.run("1\n", true, "auth", "import", "--profile", "staging"); code != 0 {
		t.Fatalf("exit %d %s", code, h.err.String())
	}
	if _, ok := h.config(t).Profiles["staging"]; !ok {
		t.Fatal("no profile staging")
	}
}

// Names, addresses and server texts pass the cleaning before they reach the
// terminal.
func TestImportCleansForeignText(t *testing.T) {
	srv := fakeServer(t, "Anna\x1b]0;GEKAPERT\x07 Albers")
	h := newHarness(t)
	h.claudeConfig(t, httpEntry("lalabase\\u001b[2J", srv.URL, writeToken))
	h.run("1\n", true, "auth", "import")
	if strings.ContainsRune(h.out.String()+h.err.String(), 0x1b) {
		t.Fatalf("an escape character reached the terminal:\n%q", h.out.String())
	}
}

func saveProfiles(t *testing.T, h *harness, srv *httptest.Server, agentToken string) {
	t.Helper()
	if err := profile.Save(h.configPath(), profile.File{Profiles: map[string]profile.Entry{
		"default": {URL: srv.URL, Token: writeToken},
		"agent":   {URL: srv.URL, Token: agentToken},
	}}); err != nil {
		t.Fatal(err)
	}
}

func TestStatusShowsBothProfilesAndWhatTheServerSupports(t *testing.T) {
	srv := fakeServer(t, "Anna Albers")
	h := newHarness(t)
	saveProfiles(t, h, srv, readToken)
	if code := h.run("", false, "auth", "status"); code != 0 {
		t.Fatalf("exit %d: %s %s", code, h.out.String(), h.err.String())
	}
	out := h.out.String()
	for _, want := range []string{"default", "agent", "Anna Albers", "0.7.0", "inbox", "inbox_by_organisation", "lalab_pat_WR"} {
		if !strings.Contains(out, want) {
			t.Errorf("status lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, writeToken) || strings.Contains(out, readToken) {
		t.Fatalf("status shows a whole token:\n%s", out)
	}
}

func TestStatusFailsWhenTheAgentTokenMayWrite(t *testing.T) {
	srv := fakeServer(t, "Anna Albers")
	h := newHarness(t)
	saveProfiles(t, h, srv, writeToken)
	if code := h.run("", false, "auth", "status"); code != 1 || !strings.Contains(h.out.String(), "read-only") {
		t.Fatalf("exit %d:\n%s", code, h.out.String())
	}
}

func TestStatusWithAProfileShowsOnlyThatOne(t *testing.T) {
	srv := fakeServer(t, "Anna Albers")
	h := newHarness(t)
	saveProfiles(t, h, srv, writeToken)
	if code := h.run("", false, "--profile", "default", "auth", "status"); code != 0 {
		t.Fatalf("exit %d:\n%s%s", code, h.out.String(), h.err.String())
	}
	if strings.Contains(h.out.String(), "agent") {
		t.Fatalf("shows the agent profile:\n%s", h.out.String())
	}
}

func TestStatusReportsHalfAnEnvPair(t *testing.T) {
	srv := fakeServer(t, "Anna Albers")
	h := newHarness(t)
	saveProfiles(t, h, srv, readToken)
	h.vars["LALABASE_URL"] = srv.URL
	if code := h.run("", false, "auth", "status"); code != 1 || !strings.Contains(h.out.String()+h.err.String(), "LALABASE_TOKEN") {
		t.Fatalf("exit %d:\n%s%s", code, h.out.String(), h.err.String())
	}
}
