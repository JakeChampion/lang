package caps

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
)

// Use records one capability a package can reach, with an example
// call chain from one of the package's own functions down to the
// tagged builtin (function names as they appear in the combined
// program, i.e. modload-mangled; the last element is the builtin).
type Use struct {
	Capability string
	Chain      []string
}

// Row is one package's report line: the package name and its
// capability uses, sorted by capability.
type Row struct {
	Package string
	Uses    []Use
}

// Analyze computes per-package capability usage for a loaded +
// checked program by call-graph reachability: each package's roots
// are ALL the functions it declares (declared reachability — an
// uncalled-but-declared function still counts; Phase 1 reports what
// the package's code could do, not what this program's live paths
// do), and the walk follows transitive callees wherever they live —
// through the stdlib and through deeper packages — mirroring the
// brief's enforcement rule.
//
// pkgOf maps a FuncDecl.SourceModule to the owning package's report
// name; returning "" marks the module as fold-through (the stdlib):
// its functions are traversal nodes, never roots, so `std/fetch`'s
// tcp_connect shows up on the row of whichever package calls into
// std/fetch.
//
// Attribution choices (see docs/PACKAGE-CAPABILITIES-BRIEF.md):
//
//   - Closures count at their definition package: a lambda's body is
//     walked as part of the enclosing FuncDecl, so a callback package
//     A hands to package B is A's usage even though B invokes it.
//   - Builtins count only in call position (builtins are free
//     functions; they cannot be taken as values), so a local variable
//     shadowing a builtin name never misreports a capability.
//   - A bare identifier naming a top-level function counts as an edge
//     (function values passed to higher-order code), matching the
//     tree-shaker's over-approximation.
//   - A `dyn Trait` method call edges to every method with that
//     simple name — the static walk cannot narrow the runtime
//     dispatch, so the report over-approximates rather than misses.
//
// The result is deterministic: rows sort by package name, uses by
// capability, and each chain is the first one a FIFO walk over the
// program's (deterministic) declaration order discovers.
func Analyze(prog *ast.Program, pkgOf func(module string) string) []Row {
	funcs := map[string]*ast.FuncDecl{}
	bySimple := map[string][]string{}
	for _, fn := range prog.Funcs {
		funcs[fn.Name] = fn
		if fn.MethodSimpleName != "" {
			bySimple[fn.MethodSimpleName] = append(bySimple[fn.MethodSimpleName], fn.Name)
		}
	}

	edges := map[string][]string{}
	direct := map[string][]string{}
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
			edges[name] = append(edges[name], callee)
		}
		ast.Walk(fn.Body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.Call:
				if id, ok := x.Callee.(*ast.Ident); ok {
					if _, tagged := BuiltinCaps[id.Name]; tagged && !seenBuiltin[id.Name] {
						seenBuiltin[id.Name] = true
						direct[name] = append(direct[name], id.Name)
					}
				} else if fa, ok := x.Callee.(*ast.FieldAccess); ok && x.DynTrait != "" {
					for _, m := range bySimple[fa.Field] {
						addEdge(m)
					}
				}
			case *ast.Ident:
				if _, isFn := funcs[x.Name]; isFn {
					addEdge(x.Name)
				}
			}
			return true
		})
	}

	pkgRoots := map[string][]string{}
	var pkgOrder []string
	for _, fn := range prog.Funcs {
		p := pkgOf(fn.SourceModule)
		if p == "" {
			continue
		}
		if _, seen := pkgRoots[p]; !seen {
			pkgOrder = append(pkgOrder, p)
		}
		pkgRoots[p] = append(pkgRoots[p], fn.Name)
	}

	rows := make([]Row, 0, len(pkgOrder))
	for _, p := range pkgOrder {
		rows = append(rows, Row{Package: p, Uses: walk(pkgRoots[p], edges, direct)})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Package < rows[j].Package })
	return rows
}

// walk BFSes from the package's declared functions and returns the
// capabilities reached, each with the first chain discovered.
func walk(roots []string, edges, direct map[string][]string) []Use {
	parent := map[string]string{}
	visited := map[string]bool{}
	var queue []string
	for _, r := range roots {
		if visited[r] {
			continue
		}
		visited[r] = true
		queue = append(queue, r)
	}
	found := map[string]Use{}
	for len(queue) > 0 {
		fn := queue[0]
		queue = queue[1:]
		for _, b := range direct[fn] {
			c := BuiltinCaps[b]
			if _, done := found[c]; done {
				continue
			}
			var chain []string
			for at := fn; at != ""; at = parent[at] {
				chain = append([]string{at}, chain...)
			}
			found[c] = Use{Capability: c, Chain: append(chain, b)}
		}
		for _, callee := range edges[fn] {
			if visited[callee] {
				continue
			}
			visited[callee] = true
			parent[callee] = fn
			queue = append(queue, callee)
		}
	}
	uses := make([]Use, 0, len(found))
	for _, u := range found {
		uses = append(uses, u)
	}
	sort.Slice(uses, func(i, j int) bool { return uses[i].Capability < uses[j].Capability })
	return uses
}

// Format renders the report: one line per package —
//
//	app  fs,net  (example: main → lib__save → write_file)
//
// with the example chain belonging to the row's first (alphabetical)
// capability, and `-` for a package that reaches none.
func Format(rows []Row) string {
	var b strings.Builder
	for _, r := range rows {
		if len(r.Uses) == 0 {
			fmt.Fprintf(&b, "%s  -\n", r.Package)
			continue
		}
		names := make([]string, len(r.Uses))
		for i, u := range r.Uses {
			names[i] = u.Capability
		}
		fmt.Fprintf(&b, "%s  %s  (example: %s)\n", r.Package, strings.Join(names, ","), strings.Join(r.Uses[0].Chain, " → "))
	}
	return b.String()
}
