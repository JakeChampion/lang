package checker

import "github.com/jakechampion/lang/internal/ast"

// checkStmtLoweringForEach checks one statement, replacing a destructuring
// `for (a, b) in xs` with the loop it lowers to. The choice of loop needs the
// iterand's TYPE — an array binds the pattern against each element, a Map
// against each entry — so unlike every other foreach shape it cannot be made at
// parse time, and the parser leaves the ast.ForEach standing for this swap.
//
// Every statement slot that can hold a loop routes through here, so the caller
// assigns the result back rather than discarding it.
func (c *checker) checkStmtLoweringForEach(st ast.Stmt, s *scope) ast.Stmt {
	fe, ok := st.(*ast.ForEach)
	if !ok || fe.Pattern == nil {
		c.checkStmt(st, s)
		return st
	}
	return c.checkPatternForEach(fe, s)
}

// checkPatternForEach builds and checks the lowering of a destructuring
// foreach, returning the Block that replaces it. The iterand's type selects the
// loop, so it is checked first, on its own; the lowering that follows is the
// same shape each form has always had, and its own check of the iterand settles
// the expression identically (identical diagnostics at one position are
// dropped, so nothing is reported twice). The synthetic statements are checked
// in a scope of their own, which is what `checkBlock` would have given the
// Block anyway.
func (c *checker) checkPatternForEach(fe *ast.ForEach, parent *scope) ast.Stmt {
	s := newScope(parent)
	iterT := c.checkExpr(fe.Iter, s)

	iterName := ast.ForEachIterName(fe.ID)
	var stmts []ast.Stmt
	if isMapType(iterT) {
		if fe.Pattern.Fields != nil || len(fe.Pattern.Names) != 2 {
			c.errfCode(fe.Pattern.P, "E024",
				"iterating a Map binds a (key, value) pair, but this pattern binds %d", len(fe.Pattern.Names))
			return &ast.Block{P: fe.P, Stmts: nil, Sugar: fe}
		}
		// Entries come off a cursor (`m.iter()` / `has_next()` / `key()` /
		// `value()` / `advance()`), so the walk is insertion-ordered and
		// allocates nothing per entry.
		declIter := &ast.Var{P: fe.P, Name: iterName, Init: &ast.Call{P: fe.P,
			Callee: &ast.FieldAccess{P: fe.P, Target: fe.Iter, Field: "iter", FieldPos: fe.P},
		}}
		stmts = append([]ast.Stmt{declIter}, ast.ForEachMapLoop(fe, iterName)...)
	} else {
		declIter := &ast.Var{P: fe.P, Name: iterName, Init: fe.Iter}
		stmts = append([]ast.Stmt{declIter}, ast.ForEachArrayLoop(fe, iterName)...)
	}
	for _, st := range stmts {
		c.checkStmt(st, s)
	}
	return &ast.Block{P: fe.P, Stmts: stmts, Sugar: fe}
}

// isMapType reports whether t is the builtin associative container, whose
// entries iterate through a cursor rather than by index.
func isMapType(t ast.Type) bool {
	st, ok := t.(ast.StructType)
	return ok && st.Name == "Map"
}
