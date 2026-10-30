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

// dialogOpeners name the ways to a dialog. M1 has no production user: no
// key opens a dialog, and no command, so these are called nowhere in the
// client but among themselves. K6 gives them their first caller; that is a
// decision to take here, not a fix for a red guard.
var dialogOpeners = []string{"WithDialog", "openDialog", "openTicketDialog"}

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
			if !ok || !slices.Contains(dialogOpeners, id.Name) || (isFunc && id == fn.Name) || (leads && id.Name == "openDialog") {
				return true
			}
			findings = append(findings, name+": a dialog opened through "+id.Name)
			return true
		})
	}
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
