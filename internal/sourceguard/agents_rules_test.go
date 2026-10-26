package sourceguard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// C9a: the agents' files. internal/agents imports what fileSystemImports
// names for it, by name; the output, environment, child process and
// goroutine rules hold there as everywhere the interface reaches; no other
// package gets its names; the views cannot reach it. `lalabase hook`
// (cmd/lalabase/hook.go), which runs inside Claude Code's turn, writes to
// no stream and reads no variable but CLAUDE_PID.

// reachAgents makes the package reachable, as the root controller does.
const reachAgents = `package ui; import _ "` + clientModule + `internal/agents"`

var agentsDeps = map[string]string{
	"internal/ui/agents.go":           reachAgents,
	"internal/agents/agents.go":       `package agents; import ( _ "` + clientModule + `internal/privatefs"; _ "` + clientModule + `internal/safefile"; _ "` + clientModule + `internal/worker" )`,
	"internal/privatefs/privatefs.go": `package privatefs`,
	"internal/safefile/safefile.go":   `package safefile`,
	"internal/worker/worker.go":       `package worker`,
}

func withAgents(extra map[string]string) map[string]string {
	files := map[string]string{}
	for k, v := range agentsDeps {
		files[k] = v
	}
	for k, v := range extra {
		files[k] = v
	}
	return files
}

func TestTheAgentsPackageImportsByName(t *testing.T) {
	for name, source := range map[string]string{
		"internal/agents/x.go":        `package agents; import ("os"; "io/fs"; "path/filepath"; "crypto/rand"; "encoding/hex"); var _ = os.MkdirAll; var _ = os.Lstat; var _ = os.Open; var _ = os.OpenFile; var _ = os.O_WRONLY | os.O_CREATE | os.O_EXCL; var _ = os.CreateTemp; var _ = os.Rename; var _ = os.Remove; var _ fs.DirEntry; var _ = filepath.Join; var _ = filepath.IsAbs; var _ = rand.Read; var _ = hex.EncodeToString`,
		"internal/agents/x_darwin.go": `package agents; import "golang.org/x/sys/unix"; var _ = unix.SysctlKinfoProcSlice`,
	} {
		if got := inspectUI(withAgents(map[string]string{name: source})); len(got) != 0 {
			t.Errorf("%s rejected: %v", name, got)
		}
	}
	for name, source := range map[string]string{
		"stdout":    `package agents; import "os"; var out = os.Stdout`,
		"stderr":    `package agents; import sys "os"; var out = sys.Stderr`,
		"getenv":    `package agents; import "os"; var env = os.Getenv`,
		"environ":   `package agents; import "os"; var env = os.Environ`,
		"lookupenv": `package agents; import "os"; var env = os.LookupEnv`,
		"exit":      `package agents; import "os"; var quit = os.Exit`,
		"readfile":  `package agents; import "os"; var read = os.ReadFile`,
		"writefile": `package agents; import "os"; var write = os.WriteFile`,
		"removeall": `package agents; import "os"; var rm = os.RemoveAll`,
		"chmod":     `package agents; import "os"; var _ = os.Chmod`,
		"symlink":   `package agents; import "os"; var _ = os.Symlink`,
		"exec":      `package agents; import "os/exec"; var _ = exec.Command`,
		"start":     `package agents; import "os"; var _ = os.StartProcess`,
		"kill":      `package agents; import "golang.org/x/sys/unix"; var _ = unix.Kill`,
		"unixwrite": `package agents; import u "golang.org/x/sys/unix"; var _ = u.Write`,
		"syscall":   `package agents; import "golang.org/x/sys/unix"; func f() { unix.Syscall6(unix.SYS_GETATTRLIST, 0, 0, 0, 0, 0, 0) }`,
		"walk":      `package agents; import "path/filepath"; var _ = filepath.WalkDir`,
		"glob":      `package agents; import "path/filepath"; var _ = filepath.Glob`,
		"go":        `package agents; func run(f func()) { go f() }`,
		"afterfunc": `package agents; import "time"; var _ = time.AfterFunc`,
		"log":       `package agents; import "log"; var out = log.Printf`,
		"println":   `package agents; import "fmt"; var out = fmt.Println`,
		"term":      `package agents; import "golang.org/x/term"; var _ = term.MakeRaw`,
	} {
		if got := inspectUI(withAgents(map[string]string{"internal/agents/probe.go": source})); len(got) == 0 {
			t.Errorf("missed %s", name)
		}
	}
}

// The allowance is the package's alone: the same names stay refused
// everywhere else the interface reaches, and a view cannot reach the
// package at all.
func TestTheAgentsAllowanceReachesNoOtherPackage(t *testing.T) {
	for name, source := range map[string]string{
		"internal/ui/x.go":        `package ui; import "os"; var _ = os.CreateTemp`,
		"internal/launch/x.go":    `package launch; import "os"; var _ = os.Rename`,
		"internal/worker/x.go":    `package worker; import "golang.org/x/sys/unix"; var _ = unix.SysctlKinfoProcSlice`,
		"internal/safefile/x.go":  `package safefile; import "os"; var _ = os.CreateTemp`,
		"internal/privatefs/x.go": `package privatefs; import "golang.org/x/sys/unix"; var _ = unix.SysctlKinfoProcSlice`,
	} {
		files := withAgents(map[string]string{name: source})
		files["internal/ui/model.go"] = `package ui; import ( _ "` + clientModule + `internal/launch" )`
		files["internal/launch/launch.go"] = `package launch`
		if got := inspectUI(files); len(got) == 0 {
			t.Errorf("missed %s", name)
		}
	}
	files := withAgents(map[string]string{"internal/ui/view/pane.go": `package view; import _ "` + clientModule + `internal/agents"`})
	if got := strings.Join(inspectUI(files), " "); !strings.Contains(got, "internal/ui/view/pane.go") {
		t.Errorf("a view reached the agents: %s", got)
	}
}

// The real package passes, and a probe in it turns the guard red.
func TestTheGuardReadsTheRealAgentsPackage(t *testing.T) {
	root := repoRoot(t)
	sources := repositorySources(t)
	for _, name := range []string{"internal/agents/agents.go", "internal/agents/hook.go", "internal/agents/process_darwin.go"} {
		if _, ok := sources[name]; !ok {
			t.Fatalf("%s is not scanned", name)
		}
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		for _, probe := range []string{"\nfunc probe(f func()) { go f() }\n", "\nfunc probe() { print(1) }\n", "\nvar _ = os.Getenv\n"} {
			files := map[string]string{}
			for k, v := range sources {
				files[k] = v
			}
			files[name] = string(data) + probe
			// Reachable from the interface, whether or not it is yet.
			files["internal/ui/agents_probe.go"] = reachAgents
			if name == "internal/agents/process_darwin.go" {
				files[name] = strings.Replace(files[name], `import "golang.org/x/sys/unix"`, `import ("os"; "golang.org/x/sys/unix")`, 1)
			}
			if got := inspectUI(files); len(got) == 0 {
				t.Errorf("%s with %q passed", name, probe)
			}
		}
	}
}
