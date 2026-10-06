package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/creack/pty"

	"github.com/gboyke/lalabase-cli/internal/contract"
	"github.com/gboyke/lalabase-cli/internal/profile"
	"github.com/gboyke/lalabase-cli/internal/transport"
)

const (
	readToken  = "lalab_pat_READREADREADREAD"
	writeToken = "lalab_pat_WRITEWRITEWRITE"
	// oldToken belongs to a server before MCP 0.7.0: /me does not say
	// whether it may write.
	oldToken = "lalab_pat_OLDSERVEROLDSERV"
)

// fakeServer answers /api/v1/me per token and the MCP initialize and
// tools/list from the contract copy.
func fakeServer(t *testing.T, displayName string) *httptest.Server {
	t.Helper()
	file, err := contract.Load()
	if err != nil {
		t.Fatal(err)
	}
	readOnly := map[string]bool{readToken: true, writeToken: false, oldToken: false}
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
			tokenInfo := map[string]any{"id": "t-1", "name": "n", "prefix": token[:12], "read_only": ro}
			if token == oldToken {
				delete(tokenInfo, "read_only")
			}
			body, _ := json.Marshal(map[string]any{"data": map[string]any{
				"user":                 map[string]any{"id": "u-1", "email": "anna@example.com", "display_name": displayName},
				"token":                tokenInfo,
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
	if !strings.Contains(h.err.String(), "Anna Albers") || strings.Contains(h.out.String()+h.err.String(), writeToken) {
		t.Fatalf("output shows the whole token or not the owner:\n%s", h.err.String())
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
	if !strings.Contains(h.err.String(), "lalabase-local") || !strings.Contains(h.err.String(), "local program") {
		t.Fatalf("the stdio entry is not listed with its reason:\n%s", h.err.String())
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
	if !strings.Contains(h.err.String(), "read-only") {
		t.Fatalf("no warning:\n%s", h.err.String())
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
	if !strings.Contains(h.err.String(), "allow_write") {
		t.Fatalf("the dropped release was not mentioned:\n%s", h.err.String())
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

// The profile "agent" is the agent's, whatever flag named it: the warning
// about a writing token comes without --agent too, and status checks it as
// the agent commands will.
func TestTheAgentProfileIsTreatedAsTheAgentsWithoutTheFlag(t *testing.T) {
	srv := fakeServer(t, "Anna Albers")
	h := newHarness(t)
	h.claudeConfig(t, httpEntry("lalabase", srv.URL, writeToken))
	if code := h.run("1\nn\n", true, "auth", "import", "--profile", "agent"); code != 1 || !strings.Contains(h.err.String(), "read-only") {
		t.Fatalf("import --profile agent: exit %d\n%s", code, h.err.String())
	}

	saveProfiles(t, h, srv, writeToken)
	if code := h.run("", false, "--profile", "agent", "auth", "status"); code != 1 || !strings.Contains(h.out.String(), "agent commands refuse") {
		t.Fatalf("status --profile agent: exit %d\n%s", code, h.out.String())
	}
}

// A server that does not say whether the token may write gets the same
// warning as a writing token.
func TestImportAsAgentWarnsWhenTheServerDoesNotSay(t *testing.T) {
	srv := fakeServer(t, "Anna Albers")
	h := newHarness(t)
	h.claudeConfig(t, httpEntry("lalabase", srv.URL, oldToken))
	if code := h.run("1\nn\n", true, "auth", "import", "--agent"); code != 1 || !strings.Contains(h.err.String(), "read-only") {
		t.Fatalf("exit %d\n%s", code, h.err.String())
	}
}

// An entry from the current directory's .mcp.json says so, and whether
// Claude Code was allowed to use it.
func TestImportMarksEntriesFromTheCurrentDirectory(t *testing.T) {
	srv := fakeServer(t, "Anna Albers")
	h := newHarness(t)
	project := `{"mcpServers": {` + httpEntry("lalabase", srv.URL, writeToken) + `}}`
	if err := os.WriteFile(filepath.Join(h.cwd, ".mcp.json"), []byte(project), 0o600); err != nil {
		t.Fatal(err)
	}
	h.run("", false, "auth", "import")
	for _, want := range []string{"check the address", "approval entry in ~/.claude.json: none.", "Effective Claude Code settings were not checked."} {
		if !strings.Contains(h.err.String(), want) {
			t.Fatalf("lacks %q:\n%s", want, h.err.String())
		}
	}
}

// A config file that cannot be read whole is never written back.
func TestImportDoesNotRewriteAConfigItCannotReadWhole(t *testing.T) {
	srv := fakeServer(t, "Anna Albers")
	h := newHarness(t)
	h.claudeConfig(t, httpEntry("lalabase", srv.URL, writeToken))
	if err := os.MkdirAll(filepath.Dir(h.configPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	big := "# " + strings.Repeat("x", 1<<20) + "\n[profiles.agent]\nurl = \"https://x.example\"\ntoken = \"lalab_pat_keepme00\"\n"
	if err := os.WriteFile(h.configPath(), []byte(big), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := h.run("1\n", true, "auth", "import"); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if after, _ := os.ReadFile(h.configPath()); string(after) != big {
		t.Fatal("the config was rewritten")
	}
}

// When the profile changed between the question and the save (another
// import, a hand edit), nothing is overwritten: whatever field changed.
func TestImportDoesNotOverwriteAProfileThatChangedMeanwhile(t *testing.T) {
	old := profile.Entry{URL: "https://old.example", Token: "lalab_pat_OLDOLDOLDOLD"}
	changes := map[string]profile.Entry{
		"token":       {URL: "https://meanwhile.example", Token: "lalab_pat_MEANWHILE000"},
		"url only":    {URL: "https://meanwhile.example", Token: old.Token},
		"allow_write": {URL: old.URL, Token: old.Token, AllowWrite: true},
	}
	for name, meanwhile := range changes {
		t.Run(name, func(t *testing.T) {
			srv := fakeServer(t, "Anna Albers")
			h := newHarness(t)
			h.claudeConfig(t, httpEntry("lalabase", srv.URL, writeToken))
			if err := profile.Save(h.configPath(), profile.File{Profiles: map[string]profile.Entry{"default": old}}); err != nil {
				t.Fatal(err)
			}
			// The answer to "Replace it?" arrives after another process
			// changed the profile.
			changer := &changingReader{answers: "1\ny\n", onSecondLine: func() {
				_ = profile.Save(h.configPath(), profile.File{Profiles: map[string]profile.Entry{"default": meanwhile}})
			}}
			code := runWith([]string{"auth", "import"}, Env{
				Stdin: changer, Stdout: &h.out, Stderr: &h.err,
				Getenv: func(k string) string { return h.vars[k] }, Home: h.home, Cwd: h.cwd, Interactive: true,
			})
			if code != 1 || !strings.Contains(h.err.String(), "changed") {
				t.Fatalf("exit %d\n%s%s", code, h.out.String(), h.err.String())
			}
			if entry := h.config(t).Profiles["default"]; entry != meanwhile {
				t.Fatalf("overwritten: %+v", entry)
			}
		})
	}
}

// changingReader hands out its answers and runs onSecondLine just before the
// second line is read.
type changingReader struct {
	answers      string
	onSecondLine func()
	lines        int
}

func (r *changingReader) Read(p []byte) (int, error) {
	if r.answers == "" {
		return 0, io.EOF
	}
	i := strings.IndexByte(r.answers, '\n') + 1
	r.lines++
	if r.lines == 2 && r.onSecondLine != nil {
		r.onSecondLine()
	}
	n := copy(p, r.answers[:i])
	r.answers = r.answers[n:]
	return n, nil
}

// An answer is one short line: an endless input (a device, a pipe that never
// ends a line) is not read without end.
func TestAnEndlessAnswerIsNotReadWithoutEnd(t *testing.T) {
	srv := fakeServer(t, "Anna Albers")
	h := newHarness(t)
	h.claudeConfig(t, httpEntry("lalabase", srv.URL, writeToken))
	endless := &zeros{}
	done := make(chan int, 1)
	go func() {
		done <- runWith([]string{"auth", "import"}, Env{
			Stdin: endless, Stdout: &h.out, Stderr: &h.err,
			Getenv: func(k string) string { return h.vars[k] }, Home: h.home, Cwd: h.cwd, Interactive: true,
		})
	}()
	select {
	case code := <-done:
		if code != 1 {
			t.Fatalf("exit %d\n%s%s", code, h.out.String(), h.err.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("import still reads its answer")
	}
	if n := endless.n.Load(); n > 64<<10 {
		t.Fatalf("read %d bytes for one answer", n)
	}
	if _, err := os.Stat(h.configPath()); err == nil {
		t.Fatal("saved a profile")
	}
}

// zeros reads like /dev/zero and counts what was taken.
type zeros struct{ n atomic.Int64 }

func (z *zeros) Read(p []byte) (int, error) {
	clear(p)
	z.n.Add(int64(len(p)))
	return len(p), nil
}

// A device is no terminal: /dev/null is a character device, as a terminal is.
func TestADeviceIsNoTerminal(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if isTerminal(f) {
		t.Fatal("/dev/null taken for a terminal")
	}
}

// auth's requests go through the queue: a 429 holds back what follows, the
// person is told until when, and the read is retried once the window opens.
func TestStatusWaitsForTheWindowOfA429(t *testing.T) {
	srv := fakeServer(t, "Anna Albers")
	var mu sync.Mutex
	var times []time.Time
	throttled := false
	limited := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		times = append(times, time.Now())
		first := !throttled
		throttled = true
		mu.Unlock()
		if first {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		srv.Config.Handler.ServeHTTP(w, r)
	}))
	t.Cleanup(limited.Close)
	h := newHarness(t)
	saveProfiles(t, h, limited, readToken)
	if code := h.run("", false, "auth", "status"); code != 0 {
		t.Fatalf("exit %d: %s %s", code, h.out.String(), h.err.String())
	}
	if !strings.Contains(h.err.String(), "wait") {
		t.Fatalf("not told of the wait:\n%s", h.err.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(times) < 2 || times[1].Sub(times[0]) < 900*time.Millisecond {
		t.Fatalf("the next request did not wait for the window: %v", times)
	}
}

// Both profiles share one queue: when the first gives up on a 429 (its retry
// met a second one), the next profile's request still waits for the window
// instead of running into it.
func TestStatusHoldsTheNextProfileBackAfterA429(t *testing.T) {
	srv := fakeServer(t, "Anna Albers")
	var mu sync.Mutex
	var times []time.Time
	limited := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		times = append(times, time.Now())
		n := len(times)
		mu.Unlock()
		if n <= 2 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		srv.Config.Handler.ServeHTTP(w, r)
	}))
	t.Cleanup(limited.Close)
	h := newHarness(t)
	saveProfiles(t, h, limited, readToken)
	if code := h.run("", false, "auth", "status"); code != 1 {
		t.Fatalf("exit %d: %s %s", code, h.out.String(), h.err.String())
	}
	if !strings.Contains(h.out.String(), "agent commands accept this profile") {
		t.Fatalf("the agent profile was not checked:\n%s", h.out.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(times) < 3 || times[2].Sub(times[1]) < 900*time.Millisecond {
		t.Fatalf("the next profile did not wait for the window: %v", times)
	}
}

// The list and the questions go to stderr, the result to stdout: with stdout
// in a file, the person still sees what is asked.
func TestImportAsksOnStderr(t *testing.T) {
	srv := fakeServer(t, "Anna Albers")
	h := newHarness(t)
	h.claudeConfig(t, httpEntry("lalabase", srv.URL, writeToken))
	if code := h.run("1\n", true, "auth", "import"); code != 0 {
		t.Fatalf("exit %d\n%s%s", code, h.out.String(), h.err.String())
	}
	for _, want := range []string{"Lalabase servers", srv.URL, "Which one?", "Anna Albers"} {
		if !strings.Contains(h.err.String(), want) || strings.Contains(h.out.String(), want) {
			t.Errorf("%q not on stderr alone:\nstdout:\n%s\nstderr:\n%s", want, h.out.String(), h.err.String())
		}
	}
	if !strings.Contains(h.out.String(), "Saved profile") {
		t.Fatalf("the result is not on stdout:\n%s", h.out.String())
	}
}

// An answer is at most 64 bytes (a number or a yes, with room to spare);
// one more and it is none.
func TestAnAnswerHasALimitToTheByte(t *testing.T) {
	ask := func(line string) string {
		return asker{in: bufio.NewReader(strings.NewReader(line + "\n")), out: io.Discard}.ask("? ")
	}
	exact := strings.Repeat("7", 64)
	if got := ask(exact); got != exact {
		t.Fatalf("an answer of 64 bytes: got %q", got)
	}
	if got := ask(exact + "7"); got != "" {
		t.Fatalf("an answer of 65 bytes: got %q", got)
	}
}

func TestYesTakesAnyCase(t *testing.T) {
	for answer, want := range map[string]bool{"y": true, "Y": true, "yes": true, "YES": true, "Yes": true, "n": false, "": false, "yess": false, "ja": false} {
		a := asker{in: bufio.NewReader(strings.NewReader(answer + "\n")), out: io.Discard}
		if got := a.yes("?"); got != want {
			t.Errorf("%q: got %v", answer, got)
		}
	}
}

// A real terminal is one: a pseudo-terminal, as a terminal window opens.
func TestATerminalIsATerminal(t *testing.T) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		// On the CI a skip would pass without testing anything.
		if os.Getenv("CI") != "" {
			t.Fatalf("no pseudo-terminal: %v", err)
		}
		t.Skipf("no pseudo-terminal here: %v", err)
	}
	defer func() { _ = ptmx.Close(); _ = tty.Close() }()
	if !isTerminal(tty) {
		t.Fatal("a pseudo-terminal not taken for a terminal")
	}
}

// auth's requests are reads: a 503 after sending is retried.
func TestStatusRetriesAReadAfterA503(t *testing.T) {
	srv := fakeServer(t, "Anna Albers")
	var calls atomic.Int32
	flaky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		srv.Config.Handler.ServeHTTP(w, r)
	}))
	t.Cleanup(flaky.Close)
	h := newHarness(t)
	saveProfiles(t, h, flaky, readToken)
	if code := h.run("", false, "auth", "status"); code != 0 {
		t.Fatalf("exit %d: %s %s", code, h.out.String(), h.err.String())
	}
}

// When the command's own limit ends inside a rate-limit pause, it says so,
// and does not promise to go on.
func TestAPauseLongerThanTheCommandIsExplained(t *testing.T) {
	old := requestTimeout
	requestTimeout = 300 * time.Millisecond
	t.Cleanup(func() { requestTimeout = old })
	var calls atomic.Int32
	limited := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(limited.Close)
	h := newHarness(t)
	h.claudeConfig(t, httpEntry("lalabase", limited.URL, writeToken))
	if code := h.run("1\n", true, "auth", "import"); code != 1 {
		t.Fatalf("exit %d\n%s", code, h.err.String())
	}
	errText := h.err.String()
	for _, want := range []string{"Rate limit: waiting until", "gave up waiting", "after this command's 300ms limit", "Nothing saved"} {
		if !strings.Contains(errText, want) {
			t.Errorf("lacks %q:\n%s", want, errText)
		}
	}
	for _, wrong := range []string{"continuing", "before it was sent"} {
		if strings.Contains(errText, wrong) {
			t.Errorf("says %q:\n%s", wrong, errText)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("%d requests, want 1", calls.Load())
	}
}

// A pause into the next day names the day; every pause says how long.
func TestWhenAPauseEnds(t *testing.T) {
	now := time.Date(2026, 10, 6, 23, 59, 50, 0, time.Local)
	if got := when(now.Add(60*time.Second), now); got != "2026-10-07 00:00:50 (in 60 s)" {
		t.Fatalf("over midnight: %q", got)
	}
	if got := when(now.Add(-time.Hour).Add(30*time.Second), now.Add(-time.Hour)); got != "23:00:20 (in 30 s)" {
		t.Fatalf("same day: %q", got)
	}
	// Rounded, not cut: 1.6 s is 2 s.
	if got := when(now.Add(-time.Hour).Add(1600*time.Millisecond), now.Add(-time.Hour)); !strings.HasSuffix(got, "(in 2 s)") {
		t.Fatalf("rounding: %q", got)
	}
}

// Why a request brought no answer: the server refused it, the command's
// limit ended (in a pause or not), or it was canceled.
func TestWhyARequestFailed(t *testing.T) {
	r := &requests{now: time.Now, until: time.Now().Add(-time.Minute)}
	expired, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	canceled, stop := context.WithCancel(context.Background())
	stop()
	cases := []struct {
		ctx  context.Context
		ans  transport.Answer
		want string
	}{
		{context.Background(), transport.Answer{Class: transport.Unauthenticated, Message: "no"}, "the server did not accept the token (unauthenticated): no"},
		{expired, transport.Answer{Class: transport.Canceled, Message: "canceled before it was sent"}, "the token could not be checked: no answer within 30s"},
		// A pause that is over is no reason any more.
		{expired, transport.Answer{Class: transport.Temporary, Message: "no answer from the server"}, "the token could not be checked: no answer within 30s"},
		{canceled, transport.Answer{Class: transport.Canceled, Message: "canceled before it was sent"}, "the token could not be checked: canceled"},
		{context.Background(), transport.Answer{Class: transport.Temporary, Message: "boom"}, "the token could not be checked (temporary): boom"},
	}
	for _, tc := range cases {
		if got := r.tokenFailed(tc.ctx, tc.ans); got != tc.want {
			t.Errorf("%v: got %q, want %q", tc.ans.Class, got, tc.want)
		}
	}
}

// Foreign text from the server is cleaned on every way to the terminal, the
// MCP failures of auth status included.
func TestStatusCleansTheServersErrorText(t *testing.T) {
	srv := fakeServer(t, "Anna Albers")
	hostile := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/mcp" {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("down \x1b]8;;https://evil.example\x07click\x1b]8;;\x07"))
			return
		}
		srv.Config.Handler.ServeHTTP(w, r)
	}))
	t.Cleanup(hostile.Close)
	h := newHarness(t)
	saveProfiles(t, h, hostile, readToken)
	if code := h.run("", false, "--profile", "default", "auth", "status"); code != 1 {
		t.Fatalf("exit %d: %s %s", code, h.out.String(), h.err.String())
	}
	if !strings.Contains(h.out.String(), "MCP:") {
		t.Fatalf("the MCP failure is not shown:\n%s", h.out.String())
	}
	if strings.ContainsRune(h.out.String()+h.err.String(), 0x1b) || strings.ContainsRune(h.out.String()+h.err.String(), 0x07) {
		t.Fatalf("a control character reached the terminal:\n%q", h.out.String())
	}
}
