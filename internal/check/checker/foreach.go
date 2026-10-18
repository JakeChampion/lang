package checker

import "github.com/jakechampion/lang/internal/syntax/ast"

// checkStmtLoweringForEach checks one statement, replacing a `for x in xs` with
// the loop it lowers to. The choice of loop needs the iterand's TYPE — an array
// is walked by index, a Map by its entry cursor, an Iterator through `next()` —
// so it cannot be made at parse time, and the parser leaves the ast.ForEach
// standing for this swap.
//
// Every statement slot that can hold a loop routes through here, so the caller
// assigns the result back rather than discarding it.
func (c *checker) checkStmtLoweringForEach(st ast.Stmt, s *scope) ast.Stmt {
	fe, ok := st.(*ast.ForEach)
	if !ok {
		c.checkStmt(st, s)
		return st
	}
	return c.checkForEach(fe, s)
}

// checkForEach builds and checks the lowering of a foreach, returning the Block
// that replaces it. The iterand's type selects the loop, so it is checked
// first, on its own; the lowering that follows checks the iterand again and
// settles it identically (identical diagnostics at one position are dropped,
// so nothing is reported twice). The synthetic statements are checked in a
// scope of their own, which is what `checkBlock` would have given the Block
// anyway.
func (c *checker) checkForEach(fe *ast.ForEach, parent *scope) ast.Stmt {
	s := newScope(parent)
	iterT := c.checkExpr(fe.Iter, s)

	iterName := ast.ForEachIterName(fe.ID)
	var stmts []ast.Stmt
	switch {
	case fe.Pattern != nil && isMapType(iterT):
		if fe.Pattern.Fields != nil || len(fe.Pattern.Names) != 2 {
			c.errfCode(fe.Pattern.P, "E024",
				"iterating a Map binds a (key, value) pair, but this pattern binds %d", len(fe.Pattern.Names))
			return &ast.Block{P: fe.P, Stmts: nil, Sugar: fe}
		}
		// The key and the value come off the entry cursor in separate
		// columns, so no whole value exists for an `@` binding to name.
		if fe.Var != ast.ForEachElemName(fe.ID) {
			c.errfCode(fe.Pattern.P, "E024",
				"iterating a Map binds the key and the value separately, so there is no whole value for %q to name — drop the `@` binding", fe.Var)
			return &ast.Block{P: fe.P, Stmts: nil, Sugar: fe}
		}
		// Entries come off a cursor (`m.iter()` / `has_next()` / `key()` /
		// `value()` / `advance()`), so the walk is insertion-ordered and
		// allocates nothing per entry.
		// The cursor holds no count on its map, so a map the iterand
		// builds is bound first: the binding owns it for the loop and
		// releases it after.
		recv := fe.Iter
		if _, named := fe.Iter.(*ast.Ident); !named {
			mapName := iterName + "_map"
			stmts = append(stmts, &ast.Var{P: fe.P, Name: mapName, Init: fe.Iter})
			recv = &ast.Ident{P: fe.P, Name: mapName}
		}
		declIter := &ast.Var{P: fe.P, Name: iterName, Init: callAt(recv, "iter", fe.P)}
		stmts = append(stmts, declIter)
		stmts = append(stmts, ast.ForEachMapLoop(fe, iterName)...)
	case indexIterable(iterT):
		declIter := &ast.Var{P: fe.P, Name: iterName, Init: fe.Iter}
		stmts = append([]ast.Stmt{declIter}, ast.ForEachArrayLoop(fe, iterName)...)
	default:
		subject := fe.Iter
		if !c.implementsIterator(iterT) {
			tn, named := methodTypeName(iterT)
			if _, isParam := iterT.(ast.ParamType); isParam || !named {
				return c.notIterable(fe, iterT)
			}
			if _, _, ok := c.resolveMethod(tn, "iter", nil); !ok {
				return c.notIterable(fe, iterT)
			}
			subject = callAt(fe.Iter, "iter", fe.Iter.Pos())
		}
		declIter := &ast.Var{P: fe.P, Name: iterName, Init: subject}
		c.checkStmt(declIter, s)
		if made, _ := s.lookup(iterName); subject != fe.Iter && !c.implementsIterator(made) {
			return c.notIterable(fe, iterT)
		}
		loop := ast.ForEachIteratorLoop(fe, iterName)
		for _, st := range loop {
			c.checkStmt(st, s)
		}
		return &ast.Block{P: fe.P, Stmts: append([]ast.Stmt{declIter}, loop...), Sugar: fe}
	}
	for _, st := range stmts {
		c.checkStmt(st, s)
	}
	return &ast.Block{P: fe.P, Stmts: stmts, Sugar: fe}
}

// notIterable reports E083 for a foreach over a value it cannot walk and
// returns the empty Block standing in for the loop.
func (c *checker) notIterable(fe *ast.ForEach, t ast.Type) ast.Stmt {
	c.errfCode(fe.Iter.Pos(), "E083",
		"cannot iterate over a value of type %s: `for` walks an array, a string, a Map, a range, an iter.Iterator, or a value whose iter() returns one", typeLabel(t))
	return &ast.Block{P: fe.P, Stmts: nil, Sugar: fe}
}

// callAt builds `recv.method()` at pos.
func callAt(recv ast.Expr, method string, pos ast.Position) *ast.Call {
	return &ast.Call{P: pos, Callee: &ast.FieldAccess{P: pos, Target: recv, Field: method, FieldPos: pos}}
}

// indexIterable reports whether a foreach over t walks it by index: an array, a
// slice or a string. An unresolved iterand (nil, after an earlier error) and a
// Map under a plain header take the same loop, whose own checks report them.
func indexIterable(t ast.Type) bool {
	switch t.(type) {
	case nil, ast.ArrayType, ast.SliceType, ast.StringType, ast.StrType:
		return true
	}
	return isMapType(t)
}

// implementsIterator reports whether t implements core/iter's Iterator: an
// impl of it for t, or for a type parameter, a bound naming it.
func (c *checker) implementsIterator(t ast.Type) bool {
	if p, ok := t.(ast.ParamType); ok {
		decl := c.typeParamDecl(p.Name)
		if decl == nil {
			return false
		}
		for _, tr := range c.expandTraits(decl.Bounds[p.Name]) {
			if tr == coreIterator {
				return true
			}
		}
		return false
	}
	tn, ok := methodTypeName(t)
	if !ok {
		return false
	}
	return c.info.Impls[coreIterator][tn]
}

// coreIterator is core/iter's Iterator as modload mangles an imported trait.
const coreIterator = "iter__Iterator"

// isMapType reports whether t is the builtin associative container, whose
// entries iterate through a cursor rather than by index.
func isMapType(t ast.Type) bool {
	st, ok := t.(ast.StructType)
	return ok && st.Name == "Map"
}
