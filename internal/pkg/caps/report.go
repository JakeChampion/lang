package caps

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jakechampion/lang/internal/check/effects"
	"github.com/jakechampion/lang/internal/syntax/ast"
)

type Use struct {
	Capability string
	Chain      []string
}

type Row struct {
	Package string
	Name    string
	Uses    []Use
}

func Analyze(prog *ast.Program, pkgOf func(module string) string) []Row {
	g := effects.Build(prog, func(name string) bool {
		if _, tagged := BuiltinCaps[name]; tagged {
			return true
		}
		return Ungated[name]
	})

	pkgRoots := map[string][]string{}
	var pkgOrder []string
	for _, fn := range prog.Funcs {
		p := pkgOf(fn.BodyModule())
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
		rows = append(rows, Row{Package: p, Uses: walk(pkgRoots[p], g)})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Package < rows[j].Package })
	return rows
}

func walk(roots []string, g *effects.Graph) []Use {
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
		for _, b := range g.Builtins[fn] {
			c, tagged := BuiltinCaps[b]
			if !tagged {
				continue
			}
			if _, done := found[c]; done {
				continue
			}
			var chain []string
			for at := fn; at != ""; at = parent[at] {
				chain = append([]string{at}, chain...)
			}
			found[c] = Use{Capability: c, Chain: append(chain, b)}
		}
		for _, callee := range g.Edges[fn] {
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

func (r Row) DisplayName() string {
	if r.Name != "" {
		return r.Name
	}
	return r.Package
}

func Format(rows []Row) string {
	var b strings.Builder
	for _, r := range rows {
		if len(r.Uses) == 0 {
			fmt.Fprintf(&b, "%s  -\n", r.DisplayName())
			continue
		}
		names := make([]string, len(r.Uses))
		for i, u := range r.Uses {
			names[i] = u.Capability
		}
		fmt.Fprintf(&b, "%s  %s  (example: %s)\n", r.DisplayName(), strings.Join(names, ","), strings.Join(r.Uses[0].Chain, " → "))
	}
	return b.String()
}
