// Package optimizer performs simple AST-level cleanup before lowering.
//
// Constant folding and dead-code elimination used to live here; both
// have moved onto the IR (ir.Fold and ir.EliminateDeadCode), where
// they see post-lowering shapes the AST passes can't — collapsed
// ternaries, decomposed short-circuits, post-inline arithmetic — and
// every IR-consuming backend inherits the wins from one place.
//
// What remains: the small-function inliner, which still runs at the
// AST level so its substituted bodies feed lowering as plain ASTs.
// A future pass would move it onto the IR too and let this package
// retire entirely.
package optimizer

import "github.com/jakechampion/lang/internal/ast"

// Optimize runs the AST-level cleanups before the program is lowered
// to IR. Today that's just the inliner; the IR-level Fold / DCE
// passes pick up the rest.
func Optimize(prog *ast.Program) {
	Inline(prog)
}
