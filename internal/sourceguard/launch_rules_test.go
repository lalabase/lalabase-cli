package sourceguard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// C7: Claude Code beside the ticket. internal/launch is the client's one
// package that starts a child process, and it imports what launchImports
// names, by name; internal/claudecfg, which it reads Claude Code's
// configuration through, gets the file names it needs, by name. The output,
// environment and goroutine rules hold in both, as everywhere the interface
// reaches, and no other package gets any of it.

// reachLaunch makes both packages reachable, as the root controller does.
const reachLaunch = `package ui; import _ "` + clientModule + `internal/launch"`

var launchDeps = map[string]string{
	"internal/ui/launch.go":           reachLaunch,
	"internal/launch/launch.go":       `package launch; import _ "` + clientModule + `internal/claudecfg"`,
	"internal/claudecfg/claudecfg.go": `package claudecfg; import _ "` + clientModule + `internal/safefile"`,
	"internal/safefile/safefile.go":   `package safefile`,
}

func withLaunch(extra map[string]string) map[string]string {
	files := map[string]string{}
	for k, v := range launchDeps {
		files[k] = v
	}
	for k, v := range extra {
		files[k] = v
	}
	return files
}

func TestTheLaunchPackagesImportByName(t *testing.T) {
	for name, source := range map[string]string{
		"internal/launch/x.go":    `package launch; import ("os"; "os/exec"; "path/filepath"); var _ = exec.Command; var _ = exec.CommandContext; var _ = exec.LookPath; var _ *exec.Cmd; var _ = os.Stat; var _ os.FileInfo; var _ = filepath.Join; var _ = filepath.IsAbs; var _ = filepath.Clean`,
		"internal/claudecfg/x.go": `package claudecfg; import ("os"; "io/fs"; "path/filepath"; "sort"); var _ = os.Lstat; var _ = fs.ErrNotExist; var _ = filepath.Join; var _ = filepath.EvalSymlinks; var _ = filepath.Dir; var _ = filepath.Clean; var _ = filepath.IsAbs; var _ = filepath.Base; var _ = sort.Strings`,
	} {
		if got := inspectUI(withLaunch(map[string]string{name: source})); len(got) != 0 {
			t.Errorf("%s rejected: %v", name, got)
		}
	}
	for name, source := range map[string]string{
		"launch#stdout":       `package launch; import "os"; var out = os.Stdout`,
		"launch#stderr":       `package launch; import sys "os"; var out = sys.Stderr`,
		"launch#getenv":       `package launch; import "os"; var env = os.Getenv`,
		"launch#environ":      `package launch; import "os"; var env = os.Environ`,
		"launch#lookupenv":    `package launch; import "os"; var env = os.LookupEnv`,
		"launch#exit":         `package launch; import "os"; var quit = os.Exit`,
		"launch#startprocess": `package launch; import "os"; var run = os.StartProcess`,
		"launch#readfile":     `package launch; import "os"; var read = os.ReadFile`,
		"launch#writefile":    `package launch; import "os"; var write = os.WriteFile`,
		"launch#remove":       `package launch; import "os"; var rm = os.Remove`,
		"launch#chdir":        `package launch; import "os"; var cd = os.Chdir`,
		"launch#syscallexec":  `package launch; import "syscall"; var run = syscall.Exec`,
		"launch#forkexec":     `package launch; import "syscall"; var run = syscall.ForkExec`,
		"launch#go":           `package launch; func run(f func()) { go f() }`,
		"launch#log":          `package launch; import "log"; var out = log.Printf`,
		"launch#println":      `package launch; import "fmt"; var out = fmt.Println`,
		"launch#glob":         `package launch; import "path/filepath"; var _ = filepath.Glob`,
		"launch#unix":         `package launch; import "golang.org/x/sys/unix"; var _ = unix.Exec`,
		"launch#execname":     `package launch; import "os/exec"; var _ = exec.ErrNotFound`,
		"launch#term":         `package launch; import "golang.org/x/term"; var _ = term.MakeRaw`,
		"claudecfg#exec":      `package claudecfg; import "os/exec"; var _ = exec.Command`,
		"claudecfg#stdout":    `package claudecfg; import "os"; var out = os.Stdout`,
		"claudecfg#getenv":    `package claudecfg; import "os"; var env = os.Getenv`,
		"claudecfg#remove":    `package claudecfg; import "os"; var rm = os.Remove`,
		"claudecfg#readfile":  `package claudecfg; import "os"; var read = os.ReadFile`,
		"claudecfg#stat":      `package claudecfg; import "os"; var _ = os.Stat`,
		"claudecfg#walk":      `package claudecfg; import "path/filepath"; var _ = filepath.WalkDir`,
		"claudecfg#go":        `package claudecfg; func run(f func()) { go f() }`,
	} {
		pkg, _, _ := strings.Cut(name, "#")
		if got := inspectUI(withLaunch(map[string]string{"internal/" + pkg + "/probe.go": source})); len(got) == 0 {
			t.Errorf("missed %s", name)
		}
	}
}

// The allowance is per package: the same names stay refused everywhere
// else the interface reaches.
func TestTheLaunchAllowanceReachesNoOtherPackage(t *testing.T) {
	for name, source := range map[string]string{
		"internal/ui/x.go":        `package ui; import "os/exec"; var _ = exec.Command`,
		"internal/ui/view/x.go":   `package view; import "os"; var _ = os.Stat`,
		"internal/gateway/x.go":   `package gateway; import "os/exec"; var _ = exec.LookPath`,
		"internal/diskcache/x.go": `package diskcache; import "os/exec"; var _ = exec.Command`,
		"internal/safefile/x.go":  `package safefile; import "path/filepath"; var _ = filepath.EvalSymlinks`,
		"internal/privatefs/x.go": `package privatefs; import "sort"; var _ = sort.Strings`,
	} {
		files := withLaunch(map[string]string{name: source})
		files["internal/ui/model.go"] = `package ui; import ( _ "` + clientModule + `internal/gateway"; _ "` + clientModule + `internal/diskcache"; _ "` + clientModule + `internal/privatefs" )`
		files["internal/gateway/gateway.go"], files["internal/diskcache/diskcache.go"], files["internal/privatefs/privatefs.go"] = `package gateway`, `package diskcache`, `package privatefs`
		if got := inspectUI(files); len(got) == 0 {
			t.Errorf("missed %s", name)
		}
	}
}

// Wherever the interface does not reach, the client still starts a child
// process only in internal/launch: os/exec and the process calls of os and
// syscall are refused in every production file of cmd/ and internal/. Tests
// and the developer tools under tools/ may run programs.
func TestOnlyTheLaunchPackageStartsAChildProcess(t *testing.T) {
	for name, source := range map[string]string{
		"cmd/lalabase/run.go":         `package main; import "os/exec"; var _ = exec.Command`,
		"cmd/lalabase/alias.go":       `package main; import x "os/exec"; var _ = x.LookPath`,
		"cmd/lalabase/start.go":       `package main; import "os"; var _ = os.StartProcess`,
		"cmd/lalabase/fork.go":        `package main; import "syscall"; var _ = syscall.ForkExec`,
		"cmd/lalabase/exec.go":        `package main; import "syscall"; var _ = syscall.Exec`,
		"cmd/lalabase/sysstart.go":    `package main; import "syscall"; var _ = syscall.StartProcess`,
		"cmd/lalabase/execabs.go":     `package main; import "golang.org/x/sys/execabs"; var _ = execabs.Command`,
		"internal/profile/execabs.go": `package profile; import e "golang.org/x/sys/execabs"; var _ = e.LookPath`,
		"cmd/lalabase/unix.go":        `package main; import "golang.org/x/sys/unix"; var _ = unix.Exec`,
		"internal/profile/x.go":       `package profile; import "os/exec"; var _ = exec.Command`,
		"internal/launch/sub/x.go":    `package sub; import "os/exec"; var _ = exec.Command`,
		"internal/launch/x_darwin.go": `package launch; import "os"; var _ = os.StartProcess`,
	} {
		if got := inspectUI(map[string]string{name: source}); len(got) == 0 {
			t.Errorf("missed %s", name)
		}
	}
	for name, source := range map[string]string{
		"cmd/lalabase/x_test.go":      `package main; import "os/exec"; var _ = exec.Command`,
		"tools/update-contract/x.go":  `package main; import "os/exec"; var _ = exec.Command`,
		"internal/launch/x_darwin.go": `package launch; import "os/exec"; var _ = exec.Command`,
	} {
		if got := inspectUI(map[string]string{name: source}); len(got) != 0 {
			t.Errorf("%s rejected: %v", name, got)
		}
	}
}

// The real launch package passes, and a probe in it turns the guard red:
// the repository scan reaches it from the interface.
func TestTheGuardReadsTheRealLaunchPackage(t *testing.T) {
	root := repoRoot(t)
	sources := repositorySources(t)
	for _, name := range []string{"internal/launch/launch.go", "internal/claudecfg/claudecfg.go"} {
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
			if got := inspectUI(files); len(got) == 0 {
				t.Errorf("%s with %q passed", name, probe)
			}
		}
	}
}
