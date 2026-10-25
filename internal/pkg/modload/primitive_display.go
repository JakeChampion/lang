package modload

import "github.com/jakechampion/lang/internal/syntax/ast"

// Primitive display bodies are runtime dependencies. They do not add source
// imports, so unrelated library names and methods retain their normal scope.
func needsPrimitiveDisplay(prog *ast.Program) bool {
	needs := false
	needsBody := func(e ast.Expr) bool {
		switch e.(type) {
		case *ast.StringLit, *ast.BoolLit:
			return false
		}
		return true
	}
	ast.WalkProgram(prog, func(node ast.Node) bool {
		if needs {
			return false
		}
		switch n := node.(type) {
		case *ast.FString:
			for _, part := range n.Parts {
				if part.Expr != nil && needsBody(part.Expr) {
					needs = true
				}
			}
		case *ast.Call:
			if fa, ok := n.Callee.(*ast.FieldAccess); ok && fa.Field == "to_string" && len(n.Args) == 0 {
				needs = needsBody(fa.Target)
			}
			if id, ok := n.Callee.(*ast.Ident); ok && (id.Name == "print" || id.Name == "write" || id.Name == "eprint") {
				for _, arg := range n.Args {
					if needsBody(arg) {
						needs = true
					}
				}
			}
		}
		return !needs
	})
	return needs
}
