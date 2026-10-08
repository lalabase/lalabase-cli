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

func surface(dir string) bool {
	return dir == "internal/ui" || strings.HasPrefix(dir, "internal/ui/") || dir == "internal/present" || dir == "internal/uitext" || dir == "internal/terminal"
}

func forbiddenImport(p string) bool {
	for _, name := range []string{"internal/api", "internal/transport", "internal/cache", "internal/queue"} {
		if p == clientModule+name || strings.HasPrefix(p, clientModule+name+"/") {
			return true
		}
	}
	return p == "net" || strings.HasPrefix(p, "net/") || p == "os" || strings.HasPrefix(p, "os/") || p == "syscall" || p == "unsafe" || p == "log" || strings.HasPrefix(p, "log/")
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
		if surface(path.Dir(name)) {
			mark(path.Dir(name))
		}
	}
	for name, file := range files {
		if !reachable[path.Dir(name)] {
			continue
		}
		aliases := map[string]string{}
		for _, imp := range file.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if surface(path.Dir(name)) && forbiddenImport(p) {
				findings = append(findings, name+": forbidden direct import "+p)
			}
			if imp.Name != nil && imp.Name.Name == "." {
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
		ast.Inspect(file, func(node ast.Node) bool {
			switch n := node.(type) {
			case *ast.GoStmt:
				findings = append(findings, name+": application goroutine")
			case *ast.Ident:
				if n.Name == "print" || n.Name == "println" {
					findings = append(findings, name+": builtin output")
				}
			case *ast.SelectorExpr:
				if ident, ok := n.X.(*ast.Ident); ok {
					p := aliases[ident.Name]
					if (p == "fmt" || p == "charm.land/bubbletea/v2" || p == "charm.land/lipgloss/v2") && strings.HasPrefix(n.Sel.Name, "Print") {
						findings = append(findings, name+": default output")
					}
					if p == "charm.land/bubbletea/v2" && n.Sel.Name == "WithoutCatchPanics" {
						findings = append(findings, name+": panic recovery disabled")
					}
				}
			}
			return true
		})
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
