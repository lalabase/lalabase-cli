package profile

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/gboyke/lalabase-cli/internal/api"
)

func env(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

func writeConfig(t *testing.T, content string, mode os.FileMode) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "lalabase")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

const twoProfiles = `
[profiles.default]
url = "https://lalabase.com"
token = "lalab_pat_default0000"

[profiles.agent]
url = "https://lalabase.com"
token = "lalab_pat_agent00000000"
`

func TestConfigPathFollowsXDGOrHome(t *testing.T) {
	if got := Path(env(map[string]string{"XDG_CONFIG_HOME": "/x"}), "/home/a"); got != filepath.Join("/x", "lalabase", "config.toml") {
		t.Errorf("with XDG: %s", got)
	}
	if got := Path(env(nil), "/home/a"); got != filepath.Join("/home/a", ".config", "lalabase", "config.toml") {
		t.Errorf("without XDG: %s", got)
	}
}

func TestTheInteractiveModeTakesTheEnvPairOrTheDefaultProfile(t *testing.T) {
	path := writeConfig(t, twoProfiles, 0o600)
	p, err := Resolve(Interactive, "", env(map[string]string{"LALABASE_URL": "https://x.example", "LALABASE_TOKEN": "lalab_pat_env"}), path)
	if err != nil || p.URL != "https://x.example" || p.Token != "lalab_pat_env" || !strings.Contains(p.Source, "LALABASE_URL") {
		t.Fatalf("env pair: %+v %v", p, err)
	}
	p, err = Resolve(Interactive, "", env(nil), path)
	if err != nil || p.Name != "default" || p.Token != "lalab_pat_default0000" {
		t.Fatalf("file: %+v %v", p, err)
	}
}

// Half an env pair is a mistake, not a hint to fall back on the file: a URL
// from here with a token from there is exactly what profiles prevent.
func TestHalfAnEnvPairIsAnError(t *testing.T) {
	path := writeConfig(t, twoProfiles, 0o600)
	cases := []struct {
		mode Mode
		vars map[string]string
	}{
		{Interactive, map[string]string{"LALABASE_URL": "https://x.example"}},
		{Interactive, map[string]string{"LALABASE_TOKEN": "lalab_pat_env"}},
		{Agent, map[string]string{"LALABASE_AGENT_URL": "https://x.example"}},
		{Agent, map[string]string{"LALABASE_AGENT_TOKEN": "lalab_pat_env"}},
	}
	for _, tc := range cases {
		if p, err := Resolve(tc.mode, "", env(tc.vars), path); err == nil {
			t.Errorf("%v %v: got %+v, want an error", tc.mode, tc.vars, p)
		}
	}
}

// Agent commands never use the person's profile or LALABASE_TOKEN.
func TestTheAgentModeNeverFallsBackToThePersonsProfile(t *testing.T) {
	onlyDefault := writeConfig(t, `
[profiles.default]
url = "https://lalabase.com"
token = "lalab_pat_default0000"
`, 0o600)
	_, err := Resolve(Agent, "", env(map[string]string{"LALABASE_URL": "https://x.example", "LALABASE_TOKEN": "lalab_pat_env"}), onlyDefault)
	if err == nil || !strings.Contains(err.Error(), "auth import --agent") {
		t.Fatalf("got %v, want the setup hint", err)
	}

	both := writeConfig(t, twoProfiles, 0o600)
	p, err := Resolve(Agent, "", env(map[string]string{"LALABASE_TOKEN": "lalab_pat_env"}), both)
	if err != nil || p.Name != "agent" || p.Token != "lalab_pat_agent00000000" {
		t.Fatalf("got %+v %v", p, err)
	}
	p, err = Resolve(Agent, "", env(map[string]string{"LALABASE_AGENT_URL": "https://a.example", "LALABASE_AGENT_TOKEN": "lalab_pat_agentenv"}), both)
	if err != nil || p.Token != "lalab_pat_agentenv" {
		t.Fatalf("agent env pair: %+v %v", p, err)
	}
}

// --profile picks one complete profile of the file; the env pairs are
// ignored entirely, even half of one.
func TestAnExplicitProfileIgnoresTheEnvironment(t *testing.T) {
	path := writeConfig(t, twoProfiles, 0o600)
	vars := map[string]string{"LALABASE_URL": "https://x.example", "LALABASE_AGENT_TOKEN": "lalab_pat_half"}
	for _, mode := range []Mode{Interactive, Agent} {
		p, err := Resolve(mode, "default", env(vars), path)
		if err != nil || p.Name != "default" || p.Token != "lalab_pat_default0000" {
			t.Errorf("%v: %+v %v", mode, p, err)
		}
	}
	if _, err := Resolve(Interactive, "nope", env(nil), path); err == nil {
		t.Error("an unknown profile was accepted")
	}
}

func TestAProfileWithoutURLOrTokenIsAnError(t *testing.T) {
	path := writeConfig(t, "[profiles.default]\nurl = \"https://lalabase.com\"\n", 0o600)
	if _, err := Resolve(Interactive, "", env(nil), path); err == nil {
		t.Fatal("a profile without token was accepted")
	}
}

func TestAMissingFileSaysHowToSetUp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "none", "config.toml")
	_, err := Resolve(Interactive, "", env(nil), path)
	if err == nil || !strings.Contains(err.Error(), "lalabase auth import") {
		t.Fatalf("got %v", err)
	}
}

// The file holds tokens: refused when anybody but the owner may read it.
func TestAFileOthersCanReadOrChangeIsRefused(t *testing.T) {
	for _, mode := range []os.FileMode{0o640, 0o604, 0o644, 0o666, 0o620, 0o602, 0o610, 0o601} {
		path := writeConfig(t, twoProfiles, mode)
		if _, err := Resolve(Interactive, "", env(nil), path); err == nil || !strings.Contains(err.Error(), "chmod 600") {
			t.Errorf("mode %o: got %v", mode, err)
		}
	}
}

func TestAFileThatIsNoRegularFileIsRefused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("a directory was read as the config")
	}
}

func TestSaveWritesAPrivateFileAtomicallyAndKeepsOtherProfiles(t *testing.T) {
	path := writeConfig(t, twoProfiles, 0o600)
	file, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	file.Profiles["default"] = Entry{URL: "https://new.example", Token: "lalab_pat_new000000"}
	if err := Save(path, file); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v %v", info.Mode(), err)
	}
	again, err := Load(path)
	if err != nil || again.Profiles["default"].Token != "lalab_pat_new000000" || again.Profiles["agent"].Token != "lalab_pat_agent00000000" {
		t.Fatalf("%+v %v", again, err)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("leftovers next to the config: %v", entries)
	}
}

func TestSaveCreatesAPrivateDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conf", "lalabase", "config.toml")
	if err := Save(path, File{Profiles: map[string]Entry{"default": {URL: "https://lalabase.com", Token: "lalab_pat_x0000000"}}}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Dir(path))
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("directory mode %v %v", info.Mode(), err)
	}
}

func TestSaveRefusesALinkedConfig(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "elsewhere.toml")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.toml")
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, File{Profiles: map[string]Entry{}}); err == nil {
		t.Fatal("a linked config was written through")
	}
	if got, _ := os.ReadFile(outside); string(got) != "x" {
		t.Fatalf("the linked file changed: %q", got)
	}
}

func readOnly(v bool) *bool { return &v }

// The agent profile must hold a token that may only read; a server that does
// not say is refused too. allow_write is the person's own choice in the file.
func TestVerifyAgent(t *testing.T) {
	me := func(ro *bool) api.Me {
		var m api.Me
		m.Token.ReadOnly = ro
		return m
	}
	if err := VerifyAgent(me(readOnly(true)), Profile{}); err != nil {
		t.Errorf("read-only token: %v", err)
	}
	if err := VerifyAgent(me(readOnly(false)), Profile{}); err == nil {
		t.Error("a writing token was accepted")
	}
	if err := VerifyAgent(me(nil), Profile{}); err == nil {
		t.Error("a token the server says nothing about was accepted")
	}
	if err := VerifyAgent(me(readOnly(false)), Profile{AllowWrite: true}); err != nil {
		t.Errorf("allow_write: %v", err)
	}
	// The person allowed writes; whether the server says so changes nothing.
	if err := VerifyAgent(me(nil), Profile{AllowWrite: true}); err != nil {
		t.Errorf("allow_write with a server that does not say: %v", err)
	}
}

// A config larger than the limit is an error, not a silent cut: a cut file
// written back by auth import would lose the profiles behind the cut.
func TestAnOversizedConfigIsAnErrorNotACut(t *testing.T) {
	big := "# " + strings.Repeat("x", maxConfigSize) + "\n" + twoProfiles
	path := writeConfig(t, big, 0o600)
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("got %v", err)
	}
}

// Two updates at the same time both land: load, merge and replace happen
// under one lock. The second update is held at the lock while the first has
// loaded and not yet saved.
func TestConcurrentUpdatesDoNotLoseAProfile(t *testing.T) {
	path := writeConfig(t, twoProfiles, 0o600)
	loaded := make(chan struct{})
	release := make(chan struct{})
	first := true
	afterLoad = func() {
		if first {
			first = false
			close(loaded)
			<-release
		}
	}
	t.Cleanup(func() { afterLoad = nil })

	errs := make(chan error, 2)
	go func() {
		errs <- Update(path, func(f *File) error {
			f.Profiles["one"] = Entry{URL: "https://one.example", Token: "lalab_pat_one00000"}
			return nil
		})
	}()
	select {
	case <-loaded:
	case <-time.After(5 * time.Second):
		t.Fatal("the first update never loaded the file")
	}
	secondDone := make(chan struct{})
	go func() {
		errs <- Update(path, func(f *File) error {
			f.Profiles["two"] = Entry{URL: "https://two.example", Token: "lalab_pat_two00000"}
			return nil
		})
		close(secondDone)
	}()
	select {
	case <-secondDone:
		t.Fatal("the second update ran while the first held the file")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	for range 2 {
		select {
		case err := <-errs:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("an update did not finish")
		}
	}
	file, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"default", "agent", "one", "two"} {
		if _, ok := file.Profiles[name]; !ok {
			t.Errorf("profile %s was lost", name)
		}
	}
}

func TestAnUpdateThatFailsWritesNothing(t *testing.T) {
	path := writeConfig(t, twoProfiles, 0o600)
	before, _ := os.ReadFile(path)
	err := Update(path, func(f *File) error {
		f.Profiles["x"] = Entry{URL: "u", Token: "t"}
		return errors.New("changed meanwhile")
	})
	if err == nil {
		t.Fatal("no error")
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatal("the file changed")
	}
}

// A config that would outgrow what Load reads is not written: it could not
// be read back, and every profile in it would be out of reach.
func TestAConfigTooLargeToReadBackIsNotWritten(t *testing.T) {
	path := writeConfig(t, twoProfiles, 0o600)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	huge := Entry{URL: "https://" + strings.Repeat("a", maxConfigSize) + ".example", Token: "lalab_pat_big000000"}
	err = Update(path, func(f *File) error {
		f.Profiles["big"] = huge
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("got %v", err)
	}
	if err := Save(path, File{Profiles: map[string]Entry{"big": huge}}); err == nil {
		t.Fatal("Save wrote a config it cannot read back")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("the config changed")
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("the old config no longer loads: %v", err)
	}
}

// sized returns a config whose encoding is exactly n bytes.
func sized(t *testing.T, n int) File {
	t.Helper()
	encode := func(f File) int {
		var buf bytes.Buffer
		if err := toml.NewEncoder(&buf).Encode(f); err != nil {
			t.Fatal(err)
		}
		return buf.Len()
	}
	with := func(k int) File {
		return File{Profiles: map[string]Entry{"default": {URL: "https://" + strings.Repeat("a", k) + ".example", Token: "lalab_pat_size0000"}}}
	}
	k := n - encode(with(0))
	f := with(k)
	if got := encode(f); got != n {
		t.Fatalf("encoded %d bytes, want %d", got, n)
	}
	return f
}

// What Save writes, Load reads back, up to the byte: a config of exactly the
// limit is saved and loaded, one byte more is not saved.
func TestTheSizeLimitIsTheSameForSaveAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lalabase", "config.toml")
	if err := Save(path, sized(t, maxConfigSize)); err != nil {
		t.Fatalf("a config of exactly the limit: %v", err)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("a config of exactly the limit does not load: %v", err)
	}
	before, _ := os.ReadFile(path)
	if err := Save(path, sized(t, maxConfigSize+1)); err == nil {
		t.Fatal("one byte over the limit was saved")
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
		t.Fatal("the config changed")
	}
}

// A rename that fails is a failed save: the old file stays, no temporary
// file is left behind.
func TestAFailedRenameIsAFailedSave(t *testing.T) {
	path := writeConfig(t, twoProfiles, 0o600)
	rename = func(string, string) error { return errors.New("disk gone") }
	t.Cleanup(func() { rename = os.Rename })
	if err := Save(path, File{Profiles: map[string]Entry{}}); err == nil || !strings.Contains(err.Error(), "disk gone") {
		t.Fatalf("got %v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != twoProfiles {
		t.Fatal("the config changed")
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "config.toml" {
			t.Fatalf("left behind: %s", e.Name())
		}
	}
}
