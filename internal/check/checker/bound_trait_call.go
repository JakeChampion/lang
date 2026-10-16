package checker

import (
	"maps"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

// A bound's signature can itself be generic when a default method declares
// type parameters. Bind those at the call, independently of the receiver's
// already-bound trait arguments.
func (c *checker) checkBoundTraitCall(call *ast.Call, method ast.TraitMethod, params []ast.Param, receiver, expected ast.Type, s *scope) ast.Type {
	sub := map[string]ast.Type{}
	bind := func(want, actual ast.Type) {
		candidate := maps.Clone(sub)
		c.unifyType(want, actual, candidate)
		// Enclosing type parameters (including Self) remain abstract. Only
		// the method's own variables can be inferred by this call.
		for _, tp := range method.TypeParams {
			if value := candidate[tp]; value != nil {
				sub[tp] = value
			}
		}
	}
	if len(call.TypeArgs) > len(method.TypeParams) {
		c.errfCode(call.P, "E040", "%s", arityMsg(method.Name, len(method.TypeParams), len(call.TypeArgs), "type argument"))
	}
	for i, arg := range call.TypeArgs {
		if i < len(method.TypeParams) {
			sub[method.TypeParams[i]] = arg
		}
	}
	types := make([]ast.Type, len(call.Args))
	for i, arg := range call.Args {
		types[i] = c.checkExpr(arg, s)
		want := ast.SubstSelf(params[i].Type, receiver)
		if len(method.TypeParams) > 0 {
			bind(want, types[i])
		}
	}
	result := ast.SubstSelf(method.Result, receiver)
	if len(method.TypeParams) > 0 && expected != nil {
		bind(result, expected)
	}
	for i, arg := range call.Args {
		want := substituteType(ast.SubstSelf(params[i].Type, receiver), sub)
		if types[i] != nil && !c.argOK(&call.Args[i], want, types[i], params[i].Own) {
			c.errfCode(arg.Pos(), "E038", "argument %d to %q: expected %s, got %s", i+1, method.Name, want, types[i])
		}
	}
	if len(method.TypeParams) > 0 {
		args := make([]ast.Type, len(method.TypeParams))
		complete := true
		for i, tp := range method.TypeParams {
			args[i] = sub[tp]
			if args[i] == nil {
				complete = false
				c.errfCode(call.P, "E040", "could not infer type parameter %s for %s", tp, method.Name)
			}
		}
		if complete {
			decl := &ast.FuncDecl{Name: method.Name, TypeParams: method.TypeParams, Bounds: method.Bounds, BoundArgs: method.BoundArgs}
			c.checkTypeArgBounds(decl, args, sub, c.typeParamsInScope(), call.P)
		}
	}
	return substituteType(result, sub)
}
