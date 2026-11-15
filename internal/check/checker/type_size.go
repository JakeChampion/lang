package checker

import "github.com/jakechampion/lang/internal/syntax/ast"

// maxTypeNodes is the limit of the types in the type of a call or of a tuple.
// A generic call that holds its argument two times doubles the type at each
// level.
const maxTypeNodes = 4096

// maxTypeDepth is the limit of the levels of nesting in a written type, and in
// the type of a call, of a tuple or of an array literal. The work on a type
// grows faster than its depth.
const maxTypeDepth = 64

// boundedType is t, or nil with an E106 when e is a call, a tuple or an array
// literal and t is over a limit. A nil type stops the growth. A value that
// holds e is over the limit too, and gets no diagnostic for it.
func (c *checker) boundedType(e ast.Expr, t ast.Type) ast.Type {
	var operands []ast.Expr
	switch x := e.(type) {
	case *ast.Call:
		operands = x.Args
	case *ast.TupleLit:
		operands = x.Elems
	case *ast.ArrayLit:
		operands = x.Elems
	default:
		return t
	}
	for _, operand := range operands {
		if c.overLimit[operand] {
			c.overLimit[e] = true
			return nil
		}
	}
	switch {
	case typeDepth(t, maxTypeDepth) > maxTypeDepth:
		c.errfCode(e.Pos(), "E106", "type nests deeper than %d levels", maxTypeDepth)
	case typeNodes(t, maxTypeNodes) > maxTypeNodes:
		c.errfCode(e.Pos(), "E106", "type has more than %d parts", maxTypeNodes)
	default:
		return t
	}
	if c.overLimit == nil {
		c.overLimit = map[ast.Expr]bool{}
	}
	c.overLimit[e] = true
	return nil
}

// typeDepth is the levels of nesting in t: `i32` has one and `Option[i32][]`
// has three. The count stops above limit.
func typeDepth(t ast.Type, limit int) int {
	if limit <= 0 {
		return 1
	}
	most := 0
	for _, child := range typeChildren(t) {
		if d := typeDepth(child, limit-1); d > most {
			most = d
		}
	}
	return most + 1
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

// MaxInstanceDepth is the limit of the levels of type arguments in an instance
// of a generic function. A function that calls itself with a larger type has
// no last instance.
const MaxInstanceDepth = 64

// TypeArgsDepth is the levels of type arguments in t: `W[W[i32]]` has two.
// An array has one level at the least, as its spelling has.
func TypeArgsDepth(t ast.Type) int {
	most := 0
	for _, child := range typeChildren(t) {
		if d := TypeArgsDepth(child); d > most {
			most = d
		}
	}
	switch t.(type) {
	case ast.StructType, ast.EnumType, ast.SliceType:
		if len(typeChildren(t)) > 0 {
			return most + 1
		}
	case ast.ArrayType:
		if most == 0 {
			return 1
		}
	}
	return most
}

// InstanceDepthError is the E107 of a generic function with an instance at
// the limit.
func InstanceDepthError(info *Info, fn *ast.FuncDecl) error {
	c := &checker{info: info}
	c.errfCode(fn.P, "E107", "generic function %q has an instance with %d levels of type arguments: a call in its body gives it a larger type each time", boundSite(fn), MaxInstanceDepth)
	return c.errors[0]
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
