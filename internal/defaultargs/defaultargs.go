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

// nonConstReason describes the first sub-expression that stops a default from
// being a constant expression, and reports whether one was found.
//
// A default is pasted into the CALL SITE, so anything it reads resolves in the
// caller's scope rather than the callee's: a default of `a * 2` read the
// CALLER's `a`, and one naming a module function silently picked up a caller
// local of the same name instead (#8445).
//
// This is a WHITELIST — a literal, or a unary / binary combination of them —
// rather than a hunt for free identifiers, because the hunt was open by
// construction. It descended into Ident / Unary / Binary / Call and nothing
// else, so a default spelled `config.timeout`, `xs[0]` or `n as i32` carried a
// caller-scope name past the check untouched, which is the exact class the
// check exists to stop. Whitelisting closes it for every shape at once,
// including the ones the AST does not have yet: a node nobody taught this
// function about is refused, not waved through.
//
// Top-level consts are folded to literals before this pass runs, so
// `= SOME_CONST` is unaffected.
func nonConstReason(e ast.Expr) (string, bool) {
	switch x := e.(type) {
	case nil:
		return "", false
	case *ast.NumberLit, *ast.FloatLit, *ast.StringLit, *ast.CharLit, *ast.BoolLit, *ast.UnitLit:
		return "", false
	case *ast.Ident:
		return fmt.Sprintf("reads %q", x.Name), true
	case *ast.Unary:
		return nonConstReason(x.Operand)
	case *ast.Binary:
		if why, bad := nonConstReason(x.Left); bad {
			return why, true
		}
		return nonConstReason(x.Right)
	case *ast.Call:
		// Name the callee when there is one to name: "calls \"size\"" points
		// at the thing to remove, where "is a call" only says it was refused.
		if id, ok := x.Callee.(*ast.Ident); ok {
			return fmt.Sprintf("calls %q", id.Name), true
		}
		return "is a call", true
	default:
		return "is " + describeExpr(e), true
	}
}

// describeExpr names an expression form for the E076 message, so the
// diagnostic says what was written rather than only that it was refused.
func describeExpr(e ast.Expr) string {
	switch e.(type) {
	case *ast.FieldAccess:
		return "a field access"
	case *ast.Index:
		return "an index"
	case *ast.CastExpr:
		return "a cast"
	case *ast.Lambda:
		return "a lambda"
	case *ast.StructLit:
		return "a struct literal"
	case *ast.ArrayLit:
		return "an array literal"
	case *ast.TupleLit:
		return "a tuple literal"
	case *ast.IfExpr:
		return "an `if` expression"
	case *ast.MatchExpr:
		return "a `match` expression"
	}
	return "not a constant expression"
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
			if why, bad := nonConstReason(pa.Default); bad {
				errs = append(errs, Error{pa.Default.Pos(), "E076", fmt.Sprintf(
					"default value for parameter %q %s: a default must be a constant expression, because it is evaluated at each call site rather than inside %s",
					pa.Name, why, f.Name)})
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
