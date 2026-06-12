// Package defaultargs fills omitted trailing arguments at call sites from the
// callee's declared default parameter values. It runs after parsing/modload
// and before the checker, so the checker and every later pass (monomorph,
// codegen) see complete positional calls and need no knowledge of defaults.
//
// Only free-function calls (a bare identifier callee) are filled; method
// default-fill is a follow-up. A call is filled only when every omitted
// parameter has a default — a genuinely missing required argument is left for
// the checker to report as an arity error.
package defaultargs

import "github.com/jakechampion/lang/internal/ast"

// Fill rewrites p in place.
func Fill(p *ast.Program) {
	if p == nil {
		return
	}
	// name -> declared params (with defaults). Methods are excluded: their
	// call sites are field-access callees, not bare identifiers, so they
	// never match the lookup below.
	funcs := map[string][]ast.Param{}
	for _, f := range p.Funcs {
		if f.Receiver == nil {
			funcs[f.Name] = f.Params
		}
	}
	ast.RewriteProgramExprs(p, func(e ast.Expr) ast.Expr {
		call, ok := e.(*ast.Call)
		if !ok {
			return e
		}
		id, ok := call.Callee.(*ast.Ident)
		if !ok {
			return e
		}
		params, ok := funcs[id.Name]
		if !ok || len(call.Args) >= len(params) {
			return e
		}
		// Fill only when every omitted parameter has a default; otherwise a
		// required argument is missing — leave it for the checker's E004.
		for i := len(call.Args); i < len(params); i++ {
			if params[i].Default == nil {
				return e
			}
		}
		for i := len(call.Args); i < len(params); i++ {
			call.Args = append(call.Args, cloneExpr(params[i].Default))
		}
		return e
	})
}

// cloneExpr makes a fresh copy of a default-value expression so that distinct
// call sites don't share a node (later passes stamp type/width info onto
// expression nodes in place). It covers the shapes a default realistically
// takes — literals, identifiers (constant references), and unary/binary
// combinations of them; anything else is returned as-is (a rare, deep default
// expression shared across call sites is still type-correct, it just loses the
// per-site annotation isolation).
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
	case *ast.BoolLit:
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
