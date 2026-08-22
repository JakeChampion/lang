package shadowrename

import (
	"fmt"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// TestWalksHandleEveryNodeKind drives every AST node kind through the pass's
// two switches. They are a private enumeration of the same unions ast.Walk
// enumerates — this pass cannot use ast.Walk, since it pushes and pops a
// scope frame around parts of a node and has to walk an initialiser before
// binding the name it declares — so both switches carry a default that
// panics, and ast.NodeKinds is what proves neither has fallen behind.
//
// A kind missing here does not fail loudly in production: the reference keeps
// the shadowed name, the IR's flat locals map binds it to the outer
// declaration's slot, and the program computes the wrong value. *ast.Lambda
// was missing exactly that way — see
// conformance/cases/shadow_captured_by_lambda.
func TestWalksHandleEveryNodeKind(t *testing.T) {
	for _, kind := range ast.NodeKinds() {
		t.Run(fmt.Sprintf("%T", kind), func(t *testing.T) {
			r := newRenamer()
			r.pushFrame()
			switch n := kind.(type) {
			case ast.Stmt:
				r.walkStmt(n)
			case ast.Expr:
				r.walkExpr(n)
			default:
				t.Skipf("%T is a declaration; the pass walks function bodies", kind)
			}
		})
	}
}
