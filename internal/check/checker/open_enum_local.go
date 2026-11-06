package checker

import "github.com/jakechampion/lang/internal/syntax/ast"

// An open local is an unannotated local whose type holds a generic enum
// with no type arguments, such as `let e = Empty;`. Its first use at a
// known type fills the arguments in.

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
		if !ok || h.Name != x.Name || len(h.Args) != len(x.Args) {
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
	if !ok || c.holdsOpenEnum(filled) {
		return
	}
	delete(c.openEnumLocals, decl)
	decl.Type = filled
	c.info.VarTypes[decl] = filled
	s.set(decl.Name, filled)
	c.settleNumeric(decl.Init, filled)
}
