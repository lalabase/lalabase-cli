package sourceguard

import (
	"crypto/tls"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net"
	"net/http"
	"net/http/httptrace"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
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

func test(name string) bool { return strings.HasSuffix(name, "_test.go") }

func view(dir string) bool {
	return strings.HasPrefix(dir, "internal/ui/") || dir == "internal/present" || dir == "internal/uitext" || dir == "internal/terminal"
}

// outputRoot: no terminal output from here or anything it reaches, the
// interface and the paths its work runs on.
func outputRoot(dir string) bool {
	return surface(dir) || dir == "internal/gateway" || dir == "internal/worker" || dir == "internal/queue"
}

func infrastructure(p string) bool {
	for _, name := range []string{"internal/api", "internal/transport", "internal/cache", "internal/queue", "internal/gateway",
		"internal/diskcache", "internal/privatefs", "internal/safefile"} {
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

// reachableImports is everything production code may import where the
// interface and its work reach, besides the client's own packages (checked
// themselves). It is an allowlist: a package that writes to the terminal or
// starts work of its own (os, log, syscall, runtime, runtime/debug, bufio,
// ...) is refused without being named. nil allows every name of a package;
// a list allows only these, for packages that can write to a writer they are
// handed (fmt.Fprintln, io.Copy, json.NewEncoder). A new import here is a
// decision: does it write, does it start a goroutine?
//
// Not seen, by design (a syntax guard; for the person reading the diff): a
// method on a writer that was handed in (w.Write, buf.WriteTo(w)), and
// packages the interface does not reach (cmd/lalabase, internal/profile).
var reachableImports = map[string][]string{
	"bytes": nil, "container/list": nil, "context": nil, "crypto/tls": nil, "crypto/x509": nil,
	"errors": nil, "net": nil, "net/http": nil, "net/url": nil, "path": nil,
	"regexp": nil, "slices": nil, "strconv": nil, "strings": nil, "sync": nil, "sync/atomic": nil,
	"time": nil, "unicode": nil, "unicode/utf8": nil,
	"encoding/json": {"Decoder", "Marshal", "NewDecoder", "RawMessage", "Unmarshal", "Valid"},
	"fmt":           {"Errorf", "Sprint", "Sprintf", "Sprintln"},
	"io":            {"EOF", "LimitReader", "ReadAll", "ReadCloser", "Reader", "Writer"},
	// Its hooks run on net/http's goroutines: see foreignCallbackTypes.
	"net/http/httptrace": {"ClientTrace", "GotConnInfo", "WithClientTrace", "WroteRequestInfo"},
	// Their writing names are refused below (Println, Fprint, Writer,
	// WithoutCatchPanics).
	"charm.land/bubbletea/v2": nil, "charm.land/bubbles/v2/help": nil, "charm.land/bubbles/v2/key": nil,
	"charm.land/lipgloss/v2": nil, "github.com/charmbracelet/x/ansi": nil,
}

// fileSystemImports are what the three packages that keep the client's
// files for it may import beyond reachableImports (C1e): the disk cache,
// the threat model of private files, and the reader of files the client
// does not control. Each import is named with the names it may use, so a
// package that can also write to the terminal, end the process or read the
// environment (os) gives only the file calls; nil allows every name of a
// pure package. Nothing here allows an output stream, a goroutine or the
// environment: those rules hold in these packages too, and no other
// reachable package gets any of this. A new name here is a decision, not a
// fix for a red guard.
var fileSystemImports = map[string]map[string][]string{
	"internal/safefile": {
		"os":      {"File", "Open", "OpenFile", "O_RDONLY", "Lstat", "ModeSymlink", "PathError", "ErrInvalid"},
		"io/fs":   nil,
		"syscall": {"O_NONBLOCK", "O_NOFOLLOW"},
	},
	"internal/privatefs": {
		"os":    {"OpenFile", "O_RDWR", "O_CREATE", "Getuid", "Lstat", "Readlink"},
		"io/fs": nil,
		// The path functions alone: Glob, Walk, WalkDir and EvalSymlinks
		// read the file system and are not among them.
		"path/filepath":   {"Abs", "Dir", "IsAbs", "Join"},
		"syscall":         {"O_NONBLOCK", "O_NOFOLLOW", "Flock", "LOCK_EX", "LOCK_NB", "LOCK_UN", "Stat_t", "EWOULDBLOCK", "EAGAIN"},
		"encoding/binary": {"NativeEndian"},
		// The access list reader of macOS: getattrlist has no libSystem
		// wrapper, so it is the one raw system call of the client. Syscall6
		// is allowed with SYS_GETATTRLIST as its first argument alone
		// (inspectRawSyscalls).
		"unsafe":                {"Pointer"},
		"golang.org/x/sys/unix": {"BytePtrFromString", "Syscall6", "SYS_GETATTRLIST"},
	},
	"internal/diskcache": {
		"os":            {"File", "MkdirAll", "Open", "Remove", "CreateTemp", "Chtimes", "Rename"},
		"io/fs":         nil,
		"path/filepath": {"Dir", "IsAbs", "Join"},
		"crypto/sha256": nil,
		"encoding/hex":  nil,
	},
}

// launchImports are what the launch of Claude Code beside a ticket (C7)
// may import beyond reachableImports, named one by one like
// fileSystemImports. internal/launch is the client's one package that
// starts a child process: osascript, which asks iTerm for a pane, and
// claude in this terminal through Bubble Tea's exec. That is a decision
// about child processes, taken here, not a fix for a red guard: os/exec
// copies a child's input and output on goroutines of its own, which end
// with the child, and the launch runs under the supervisor. It may stat a
// folder, and nothing else of os: no environment (the command hands it
// the values it needs), no output stream. internal/claudecfg reads Claude
// Code's configuration for it, through internal/safefile, and may look at
// paths the way Claude Code does (a .git entry, a link resolved). Nothing
// else gets any of this, and inspectChildProcesses refuses a child process
// everywhere else in the client.
var launchImports = map[string]map[string][]string{
	"internal/launch": {
		"os/exec":       {"Cmd", "Command", "CommandContext", "LookPath"},
		"os":            {"FileInfo", "Stat"},
		"path/filepath": {"Clean", "IsAbs", "Join"},
	},
	"internal/claudecfg": {
		"os":            {"Lstat"},
		"io/fs":         {"ErrNotExist"},
		"path/filepath": {"Base", "Clean", "Dir", "EvalSymlinks", "IsAbs", "Join"},
		"sort":          {"Strings"},
	},
}

// childProcessDir is the one directory whose production code may start a
// child process.
const childProcessDir = "internal/launch"

// childProcessCalls start a program without os/exec.
var childProcessCalls = map[string][]string{
	"os":                    {"StartProcess"},
	"syscall":               {"Exec", "ForkExec", "StartProcess"},
	"golang.org/x/sys/unix": {"Exec"},
}

// inspectChildProcesses: in the client's production code (cmd/ and
// internal/; the developer tools under tools/ are no part of it) os/exec is
// imported in childProcessDir alone, and the process calls of os, syscall
// and x/sys/unix nowhere.
func inspectChildProcesses(name string, file *ast.File, aliases map[string]string) []string {
	if test(name) || (!strings.HasPrefix(name, "cmd/") && !strings.HasPrefix(name, "internal/")) {
		return nil
	}
	var findings []string
	for _, imp := range file.Imports {
		if p, _ := strconv.Unquote(imp.Path.Value); p == "os/exec" && path.Dir(name) != childProcessDir {
			findings = append(findings, name+": os/exec outside "+childProcessDir)
		}
	}
	ast.Inspect(file, func(node ast.Node) bool {
		if sel, ok := node.(*ast.SelectorExpr); ok {
			if ident, ok := sel.X.(*ast.Ident); ok && slices.Contains(childProcessCalls[aliases[ident.Name]], sel.Sel.Name) {
				findings = append(findings, name+": "+aliases[ident.Name]+"."+sel.Sel.Name+" starts a child process")
			}
		}
		return true
	})
	return findings
}

// rawSyscall is the one raw system call the client makes, and the number
// it may carry: anything else by number would be any call at all.
const (
	rawSyscall       = "Syscall6"
	rawSyscallNumber = "SYS_GETATTRLIST"
)

// inspectRawSyscalls: a call of x/sys/unix's Syscall6 names rawSyscallNumber
// of the same package as its first argument; a number, a variable or
// another constant is refused.
func inspectRawSyscalls(name string, file *ast.File, aliases map[string]string) []string {
	var findings []string
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != rawSyscall {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || aliases[pkg.Name] != "golang.org/x/sys/unix" {
			return true
		}
		if len(call.Args) == 0 {
			findings = append(findings, name+": "+rawSyscall+" without a system call")
			return true
		}
		named := false
		if first, ok := call.Args[0].(*ast.SelectorExpr); ok {
			if firstPkg, isIdent := first.X.(*ast.Ident); isIdent {
				named = aliases[firstPkg.Name] == "golang.org/x/sys/unix" && first.Sel.Name == rawSyscallNumber
			}
		}
		if !named {
			findings = append(findings, name+": "+rawSyscall+" with a system call other than "+rawSyscallNumber)
		}
		return true
	})
	return findings
}

// fileSystemNames: the names dir may use of p beyond reachableImports; ok
// is false when dir has no such allowance for p.
func fileSystemNames(dir, p string) (names []string, ok bool) {
	names, ok = fileSystemImports[dir][p]
	return names, ok
}

// launchNames: the names dir may use of p under launchImports.
func launchNames(dir, p string) (names []string, ok bool) {
	names, ok = launchImports[dir][p]
	return names, ok
}

// foreignCallbackTypes are standard library types with fields whose value
// the library uses on a goroutine of its own (a trace hook, a dial, a
// handshake): a function, an interface (its methods are called there) or a
// map of functions. The fields come from the types themselves.
var foreignCallbackTypes = map[string]reflect.Type{
	"net/http/httptrace.ClientTrace": reflect.TypeFor[httptrace.ClientTrace](),
	"net/http.Transport":             reflect.TypeFor[http.Transport](),
	"crypto/tls.Config":              reflect.TypeFor[tls.Config](),
	"net.Dialer":                     reflect.TypeFor[net.Dialer](),
	"net.Resolver":                   reflect.TypeFor[net.Resolver](),
}

// callbackFields are the exported fields of t that hold code: functions,
// interfaces and maps of functions.
func callbackFields(t reflect.Type) []string {
	var names []string
	for i := range t.NumField() {
		f := t.Field(i)
		kind := f.Type.Kind()
		if f.IsExported() && (kind == reflect.Func || kind == reflect.Interface || (kind == reflect.Map && f.Type.Elem().Kind() == reflect.Func)) {
			names = append(names, f.Name)
		}
	}
	return names
}

// onWireCall: a call of one of transport's wrappers (onWire0, onWire1, ...).
func onWireCall(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	fun := call.Fun
	if index, ok := fun.(*ast.IndexExpr); ok {
		fun = index.X
	}
	if index, ok := fun.(*ast.IndexListExpr); ok {
		fun = index.X
	}
	ident, ok := fun.(*ast.Ident)
	return ok && strings.HasPrefix(ident.Name, "onWire")
}

// inspectForeignCallbacks: in production code anywhere, a callback field of
// foreignCallbackTypes is set only in its type's literal and only through
// transport's onWire wrappers, which raise a panic again on the request's
// goroutine, under the supervisor. An assignment to such a field is caught
// by its name, whatever the variable's type (a syntax guard cannot know it).
// onWire is transport's own, so elsewhere such a field cannot be set at all.
// http.Client.CheckRedirect runs on the caller's goroutine and is allowed.
func inspectForeignCallbacks(name string, file *ast.File, aliases map[string]string) []string {
	var findings []string
	typeOf := func(expr ast.Expr) string {
		if sel, ok := expr.(*ast.SelectorExpr); ok {
			if ident, ok := sel.X.(*ast.Ident); ok && aliases[ident.Name] != "" {
				return aliases[ident.Name] + "." + sel.Sel.Name
			}
		}
		return ""
	}
	callbackField := map[string]bool{}
	for _, typ := range foreignCallbackTypes {
		for _, field := range callbackFields(typ) {
			callbackField[field] = true
		}
	}
	ast.Inspect(file, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.CompositeLit:
			typ, ok := foreignCallbackTypes[typeOf(n.Type)]
			if !ok {
				return true
			}
			fields := callbackFields(typ)
			for _, elt := range n.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				key, isIdent := kv.Key.(*ast.Ident)
				if !ok || !isIdent {
					findings = append(findings, name+": "+typeOf(n.Type)+" without field names")
					continue
				}
				if slices.Contains(fields, key.Name) && !onWireCall(kv.Value) {
					findings = append(findings, name+": "+typeOf(n.Type)+"."+key.Name+" runs on a foreign goroutine; set it through onWire")
				}
			}
		case *ast.AssignStmt:
			for _, lhs := range n.Lhs {
				if sel, ok := lhs.(*ast.SelectorExpr); ok && callbackField[sel.Sel.Name] {
					findings = append(findings, name+": "+sel.Sel.Name+" set outside its literal; set it there, through onWire")
				}
			}
		}
		return true
	})
	return findings
}

// inspectReachableImports applies reachableImports to a production file,
// and fileSystemImports and launchImports to the files of the packages
// named there.
func inspectReachableImports(name string, file *ast.File, aliases map[string]string) []string {
	var findings []string
	dir := path.Dir(name)
	allowed := func(p string) (names []string, ok bool) {
		if names, ok = reachableImports[p]; ok {
			return names, true
		}
		if names, ok = launchNames(dir, p); ok {
			return names, true
		}
		return fileSystemNames(dir, p)
	}
	for _, imp := range file.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		if _, ok := allowed(p); !ok && !strings.HasPrefix(p, clientModule) {
			findings = append(findings, name+": import "+p+" is not allowed where the interface reaches")
		}
	}
	ast.Inspect(file, func(node ast.Node) bool {
		sel, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		if names, _ := allowed(aliases[ident.Name]); names != nil && !slices.Contains(names, sel.Sel.Name) {
			findings = append(findings, name+": "+aliases[ident.Name]+"."+sel.Sel.Name+" is not allowed where the interface reaches")
		}
		return true
	})
	return findings
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
			// Nowhere: a dot import turns every rule below that looks for a
			// package's name into a bare name it does not see.
			if imp.Name != nil && imp.Name.Name == "." {
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
			// The one raw system call (the access list reader) is allowed
			// by name in fileSystemImports; everywhere else x/sys/unix is
			// terminal access.
			_, fileSystem := fileSystemNames(path.Dir(name), p)
			if direct || (reachable[path.Dir(name)] && ((terminalAccess(p) && (!fileSystem || test(name))) || logging)) {
				findings = append(findings, name+": forbidden direct import "+p)
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
			findings = append(findings, inspectForeignCallbacks(name, file, aliases)...)
			findings = append(findings, inspectRawSyscalls(name, file, aliases)...)
			findings = append(findings, inspectChildProcesses(name, file, aliases)...)
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
		if !test {
			findings = append(findings, inspectReachableImports(name, file, aliases)...)
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
// a go statement, a method value Go (WaitGroup.Go, errgroup), AfterFunc and
// a finalizer or cleanup.
func inspectWorkers(name string, file *ast.File, aliases map[string]string) []string {
	var findings []string
	for _, decl := range file.Decls {
		allowed := name == workerFile && isWorkerStart(decl)
		ast.Inspect(decl, func(node ast.Node) bool {
			switch n := node.(type) {
			case *ast.GoStmt:
				if !allowed {
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
				// A finalizer or cleanup runs on the runtime's own goroutine.
				if ident, ok := n.X.(*ast.Ident); ok && aliases[ident.Name] == "runtime" && (n.Sel.Name == "SetFinalizer" || n.Sel.Name == "AddCleanup") {
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

// repositorySources reads every tracked Go file: what the repository scan
// inspects.
func repositorySources(t *testing.T) map[string]string {
	t.Helper()
	root := repoRoot(t)
	sources := map[string]string{}
	for _, name := range trackedFiles(t, root) {
		if !strings.HasSuffix(name, ".go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		sources[filepath.ToSlash(name)] = string(data)
	}
	return sources
}

func TestRepositoryUIBoundaries(t *testing.T) {
	sources := repositorySources(t)
	protected := 0
	for name := range sources {
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
