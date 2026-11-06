package checker

import "github.com/jakechampion/lang/internal/syntax/ast"

// An open local is an unannotated local whose type holds a generic enum
// with no type arguments, such as `let e = Empty;`, or a map with no type
// arguments, such as `let m = Map { };`. Its first use at a known type fills the arguments in.

func (c *checker) holdsOpenEnum(t ast.Type) bool {
	switch x := t.(type) {
	case ast.EnumType:
		if len(x.Args) == 0 {
			ed := c.info.Enums[x.Name]
			return ed != nil && len(ed.TypeParams) > 0
		}
		for _, a := range x.Args {
			if _, open := a.(ast.UnboundType); open || c.holdsOpenEnum(a) {
				return true
			}
		}
	case ast.StructType:
		if len(x.Args) == 0 {
			return x.Name == "Map"
		}
		for _, a := range x.Args {
			if c.holdsOpenEnum(a) {
				return true
			}
		}
	case ast.ArrayType:
		return c.holdsOpenEnum(x.Elem)
	case ast.TupleType:
		for _, el := range x.Elems {
			if c.holdsOpenEnum(el) {
				return true
			}
		}
	}
	return false
}

// fillOpenEnums gives each open enum in t the arguments that hint has at
// the same place. It reports false when the two types do not have the same
// shape.
func (c *checker) fillOpenEnums(t, hint ast.Type) (ast.Type, bool) {
	switch x := t.(type) {
	case ast.EnumType:
		h, ok := hint.(ast.EnumType)
		if !ok || h.Name != x.Name {
			return t, false
		}
		if len(x.Args) == 0 {
			return h, len(h.Args) > 0 && !c.holdsOpenEnum(h)
		}
		if len(h.Args) != len(x.Args) {
			return t, false
		}
		args := make([]ast.Type, len(x.Args))
		for i, a := range x.Args {
			if _, open := a.(ast.UnboundType); open {
				args[i] = h.Args[i]
				continue
			}
			filled, ok := c.fillOpenEnums(a, h.Args[i])
			if !ok {
				return t, false
			}
			args[i] = filled
		}
		return ast.EnumType{Name: x.Name, Args: args}, true
	case ast.StructType:
		h, ok := hint.(ast.StructType)
		if !ok || h.Name != x.Name {
			return t, false
		}
		if len(x.Args) == 0 {
			return h, len(h.Args) > 0 && !c.holdsOpenEnum(h)
		}
		if len(h.Args) != len(x.Args) {
			return t, false
		}
		args := make([]ast.Type, len(x.Args))
		for i, a := range x.Args {
			filled, ok := c.fillOpenEnums(a, h.Args[i])
			if !ok {
				return t, false
			}
			args[i] = filled
		}
		return ast.StructType{Name: x.Name, Args: args}, true
	case ast.ArrayType:
		h, ok := hint.(ast.ArrayType)
		if !ok {
			return t, false
		}
		elem, ok := c.fillOpenEnums(x.Elem, h.Elem)
		return ast.ArrayType{Elem: elem}, ok
	case ast.TupleType:
		h, ok := hint.(ast.TupleType)
		if !ok || len(h.Elems) != len(x.Elems) {
			return t, false
		}
		elems := make([]ast.Type, len(x.Elems))
		for i, el := range x.Elems {
			filled, ok := c.fillOpenEnums(el, h.Elems[i])
			if !ok {
				return t, false
			}
			elems[i] = filled
		}
		return ast.TupleType{Elems: elems}, true
	}
	return t, true
}

func (c *checker) beginOpenEnumLocal(decl *ast.Var, s *scope) {
	if !c.holdsOpenEnum(decl.Type) {
		return
	}
	if c.openEnumLocals == nil {
		c.openEnumLocals = map[*ast.Var]*scope{}
	}
	if c.litCompositeReads == nil {
		c.litCompositeReads = map[*ast.Ident]*ast.Var{}
	}
	c.openEnumLocals[decl] = s
}

// settleOpenEnumLocal fills the open local decl from hint.
func (c *checker) settleOpenEnumLocal(decl *ast.Var, hint ast.Type) {
	s := c.openEnumLocals[decl]
	if s == nil {
		return
	}
	filled, ok := c.fillOpenEnums(decl.Type, hint)
	if !ok || c.holdsOpenEnum(filled) || c.namesForeignParam(filled) {
		return
	}
	delete(c.openEnumLocals, decl)
	if c.openPassedOver[decl] {
		c.info.recheckOpenLocals = true
	}
	decl.Type = filled
	c.info.VarTypes[decl] = filled
	s.set(decl.Name, filled)
	c.settleNumeric(decl.Init, filled)
}

func emptyMapLit(e ast.Expr) bool {
	ml, ok := e.(*ast.MapLit)
	return ok && len(ml.Entries) == 0
}

func bareMap(t ast.Type) bool {
	st, ok := t.(ast.StructType)
	return ok && st.Name == "Map" && len(st.Args) == 0
}

func (c *checker) errMapNeedsType(decl *ast.Var) {
	c.errfCode(decl.P, "E020", "map %q needs a type annotation: no use gives the types of its keys and values", decl.Name)
}

// finishOpenEnumLocals ends a function body: a map that no use gave a type
// is an error at its `let`.
func (c *checker) finishOpenEnumLocals() {
	for decl := range c.openEnumLocals {
		if bareMap(decl.Type) {
			c.errMapNeedsType(decl)
		}
	}
	c.openEnumLocals = nil
	c.openPassedOver = nil
}

// namesForeignParam is true when t names a type parameter that the function
// being checked does not declare, such as the K of a Map method.
func (c *checker) namesForeignParam(t ast.Type) bool {
	own := map[string]bool{}
	foreign := false
	var walk func(t ast.Type)
	collect := false
	walk = func(t ast.Type) {
		switch x := t.(type) {
		case ast.ParamType:
			if collect {
				own[x.Name] = true
			} else {
				foreign = foreign || !own[x.Name]
			}
		case ast.EnumType:
			for _, a := range x.Args {
				walk(a)
			}
		case ast.StructType:
			for _, a := range x.Args {
				walk(a)
			}
		case ast.ArrayType:
			walk(x.Elem)
		case ast.TupleType:
			for _, el := range x.Elems {
				walk(el)
			}
		}
	}
	if c.current != nil {
		for _, tp := range c.current.TypeParams {
			own[tp] = true
		}
		if c.current.Receiver != nil {
			collect = true
			walk(c.current.Receiver.Type)
			collect = false
		}
	}
	walk(t)
	return foreign
}

// quietOpenReceiver passes over a method call on a map local that no use has
// given a type yet: the call adds no error, and a later use can still give
// the type. The program is then checked again with that type.
func (c *checker) quietOpenReceiver(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	if !ok {
		return false
	}
	decl := c.litCompositeReads[id]
	if c.openEnumLocals[decl] == nil || !bareMap(decl.Type) {
		return false
	}
	if c.openPassedOver == nil {
		c.openPassedOver = map[*ast.Var]bool{}
	}
	c.openPassedOver[decl] = true
	return true
}

// settleOpenMapByCall gives an open map local the types of the key and the
// value that a call of insert or get_or passes to it.
func (c *checker) settleOpenMapByCall(call *ast.Call, fa *ast.FieldAccess, s *scope) {
	id, ok := fa.Target.(*ast.Ident)
	if !ok || len(call.Args) != 2 || fa.Field != "insert" && fa.Field != "set" && fa.Field != "get_or" {
		return
	}
	decl := s.lookupVarDecl(id.Name)
	if decl == nil || c.openEnumLocals[decl] == nil || !bareMap(decl.Type) {
		return
	}
	k := settledLiteralType(c.checkExpr(call.Args[0], s))
	v := settledLiteralType(c.checkExpr(call.Args[1], s))
	if k == nil || v == nil {
		return
	}
	c.settleOpenEnumLocal(decl, ast.StructType{Name: "Map", Args: []ast.Type{k, v}})
}

// settledLiteralType is the type an untyped literal settles to by default.
func settledLiteralType(t ast.Type) ast.Type {
	switch x := t.(type) {
	case ast.NumberType:
		if x.Polymorphic {
			return ast.NumberType{Width: 32, Signed: true}
		}
	case ast.FloatType:
		if x.Polymorphic {
			return ast.FloatType{Width: 64}
		}
	}
	return t
}
