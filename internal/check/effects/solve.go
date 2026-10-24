package effects

import (
	"sort"
)

type Set uint64

type Vocabulary struct {
	Labels []string
	bit    map[string]Set
	of     map[string]Set
}

func NewVocabulary(table map[string]string) *Vocabulary {
	seen := map[string]bool{}
	v := &Vocabulary{bit: map[string]Set{}, of: map[string]Set{}}
	for _, label := range table {
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

func (v *Vocabulary) Bit(label string) (Set, bool) {
	b, ok := v.bit[label]
	return b, ok
}

func (v *Vocabulary) Names(s Set) []string {
	var out []string
	for i, label := range v.Labels {
		if s&(1<<uint(i)) != 0 {
			out = append(out, label)
		}
	}
	return out
}

type Solution struct {
	Vocab    *Vocabulary
	Rows     map[string]Set
	Indirect Set
}

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

type Chain struct {
	Label string
	Path  []string
}

const IndirectNode = "(function value)"

func Witness(g *Graph, sol *Solution, fn string, want Set) []Chain {
	v := sol.Vocab
	esc := escapingOrder(g)

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

func escapingOrder(g *Graph) []string {
	var out []string
	for _, name := range g.Order {
		if g.Escaping[name] {
			out = append(out, name)
		}
	}
	return out
}
