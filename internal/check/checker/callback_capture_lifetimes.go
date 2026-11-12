package checker

import (
	"github.com/jakechampion/lang/internal/syntax/ast"
	"strconv"
)

// Local callback values retain the resource captures of their definition.
// Reassigned and shadowed bindings need a separate flow proof.
func (c *checker) localCallbackCaptures(fn *ast.FuncDecl) (map[string][]ast.Param, map[ast.Expr][]ast.Param) {
	c.computeCallbackFactories()
	names, values := c.callbackCaptureFacts(fn, false)
	out := map[string][]ast.Param{}
	inline := map[ast.Expr][]ast.Param{}
	for name, loans := range names {
		out[name] = callbackLoanParams(loans)
	}
	for value, loans := range values {
		if c.callbackValues[value] {
			inline[value] = callbackLoanParams(loans)
		}
	}
	return out, inline
}

// A factory summary names the inputs retained by its returned callback.
// Owning a callback moves its value, but does not end its environment loans.
func (c *checker) computeCallbackFactories() {
	if c.callbackFactories != nil {
		return
	}
	c.callbackFactories = map[string][]callbackLoan{}
	var factories []*ast.FuncDecl
	containers := c.callbackContainerTypes()
	for _, fn := range c.callbackFactoryFuncs {
		if fn.Body != nil && c.callbackContainer(fn.ReturnType, containers) {
			factories = append(factories, fn)
		}
	}
	changed := true
	for changed {
		changed = false
		for _, fn := range factories {
			_, values := c.callbackCaptureFacts(fn, true)
			var captures []callbackLoan
			ast.Walk(fn.Body, func(n ast.Node) bool {
				switch node := n.(type) {
				case *ast.Lambda, *ast.FuncDecl:
					return false
				case *ast.Return:
					captures = mergeCallbackLoans(captures, values[node.Value])
				}
				return true
			})
			sources := c.callbackFactories[fn.Name]
			for _, capture := range captures {
				for _, param := range fn.Params {
					if capture.param.Name == param.Name {
						var folded, recursive bool
						capture.path, folded = c.boundCallbackPath(param.Type, capture.path)
						capture.result, recursive = c.boundCallbackPath(fn.ReturnType, capture.result)
						capture.wide = capture.wide || folded || recursive
						sources = mergeCallbackLoans(sources, []callbackLoan{capture})
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

func (c *checker) callbackCaptureFacts(fn *ast.FuncDecl, summarize bool) (map[string][]callbackLoan, map[ast.Expr][]callbackLoan) {
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
		case *ast.Call, *ast.FieldAccess, *ast.Index:
			values = append(values, n.(ast.Expr))
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
			add(n.Name, &ast.Lambda{Captures: n.Captures, Params: n.Params, Body: n.Body, ReturnType: n.ReturnType})
			bindingTypes[n.Name] = &ast.FuncType{}
			params(n.Params)
		}
		return true
	})
	var resolve func(string, map[string]bool) []callbackLoan
	var valueCaptures func(ast.Expr, map[string]bool) []callbackLoan
	memo := map[string][]callbackLoan{}
	cyclic := map[string]bool{}
	captures := func(value *ast.Lambda, active map[string]bool) []callbackLoan {
		var out []callbackLoan
		for _, capture := range value.Captures {
			if len(definitions[capture.Name]) > 1 || c.copyableCallResult(capture.Type) {
				continue
			}
			out = mergeCallbackLoans(out, []callbackLoan{{param: capture, direct: true}})
			for _, path := range c.callbackCapturePlaces(value, capture.Name) {
				sources := selectCallbackLoans(resolve(capture.Name, active), path)
				out = mergeCallbackLoans(out, flattenCallbackLoans(sources))
			}
		}
		return out
	}
	valueCaptures = func(value ast.Expr, active map[string]bool) []callbackLoan {
		switch v := value.(type) {
		case *ast.Ident:
			return resolve(v.Name, active)
		case *ast.FieldAccess:
			return projectCallbackLoans(valueCaptures(v.Target, active), callbackFieldSelector(v.Field))
		case *ast.Index:
			return projectCallbackLoans(valueCaptures(v.Array, active), "a:*")
		case *ast.StructLit:
			var out []callbackLoan
			replaced := map[string]bool{}
			var parts []callbackPart
			for _, field := range v.Fields {
				replaced[field.Name] = true
				part := callbackPart{selector: "f:" + field.Name, value: field.Value}
				if id, ok := field.Value.(*ast.Ident); ok && id.Local && len(definitions[id.Name]) == 1 {
					if ty := c.declaredFieldType(c.litTypes[v], field.Name); ty != nil {
						part.group = id.Name + "\x00" + ty.String()
					}
				}
				parts = append(parts, part)
			}
			for _, part := range groupCallbackParts(parts) {
				out = mergeCallbackLoans(out, prefixCallbackLoans(part.selector, valueCaptures(part.value, active)))
			}
			if v.Base != nil {
				for _, field := range c.info.Structs[v.TypeName].Fields {
					if !replaced[field.Name] {
						part := "f:" + field.Name
						out = mergeCallbackLoans(out, prefixCallbackLoans(part, projectCallbackLoans(valueCaptures(v.Base, active), part)))
					}
				}
			}
			return out
		case *ast.TupleLit:
			var out []callbackLoan
			var parts []callbackPart
			for i, elem := range v.Elems {
				part := callbackPart{selector: "t:" + strconv.Itoa(i), value: elem}
				if id, ok := elem.(*ast.Ident); ok && id.Local && len(definitions[id.Name]) == 1 {
					part.group = id.Name
				}
				parts = append(parts, part)
			}
			for _, part := range groupCallbackParts(parts) {
				out = mergeCallbackLoans(out, prefixCallbackLoans(part.selector, valueCaptures(part.value, active)))
			}
			return out
		case *ast.ArrayLit:
			var out []callbackLoan
			for _, elem := range v.Elems {
				out = mergeCallbackLoans(out, prefixCallbackLoans("a:*", valueCaptures(elem, active)))
			}
			return out
		case *ast.Call:
			id, named := v.Callee.(*ast.Ident)
			if c.callValueCallees[v] || named && id.Local {
				if !c.callbackResultCalls[v] {
					return nil
				}
				out := flattenCallbackLoans(valueCaptures(v.Callee, active))
				flags := c.callOwnFlags[v]
				for i, arg := range v.Args {
					out = mergeCallbackLoans(out, flattenCallbackLoans(valueCaptures(arg, active)))
					if input, ok := arg.(*ast.Ident); ok && input.Local && (i >= len(flags) || !flags[i]) && !c.copyableCallResult(bindingTypes[input.Name]) {
						out = mergeCallbackLoans(out, []callbackLoan{{param: ast.Param{Name: input.Name, Type: bindingTypes[input.Name]}, direct: true}})
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
			var out []callbackLoan
			for i, param := range factory.Params {
				if i >= len(v.Args) {
					break
				}
				for _, source := range c.callbackFactories[id.Name] {
					if source.param.Name != param.Name {
						continue
					}
					arg := v.Args[i]
					if !source.direct {
						selected := selectCallbackLoans(valueCaptures(arg, active), source.path)
						if source.wide {
							selected = flattenCallbackLoans(selected)
						}
						out = mergeCallbackLoans(out, prefixCallbackLoans(source.result, selected))
					}
					if name, ok := arg.(*ast.Ident); ok && !param.Own && !c.copyableCallResult(param.Type) {
						out = mergeCallbackLoans(out, []callbackLoan{{param: ast.Param{Name: name.Name, Type: param.Type}, result: source.result, direct: true}})
					}
				}
			}
			return out
		case *ast.Lambda:
			return captures(v, active)
		case *ast.IfExpr:
			return mergeCallbackLoans(valueCaptures(v.Then, active), valueCaptures(v.Else, active))
		case *ast.MatchExpr:
			var out []callbackLoan
			for _, arm := range v.Arms {
				out = mergeCallbackLoans(out, valueCaptures(arm.Body, active))
			}
			return out
		case *ast.BlockExpr:
			return valueCaptures(v.Tail, active)
		}
		return nil
	}
	resolve = func(name string, active map[string]bool) []callbackLoan {
		values := definitions[name]
		if active[name] {
			for binding := range active {
				cyclic[binding] = true
			}
			return nil
		}
		if len(values) != 1 {
			return nil
		}
		if loans, known := memo[name]; known {
			return loans
		}
		if root, ok := roots[name]; ok {
			return []callbackLoan{{param: root}}
		}
		active[name] = true
		defer delete(active, name)
		loans := valueCaptures(values[0], active)
		// A recursive binding can depend on an unfinished ancestor proof.
		if !cyclic[name] {
			memo[name] = loans
		}
		return loans
	}
	out := map[string][]callbackLoan{}
	for name := range definitions {
		switch bindingTypes[name].(type) {
		case *ast.FuncType, ast.ParamType:
		default:
			continue
		}
		if captures := resolve(name, map[string]bool{}); len(captures) > 0 {
			out[name] = captures
		}
	}
	inline := map[ast.Expr][]callbackLoan{}
	for _, value := range values {
		inline[value] = valueCaptures(value, map[string]bool{})
	}
	return out, inline
}
