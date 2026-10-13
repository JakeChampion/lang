package fernsmith

import (
	"strings"
)

// The exhaustion convention needs a spent corpus to take the SMALLEST
// expression of a type, and for a builtin type the literal is it. A
// program-declared type is different: its literal spells every field, which
// can be larger than a projection out of a value already in scope or a call
// to a helper with fewer parameters — so truncation could swap a two-argument
// call for a four-field literal and grow the program. leafMin picks by size
// instead. Sizes are in parsed AST nodes, what
// TestGenBytesShrinkIsMonotonicAndValid counts.

// leafMin writes the smallest expression of the program-declared type t that
// needs no var of type t in scope. Called only on an exhausted stream.
func (g *Generator) leafMin(b *strings.Builder, sc *scope, t gtype, depth int) {
	lit := g.literalCost(t, 0)
	path, pathCost := g.cheapestPath(sc, t)
	call, callCost := g.cheapestCall(sc, t)
	switch {
	case path != "" && pathCost <= lit && (call == nil || pathCost <= callCost):
		b.WriteString(path)
	case call != nil && callCost < lit:
		g.emitCallTo(b, sc, *call, depth)
	default:
		g.literal(b, sc, t, depth)
	}
}

// cheapestPath is the smallest access path of type t, and its size.
func (g *Generator) cheapestPath(sc *scope, t gtype) (string, int) {
	best, cost := "", 0
	for _, p := range g.accessPaths(sc, t) {
		if c := pathCost(p); best == "" || c < cost {
			best, cost = p, c
		}
	}
	return best, cost
}

// pathCost is an access path's size: the root, plus one node per field read
// and two per `[0i32]` (the index node and its literal).
func pathCost(p string) int {
	return 1 + strings.Count(p, ".") + 2*strings.Count(p, "[")
}

// cheapestCall is the helper returning t whose call is smallest when every
// argument is itself a smallest expression, and that size. A recursive
// helper's own self-call is not a candidate: it is budgeted, and a leaf is
// not the place to spend it.
func (g *Generator) cheapestCall(sc *scope, t gtype) (*helperSig, int) {
	var best *helperSig
	cost := 0
	cands := append(append([]helperSig{}, g.helpers...), g.localHelpers...)
	for i := range cands {
		if cands[i].retType != t {
			continue
		}
		if c := g.callCost(sc, cands[i]); best == nil || c < cost {
			best, cost = &cands[i], c
		}
	}
	return best, cost
}

// callCost is the size of a call to h whose arguments are each a smallest
// expression.
func (g *Generator) callCost(sc *scope, h helperSig) int {
	c := 2 // the call and its callee
	if h.fueled {
		c++
	}
	for _, p := range h.params {
		c += g.minCost(sc, p, 1)
	}
	return c
}

// minCost is the size of the smallest expression of type t at a leaf.
func (g *Generator) minCost(sc *scope, t gtype, depth int) int {
	if len(sc.inScope(t)) > 0 {
		return 1
	}
	return g.literalCost(t, depth)
}

// literalCost is the size of the literal an exhausted stream writes for t.
func (g *Generator) literalCost(t gtype, depth int) int {
	if depth > 4 {
		return 1
	}
	if sh, ok := g.structShapes[t]; ok {
		c := 1
		for _, f := range sh.fields {
			c += g.literalCost(f.t, depth+1)
		}
		return c
	}
	if sh, ok := g.enumShapes[t]; ok {
		for _, v := range sh.variants {
			if len(v.payload) == 0 {
				return 1
			}
		}
		c := 2
		for _, p := range sh.variants[0].payload {
			c += g.literalCost(p, depth+1)
		}
		return c
	}
	if elem, ok := g.arrayElemOf(t); ok {
		return 1 + g.literalCost(elem, depth+1)
	}
	switch t {
	case tPair, tTupI32I64, tTupF32F64, tVec2:
		return 3
	case tXyz:
		return 3
	case tOptI32, tResI32I32:
		return 2
	case tFnI32I32:
		return 4
	}
	return 1
}
