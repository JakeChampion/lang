package checker

import "github.com/jakechampion/lang/internal/syntax/ast"

func (c *checker) checkMustConsume(fn *ast.FuncDecl) {
	if fn.Body == nil {
		return
	}
	for _, p := range fn.Params {
		if p.Own {
			continue
		}
		if tn, ok := c.mustConsumeTypeName(p.Type); ok {
			c.mcCheckBinding(fn, p.Name, tn, fn.P, fn.Body.Stmts)
		}
	}
	c.mcWalkBlock(fn, fn.Body)
}

func (c *checker) mcWalkBlock(fn *ast.FuncDecl, b *ast.Block) {
	if b == nil {
		return
	}
	for i, s := range b.Stmts {
		if v, ok := s.(*ast.Var); ok {
			t := v.Type
			if t == nil {
				t = c.info.VarTypes[v]
			}
			if tn, ok := c.mustConsumeTypeName(t); ok {
				c.mcCheckBinding(fn, v.Name, tn, v.P, b.Stmts[i+1:])
			}
		}
		switch x := s.(type) {
		case *ast.Block:
			c.mcWalkBlock(fn, x)
		case *ast.If:
			c.mcWalkStmtBlock(fn, x.Then)
			c.mcWalkStmtBlock(fn, x.Else)
		case *ast.While:
			c.mcWalkStmtBlock(fn, x.Body)
		case *ast.Loop:
			c.mcWalkStmtBlock(fn, x.Body)
		case *ast.For:
			c.mcWalkStmtBlock(fn, x.Body)
		case *ast.Match:
			for _, arm := range x.Arms {
				c.mcWalkBlock(fn, arm.Body)
			}
		}
	}
}

func (c *checker) mcWalkStmtBlock(fn *ast.FuncDecl, s ast.Stmt) {
	if s == nil {
		return
	}
	if b, ok := s.(*ast.Block); ok {
		c.mcWalkBlock(fn, b)
		return
	}
}

func (c *checker) mcCheckBinding(fn *ast.FuncDecl, name, typeName string, bindPos ast.Position, rest []ast.Stmt) {
	consumed, violated := c.mcSeq(rest, name, typeName)
	if !consumed || violated {
		c.errfCode(bindPos, "E067",
			"value of @must_consume type %q bound to %q may go out of scope without being consumed — pass it, return it, match on it, or store it into another @must_consume type on every path",
			typeName, name)
		return
	}
	for _, s := range rest {
		if c.mcStmtConsumesQuiet(s, name) {
			return
		}
		if es, ok := s.(*ast.ExprStmt); ok {
			if as, ok := es.Expr.(*ast.Assign); ok {
				if id, ok := as.Target.(*ast.Ident); ok && id.Name == name {
					c.errfCode(as.P, "E067",
						"overwriting %q while it still holds an unconsumed @must_consume %q value — consume the old value first",
						name, typeName)
					return
				}
			}
		}
	}
}

func (c *checker) mcSeq(stmts []ast.Stmt, name, typeName string) (bool, bool) {
	for _, s := range stmts {
		if c.mcStmtConsumes(s, name, typeName) {
			return true, false
		}
		switch x := s.(type) {
		case *ast.Return:
			return false, true
		case *ast.Break, *ast.Continue:
			return false, true
		case *ast.Block:
			con, div := c.mcSeq(x.Stmts, name, typeName)
			if div {
				return false, true
			}
			if con {
				return true, false
			}
		case *ast.If:
			tc, tv := c.mcArm(x.Then, name, typeName)
			ec, ev := false, false
			if x.Else != nil {
				ec, ev = c.mcArm(x.Else, name, typeName)
			}
			if tv || ev {
				return false, true
			}
			if tc && x.Else != nil && ec {
				return true, false
			}
		case *ast.Match:
			if c.mcIsBinding(x.Tag, name) {
				return true, false
			}
			if x.Origin != "" {
				break
			}
			all := len(x.Arms) > 0
			for _, arm := range x.Arms {
				ac, av := c.mcSeq(arm.Body.Stmts, name, typeName)
				if av {
					return false, true
				}
				if !ac {
					all = false
				}
			}
			if all {
				return true, false
			}
		}
	}
	return false, false
}

func (c *checker) mcArm(s ast.Stmt, name, typeName string) (bool, bool) {
	if s == nil {
		return false, false
	}
	if b, ok := s.(*ast.Block); ok {
		return c.mcSeq(b.Stmts, name, typeName)
	}
	return c.mcSeq([]ast.Stmt{s}, name, typeName)
}

func (c *checker) mcStmtConsumesQuiet(s ast.Stmt, name string) bool {
	return c.mcStmtConsumesImpl(s, name, "", false)
}

func (c *checker) mcStmtConsumes(s ast.Stmt, name, typeName string) bool {
	return c.mcStmtConsumesImpl(s, name, typeName, true)
}

func (c *checker) mcStmtConsumesImpl(s ast.Stmt, name, typeName string, report bool) bool {
	switch x := s.(type) {
	case *ast.ExprStmt:
		return c.mcExprConsumes(x.Expr, name, typeName, report)
	case *ast.Var:
		if c.mcIsBinding(x.Init, name) {
			return true
		}
		return c.mcExprConsumes(x.Init, name, typeName, report)
	case *ast.Return:
		if x.Value == nil {
			return false
		}
		if c.mcIsBinding(x.Value, name) {
			return true
		}
		return c.mcExprConsumes(x.Value, name, typeName, report)
	case *ast.Match:
		if c.mcIsBinding(x.Tag, name) {
			return true
		}
	}
	return false
}

func (c *checker) mcIsBinding(e ast.Expr, name string) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == name
}

func (c *checker) mcExprConsumes(e ast.Expr, name, typeName string, report bool) bool {
	if e == nil {
		return false
	}
	consumed := false
	ast.Walk(e, func(n ast.Node) bool {
		if consumed {
			return false
		}
		switch x := n.(type) {
		case *ast.Assign:
			if c.mcIsBinding(x.Value, name) {
				consumed = true
				return false
			}
		case *ast.Call:
			for _, a := range x.Args {
				if c.mcIsBinding(a, name) {
					consumed = true
					return false
				}
			}
		case *ast.MatchExpr:
			if c.mcIsBinding(x.Tag, name) {
				consumed = true
				return false
			}
		case *ast.StructLit:
			for _, f := range x.Fields {
				if c.mcIsBinding(f.Value, name) {
					if _, marked := c.mustConsumeStructName(x.TypeName); marked {
						consumed = true
					} else {
						consumed = true
						if !report {
							return false
						}
						c.errfCode(x.P, "E067",
							"value of @must_consume type %q stored into unmarked struct %q — the consumption obligation is lost; mark %q @must_consume or consume the value directly",
							typeName, x.TypeName, x.TypeName)
					}
					return false
				}
			}
		case *ast.EnumLit:
			for _, a := range x.Args {
				if c.mcIsBinding(a, name) {
					if ed, ok := c.info.Enums[x.EnumName]; ok && ed.MustConsume {
						consumed = true
					} else {
						consumed = true
						if !report {
							return false
						}
						c.errfCode(x.P, "E067",
							"value of @must_consume type %q stored into unmarked enum %q — the consumption obligation is lost; mark %q @must_consume or consume the value directly",
							typeName, x.EnumName, x.EnumName)
					}
					return false
				}
			}
		case *ast.ArrayLit:
			for _, el := range x.Elems {
				if c.mcIsBinding(el, name) {
					consumed = true
					if report {
						c.errfCode(x.P, "E067",
							"value of @must_consume type %q stored into an array literal — arrays cannot carry consumption obligations; consume the value directly",
							typeName)
					}
					return false
				}
			}
		case *ast.TupleLit:
			for _, el := range x.Elems {
				if c.mcIsBinding(el, name) {
					consumed = true
					if report {
						c.errfCode(x.P, "E067",
							"value of @must_consume type %q stored into a tuple — tuples cannot carry consumption obligations; consume the value directly",
							typeName)
					}
					return false
				}
			}
		case *ast.Lambda:
			if mcMentions(x.Body, name) {
				consumed = true
				if report {
					c.errfCode(x.P, "E067",
						"value of @must_consume type %q captured by a closure — obligations cannot flow into closures; consume the value directly",
						typeName)
				}
				return false
			}
			return false
		}
		return true
	})
	return consumed
}

func mcMentions(b *ast.Block, name string) bool {
	if b == nil {
		return false
	}
	found := false
	ast.Walk(b, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == name {
			found = true
			return false
		}
		return !found
	})
	return found
}

func (c *checker) mustConsumeTypeName(t ast.Type) (string, bool) {
	switch x := t.(type) {
	case ast.StructType:
		return c.mustConsumeStructName(x.Name)
	case ast.EnumType:
		if ed, ok := c.info.Enums[x.Name]; ok && ed.MustConsume {
			return x.Name, true
		}
	}
	return "", false
}

func (c *checker) mustConsumeStructName(name string) (string, bool) {
	if sd, ok := c.info.Structs[name]; ok && sd.MustConsume {
		return name, true
	}
	return "", false
}
