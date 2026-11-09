package checker

import "github.com/jakechampion/lang/internal/syntax/ast"

// An open generic call is the value of a `let` with no type, such as
// `let m = ordmap.new();`, whose call leaves type parameters unbound. The
// local is an open local, and its first use at a known type gives the call
// its type arguments (#14377).
type openCall struct {
	call *ast.Call
	fn   *ast.FuncDecl
	ft   *ast.FuncType
	sub  map[string]ast.Type
}

// openGenericResult gives the result of call with each unbound type
// parameter left open: the bare enum of the unit variants that bound it in
// opened, or else unbound. It is false when every parameter is bound.
func (c *checker) openGenericResult(call *ast.Call, fn *ast.FuncDecl, ft *ast.FuncType, sub, opened map[string]ast.Type) (ast.Type, bool) {
	open := map[string]ast.Type{}
	for k, v := range sub {
		open[k] = v
	}
	missing := false
	for _, tp := range fn.TypeParams {
		if _, ok := sub[tp]; !ok {
			open[tp] = ast.UnboundType{}
			if bare, ok := opened[tp]; ok {
				open[tp] = bare
			}
			missing = true
		}
	}
	if !missing {
		return nil, false
	}
	result := substituteType(ft.Result, open)
	if containsParamType(result) {
		return nil, false
	}
	c.pendingOpenCall = &openCall{call: call, fn: fn, ft: ft, sub: sub}
	return result, true
}

// unbindOpenVariantParams leaves unbound each type parameter that only unit
// variants of a generic enum bind, such as the T of `id(No)`, and gives the
// bare enum each one had. The variant names no type arguments, so only the
// expected type of the call, or a use of the local it initialises, can give
// them (#14410).
func (c *checker) unbindOpenVariantParams(call *ast.Call, ft *ast.FuncType, fn *ast.FuncDecl, sub map[string]ast.Type) map[string]ast.Type {
	var opened map[string]ast.Type
	for _, tp := range fn.TypeParams {
		et, ok := sub[tp].(ast.EnumType)
		if !ok || len(et.Args) > 0 {
			continue
		}
		open := false
		for i, p := range ft.Params {
			if pt, ok := p.(ast.ParamType); ok && pt.Name == tp && i < len(call.Args) {
				if !c.openVariant(call.Args[i]) {
					open = false
					break
				}
				open = true
			} else if typeMentionsParam(p, tp) {
				open = false
				break
			}
		}
		if open {
			if opened == nil {
				opened = map[string]ast.Type{}
			}
			opened[tp] = et
			delete(sub, tp)
		}
	}
	return opened
}

// openVariant reports a unit variant of a generic enum, such as `No`.
func (c *checker) openVariant(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	if !ok || id.Local || id.EnumName == "" {
		return false
	}
	ed := c.info.Enums[id.EnumName]
	return ed != nil && len(ed.TypeParams) > 0
}

func (c *checker) errUninferredParam(call *ast.Call, fn *ast.FuncDecl, tp string) {
	display, spelling := callSiteName(fn)
	c.errfCode(call.P, "E040", "could not infer type parameter %s for %s — supply it explicitly at the call site (e.g. %s[i32](...)) or annotate the binding", tp, display, spelling)
}

// beginOpenGenericCall ties the open call of decl's value to decl.
func (c *checker) beginOpenGenericCall(decl *ast.Var) {
	oc := c.pendingOpenCall
	c.pendingOpenCall = nil
	if oc == nil || oc.call != decl.Init {
		return
	}
	if c.openEnumLocals[decl] == nil {
		oc.errUnbound(c)
		return
	}
	if c.openGenericCalls == nil {
		c.openGenericCalls = map[*ast.Var]*openCall{}
	}
	c.openGenericCalls[decl] = oc
}

// settleOpenCall gives the open call of decl the type arguments that the
// settled type of decl implies.
func (c *checker) settleOpenCall(decl *ast.Var, filled ast.Type) {
	oc := c.openGenericCalls[decl]
	if oc == nil {
		return
	}
	delete(c.openGenericCalls, decl)
	tpSet := make(map[string]bool, len(oc.fn.TypeParams))
	sub := make(map[string]ast.Type, len(oc.sub))
	for _, tp := range oc.fn.TypeParams {
		if _, ok := oc.sub[tp]; !ok {
			tpSet[tp] = true
		}
	}
	for k, v := range oc.sub {
		sub[k] = v
	}
	bindOpenParams(oc.ft.Result, filled, tpSet, sub)
	args := make([]ast.Type, len(oc.fn.TypeParams))
	for i, tp := range oc.fn.TypeParams {
		v, ok := sub[tp]
		if !ok {
			c.errUninferredParam(oc.call, oc.fn, tp)
			return
		}
		args[i] = v
	}
	c.checkTypeArgBounds(oc.fn, args, sub, tpSet, oc.call.P)
	oc.call.TypeArgs = args
}

// finishOpenGenericCalls ends a function body: an open call that no use gave
// its type arguments is the E040 it would have been at the call.
func (c *checker) finishOpenGenericCalls() {
	for _, oc := range c.openGenericCalls {
		oc.errUnbound(c)
	}
	c.openGenericCalls = nil
}

func (oc *openCall) errUnbound(c *checker) {
	for _, tp := range oc.fn.TypeParams {
		if _, ok := oc.sub[tp]; !ok {
			c.errUninferredParam(oc.call, oc.fn, tp)
		}
	}
}

// settleOpenCallByMethod gives an open generic-call local the type
// arguments that a method call on it binds, such as `m.insert(1, 2)` for
// `insert(k: K, v: V)` on `OrdMap[K, V]`.
func (c *checker) settleOpenCallByMethod(call *ast.Call, fa *ast.FieldAccess, s *scope) {
	id, ok := fa.Target.(*ast.Ident)
	if !ok {
		return
	}
	decl := s.lookupVarDecl(id.Name)
	if decl == nil || c.openGenericCalls[decl] == nil {
		return
	}
	name, have, ok := nominalParts(decl.Type)
	if !ok {
		return
	}
	ft := c.info.FuncSigs[c.info.Methods[name+"."+fa.Field]]
	if ft == nil || len(ft.Params) != len(call.Args)+1 {
		return
	}
	recvName, recvArgs, ok := nominalParts(ft.Params[0])
	if !ok || recvName != name || len(recvArgs) != len(have) {
		return
	}
	tpSet := map[string]bool{}
	for _, a := range recvArgs {
		if p, ok := a.(ast.ParamType); ok {
			tpSet[p.Name] = true
		}
	}
	sub := map[string]ast.Type{}
	for i, a := range call.Args {
		if t := settledLiteralType(c.checkExpr(a, s)); t != nil {
			bindOpenParams(ft.Params[i+1], t, tpSet, sub)
		}
	}
	args := make([]ast.Type, len(have))
	for i, a := range have {
		if _, open := a.(ast.UnboundType); !open {
			args[i] = a
			continue
		}
		p, ok := recvArgs[i].(ast.ParamType)
		if !ok {
			return
		}
		v, ok := sub[p.Name]
		if !ok {
			return
		}
		args[i] = v
	}
	if _, isEnum := decl.Type.(ast.EnumType); isEnum {
		c.settleOpenEnumLocal(decl, ast.EnumType{Name: name, Args: args})
		return
	}
	c.settleOpenEnumLocal(decl, ast.StructType{Name: name, Args: args})
}

// nominalParts is the name and type arguments of a struct or enum type.
func nominalParts(t ast.Type) (string, []ast.Type, bool) {
	switch n := t.(type) {
	case ast.StructType:
		return n.Name, n.Args, true
	case ast.EnumType:
		return n.Name, n.Args, true
	}
	return "", nil, false
}

// bindOpenParams binds each type parameter in tpSet that pattern names to the
// part of got at the same place. The first binding of a parameter holds.
func bindOpenParams(pattern, got ast.Type, tpSet map[string]bool, sub map[string]ast.Type) {
	switch p := pattern.(type) {
	case ast.ParamType:
		if _, done := sub[p.Name]; tpSet[p.Name] && !done && got != nil {
			sub[p.Name] = got
		}
	case ast.StructType:
		if g, ok := got.(ast.StructType); ok && g.Name == p.Name && len(g.Args) == len(p.Args) {
			for i := range p.Args {
				bindOpenParams(p.Args[i], g.Args[i], tpSet, sub)
			}
		}
	case ast.EnumType:
		if g, ok := got.(ast.EnumType); ok && g.Name == p.Name && len(g.Args) == len(p.Args) {
			for i := range p.Args {
				bindOpenParams(p.Args[i], g.Args[i], tpSet, sub)
			}
		}
	case ast.ArrayType:
		if g, ok := got.(ast.ArrayType); ok {
			bindOpenParams(p.Elem, g.Elem, tpSet, sub)
		}
	case ast.TupleType:
		if g, ok := got.(ast.TupleType); ok && len(g.Elems) == len(p.Elems) {
			for i := range p.Elems {
				bindOpenParams(p.Elems[i], g.Elems[i], tpSet, sub)
			}
		}
	}
}
