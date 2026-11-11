package checker

import "github.com/jakechampion/lang/internal/syntax/ast"

// Local callback aliases retain the resource captures of their definition.
// Reassigned and shadowed bindings need a separate flow proof.
func (c *checker) localCallbackCaptures(fn *ast.FuncDecl) (map[string][]ast.Param, map[*ast.Lambda][]ast.Param) {
	definitions := map[string][]ast.Expr{}
	var lambdas []*ast.Lambda
	add := func(name string, value ast.Expr) {
		if name != "" && name != "_" {
			definitions[name] = append(definitions[name], value)
		}
	}
	params := func(ps []ast.Param) {
		for _, p := range ps {
			add(p.Name, nil)
		}
	}
	params(fn.Params)
	ast.Walk(fn.Body, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.Var:
			add(n.Name, n.Init)
		case *ast.Assign:
			if id, ok := n.Target.(*ast.Ident); ok {
				add(id.Name, n.Value)
			}
		case *ast.Destructure:
			for _, name := range n.Names {
				add(name, nil)
			}
		case *ast.ForEach:
			add(n.Var, nil)
		case *ast.Match:
			for _, arm := range n.Arms {
				for _, name := range arm.Binders() {
					add(name, nil)
				}
			}
		case *ast.MatchExpr:
			for _, arm := range n.Arms {
				for _, name := range arm.Binders() {
					add(name, nil)
				}
			}
		case *ast.Lambda:
			lambdas = append(lambdas, n)
			params(n.Params)
		case *ast.FuncDecl:
			add(n.Name, &ast.Lambda{Captures: n.Captures})
			params(n.Params)
		}
		return true
	})
	var resolve func(string, map[string]bool) []ast.Param
	var captures func(*ast.Lambda, map[string]bool) []ast.Param
	captures = func(value *ast.Lambda, active map[string]bool) []ast.Param {
		var out []ast.Param
		seen := map[string]bool{}
		for _, capture := range value.Captures {
			if len(definitions[capture.Name]) > 1 || c.copyableCallResult(capture.Type) {
				continue
			}
			for _, source := range append([]ast.Param{capture}, resolve(capture.Name, active)...) {
				if !seen[source.Name] {
					seen[source.Name] = true
					out = append(out, source)
				}
			}
		}
		return out
	}
	resolve = func(name string, active map[string]bool) []ast.Param {
		values := definitions[name]
		if active[name] || len(values) != 1 {
			return nil
		}
		active[name] = true
		defer delete(active, name)
		switch value := values[0].(type) {
		case *ast.Ident:
			return resolve(value.Name, active)
		case *ast.Lambda:
			return captures(value, active)
		}
		return nil
	}
	out := map[string][]ast.Param{}
	for name := range definitions {
		if captures := resolve(name, map[string]bool{}); len(captures) > 0 {
			out[name] = captures
		}
	}
	inline := map[*ast.Lambda][]ast.Param{}
	for _, lambda := range lambdas {
		inline[lambda] = captures(lambda, map[string]bool{})
	}
	return out, inline
}
