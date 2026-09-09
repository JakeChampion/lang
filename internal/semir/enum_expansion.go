package semir

import (
	"fmt"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
)

// Enum interfaces are instantiated by substitution, not by evaluating types.
// A parameter can only be copied, wrapped in type constructors, or forgotten.
// The instantiated graph is finite exactly when no reachable parameter-flow
// cycle wraps a parameter. Zero-weight cycles permit finite permutations;
// constant resets break dependency cycles. Check declarations before unfolding
// instances, so E[T] -> E[T[]] cannot start an unbounded import.
func verifyFiniteEnumExpansion(root ast.Type, info *checker.Info) error {
	type edge struct {
		to   int
		grow bool
	}
	var edges [][]edge
	var names []string
	var enums []*ast.EnumDecl
	starts := make(map[string]int)
	records := make(map[string]bool)
	var visit func(ast.Type) error
	visit = func(typ ast.Type) error {
		switch t := typ.(type) {
		case ast.EnumType:
			decl := info.Enums[t.Name]
			if decl == nil || decl.Name != t.Name || len(decl.TypeParams) != len(t.Args) {
				return fmt.Errorf("semir enum %s: incomplete checked type arguments", t.Name)
			}
			if _, seen := starts[t.Name]; !seen {
				starts[t.Name] = len(edges)
				for _, param := range decl.TypeParams {
					edges = append(edges, nil)
					names = append(names, t.Name+"."+param)
				}
				enums = append(enums, decl)
			}
		case ast.StructType:
			if len(t.Args) != 0 {
				return fmt.Errorf("semir record %s: monomorphization must precede typed construction", t.Name)
			}
			if !records[t.Name] {
				records[t.Name] = true
				decl := info.Structs[t.Name]
				if decl == nil || decl.Name != t.Name || len(decl.TypeParams) != 0 {
					return fmt.Errorf("semir record %s: missing concrete checked field interface", t.Name)
				}
				for _, field := range decl.Fields {
					if err := visit(field.Type); err != nil {
						return err
					}
				}
			}
		}
		return enumTypeChildren(typ, visit)
	}
	if err := visit(root); err != nil {
		return err
	}
	for next := 0; next < len(enums); next++ {
		decl := enums[next]
		params := make(map[string]int, len(decl.TypeParams))
		for i, name := range decl.TypeParams {
			if _, exists := params[name]; name == "" || exists {
				return fmt.Errorf("semir enum %s: empty or duplicate type parameter", decl.Name)
			}
			params[name] = starts[decl.Name] + i
		}
		var dependencies func(ast.Type) error
		dependencies = func(typ ast.Type) error {
			if target, ok := typ.(ast.EnumType); ok {
				for i, arg := range target.Args {
					var parameter func(ast.Type, bool) error
					parameter = func(typ ast.Type, nested bool) error {
						if p, ok := typ.(ast.ParamType); ok {
							from, bound := params[p.Name]
							if !bound {
								return fmt.Errorf("semir enum %s: unresolved type parameter %s", decl.Name, p.Name)
							}
							edges[from] = append(edges[from], edge{starts[target.Name] + i, nested})
							return nil
						}
						return enumTypeChildren(typ, func(child ast.Type) error { return parameter(child, true) })
					}
					if err := parameter(arg, false); err != nil {
						return err
					}
				}
			}
			return enumTypeChildren(typ, dependencies)
		}
		for _, variant := range decl.Variants {
			for _, payload := range variant.Payloads {
				if err := visit(payload); err != nil {
					return err
				}
				if err := dependencies(payload); err != nil {
					return err
				}
			}
		}
	}

	// Tarjan's SCC partition makes the expansion check linear in the finite
	// declaration-parameter graph. No instantiated type depth limit is involved.
	index := make([]int, len(edges))
	low := make([]int, len(edges))
	component := make([]int, len(edges))
	onStack := make([]bool, len(edges))
	var stack []int
	serial, components := 0, 0
	var connect func(int)
	connect = func(v int) {
		serial++
		index[v], low[v] = serial, serial
		stack, onStack[v] = append(stack, v), true
		for _, e := range edges[v] {
			if index[e.to] == 0 {
				connect(e.to)
				low[v] = min(low[v], low[e.to])
			} else if onStack[e.to] {
				low[v] = min(low[v], index[e.to])
			}
		}
		if low[v] == index[v] {
			components++
			for {
				last := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[last], component[last] = false, components
				if last == v {
					break
				}
			}
		}
	}
	for v := range edges {
		if index[v] == 0 {
			connect(v)
		}
	}
	for from, es := range edges {
		for _, e := range es {
			if e.grow && component[from] == component[e.to] {
				return fmt.Errorf("semir: unbounded enum type instantiation from %s to %s", names[from], names[e.to])
			}
		}
	}
	return nil
}

// Only the implemented semantic type surface participates in instantiation.
// Other types are still rejected independently by the semantic type verifier.
func enumTypeChildren(typ ast.Type, visit func(ast.Type) error) error {
	var children []ast.Type
	switch t := typ.(type) {
	case ast.ArrayType:
		return visit(t.Elem)
	case ast.TupleType:
		children = t.Elems
	case ast.EnumType:
		children = t.Args
	case ast.StructType:
		children = t.Args
	}
	for _, child := range children {
		if err := visit(child); err != nil {
			return err
		}
	}
	return nil
}
