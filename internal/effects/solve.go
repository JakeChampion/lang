package effects

import (
	"sort"
)

// Set is an effect row: a set of labels drawn from a fixed, sorted
// vocabulary, held as a bitmask so union and subset are one
// instruction. A row is small and closed by construction — the
// vocabulary is a compile-time constant, not user-extensible — which
// is what lets the solver be a plain bitmask fixpoint.
type Set uint64

// Vocabulary is one analysis's label universe: the sorted label names
// and the builtin→label table projected onto them.
type Vocabulary struct {
	Labels []string // sorted, deduped; at most 64
	bit    map[string]Set
	of     map[string]Set // builtin name → its label's bit
}

// NewVocabulary builds a vocabulary from a builtin→label table. The
// label set is the table's value set, sorted, so a row's rendering is
// deterministic and independent of map order.
func NewVocabulary(table map[string]string) *Vocabulary {
	seen := map[string]bool{}
	v := &Vocabulary{bit: map[string]Set{}, of: map[string]Set{}}
	for _, label := range table {
		// A builtin mapped to the empty label carries no effect: the
		// table may list it to say "known and deliberately untagged".
		if label == "" {
			continue
		}
		if !seen[label] {
			seen[label] = true
			v.Labels = append(v.Labels, label)
		}
	}
	sort.Strings(v.Labels)
	if len(v.Labels) > 64 {
		panic("effects: vocabulary exceeds the 64 labels a Set holds")
	}
	for i, label := range v.Labels {
		v.bit[label] = 1 << uint(i)
	}
	for builtin, label := range table {
		if label == "" {
			continue
		}
		v.of[builtin] = v.bit[label]
	}
	return v
}

// Bit returns the single-label row for a label name, and whether the
// name is in the vocabulary at all.
func (v *Vocabulary) Bit(label string) (Set, bool) {
	b, ok := v.bit[label]
	return b, ok
}

// Names renders a row as its sorted label names.
func (v *Vocabulary) Names(s Set) []string {
	var out []string
	for i, label := range v.Labels {
		if s&(1<<uint(i)) != 0 {
			out = append(out, label)
		}
	}
	return out
}

// Solution is the per-function result of the fixpoint.
type Solution struct {
	Vocab *Vocabulary
	// Rows maps a function name to the row it can reach.
	Rows map[string]Set
	// Indirect is the row an unresolved call through a function value
	// reaches: the union over every escaping callable. Functions making
	// such a call carry it.
	Indirect Set
}

// Solve computes each function's effect row: the union of the labels
// of the builtins it calls directly, the rows of its callees, and —
// for a function making a call through a function value — the union
// over everything a function value could be carrying.
//
// The result is a sound over-approximation. It is computed by monotone
// iteration to a fixpoint, the shape checker.returnViewSummaries
// already uses for interprocedural view summaries: rows only ever
// grow, the lattice is a 64-element powerset, so the loop terminates
// and recursion (direct or mutual) needs no special case.
//
// The indirect row is itself part of the fixpoint: an escaping
// function that makes an indirect call contributes to the very row it
// reads. Folding it into the same loop is what keeps that sound.
func Solve(g *Graph, table map[string]string) *Solution {
	v := NewVocabulary(table)
	sol := &Solution{Vocab: v, Rows: make(map[string]Set, len(g.Order))}

	direct := make(map[string]Set, len(g.Order))
	for name, builtins := range g.Builtins {
		var s Set
		for _, b := range builtins {
			s |= v.of[b]
		}
		direct[name] = s
	}
	var escSeed Set
	for _, b := range g.EscapingBuiltins {
		escSeed |= v.of[b]
	}

	for changed := true; changed; {
		changed = false
		// The indirect row: what any function value might reach.
		indirect := escSeed
		for name := range g.Escaping {
			indirect |= sol.Rows[name]
		}
		if indirect != sol.Indirect {
			sol.Indirect = indirect
			changed = true
		}
		for _, name := range g.Order {
			row := direct[name]
			if g.Indirect[name] {
				row |= sol.Indirect
			}
			for _, callee := range g.Edges[name] {
				row |= sol.Rows[callee]
			}
			if row != sol.Rows[name] {
				sol.Rows[name] = row
				changed = true
			}
		}
	}
	return sol
}

// Chain is a witness for one label in a function's row: the call path
// from that function down to the builtin that carries the label. The
// last element is the builtin; an element may be the pseudo-node
// "(function value)" where the path goes through an indirect call.
type Chain struct {
	Label string
	Path  []string
}

// IndirectNode is the pseudo-node a witness chain uses where the path
// leaves through a call the analysis could not resolve to a name.
const IndirectNode = "(function value)"

// Witness finds, for each label in fn's row, one call chain that
// explains it — the first such chain a breadth-first walk from fn
// discovers, so the shortest explanation wins and the result is
// deterministic. Labels are returned in vocabulary order.
//
// A hop through a call the analysis could not resolve is rendered as
// the pseudo-node IndirectNode, so a chain never claims a direct edge
// the source does not have. That hop is also the whole ergonomic
// argument in one line of output: "you are charged for this because a
// function value could be anything."
//
// Diagnostics live or die on this: "your function reaches `net`" is
// only actionable next to the path that reaches it.
func Witness(g *Graph, sol *Solution, fn string, want Set) []Chain {
	v := sol.Vocab
	esc := escapingOrder(g)

	// The indirect hop is modelled as a real node in the walk: every
	// function making an unresolved call edges to it, and it edges to
	// everything a function value could be carrying.
	builtinsOf := func(at string) []string {
		if at == IndirectNode {
			return g.EscapingBuiltins
		}
		return g.Builtins[at]
	}
	edgesOf := func(at string) []string {
		if at == IndirectNode {
			return esc
		}
		out := g.Edges[at]
		if g.Indirect[at] {
			out = append(append([]string(nil), out...), IndirectNode)
		}
		return out
	}

	found := map[string]Chain{}
	parent := map[string]string{}
	visited := map[string]bool{fn: true}
	queue := []string{fn}
	pathTo := func(at string) []string {
		var path []string
		for n := at; ; n = parent[n] {
			path = append([]string{n}, path...)
			if n == fn {
				break
			}
		}
		return path
	}
	for len(queue) > 0 {
		at := queue[0]
		queue = queue[1:]
		for _, b := range builtinsOf(at) {
			bit := v.of[b]
			if bit&want == 0 {
				continue
			}
			label := v.Names(bit)[0]
			if _, done := found[label]; done {
				continue
			}
			found[label] = Chain{Label: label, Path: append(pathTo(at), b)}
		}
		for _, callee := range edgesOf(at) {
			if visited[callee] {
				continue
			}
			visited[callee] = true
			parent[callee] = at
			queue = append(queue, callee)
		}
	}
	out := make([]Chain, 0, len(found))
	for _, label := range v.Labels {
		if c, ok := found[label]; ok {
			out = append(out, c)
		}
	}
	return out
}

// escapingOrder returns the escaping callables in program declaration
// order, so a witness chain through an indirect call is deterministic.
func escapingOrder(g *Graph) []string {
	var out []string
	for _, name := range g.Order {
		if g.Escaping[name] {
			out = append(out, name)
		}
	}
	return out
}
