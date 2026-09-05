// Package defaultargs resolves call arguments against the callee's declared
// parameters before type-checking: it reorders named arguments
// (`f(b = 2, a = 1)`) into positional order and fills omitted parameters from
// their default values (`function f(a, b = 128)` called `f(5)` → `f(5, 128)`).
// It runs at the start of the checker, so the checker and every later pass
// (monomorph, codegen) see a complete positional `Args` list and need no
// knowledge of defaults or names.
//
// Only direct calls to a named free function are resolved (a bare identifier
// callee); methods and indirect calls are out of scope (a named argument on
// one is an error). For a purely positional call, only trailing defaults are
// filled — a genuinely missing required argument is left for the checker's
// arity error (E004), preserving prior behaviour.
//
// A default value must be a CONSTANT EXPRESSION (E076). Filling copies the
// expression into the call site, so a name inside it would resolve in the
// caller's scope rather than the callee's; requiring the expression to carry
// no free names is what makes the copy sound. Top-level consts are folded to
// literals before this pass, so `= SOME_CONST` still works.
package defaultargs

import (
	"fmt"

	"github.com/jakechampion/lang/internal/ast"
)

// Error is a resolution diagnostic the checker surfaces.
type Error struct {
	Pos  ast.Position
	Code string
	Msg  string
}

// freeIdents reports the identifiers a default-value expression reads from
// its surroundings. A default is pasted into the CALL SITE, so any name it
// carries is resolved in the caller's scope rather than the callee's: a
// default of `a * 2` read the CALLER's `a`, and one naming a module
// function silently picked up a caller local of the same name instead
// (#8445). Requiring defaults to be self-contained removes the question —
// there is nothing left to resolve in the wrong place.
//
// Top-level consts are folded to literals before this pass runs, so
// `= SOME_CONST` is unaffected.
func freeIdents(e ast.Expr) []*ast.Ident {
	var out []*ast.Ident
	switch x := e.(type) {
	case nil:
		return nil
	case *ast.Ident:
		out = append(out, x)
	case *ast.Unary:
		out = append(out, freeIdents(x.Operand)...)
	case *ast.Binary:
		out = append(out, freeIdents(x.Left)...)
		out = append(out, freeIdents(x.Right)...)
	case *ast.Call:
		out = append(out, freeIdents(x.Callee)...)
		for _, a := range x.Args {
			out = append(out, freeIdents(a)...)
		}
	}
	return out
}

// checkDefaults rejects a default expression that is not self-contained.
// Reported at the declaration, which is where the author can act on it —
// the call site that triggers the fill may be in another module and did
// nothing wrong.
func checkDefaults(p *ast.Program) []Error {
	var errs []Error
	for _, f := range p.Funcs {
		for _, pa := range f.Params {
			if pa.Default == nil {
				continue
			}
			for _, id := range freeIdents(pa.Default) {
				errs = append(errs, Error{id.P, "E076", fmt.Sprintf(
					"default value for parameter %q reads %q: a default must be a constant expression, because it is evaluated at each call site rather than inside %s",
					pa.Name, id.Name, f.Name)})
			}
		}
	}
	return errs
}

// Fill rewrites p in place and returns any resolution errors.
func Fill(p *ast.Program) []Error {
	if p == nil {
		return nil
	}
	// Validate before filling: a default carrying a free name must not be
	// pasted anywhere, since the paste is what resolves it in the wrong
	// scope.
	if errs := checkDefaults(p); len(errs) > 0 {
		return errs
	}
	// name -> declared params (with defaults). Methods are excluded: their
	// call sites are field-access callees, not bare identifiers.
	funcs := map[string][]ast.Param{}
	for _, f := range p.Funcs {
		if f.Receiver == nil {
			funcs[f.Name] = f.Params
		}
	}
	var errs []Error
	ast.RewriteProgramExprs(p, func(e ast.Expr) ast.Expr {
		call, ok := e.(*ast.Call)
		if !ok {
			return e
		}
		hasNamed := false
		for _, n := range call.ArgNames {
			if n != "" {
				hasNamed = true
				break
			}
		}
		id, isIdent := call.Callee.(*ast.Ident)

		if !hasNamed {
			// Purely positional — fill trailing defaults only.
			if !isIdent {
				return e
			}
			params, known := funcs[id.Name]
			if !known || len(call.Args) >= len(params) {
				return e
			}
			for i := len(call.Args); i < len(params); i++ {
				if params[i].Default == nil {
					return e // a required arg is missing; leave for E004
				}
			}
			for i := len(call.Args); i < len(params); i++ {
				call.Args = append(call.Args, cloneExpr(params[i].Default))
			}
			return e
		}

		// Named arguments are present.
		if !isIdent {
			errs = append(errs, Error{call.P, "E077", "named arguments are only supported on direct calls to named functions"})
			return e
		}
		params, known := funcs[id.Name]
		if !known {
			errs = append(errs, Error{call.P, "E077", fmt.Sprintf("named arguments are not supported for call to %q", id.Name)})
			return e
		}
		result := make([]ast.Expr, len(params))
		filled := make([]bool, len(params))
		pos := 0
		seenNamed := false
		for i, a := range call.Args {
			name := ""
			if i < len(call.ArgNames) {
				name = call.ArgNames[i]
			}
			if name == "" {
				if seenNamed {
					errs = append(errs, Error{call.P, "E077", "positional argument after named argument"})
					return e
				}
				if pos >= len(params) {
					errs = append(errs, Error{call.P, "E004", fmt.Sprintf("function %q expects %d argument(s), got more", id.Name, len(params))})
					return e
				}
				result[pos] = a
				filled[pos] = true
				pos++
			} else {
				seenNamed = true
				idx := paramIndex(params, name)
				if idx < 0 {
					errs = append(errs, Error{call.P, "E077", fmt.Sprintf("%q has no parameter named %q", id.Name, name)})
					return e
				}
				if filled[idx] {
					errs = append(errs, Error{call.P, "E077", fmt.Sprintf("duplicate argument for parameter %q", name)})
					return e
				}
				result[idx] = a
				filled[idx] = true
			}
		}
		// Fill the rest from defaults; a still-unfilled required param is an error.
		for i := range params {
			if filled[i] {
				continue
			}
			if params[i].Default != nil {
				result[i] = cloneExpr(params[i].Default)
			} else {
				errs = append(errs, Error{call.P, "E004", fmt.Sprintf("missing argument for parameter %q of %q", params[i].Name, id.Name)})
				return e
			}
		}
		call.Args = result
		call.ArgNames = nil
		return e
	})
	return errs
}

func paramIndex(params []ast.Param, name string) int {
	for i := range params {
		if params[i].Name == name {
			return i
		}
	}
	return -1
}

// cloneExpr makes a fresh copy of a default-value expression so distinct call
// sites don't share a node (later passes stamp type/width info onto nodes in
// place). Covers the shapes a default realistically takes; anything else is
// returned as-is.
func cloneExpr(e ast.Expr) ast.Expr {
	switch x := e.(type) {
	case *ast.NumberLit:
		c := *x
		return &c
	case *ast.FloatLit:
		c := *x
		return &c
	case *ast.StringLit:
		c := *x
		return &c
	case *ast.CharLit:
		c := *x
		return &c
	case *ast.BoolLit:
		c := *x
		return &c
	case *ast.UnitLit:
		c := *x
		return &c
	case *ast.Ident:
		c := *x
		return &c
	case *ast.Unary:
		c := *x
		c.Operand = cloneExpr(x.Operand)
		return &c
	case *ast.Binary:
		c := *x
		c.Left = cloneExpr(x.Left)
		c.Right = cloneExpr(x.Right)
		return &c
	case *ast.Call:
		c := *x
		c.Args = make([]ast.Expr, len(x.Args))
		for i, a := range x.Args {
			c.Args[i] = cloneExpr(a)
		}
		return &c
	default:
		return e
	}
}
