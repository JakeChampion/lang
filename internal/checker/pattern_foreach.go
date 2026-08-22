package checker

import (
	"fmt"

	"github.com/jakechampion/lang/internal/ast"
)

// checkStmtLoweringForEach checks one statement of a block, replacing a
// destructuring `for (a, b) in xs` with the loop it lowers to. The choice of
// loop needs the iterand's TYPE — an array binds the pattern against each
// element, a Map against each entry — so unlike every other foreach shape it
// cannot be made at parse time. The parser leaves the ast.ForEach standing as a
// statement of a block for exactly this swap.
func (c *checker) checkStmtLoweringForEach(st ast.Stmt, s *scope) ast.Stmt {
	fe, ok := st.(*ast.ForEach)
	if !ok || fe.Pattern == nil {
		c.checkStmt(st, s)
		return st
	}
	return c.checkPatternForEach(fe, s)
}

// checkPatternForEach builds and checks the lowering of a destructuring
// foreach, returning the Block that replaces it. The iterand binding is built
// and checked FIRST — its type is what selects the loop below it, and binding
// the iterand once is what keeps a diagnostic inside it from being reported
// twice. The synthetic statements are checked in a scope of their own, which is
// what `checkBlock` would have given the Block anyway.
func (c *checker) checkPatternForEach(fe *ast.ForEach, parent *scope) ast.Stmt {
	s := newScope(parent)
	iterName := ast.ForEachIterName(fe.ID)
	declIter := &ast.Var{P: fe.P, Name: iterName, Init: fe.Iter}
	c.checkStmt(declIter, s)

	stmts := []ast.Stmt{declIter}
	var rest []ast.Stmt
	if isMapType(c.info.VarTypes[declIter]) {
		if fe.Pattern.Fields != nil || len(fe.Pattern.Names) != 2 {
			c.errfCode(fe.Pattern.P, "E024",
				"iterating a Map binds a (key, value) pair, but this pattern binds %d", len(fe.Pattern.Names))
			return &ast.Block{P: fe.P, Stmts: stmts, Sugar: fe}
		}
		// Entries come off a cursor (`m.iter()` / `has_next()` / `key()` /
		// `value()` / `advance()`), so the walk is insertion-ordered and
		// allocates nothing per entry.
		cursorName := fmt.Sprintf("__foreach_cursor_%d", fe.ID)
		declCursor := &ast.Var{P: fe.P, Name: cursorName, Init: &ast.Call{P: fe.P,
			Callee: &ast.FieldAccess{P: fe.P, Target: &ast.Ident{P: fe.P, Name: iterName}, Field: "iter", FieldPos: fe.P},
		}}
		c.checkStmt(declCursor, s)
		stmts = append(stmts, declCursor)
		rest = ast.ForEachMapLoop(fe, cursorName)
	} else {
		rest = ast.ForEachArrayLoop(fe, iterName)
	}
	for _, st := range rest {
		c.checkStmt(st, s)
	}
	return &ast.Block{P: fe.P, Stmts: append(stmts, rest...), Sugar: fe}
}

// isMapType reports whether t is the builtin associative container, whose
// entries iterate through a cursor rather than by index.
func isMapType(t ast.Type) bool {
	st, ok := t.(ast.StructType)
	return ok && st.Name == "Map"
}
