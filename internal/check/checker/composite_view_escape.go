package checker

import "github.com/jakechampion/lang/internal/syntax/ast"

const compositeViewEscapeMsg = "the returned view's source cannot be kept alive beyond this function: return owned storage or a view of a parameter instead"

// checkCompositeViewEscapes reports a function that returns a struct or a
// tuple whose `[T]` or `str` part views function-local storage. It reports
// once, at the function, with the self-host lowering's E063/E065.
func (c *checker) checkCompositeViewEscapes(prog *ast.Program) {
	envs, order := c.escapeEnvs(prog, func(ast.Type) bool { return true })
	slices := returnViewSummaries(order, func(fn *ast.FuncDecl, ret ast.Expr, sums map[string]viewSummary) viewRoots {
		if _, isSlice := fn.ReturnType.(ast.SliceType); isSlice {
			return c.sliceViewRoots(ret, envs[fn], sums)
		}
		return c.sliceSourceRoots(ret, envs[fn], sums)
	})
	var strOrder []*ast.FuncDecl
	for _, fn := range order {
		switch fn.ReturnType.(type) {
		case ast.StrType, ast.StringType:
			strOrder = append(strOrder, fn)
		}
	}
	strs := returnViewSummaries(strOrder, func(fn *ast.FuncDecl, ret ast.Expr, sums map[string]viewSummary) viewRoots {
		if _, isStr := fn.ReturnType.(ast.StrType); isStr {
			return c.strViewRoots(ret, envs[fn], sums)
		}
		return c.stringSourceRoots(ret, envs[fn], sums)
	})
	for _, fn := range order {
		switch fn.ReturnType.(type) {
		case ast.StructType, ast.TupleType:
		default:
			continue
		}
		code := ""
		forEachReturn(fn, func(ret *ast.Return) {
			if code == "" {
				code = c.compositeViewEscape(ret.Value, fn.ReturnType, envs[fn], slices, strs, 0)
			}
		})
		if code != "" {
			c.errfCode(fn.P, code, compositeViewEscapeMsg)
		}
	}
}

func (c *checker) compositeViewEscape(e ast.Expr, t ast.Type, env *escapeEnv, slices, strs map[string]viewSummary, depth int) string {
	if depth > 8 {
		return ""
	}
	switch t := t.(type) {
	case ast.SliceType:
		if c.sliceViewRoots(e, env, slices).local {
			return "E063"
		}
	case ast.StrType:
		if c.strViewRoots(e, env, strs).local {
			return "E065"
		}
	case ast.StructType:
		sd := c.info.Structs[t.Name]
		sl, ok := literalInit(e, env).(*ast.StructLit)
		if sd == nil || len(sd.TypeParams) > 0 || !ok || sl.Base != nil {
			return ""
		}
		for _, f := range sl.Fields {
			for _, decl := range sd.Fields {
				if decl.Name != f.Name {
					continue
				}
				if code := c.compositeViewEscape(f.Value, decl.Type, env, slices, strs, depth+1); code != "" {
					return code
				}
			}
		}
	case ast.TupleType:
		tl, ok := literalInit(e, env).(*ast.TupleLit)
		if !ok {
			return ""
		}
		for i, el := range tl.Elems {
			if i < len(t.Elems) {
				if code := c.compositeViewEscape(el, t.Elems[i], env, slices, strs, depth+1); code != "" {
					return code
				}
			}
		}
	}
	return ""
}

// literalInit follows a local to the expression that initialised it.
func literalInit(e ast.Expr, env *escapeEnv) ast.Expr {
	for range 8 {
		id, ok := e.(*ast.Ident)
		if !ok {
			return e
		}
		v, ok := env.locals[id.Name]
		if !ok || v.Init == nil {
			return e
		}
		e = v.Init
	}
	return e
}
