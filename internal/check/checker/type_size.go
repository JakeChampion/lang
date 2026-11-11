package checker

import "github.com/jakechampion/lang/internal/syntax/ast"

// maxTypeNodes is the limit of the types in the type of a call or of a tuple.
// A generic call that holds its argument two times doubles the type at each
// level.
const maxTypeNodes = 4096

// boundedType is t, or nil with an E106 when e is a call or a tuple and t has
// more types than the limit. A nil type stops the growth, and the values that
// hold e get no diagnostic for it.
func (c *checker) boundedType(e ast.Expr, t ast.Type) ast.Type {
	switch e.(type) {
	case *ast.Call, *ast.TupleLit:
	default:
		return t
	}
	if typeNodes(t, maxTypeNodes) <= maxTypeNodes {
		return t
	}
	c.errfCode(e.Pos(), "E106", "type has more than %d parts", maxTypeNodes)
	return nil
}

// typeNodes is the number of types in t, t included. The count stops above
// limit.
func typeNodes(t ast.Type, limit int) int {
	n := 1
	for _, child := range typeChildren(t) {
		if n > limit {
			return n
		}
		n += typeNodes(child, limit-n)
	}
	return n
}

// typeChildren is the types that t holds directly.
func typeChildren(t ast.Type) []ast.Type {
	switch x := t.(type) {
	case ast.ArrayType:
		return []ast.Type{x.Elem}
	case ast.SliceType:
		return []ast.Type{x.Elem}
	case ast.TupleType:
		return x.Elems
	case ast.StructType:
		return x.Args
	case ast.EnumType:
		return x.Args
	case *ast.FuncType:
		out := make([]ast.Type, 0, len(x.Params)+1)
		for _, p := range x.Params {
			out = append(out, p)
		}
		return append(out, x.Result)
	}
	return nil
}

// anyFailed reports an argument whose check reported an error. The call that
// holds it does not report the type parameter that the argument would bind.
func (c *checker) anyFailed(args []ast.Expr) bool {
	for _, a := range args {
		if c.failedExprs[a] {
			return true
		}
	}
	return false
}
