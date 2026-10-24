package effects

import (
	"sort"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

type Graph struct {
	Order            []string
	Funcs            map[string]*ast.FuncDecl
	Edges            map[string][]string
	Builtins         map[string][]string
	Indirect         map[string]bool
	Escaping         map[string]bool
	EscapingBuiltins []string
}

func Build(prog *ast.Program, isBuiltin func(name string) bool) *Graph {
	g := &Graph{
		Funcs:    make(map[string]*ast.FuncDecl, len(prog.Funcs)),
		Edges:    map[string][]string{},
		Builtins: map[string][]string{},
		Indirect: map[string]bool{},
		Escaping: map[string]bool{},
	}
	bySimple := map[string][]string{}
	for _, fn := range prog.Funcs {
		g.Order = append(g.Order, fn.Name)
		g.Funcs[fn.Name] = fn
		if fn.MethodSimpleName != "" {
			bySimple[fn.MethodSimpleName] = append(bySimple[fn.MethodSimpleName], fn.Name)
		}
	}

	escBuiltins := map[string]bool{}
	for _, fn := range prog.Funcs {
		if fn.Body == nil {
			continue
		}
		name := fn.Name
		seenEdge := map[string]bool{}
		seenBuiltin := map[string]bool{}
		addEdge := func(callee string) {
			if callee == name || seenEdge[callee] {
				return
			}
			seenEdge[callee] = true
			g.Edges[name] = append(g.Edges[name], callee)
		}
		applied := map[ast.Node]bool{}
		calleeIdent := map[ast.Node]bool{}
		locals := map[string]bool{}
		ast.Walk(fn.Body, func(n ast.Node) bool {
			if d, ok := n.(*ast.FuncDecl); ok && d.IsLocal {
				locals[d.Name] = true
			}
			return true
		})
		ast.Walk(fn.Body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.Call:
				switch callee := x.Callee.(type) {
				case *ast.Ident:
					calleeIdent[callee] = true
					switch {
					case locals[callee.Name]:
					case callee.Local:
						g.Indirect[name] = true
					case g.Funcs[callee.Name] != nil:
						addEdge(callee.Name)
					case isBuiltin(callee.Name):
						if !seenBuiltin[callee.Name] {
							seenBuiltin[callee.Name] = true
							g.Builtins[name] = append(g.Builtins[name], callee.Name)
						}
					default:
						g.Indirect[name] = true
					}
				case *ast.FieldAccess:
					if x.DynTrait != "" {
						for _, m := range bySimple[callee.Field] {
							addEdge(m)
						}
					} else {
						g.Indirect[name] = true
					}
				case *ast.Lambda:
					applied[callee] = true
				default:
					g.Indirect[name] = true
				}
			case *ast.Ident:
				if calleeIdent[x] || x.Local {
					return true
				}
				if _, isFn := g.Funcs[x.Name]; isFn {
					addEdge(x.Name)
					g.Escaping[x.Name] = true
				}
			}
			return true
		})
		ast.Walk(fn.Body, func(n ast.Node) bool {
			lam, ok := n.(*ast.Lambda)
			if !ok || applied[lam] || lam.Body == nil {
				return true
			}
			ast.Walk(lam.Body, func(m ast.Node) bool {
				switch y := m.(type) {
				case *ast.Call:
					if id, ok := y.Callee.(*ast.Ident); ok && !id.Local {
						if g.Funcs[id.Name] != nil {
							g.Escaping[id.Name] = true
						} else if isBuiltin(id.Name) {
							escBuiltins[id.Name] = true
						}
					}
				case *ast.Ident:
					if _, isFn := g.Funcs[y.Name]; isFn && !y.Local {
						g.Escaping[y.Name] = true
					}
				}
				return true
			})
			return true
		})
	}
	for b := range escBuiltins {
		g.EscapingBuiltins = append(g.EscapingBuiltins, b)
	}
	sort.Strings(g.EscapingBuiltins)
	return g
}
