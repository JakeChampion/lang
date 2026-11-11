package checker

import "github.com/jakechampion/lang/internal/syntax/ast"

// Local callback values retain the resource captures of their definition.
// Reassigned and shadowed bindings need a separate flow proof.
func (c *checker) localCallbackCaptures(fn *ast.FuncDecl) (map[string][]ast.Param, map[ast.Expr][]ast.Param) {
	definitions := map[string][]ast.Expr{}
	var values []ast.Expr
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
			values = append(values, n)
			for _, arm := range n.Arms {
				for _, name := range arm.Binders() {
					add(name, nil)
				}
			}
		case *ast.Lambda:
			values = append(values, n)
			params(n.Params)
		case *ast.IfExpr, *ast.BlockExpr:
			values = append(values, n.(ast.Expr))
		case *ast.FuncDecl:
			add(n.Name, &ast.Lambda{Captures: n.Captures})
			params(n.Params)
		}
		return true
	})
	var resolve func(string, map[string]bool) []ast.Param
	var valueCaptures func(ast.Expr, map[string]bool) []ast.Param
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
	valueCaptures = func(value ast.Expr, active map[string]bool) []ast.Param {
		switch v := value.(type) {
		case *ast.Ident:
			return resolve(v.Name, active)
		case *ast.Lambda:
			return captures(v, active)
		case *ast.IfExpr:
			return mergeCallbackCaptures(valueCaptures(v.Then, active), valueCaptures(v.Else, active))
		case *ast.MatchExpr:
			var out []ast.Param
			for _, arm := range v.Arms {
				out = mergeCallbackCaptures(out, valueCaptures(arm.Body, active))
			}
			return out
		case *ast.BlockExpr:
			return valueCaptures(v.Tail, active)
		}
		return nil
	}
	resolve = func(name string, active map[string]bool) []ast.Param {
		values := definitions[name]
		if active[name] || len(values) != 1 {
			return nil
		}
		active[name] = true
		defer delete(active, name)
		return valueCaptures(values[0], active)
	}
	out := map[string][]ast.Param{}
	for name := range definitions {
		if captures := resolve(name, map[string]bool{}); len(captures) > 0 {
			out[name] = captures
		}
	}
	inline := map[ast.Expr][]ast.Param{}
	for _, value := range values {
		inline[value] = valueCaptures(value, map[string]bool{})
	}
	return out, inline
}

func mergeCallbackCaptures(into, from []ast.Param) []ast.Param {
	seen := map[string]bool{}
	for _, capture := range into {
		seen[capture.Name] = true
	}
	for _, capture := range from {
		if !seen[capture.Name] {
			seen[capture.Name] = true
			into = append(into, capture)
		}
	}
	return into
}
