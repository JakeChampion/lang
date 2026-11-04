package checker

import (
	"github.com/jakechampion/lang/internal/syntax/ast"
)

// A `send` parameter is a worker boundary: its argument is copied into
// another heap, so the argument's type, and a closure argument's captures
// and result, must be send-safe (E100). compiler/checker.fern's
// send_guard_call holds the same rule.
func (c *checker) guardSendArg(arg ast.Expr, pname string, at ast.Type) {
	if lam, ok := arg.(*ast.Lambda); ok {
		for _, capture := range lam.Captures {
			if why := c.sendUnsafeReason(capture.Type, map[string]bool{}); why != "" {
				c.errfCode(arg.Pos(), "E100", "closure passed to `send` parameter %q captures %q of type %s, which cannot cross a worker boundary: %s", pname, capture.Name, capture.Type, why)
			}
		}
		if ft, ok := at.(*ast.FuncType); ok && ft.Result != nil {
			if why := c.sendUnsafeReason(ft.Result, map[string]bool{}); why != "" {
				c.errfCode(arg.Pos(), "E100", "closure passed to `send` parameter %q returns %s, which cannot cross a worker boundary: %s", pname, ft.Result, why)
			}
		}
		return
	}
	if why := c.sendUnsafeReason(at, map[string]bool{}); why != "" {
		c.errfCode(arg.Pos(), "E100", "argument to `send` parameter %q has type %s, which cannot cross a worker boundary: %s", pname, at, why)
	}
}

// sendUnsafeReason is why a value of type t cannot be copied into another
// worker's heap, or "" when every part of it can. A name in seen is on the
// path above and holds nothing its parent did not refuse.
func (c *checker) sendUnsafeReason(t ast.Type, seen map[string]bool) string {
	switch v := t.(type) {
	case ast.StrType, ast.SliceType:
		return "a view borrows another value's storage"
	case ast.ArrayType:
		return c.sendUnsafeReason(v.Elem, seen)
	case ast.StreamType:
		return c.sendUnsafeReason(v.Elem, seen)
	case ast.TupleType:
		return c.sendUnsafeReasonList(v.Elems, seen)
	case *ast.FuncType:
		return "a function value carries its environment"
	case ast.DynTraitType:
		return "a dyn value hides its concrete type"
	case ast.HandleType:
		return "a resource handle is not copied"
	case ast.ParamType:
		// Inside the standard library a generic body copies a value its
		// caller's `send` argument already admitted.
		if c.inStdlibContext() {
			return ""
		}
		return "type parameter " + v.Name + " is not known to be send-safe"
	case ast.SelfType:
		return "type parameter Self is not known to be send-safe"
	case ast.ProjType:
		return "type parameter " + v.String() + " is not known to be send-safe"
	case ast.StructType:
		return c.sendUnsafeStructReason(v, seen)
	case ast.EnumType:
		return c.sendUnsafeEnumReason(v, seen)
	}
	return ""
}

func (c *checker) sendUnsafeStructReason(v ast.StructType, seen map[string]bool) string {
	if v.Name == "Cell" {
		return "a Cell is shared by aliasing"
	}
	if builtinHandle(v.Name) {
		return "a built-in handle owns a descriptor"
	}
	if seen[v.Name] {
		return ""
	}
	if sd, ok := c.info.Structs[v.Name]; ok && sd.MustConsume {
		return "a @must_consume value carries an obligation that a copy would double"
	}
	seen[v.Name] = true
	defer delete(seen, v.Name)
	if why := c.sendUnsafeReasonList(v.Args, seen); why != "" {
		return why
	}
	sd, ok := c.info.Structs[v.Name]
	if !ok {
		return ""
	}
	sub := declArgs(sd.TypeParams, v.Args)
	for _, f := range sd.Fields {
		if why := c.sendUnsafeReason(substituteType(f.Type, sub), seen); why != "" {
			return why
		}
	}
	return ""
}

func (c *checker) sendUnsafeEnumReason(v ast.EnumType, seen map[string]bool) string {
	if seen[v.Name] {
		return ""
	}
	if ed, ok := c.info.Enums[v.Name]; ok && ed.MustConsume {
		return "a @must_consume value carries an obligation that a copy would double"
	}
	seen[v.Name] = true
	defer delete(seen, v.Name)
	if why := c.sendUnsafeReasonList(v.Args, seen); why != "" {
		return why
	}
	ed, ok := c.info.Enums[v.Name]
	if !ok {
		return ""
	}
	sub := declArgs(ed.TypeParams, v.Args)
	for _, variant := range ed.Variants {
		for _, pt := range variant.Payloads {
			if why := c.sendUnsafeReason(substituteType(pt, sub), seen); why != "" {
				return why
			}
		}
	}
	return ""
}

func (c *checker) sendUnsafeReasonList(ts []ast.Type, seen map[string]bool) string {
	for _, t := range ts {
		if why := c.sendUnsafeReason(t, seen); why != "" {
			return why
		}
	}
	return ""
}
