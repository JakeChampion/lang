package checker

import "github.com/jakechampion/lang/internal/syntax/ast"

// Local callback values retain the resource captures of their definition.
// Reassigned and shadowed bindings need a separate flow proof.
func (c *checker) localCallbackCaptures(fn *ast.FuncDecl) (map[string][]ast.Param, map[ast.Expr][]ast.Param) {
	c.computeCallbackFactories()
	return c.callbackCaptureFacts(fn, false)
}

// A factory summary names the inputs retained by its returned callback.
// Owning a callback moves its value, but does not end its environment loans.
func (c *checker) computeCallbackFactories() {
	if c.callbackFactories != nil {
		return
	}
	c.callbackFactories = map[string][]ast.Param{}
	changed := true
	for changed {
		changed = false
		for _, fn := range c.callbackFactoryFuncs {
			switch fn.ReturnType.(type) {
			case *ast.FuncType, ast.ParamType:
			default:
				continue
			}
			_, values := c.callbackCaptureFacts(fn, true)
			var captures []ast.Param
			ast.Walk(fn.Body, func(n ast.Node) bool {
				switch node := n.(type) {
				case *ast.Lambda, *ast.FuncDecl:
					return false
				case *ast.Return:
					captures = mergeCallbackCaptures(captures, values[node.Value])
				}
				return true
			})
			var sources []ast.Param
			for _, param := range fn.Params {
				for _, capture := range captures {
					if capture.Name == param.Name {
						sources = append(sources, param)
						break
					}
				}
			}
			if len(sources) > len(c.callbackFactories[fn.Name]) {
				c.callbackFactories[fn.Name] = sources
				changed = true
			}
		}
	}
}

func (c *checker) callbackCaptureFacts(fn *ast.FuncDecl, summarize bool) (map[string][]ast.Param, map[ast.Expr][]ast.Param) {
	definitions := map[string][]ast.Expr{}
	bindingTypes := map[string]ast.Type{}
	for _, param := range fn.Params {
		bindingTypes[param.Name] = param.Type
	}
	var values []ast.Expr
	roots := map[string]ast.Param{}
	if summarize {
		for _, param := range fn.Params {
			roots[param.Name] = param
		}
	}
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
		case *ast.Return:
			values = append(values, n.Value)
		case *ast.Call:
			values = append(values, n)
		case *ast.Var:
			add(n.Name, n.Init)
			bindingTypes[n.Name] = c.info.VarTypes[n]
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
		case *ast.Call:
			if !c.callbackResultCalls[v] {
				return nil
			}
			id, named := v.Callee.(*ast.Ident)
			if c.callValueCallees[v] || named && id.Local {
				out := valueCaptures(v.Callee, active)
				flags := c.callOwnFlags[v]
				for i, arg := range v.Args {
					out = mergeCallbackCaptures(out, valueCaptures(arg, active))
					if input, ok := arg.(*ast.Ident); ok && input.Local && (i >= len(flags) || !flags[i]) && !c.copyableCallResult(bindingTypes[input.Name]) {
						out = mergeCallbackCaptures(out, []ast.Param{{Name: input.Name, Type: bindingTypes[input.Name]}})
					}
				}
				return out
			}
			if !named {
				return nil
			}
			factory := c.callbackFactoryFuncs[id.Name]
			if factory == nil {
				return nil
			}
			var out []ast.Param
			for i, param := range factory.Params {
				if i >= len(v.Args) {
					break
				}
				for _, source := range c.callbackFactories[id.Name] {
					if source.Name != param.Name {
						continue
					}
					arg := v.Args[i]
					out = mergeCallbackCaptures(out, valueCaptures(arg, active))
					if name, ok := arg.(*ast.Ident); ok && !param.Own && !c.copyableCallResult(param.Type) {
						out = mergeCallbackCaptures(out, []ast.Param{{Name: name.Name, Type: param.Type}})
					}
				}
			}
			return out
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
		if root, ok := roots[name]; ok {
			return []ast.Param{root}
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
