package sourceguard

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// dialogFile is the dialog's own file in the interface, the one place that
// opens one.
const dialogFile = "internal/ui/dialog.go"

// dialogOpeners name the ways to a dialog. They are called nowhere in the
// client but among themselves and by dialogCallers.
var dialogOpeners = []string{"WithDialog", "openDialog", "openTicketDialog"}

// dialogCallers are the production callers of an opener, by file: since K6,
// b on a ticket, in book.go, through the one opener that takes the target
// from the focused pane. A new one is a decision to take here, not a fix for
// a red guard: a dialog is where a person writes to a server, and the file
// that opens one owns what its send does.
var dialogCallers = map[string]string{"internal/ui/book.go": "openTicketDialog"}

// inspectDialogOpeners reports every use of a dialog opener in a production
// file of the client (cmd/, internal/; the fixture tools under tools/ are no
// part of it), by its name, a method or a package's. Their declarations are
// no use, and in the dialog's file the two others lead to openDialog.
func inspectDialogOpeners(name string, file *ast.File) []string {
	if test(name) || (!strings.HasPrefix(name, "cmd/") && !strings.HasPrefix(name, "internal/")) {
		return nil
	}
	var findings []string
	for _, decl := range file.Decls {
		fn, isFunc := decl.(*ast.FuncDecl)
		leads := isFunc && name == dialogFile && slices.Contains(dialogOpeners, fn.Name.Name)
		ast.Inspect(decl, func(node ast.Node) bool {
			id, ok := node.(*ast.Ident)
			if !ok || !slices.Contains(dialogOpeners, id.Name) || (isFunc && id == fn.Name) || (leads && id.Name == "openDialog") || dialogCallers[name] == id.Name {
				return true
			}
			findings = append(findings, name+": a dialog opened through "+id.Name)
			return true
		})
	}
	return append(findings, inspectDialogState(name, file)...)
}

// inspectDialogState reports what opens a dialog without an opener: outside
// the dialog's file nothing makes the state of one, sets the model's field
// but to nil (a 401 ends it), or sets a dialog's mode. A syntax guard: a
// field set in a literal of the model is not seen.
func inspectDialogState(name string, file *ast.File) []string {
	if name == dialogFile {
		return nil
	}
	var findings []string
	isState := func(expr ast.Expr) bool {
		id, ok := expr.(*ast.Ident)
		return ok && id.Name == "dialog"
	}
	ast.Inspect(file, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.CompositeLit:
			if isState(n.Type) {
				findings = append(findings, name+": the state of a dialog made outside "+dialogFile)
			}
		case *ast.CallExpr:
			if id, ok := n.Fun.(*ast.Ident); ok && id.Name == "new" && len(n.Args) == 1 && isState(n.Args[0]) {
				findings = append(findings, name+": the state of a dialog made outside "+dialogFile)
			}
		case *ast.AssignStmt:
			for i, lhs := range n.Lhs {
				sel, ok := lhs.(*ast.SelectorExpr)
				if !ok {
					continue
				}
				// Several values from one call name none of them.
				var value *ast.Ident
				if len(n.Rhs) == len(n.Lhs) {
					value, _ = n.Rhs[i].(*ast.Ident)
				}
				switch {
				case sel.Sel.Name == "dialog" && (value == nil || value.Name != "nil"):
					findings = append(findings, name+": a dialog set outside "+dialogFile)
				case sel.Sel.Name == "mode" && value != nil && (value.Name == "Dialog" || value.Name == "DialogLines"):
					findings = append(findings, name+": a dialog's mode set outside "+dialogFile)
				}
			}
		}
		return true
	})
	return findings
}

func TestNoProductionPathOpensADialog(t *testing.T) {
	sources := repositorySources(t)
	if _, ok := sources[dialogFile]; !ok {
		t.Fatalf("%s is not among the sources", dialogFile)
	}
	for name, source := range sources {
		file, err := parser.ParseFile(token.NewFileSet(), name, source, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, finding := range inspectDialogOpeners(name, file) {
			t.Error(finding)
		}
	}
}

func TestTheDialogGuardSeesAWayIn(t *testing.T) {
	for name, source := range map[string]string{
		"internal/ui/keys.go":    `package ui; func (m Model) press() Model { return m.openDialog(DialogSpec{}) }`,
		"internal/ui/tickets.go": `package ui; func (m Model) press() Model { return m.openTicketDialog(DialogSpec{}) }`,
		"cmd/lalabase/main.go":   `package main; func run() { ui.New(ui.WithDialog(spec)) }`,
		"internal/gateway/g.go":  `package gateway; var way = ui.WithDialog`,
		dialogFile:               `package ui; func (m Model) closeDialog() Model { return m.openDialog(DialogSpec{}) }`,
		// The one caller, through another way in.
		"internal/ui/book.go":   `package ui; func (m Model) pressB() Model { return m.openDialog(DialogSpec{}) }`,
		"internal/ui/update.go": `package ui; func (m Model) press() Model { m.dialog = &dialog{}; return m }`,
		"internal/ui/mark.go":   `package ui; func (m Model) press() Model { m.mode, m.dialog = Dialog, new(dialog); return m }`,
		"internal/ui/agents.go": `package ui; func (m Model) press() Model { m.mode = DialogLines; return m }`,
		"internal/ui/inbox.go":  `package ui; func make() *dialog { return &dialog{} }`,
		"internal/ui/status.go": `package ui; func (m Model) press() Model { m.dialog = build(); return m }`,
		"internal/ui/time.go":   `package ui; func (m Model) press() Model { m.dialog, err = build(); return m }`,
		"internal/ui/counts.go": `package ui; func make() *dialog { return new(dialog) }`,
	} {
		file, err := parser.ParseFile(token.NewFileSet(), name, source, 0)
		if err != nil {
			t.Fatal(err)
		}
		if got := inspectDialogOpeners(name, file); len(got) == 0 {
			t.Errorf("missed the way in %s", name)
		}
	}
	for name, source := range map[string]string{
		"cmd/lalabase/dialog_terminal_test.go": `package main; func run() { ui.New(ui.WithDialog(spec)) }`,
		"tools/dialog-fixture/main.go":         `package main; func run() { ui.New(ui.WithDialog(spec)) }`,
		dialogFile:                             `package ui; func WithDialog() Option { return func(m *Model) { *m = m.openDialog(spec) } }`,
		"internal/ui/book.go":                  `package ui; func (m Model) pressB() Model { return m.openTicketDialog(DialogSpec{}) }`,
		"internal/ui/startup.go":               `package ui; func (m *Model) expire() { m.mode, m.dialog = Navigate, nil }`,
		"internal/ui/keys.go":                  `package ui; func route(mode Mode) bool { return mode == Dialog || mode == DialogLines }`,
	} {
		file, err := parser.ParseFile(token.NewFileSet(), name, source, 0)
		if err != nil {
			t.Fatal(err)
		}
		if got := inspectDialogOpeners(name, file); len(got) != 0 {
			t.Errorf("%s: %v", name, got)
		}
	}
}

// The dialog holds what a person writes and hands it to its user: neither
// the buffer nor the dialog's file reaches a way to the server, by import or
// by a call named Write.
func TestTheDialogReachesNoWritePath(t *testing.T) {
	sources := repositorySources(t)
	checked := 0
	for name, source := range sources {
		if test(name) || (name != dialogFile && !strings.HasPrefix(name, "internal/textentry/")) {
			continue
		}
		checked++
		for _, finding := range inspectDialogWrites(t, name, source) {
			t.Error(finding)
		}
	}
	if checked < 2 {
		t.Fatalf("%d files inspected", checked)
	}
}

func inspectDialogWrites(t *testing.T, name, source string) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), name, source, 0)
	if err != nil {
		t.Fatal(err)
	}
	var findings []string
	for _, imp := range file.Imports {
		if p, _ := strconv.Unquote(imp.Path.Value); infrastructure(p) {
			findings = append(findings, name+": import "+p)
		}
	}
	ast.Inspect(file, func(node ast.Node) bool {
		if sel, ok := node.(*ast.SelectorExpr); ok && sel.Sel.Name == "Write" {
			findings = append(findings, name+": a call of Write")
		}
		return true
	})
	return findings
}

func TestTheWriteGuardSeesAWay(t *testing.T) {
	for _, source := range []string{
		`package ui; import "` + clientModule + `internal/gateway"; var _ = gateway.New`,
		`package ui; import g "` + clientModule + `internal/queue"; var _ = g.New`,
		`package ui; func (m Model) send() { m.app.deps.Gateway.Write(nil) }`,
	} {
		if got := inspectDialogWrites(t, dialogFile, source); len(got) == 0 {
			t.Errorf("missed %s", source)
		}
	}
}
