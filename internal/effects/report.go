package effects

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
)

// Row is one function's effect row under one vocabulary.
type Row struct {
	Function string
	Module   string
	// Reached is the sorted set of effects the function can reach.
	Reached []string
	// ViaIndirect is the effects reached ONLY through a call that could
	// not be resolved to a name. It is the price of having no effect
	// row on function TYPES: the analysis must assume a function value
	// is any escaping callable. This is the single number that says
	// what row-polymorphic function types would buy.
	ViaIndirect []string
	// Witness explains each reached effect with a call chain.
	Witness []Chain
	// Public mirrors the declaration's visibility. The literature's
	// recommended middle ground is to require a row on `pub`
	// signatures and infer elsewhere, so the count of effectful public
	// functions is the size of that obligation.
	Public bool
	// Origin is true when the function calls a tagged builtin itself,
	// rather than only inheriting effects from its callees. It is where
	// an annotation would carry information a reader could not get from
	// the callee's own signature — so the split between origins and
	// inheritors is the shape of the annotation burden.
	Origin bool
}

// Report computes the per-function rows of a graph under one
// vocabulary. Report mode only — nothing here enforces.
func Report(g *Graph, table map[string]string) []Row {
	sol := Solve(g, table)

	// Re-solve with indirect calls contributing nothing, to separate
	// what a function genuinely reaches from what it is only charged
	// for because a function value could be carrying anything.
	direct := *g
	direct.Indirect = map[string]bool{}
	directSol := Solve(&direct, table)

	out := make([]Row, 0, len(g.Order))
	for _, name := range g.Order {
		fn := g.Funcs[name]
		row := Row{
			Function:    name,
			Module:      fn.BodyModule(),
			Reached:     sol.Vocab.Names(sol.Rows[name]),
			ViaIndirect: sol.Vocab.Names(sol.Rows[name] &^ directSol.Rows[name]),
			Witness:     Witness(g, sol, name, sol.Rows[name]),
			Public:      fn.Public,
		}
		for _, b := range g.Builtins[name] {
			if sol.Vocab.of[b] != 0 {
				row.Origin = true
				break
			}
		}
		out = append(out, row)
	}
	return out
}

// Format renders a report: one line per function that reaches or
// declares anything, then the distribution — which is the measurement
// the prototype exists to take (docs/EFFECT-ROWS-BRIEF.md). `title`
// names the vocabulary, since the same graph is worth reading under
// more than one.
func Format(title string, rows []Row) string {
	var b strings.Builder
	fmt.Fprintf(&b, "== %s ==\n", title)
	hist := map[int]int{}
	var indirect, origins, effectful, pub, pubEffectful int
	for _, r := range rows {
		hist[len(r.Reached)]++
		if len(r.ViaIndirect) > 0 {
			indirect++
		}
		if len(r.Reached) > 0 {
			effectful++
		}
		if r.Origin {
			origins++
		}
		if r.Public {
			pub++
			if len(r.Reached) > 0 {
				pubEffectful++
			}
		}
		if len(r.Reached) == 0 {
			continue
		}
		reached := "-"
		if len(r.Reached) > 0 {
			reached = strings.Join(r.Reached, ",")
		}
		fmt.Fprintf(&b, "%s  %s", r.Function, reached)
		if len(r.ViaIndirect) > 0 {
			fmt.Fprintf(&b, "  (via a function value: %s)", strings.Join(r.ViaIndirect, ","))
		}
		if len(r.Witness) > 0 {
			fmt.Fprintf(&b, "  (example: %s)", strings.Join(r.Witness[0].Path, " → "))
		}
		b.WriteString("\n")
	}
	sizes := make([]int, 0, len(hist))
	for n := range hist {
		sizes = append(sizes, n)
	}
	sort.Ints(sizes)
	fmt.Fprintf(&b, "\n%d functions; %d reach an effect, of which %d call a tagged builtin themselves and %d only inherit\n",
		len(rows), effectful, origins, effectful-origins)
	fmt.Fprintf(&b, "%d are `pub`, of which %d reach an effect — the size of a \"require a row on public signatures\" rule\n",
		pub, pubEffectful)
	fmt.Fprintf(&b, "%d reach an effect only through a function value\n", indirect)
	for _, n := range sizes {
		fmt.Fprintf(&b, "  row size %d: %d functions\n", n, hist[n])
	}
	return b.String()
}

// compile-time assertion that Row keeps a handle on the decl it came
// from; kept so a refactor that drops SourceModule fails here.
var _ = func(fn *ast.FuncDecl) string { return fn.SourceModule }
