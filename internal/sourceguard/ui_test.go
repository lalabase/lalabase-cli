package sourceguard

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const clientModule = "github.com/gboyke/lalabase-cli/"

// rootController is the root model's package: it may use the infrastructure
// (queue, gateway, cache), its views may not.
const rootController = "internal/ui"

// workerFile and workerStart are the one place production code starts a
// goroutine: the method Start of *Supervisor in this file.
const (
	workerFile  = "internal/worker/worker.go"
	workerStart = "Start"
)

func surface(dir string) bool { return dir == rootController || view(dir) }

func view(dir string) bool {
	return strings.HasPrefix(dir, "internal/ui/") || dir == "internal/present" || dir == "internal/uitext" || dir == "internal/terminal"
}

// outputRoot: no terminal output from here or anything it reaches, the
// interface and the paths its work runs on.
func outputRoot(dir string) bool {
	return surface(dir) || dir == "internal/gateway" || dir == "internal/worker" || dir == "internal/queue"
}

func infrastructure(p string) bool {
	for _, name := range []string{"internal/api", "internal/transport", "internal/cache", "internal/queue", "internal/gateway"} {
		if p == clientModule+name || strings.HasPrefix(p, clientModule+name+"/") {
			return true
		}
	}
	return false
}

func forbiddenImport(p string) bool { return infrastructure(p) || systemImport(p) }

func systemImport(p string) bool {
	return p == "net" || strings.HasPrefix(p, "net/") || p == "os" || strings.HasPrefix(p, "os/") || p == "syscall" || p == "unsafe" || p == "log" || strings.HasPrefix(p, "log/") || terminalAccess(p)
}

func terminalAccess(p string) bool {
	return p == "golang.org/x/sys/unix" || p == "golang.org/x/term" || p == "github.com/charmbracelet/x/term"
}

func inspectUI(sources map[string]string) []string {
	var findings []string
	graph := map[string][]string{}
	files := map[string]*ast.File{}
	for name, source := range sources {
		file, err := parser.ParseFile(token.NewFileSet(), name, source, 0)
		if err != nil {
			findings = append(findings, err.Error())
			continue
		}
		files[name] = file
		for _, imp := range file.Imports {
			p, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				findings = append(findings, err.Error())
				continue
			}
			if !strings.HasSuffix(name, "_test.go") {
				graph[path.Dir(name)] = append(graph[path.Dir(name)], p)
			}
			if surface(path.Dir(name)) && imp.Name != nil && imp.Name.Name == "." {
				findings = append(findings, name+": dot import")
			}
		}
	}
	// Follow runtime edges only: a dependency's tests do not enter the UI build.
	// Still parse all files, and inspect output in reachable local test helpers.
	reachable := map[string]bool{}
	var mark func(string)
	mark = func(dir string) {
		if reachable[dir] {
			return
		}
		reachable[dir] = true
		for _, p := range graph[dir] {
			if local, ok := strings.CutPrefix(p, clientModule); ok {
				mark(local)
			}
		}
	}
	for name := range files {
		if outputRoot(path.Dir(name)) {
			mark(path.Dir(name))
		}
	}
	for name, file := range files {
		aliases := map[string]string{}
		for _, imp := range file.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			direct := (view(path.Dir(name)) && forbiddenImport(p)) || (path.Dir(name) == rootController && systemImport(p))
			// The log packages write to stderr: barred wherever the
			// interface or its work reaches, not only in the views.
			logging := p == "log" || strings.HasPrefix(p, "log/")
			if direct || (reachable[path.Dir(name)] && (terminalAccess(p) || logging)) {
				findings = append(findings, name+": forbidden direct import "+p)
			}
			if imp.Name != nil && imp.Name.Name == "." && (reachable[path.Dir(name)] || p == clientModule+"internal/ui") {
				findings = append(findings, name+": dot import")
			}
			alias := path.Base(p)
			if p == "charm.land/bubbletea/v2" {
				alias = "tea"
			}
			if p == "charm.land/lipgloss/v2" {
				alias = "lipgloss"
			}
			if imp.Name != nil {
				alias = imp.Name.Name
			}
			aliases[alias] = p
		}
		test := strings.HasSuffix(name, "_test.go")
		if !test {
			findings = append(findings, inspectUIEntry(name, file, aliases)...)
		}
		// Goroutines: production code anywhere starts one only in the
		// worker's Start; a test harness may, except in the interface.
		if !test || surface(path.Dir(name)) {
			findings = append(findings, inspectWorkers(name, file, aliases)...)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			if sel, ok := node.(*ast.SelectorExpr); ok && sel.Sel.Name == "WithoutCatchPanics" {
				if ident, ok := sel.X.(*ast.Ident); ok && aliases[ident.Name] == "charm.land/bubbletea/v2" {
					findings = append(findings, name+": panic recovery disabled")
				}
			}
			return true
		})
		if !reachable[path.Dir(name)] {
			continue
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch n := node.(type) {
			case *ast.Ident:
				if n.Name == "print" || n.Name == "println" {
					findings = append(findings, name+": builtin output")
				}
			case *ast.SelectorExpr:
				if ident, ok := n.X.(*ast.Ident); ok {
					p := aliases[ident.Name]
					if p == "charm.land/lipgloss/v2" && (n.Sel.Name == "Writer" || strings.HasPrefix(n.Sel.Name, "Fprint")) {
						findings = append(findings, name+": default output")
					}
					if p == "os" && (n.Sel.Name == "Stdout" || n.Sel.Name == "Stderr") {
						findings = append(findings, name+": terminal output stream")
					}
					if (p == "fmt" || p == "charm.land/bubbletea/v2" || p == "charm.land/lipgloss/v2") && strings.HasPrefix(n.Sel.Name, "Print") {
						findings = append(findings, name+": default output")
					}
				}
			}
			return true
		})
		if !view(path.Dir(name)) {
			continue
		}
		seen := map[string]bool{}
		var visit func(string)
		visit = func(dir string) {
			if seen[dir] {
				return
			}
			seen[dir] = true
			for _, p := range graph[dir] {
				if forbiddenImport(p) {
					findings = append(findings, fmt.Sprintf("%s: dependency %s", name, p))
				}
				if local, ok := strings.CutPrefix(p, clientModule); ok {
					visit(local)
				}
			}
		}
		visit(path.Dir(name))
	}
	return findings
}

// inspectWorkers reports every goroutine started outside the worker's Start:
// a go statement, a method value Go (WaitGroup.Go, errgroup) and AfterFunc.
func inspectWorkers(name string, file *ast.File, aliases map[string]string) []string {
	var findings []string
	// A dot import makes AfterFunc a bare name.
	bareAfterFunc := false
	for _, imp := range file.Imports {
		if imp.Name != nil && imp.Name.Name == "." && (imp.Path.Value == `"time"` || imp.Path.Value == `"context"`) {
			bareAfterFunc = true
		}
	}
	for _, decl := range file.Decls {
		allowed := name == workerFile && isWorkerStart(decl)
		ast.Inspect(decl, func(node ast.Node) bool {
			switch n := node.(type) {
			case *ast.GoStmt:
				if !allowed {
					findings = append(findings, name+": application goroutine")
				}
			case *ast.Ident:
				if bareAfterFunc && n.Name == "AfterFunc" {
					findings = append(findings, name+": application goroutine")
				}
			case *ast.SelectorExpr:
				// Forbid taking the method value too, so a later alias cannot
				// hide WaitGroup.Go. TryGo (errgroup) and DoChan (singleflight)
				// start one as well.
				if n.Sel.Name == "Go" || n.Sel.Name == "TryGo" || n.Sel.Name == "DoChan" {
					findings = append(findings, name+": application goroutine")
				}
				if ident, ok := n.X.(*ast.Ident); ok && n.Sel.Name == "AfterFunc" && (aliases[ident.Name] == "time" || aliases[ident.Name] == "context") {
					findings = append(findings, name+": application goroutine")
				}
			}
			return true
		})
	}
	return findings
}

func isWorkerStart(decl ast.Decl) bool {
	fn, ok := decl.(*ast.FuncDecl)
	if !ok || fn.Name.Name != workerStart || fn.Recv == nil || len(fn.Recv.List) != 1 {
		return false
	}
	star, ok := fn.Recv.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	ident, ok := star.X.(*ast.Ident)
	return ok && ident.Name == "Supervisor"
}

func TestRepositoryUIBoundaries(t *testing.T) {
	root := repoRoot(t)
	sources := map[string]string{}
	protected := 0
	for _, name := range trackedFiles(t, root) {
		if !strings.HasSuffix(name, ".go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		sources[filepath.ToSlash(name)] = string(data)
		if surface(path.Dir(name)) && !strings.HasSuffix(name, "_test.go") {
			protected++
		}
	}
	if protected < 5 {
		t.Fatalf("only %d protected source files inspected", protected)
	}
	for _, finding := range inspectUI(sources) {
		t.Error(finding)
	}
}

func TestUIViewDependencies(t *testing.T) {
	for _, path := range []string{"internal/api", "internal/transport", "internal/cache", "internal/queue"} {
		for _, alias := range []string{"", "wire ", ". "} {
			files := map[string]string{"internal/ui/view/pane_darwin.go": "package view\nimport " + alias + "\"" + clientModule + path + "\""}
			if got := inspectUI(files); len(got) == 0 {
				t.Fatalf("missed %s with alias %q", path, alias)
			}
		}
		files := map[string]string{
			"internal/ui/view/pane.go":  "package view\nimport \"" + clientModule + "internal/bridge\"",
			"internal/bridge/bridge.go": "package bridge\nimport api \"" + clientModule + path + "\"",
		}
		if got := inspectUI(files); len(got) == 0 || !strings.Contains(strings.Join(got, " "), path) {
			t.Fatalf("missed local edge to %s: %v", path, got)
		}
	}
	if got := inspectUI(map[string]string{"internal/ui/view/pane.go": "package view\nimport \"" + clientModule + "internal/present\"", "internal/present/text.go": "package present\nimport \"" + clientModule + "internal/clean\""}); len(got) != 0 {
		t.Fatal(got)
	}
	if got := inspectUI(map[string]string{"internal/ui/view/broken.go": "package"}); len(got) == 0 {
		t.Fatal("malformed source was skipped")
	}
}

func TestUIHasNoDirectOutputOrUnguardedWorkers(t *testing.T) {
	for _, name := range []string{"helper.go", "helper_test.go", "helper_linux.go"} {
		files := map[string]string{"internal/ui/view/pane.go": "package view\nimport \"" + clientModule + "internal/bridge\"", "internal/bridge/" + name: `package bridge; import f "fmt"; var output=f.Printf`}
		if got := inspectUI(files); len(got) == 0 {
			t.Errorf("missed output through local helper %s", name)
		}
	}
	for _, source := range []string{
		`import "os"; var stream = os.Stdout`,
		`import sys "os"; var stream = sys.Stderr`,
		`import f "fmt"; var output = f.Printf`,
		`import "log"; var output = log.Printf`,
		`import "log/slog"; var output = slog.Info`,
		`import "unsafe"; var _ unsafe.Pointer`,
		`import "syscall"; var output = syscall.Write`,
		`import tea "charm.land/bubbletea/v2"; var output = tea.Println`,
		`import lg "charm.land/lipgloss/v2"; var output = lg.Println`,
		`import tea "charm.land/bubbletea/v2"; var option = tea.WithoutCatchPanics`,
		`func bad(){ println("oops") }`,
		`func bad(){ print("oops") }`,
		`func bad(){ go func(){}() }`,
	} {
		if got := inspectUI(map[string]string{"internal/ui/bad.go": "package ui\n" + source}); len(got) == 0 {
			t.Errorf("missed %s", source)
		}
	}
	if got := inspectUI(map[string]string{"internal/ui/good.go": `package ui; import f "fmt"; var _ = f.Sprintf("%d", 1)`}); len(got) != 0 {
		t.Fatal(got)
	}
}

func TestUISurfaceProtectionIsScoped(t *testing.T) {
	for _, dir := range []string{"internal/ui", "internal/ui/view", "internal/ui/view/detail", "internal/present", "internal/uitext", "internal/terminal"} {
		for _, file := range []string{"probe.go", "probe_test.go", "probe_darwin.go", "probe_linux.go"} {
			name := dir + "/" + file
			for _, code := range []string{
				`func bad(){ go func(){}() }`,
				`import f "fmt"; var output = f.Printf`,
				`import t "charm.land/bubbletea/v2"; var option = t.WithoutCatchPanics`,
			} {
				if got := inspectUI(map[string]string{name: "package probe; " + code}); len(got) == 0 {
					t.Errorf("unprotected %s: %s", name, code)
				}
			}
			if got := inspectUI(map[string]string{name: `package probe; import f "fmt"; var value = f.Sprintf("%s", "safe")`}); len(got) != 0 {
				t.Errorf("safe formatting in %s was rejected: %v", name, got)
			}
		}
	}
	if got := inspectUI(map[string]string{
		"internal/ui/view/pane.go":      `package view`,
		"internal/queue/worker_test.go": `package queue; func harness(){ go func(){}() }`,
		"cmd/lalabase/main.go":          `package main; import sys "os"; var output = sys.Stderr`,
	}); len(got) != 0 {
		t.Fatalf("guard crossed into unrelated CLI output or a test harness: %v", got)
	}
}

func TestUIReachableHelpersCannotAliasTerminalStreams(t *testing.T) {
	for _, name := range []string{"helper.go", "helper_test.go", "helper_darwin.go", "helper_linux.go"} {
		for _, alias := range []string{"os", "sys"} {
			importName := ""
			if alias != "os" {
				importName = alias + " "
			}
			for _, stream := range []string{"Stdout", "Stderr"} {
				files := map[string]string{
					"internal/ui/view/pane.go":  "package view; import \"" + clientModule + "internal/bridge\"",
					"internal/bridge/bridge.go": "package bridge; import \"" + clientModule + "internal/helper\"",
					"internal/helper/" + name:   "package helper; import " + importName + "\"os\"; var output = " + alias + "." + stream,
				}
				if got := inspectUI(files); len(got) == 0 {
					t.Errorf("missed terminal stream alias %s.%s through %s", alias, stream, name)
				}
			}
		}
	}
	// Reading fixture files in a dependency's tests does not write to the terminal.
	if got := inspectUI(map[string]string{
		"internal/ui/view/pane.go":     "package view; import \"" + clientModule + "internal/helper\"",
		"internal/helper/text.go":      `package helper`,
		"internal/helper/text_test.go": `package helper; import sys "os"; var read = sys.ReadFile`,
		"internal/unrelated/worker.go": `package unrelated; import sys "os"; var output = sys.Stdout`,
	}); len(got) != 0 {
		t.Fatalf("safe fixture access or unrelated package rejected: %v", got)
	}
}
