package sourceguard

import "go/ast"

// Runtime entry points construct the root directly. This makes its local
// dependencies reachable by the guard even when the caller is outside ui.
// Test callers can supply fault decorators without adding production hooks.
func inspectUIEntry(name string, file *ast.File, aliases map[string]string) []string {
	fromUI := func(expr ast.Expr, member string) bool {
		sel, ok := expr.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != member {
			return false
		}
		ident, ok := sel.X.(*ast.Ident)
		return ok && aliases[ident.Name] == clientModule+"internal/ui"
	}
	allowed := map[ast.Expr]bool{}
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || !fromUI(call.Fun, "Run") || len(call.Args) != 5 {
			return true
		}
		root, ok := call.Args[4].(*ast.CallExpr)
		if ok && (fromUI(root.Fun, "New") || fromUI(root.Fun, "NewWithContent")) {
			allowed[call.Fun] = true
		}
		return true
	})
	var findings []string
	ast.Inspect(file, func(node ast.Node) bool {
		if expr, ok := node.(ast.Expr); ok && fromUI(expr, "Run") && !allowed[expr] {
			findings = append(findings, name+": UI entry requires a protected root constructor")
		}
		return true
	})
	return findings
}
