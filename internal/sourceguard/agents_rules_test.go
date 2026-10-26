package sourceguard

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
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

// hookFile is `lalabase hook`: it runs inside Claude Code's turn.
const hookFile = "cmd/lalabase/hook.go"

// hookBuiltins are the unqualified calls the hook's file may make.
var hookBuiltins = map[string]bool{"recover": true, "len": true, "string": true}

// inspectHook: the hook's file names no output stream and no printing
// call, reads the environment through Getenv with the literal CLAUDE_PID
// alone, and imports no package of the client but internal/agents (no
// profile, no transport, no cache). Behaviour is tested in cmd/lalabase;
// this keeps a later edit from adding a path the tests do not take.
//
// mainFuncs are the functions the other files of package main declare:
// none of them may be named in the hook's file, called or not (review
// rounds 2 and 3 of #34, F4): (runWith)(...), a slice or a struct of them.
func inspectHook(source string, mainFuncs map[string]bool) []string {
	file, err := parser.ParseFile(token.NewFileSet(), hookFile, source, 0)
	if err != nil {
		return []string{err.Error()}
	}
	// Getenv is read only as a call with the literal: as a value it could
	// be called with anything (review round 1 of #34, F5).
	calledWithPID := map[*ast.SelectorExpr]bool{}
	ast.Inspect(file, func(node ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Getenv" && len(call.Args) == 1 {
				if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Value == `"CLAUDE_PID"` {
					calledWithPID[sel] = true
				}
			}
		}
		return true
	})
	var findings []string
	for _, imp := range file.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		if strings.HasPrefix(p, clientModule) && p != clientModule+"internal/agents" {
			findings = append(findings, hookFile+": imports "+p)
		}
		if p == "fmt" || p == "log" || strings.HasPrefix(p, "log/") || p == "os" {
			findings = append(findings, hookFile+": imports "+p)
		}
	}
	ast.Inspect(file, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.Ident:
			if n.Name == "print" || n.Name == "println" {
				findings = append(findings, hookFile+": builtin output")
			}
		case *ast.SelectorExpr:
			switch name := n.Sel.Name; {
			case name == "Stdout" || name == "Stderr" || strings.HasPrefix(name, "Print") || strings.HasPrefix(name, "Fprint"):
				findings = append(findings, hookFile+": output through "+name)
			case name == "Environ" || name == "LookupEnv":
				findings = append(findings, hookFile+": the environment through "+name)
			}
		}
		// No function of package main but recover: any of them may print
		// or read the profile (review round 2 of #34, F4).
		if call, ok := node.(*ast.CallExpr); ok {
			if ident, ok := call.Fun.(*ast.Ident); ok && !hookBuiltins[ident.Name] {
				findings = append(findings, hookFile+": calls "+ident.Name+" of package main")
			}
		}
		if ident, ok := node.(*ast.Ident); ok && ident != file.Name && mainFuncs[ident.Name] {
			findings = append(findings, hookFile+": names "+ident.Name+" of package main")
		}
		if sel, ok := node.(*ast.SelectorExpr); ok && sel.Sel.Name == "Getenv" && !calledWithPID[sel] {
			findings = append(findings, hookFile+": Getenv of anything but CLAUDE_PID, or as a value")
		}
		return true
	})
	return findings
}

// mainFuncsOf are the functions declared in package main's production
// files other than the hook's.
func mainFuncsOf(t *testing.T, sources map[string]string) map[string]bool {
	t.Helper()
	funcs := map[string]bool{}
	for name, source := range sources {
		if path.Dir(name) != path.Dir(hookFile) || name == hookFile || test(name) {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, source, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil {
				funcs[fn.Name.Name] = true
			}
		}
	}
	if !funcs["runWith"] || !funcs["runAuth"] {
		t.Fatalf("package main's functions not found: %v", funcs)
	}
	return funcs
}

func TestTheHookWritesNothingAndReadsOnlyCLAUDEPID(t *testing.T) {
	sources := repositorySources(t)
	source, ok := sources[hookFile]
	if !ok {
		t.Fatalf("%s is not scanned", hookFile)
	}
	mainFuncs := mainFuncsOf(t, sources)
	if got := inspectHook(source, mainFuncs); len(got) != 0 {
		t.Fatal(got)
	}
	for _, probe := range []string{
		"\nfunc probe(env Env) { env.Stdout.Write(nil) }\n",
		"\nfunc probe(env Env) { _ = env.Stderr }\n",
		"\nfunc probe(env Env) { _ = env.Getenv(\"CLAUDE_CODE_MESSAGING_TOKEN\") }\n",
		"\nfunc probe(env Env) { key := \"CLAUDE_PID\"; _ = env.Getenv(key) }\n",
		"\nfunc probe(env Env) { get := env.Getenv; _ = get(\"CLAUDE_CODE_MESSAGING_TOKEN\") }\n",
		"\nfunc probe(env Env) { _ = env.Getenv(\"CLAUDE_PID\" + \"X\") }\n",
		"\nfunc probe(env Env) { _ = runWith([]string{\"version\"}, env) }\n",
		"\nfunc probe(env Env) { _ = runAuth([]string{\"status\"}, \"\", env) }\n",
		"\nfunc probe(env Env) { _ = (runWith)([]string{\"version\"}, env) }\n",
		"\nfunc probe(env Env) { _ = []func([]string, Env) int{runWith}[0](nil, env) }\n",
		"\nfunc probe(env Env) { _ = struct{ f func([]string, Env) int }{runWith}.f(nil, env) }\n",
		"\nvar probe = launchConfig\n",
		"\nfunc probe() { print(1) }\n",
	} {
		if got := inspectHook(source+probe, mainFuncs); len(got) == 0 {
			t.Errorf("missed %q", probe)
		}
	}
	for _, imp := range []string{`"fmt"`, `"os"`, `"log"`, `"` + clientModule + `internal/profile"`, `"` + clientModule + `internal/transport"`} {
		if got := inspectHook(strings.Replace(source, "import (", "import (\n\t_ "+imp, 1), mainFuncs); len(got) == 0 {
			t.Errorf("missed import %s", imp)
		}
	}
}

// timerCalls start a clock of their own: a tick, a timer, a sleep. The
// interface and the agents' reader wake only through the gateway's Events
// (Expiry, Timer), which run under the supervisor (C9a: the agents are
// read every 500 ms that way, and the spinner moves with each read).
var timerCalls = map[string][]string{
	"time":                    {"Tick", "NewTicker", "NewTimer", "After", "AfterFunc", "Sleep"},
	"charm.land/bubbletea/v2": {"Tick", "Every"},
}

// timerFree are the directories that may use none of timerCalls; the
// hook's lock retries in internal/agents may sleep, nothing else there.
func timerFree(dir string) (refused []string, ok bool) {
	switch {
	case surface(dir):
		return nil, true
	case dir == "internal/agents":
		return []string{"Sleep"}, true
	}
	return nil, false
}

func inspectTimers(name string, file *ast.File) []string {
	allowed, ok := timerFree(path.Dir(name))
	if !ok || test(name) {
		return nil
	}
	aliases := map[string]string{}
	for _, imp := range file.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		alias := path.Base(p)
		if p == "charm.land/bubbletea/v2" {
			alias = "tea"
		}
		if imp.Name != nil {
			alias = imp.Name.Name
		}
		aliases[alias] = p
	}
	var findings []string
	ast.Inspect(file, func(node ast.Node) bool {
		sel, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		if slices.Contains(timerCalls[aliases[ident.Name]], sel.Sel.Name) && !slices.Contains(allowed, sel.Sel.Name) {
			findings = append(findings, name+": a clock of its own: "+aliases[ident.Name]+"."+sel.Sel.Name)
		}
		return true
	})
	return findings
}

func TestTheInterfaceHasNoClockOfItsOwn(t *testing.T) {
	inspect := func(sources map[string]string) []string {
		var findings []string
		for name, source := range sources {
			file, err := parser.ParseFile(token.NewFileSet(), name, source, 0)
			if err != nil {
				findings = append(findings, err.Error())
				continue
			}
			findings = append(findings, inspectTimers(name, file)...)
		}
		return findings
	}
	if got := inspect(repositorySources(t)); len(got) != 0 {
		t.Fatal(got)
	}
	for name, source := range map[string]string{
		"internal/ui/x.go":      `package ui; import tea "charm.land/bubbletea/v2"; var _ = tea.Tick`,
		"internal/ui/y.go":      `package ui; import b "charm.land/bubbletea/v2"; var _ = b.Every`,
		"internal/ui/z.go":      `package ui; import "time"; var _ = time.NewTicker`,
		"internal/ui/view/x.go": `package view; import "time"; var _ = time.After`,
		"internal/ui/s.go":      `package ui; import "time"; func f() { time.Sleep(1) }`,
		"internal/agents/x.go":  `package agents; import "time"; var _ = time.Tick`,
		"internal/agents/y.go":  `package agents; import t "time"; var _ = t.NewTimer`,
		"internal/present/x.go": `package present; import "time"; var _ = time.AfterFunc`,
	} {
		if got := inspect(map[string]string{name: source}); len(got) == 0 {
			t.Errorf("missed %s", name)
		}
	}
	if got := inspect(map[string]string{"internal/agents/x.go": `package agents; import "time"; func f() { time.Sleep(1) }`, "internal/ui/x.go": `package ui; import "time"; var _ = time.Now`}); len(got) != 0 {
		t.Errorf("refused %v", got)
	}
}
