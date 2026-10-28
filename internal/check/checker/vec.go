package checker

import (
	"sort"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

// `Vec[N, T]` is the second spelling of a lane type. `Vec[2, f64]` is f64x2
// and `Vec[4, f32]` is f32x4. A `Vec` whose arguments name a type parameter
// is a lane type that each instance picks.

var vecTable = []ast.LanesType{{Width: 64, Spelling: "f64x2"}, {Width: 32, Spelling: "f32x4"}}

// vecLanes is the lane type that `Vec[n, elem]` names on the portable tier.
func vecLanes(args []ast.Type) (ast.LanesType, bool) {
	if len(args) != 2 {
		return ast.LanesType{}, false
	}
	n, nok := args[0].(ast.ConstArg)
	f, fok := args[1].(ast.FloatType)
	if !nok || !fok {
		return ast.LanesType{}, false
	}
	for _, l := range vecTable {
		if n.Value == int64(l.Lanes()) && f.NormalWidth() == l.Width {
			return l, true
		}
	}
	return ast.LanesType{}, false
}

// vecLaneTypes are the lane types that a `Vec[N, T]` can stand for.
func vecLaneTypes(args []ast.Type) []ast.LanesType {
	var out []ast.LanesType
	for _, l := range vecTable {
		if vecArgFits(args[0], ast.ConstArg{Value: int64(l.Lanes())}) && vecArgFits(args[1], ast.FloatType{Width: l.Width}) {
			out = append(out, l)
		}
	}
	return out
}

func vecArgFits(a, want ast.Type) bool {
	switch x := a.(type) {
	case ast.ParamType:
		return true
	case ast.ConstArg:
		w, ok := want.(ast.ConstArg)
		return ok && w.Value == x.Value
	case ast.FloatType:
		w, ok := want.(ast.FloatType)
		return ok && w.NormalWidth() == x.NormalWidth()
	}
	return false
}

// isBuiltinVec is a `Vec` with two arguments where no declared type is named Vec.
func (c *checker) isBuiltinVec(name string, args []ast.Type) bool {
	if name != "Vec" || len(args) != 2 {
		return false
	}
	_, s := c.info.Structs[name]
	_, e := c.info.Enums[name]
	_, t := c.info.Traits[name]
	return !s && !e && !t
}

// genericVec is the lane types a `Vec[N, T]` that names a type parameter can
// stand for.
func (c *checker) genericVec(t ast.Type) ([]ast.LanesType, bool) {
	st, ok := t.(ast.StructType)
	if !ok || !c.isBuiltinVec(st.Name, st.Args) || !containsParamType(st) {
		return nil, false
	}
	return vecLaneTypes(st.Args), true
}

// vecTraits are the traits that every lane type in lanes implements.
func (c *checker) vecTraits(lanes []ast.LanesType) []string {
	var out []string
	for trait, types := range c.info.Impls {
		all := len(lanes) > 0
		for _, l := range lanes {
			if !types[l.Spelling] {
				all = false
			}
		}
		if all {
			out = append(out, trait)
		}
	}
	sort.Strings(out)
	return out
}

// resolveTraitMethodForVec finds a method of a generic `Vec` among the traits
// that every lane type it can stand for implements.
func (c *checker) resolveTraitMethodForVec(lanes []ast.LanesType, self ast.Type, field string) (ast.TraitMethod, string, bool) {
	names := c.vecTraits(lanes)
	return c.traitMethodIn(c.expandTraitRequirements(names, make([][]ast.Type, len(names)), self), field)
}
