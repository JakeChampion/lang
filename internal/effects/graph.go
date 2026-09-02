// Package effects is the effect-rows prototype (#5320): per-FUNCTION
// effect sets, inferred from the call graph, plus verification of the
// rows a function declares with `uses [...]`.
//
// It is deliberately vocabulary-agnostic. Graph records "which
// builtins does this function reach", and Solve projects that through
// a caller-supplied builtin→label table — so the same graph serves the
// package-capability vocabulary (internal/caps) and the target
// capability vocabulary (internal/platforms) without a third walk.
//
// The graph is the one shared call-graph builder in the compiler:
// internal/caps builds its per-package report on it too. Its
// over-approximations are documented on Build.
package effects

import (
	"sort"

	"github.com/jakechampion/lang/internal/ast"
)

// Graph is a program's call graph, plus the tagged-builtin calls each
// function makes directly.
type Graph struct {
	// Order lists every function name in program declaration order, so
	// every derived result is deterministic.
	Order []string
	// Funcs indexes the declarations by their (modload-mangled) name.
	Funcs map[string]*ast.FuncDecl
	// Edges maps a caller to its statically-resolved callees, deduped
	// and in first-occurrence order.
	Edges map[string][]string
	// Builtins maps a function to the runtime builtins it calls
	// directly, deduped and in first-occurrence order. Every builtin is
	// recorded, tagged or not — the vocabulary decides which carry an
	// effect, so one graph serves several vocabularies.
	Builtins map[string][]string
	// Indirect holds the functions that make at least one call the walk
	// could not resolve to a name — a call through a function value.
	// Such a call reaches anything in Escaping (see Solve).
	Indirect map[string]bool
	// Escaping holds every function whose address is taken somewhere in
	// the program: named in a non-call position, so a function value
	// may carry it to an indirect call site. Lambdas have no name of
	// their own before closureconv hoists them, so a lambda body's
	// reach is folded into EscapingBuiltins instead.
	Escaping map[string]bool
	// EscapingBuiltins holds the builtins reached directly from a
	// lambda body, deduped. A lambda is assumed to escape: its body is
	// also walked as part of the enclosing function (so an immediately
	// applied lambda is not undercounted), and the duplication only
	// widens the indirect-call row, never narrows it.
	EscapingBuiltins []string
}

// Build walks a type-checked program and records its call graph.
//
// The program must have been through checker.Check: method calls are
// rewritten to their hoisted top-level names there, so a call is
// either an `*ast.Ident` callee (a named function or a builtin), a
// `dyn Trait` dispatch, or an indirect call through a value.
//
// The four over-approximations, each chosen so the result is an upper
// bound on what a function can reach:
//
//   - A bare identifier naming a top-level function is an edge, not
//     just a call — a function passed as a value is reachable through
//     whoever invokes it. It is also recorded in Escaping.
//   - A `dyn Trait` method call edges to every method with that simple
//     name: the static walk cannot narrow runtime dispatch.
//   - A lambda body is walked as part of its enclosing function AND
//     folded into EscapingBuiltins, because the value may be called
//     anywhere.
//   - An unresolved callee marks the caller Indirect, which Solve
//     resolves to the union over Escaping — every function a value
//     could be carrying.
//
// Builtins are recorded only in call position. They are free
// functions that cannot be taken as values, so a local shadowing a
// builtin name never contributes a phantom reach.
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
		// Call nodes whose callee is a Lambda literal are applied on
		// the spot, so they are not an escape; every other Lambda is.
		applied := map[ast.Node]bool{}
		// Callee identifiers, which ast.Walk also visits as bare
		// Idents. A name in call position is not a function VALUE, so
		// it must not count as an escape.
		calleeIdent := map[ast.Node]bool{}
		// Functions declared as statements inside this body, by name.
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
					case g.Funcs[callee.Name] != nil:
						addEdge(callee.Name)
					case isBuiltin(callee.Name):
						if !seenBuiltin[callee.Name] {
							seenBuiltin[callee.Name] = true
							g.Builtins[name] = append(g.Builtins[name], callee.Name)
						}
					case locals[callee.Name]:
						// A function declared as a statement inside this
						// body. closureconv has not hoisted it yet, so it
						// has no top-level name — but its body is walked
						// as part of this one, so its reach is already
						// counted here.
					default:
						// A local, a parameter, or a struct field
						// holding a function value.
						g.Indirect[name] = true
					}
				case *ast.FieldAccess:
					if x.DynTrait != "" {
						for _, m := range bySimple[callee.Field] {
							addEdge(m)
						}
					} else {
						// A field holding a function value: the target
						// is not statically known.
						g.Indirect[name] = true
					}
				case *ast.Lambda:
					applied[callee] = true
				default:
					g.Indirect[name] = true
				}
			case *ast.Ident:
				// A function named outside call position is a value:
				// an edge (whoever invokes it runs it here) and an
				// escape (it may reach an indirect call anywhere).
				if calleeIdent[x] {
					return true
				}
				if _, isFn := g.Funcs[x.Name]; isFn {
					addEdge(x.Name)
					g.Escaping[x.Name] = true
				}
			}
			return true
		})
		// Second pass for lambda bodies: a lambda that is not applied
		// on the spot may be invoked from anywhere, so the builtins it
		// reaches directly widen every indirect call site.
		ast.Walk(fn.Body, func(n ast.Node) bool {
			lam, ok := n.(*ast.Lambda)
			if !ok || applied[lam] || lam.Body == nil {
				return true
			}
			ast.Walk(lam.Body, func(m ast.Node) bool {
				switch y := m.(type) {
				case *ast.Call:
					if id, ok := y.Callee.(*ast.Ident); ok {
						if g.Funcs[id.Name] != nil {
							g.Escaping[id.Name] = true
						} else if isBuiltin(id.Name) {
							escBuiltins[id.Name] = true
						}
					}
				case *ast.Ident:
					if _, isFn := g.Funcs[y.Name]; isFn {
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
