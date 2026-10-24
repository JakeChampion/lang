package checker

import "github.com/jakechampion/lang/internal/syntax/ast"

func (c *checker) checkStmtLoweringForEach(st ast.Stmt, s *scope) ast.Stmt {
	fe, ok := st.(*ast.ForEach)
	if !ok {
		c.checkStmt(st, s)
		return st
	}
	return c.checkForEach(fe, s)
}

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
		if fe.Var != ast.ForEachElemName(fe.ID) {
			c.errfCode(fe.Pattern.P, "E024",
				"iterating a Map binds the key and the value separately, so there is no whole value for %q to name — drop the `@` binding", fe.Var)
			return &ast.Block{P: fe.P, Stmts: nil, Sugar: fe}
		}
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

func (c *checker) notIterable(fe *ast.ForEach, t ast.Type) ast.Stmt {
	c.errfCode(fe.Iter.Pos(), "E083",
		"cannot iterate over a value of type %s: `for` walks an array, a string, a Map, a range, an iter.Iterator, or a value whose iter() returns one", typeLabel(t))
	return &ast.Block{P: fe.P, Stmts: nil, Sugar: fe}
}

func callAt(recv ast.Expr, method string, pos ast.Position) *ast.Call {
	return &ast.Call{P: pos, Callee: &ast.FieldAccess{P: pos, Target: recv, Field: method, FieldPos: pos}}
}

func indexIterable(t ast.Type) bool {
	switch t.(type) {
	case nil, ast.ArrayType, ast.SliceType, ast.StringType, ast.StrType:
		return true
	}
	return isMapType(t)
}

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

const coreIterator = "iter__Iterator"

func isMapType(t ast.Type) bool {
	st, ok := t.(ast.StructType)
	return ok && st.Name == "Map"
}
