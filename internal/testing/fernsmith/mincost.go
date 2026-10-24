package fernsmith

import (
	"strings"
)

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

func (g *Generator) cheapestPath(sc *scope, t gtype) (string, int) {
	best, cost := "", 0
	for _, p := range g.accessPaths(sc, t) {
		if c := pathCost(p); best == "" || c < cost {
			best, cost = p, c
		}
	}
	return best, cost
}

func pathCost(p string) int {
	return 1 + strings.Count(p, ".") + 2*strings.Count(p, "[")
}

func (g *Generator) cheapestCall(sc *scope, t gtype) (*helperSig, int) {
	var best *helperSig
	cost := 0
	cands := append(append([]helperSig{}, g.helpers...), g.localHelpers...)
	if g.self != nil && g.selfCalls > 0 && g.loopDepth == 0 && g.inClosure == 0 {
		cands = append(cands, *g.self)
	}
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

func (g *Generator) callCost(sc *scope, h helperSig) int {
	c := 2
	switch {
	case g.self != nil && h.name == g.self.name:
		c += 3
	case h.fueled:
		c++
	}
	for _, p := range h.params {
		c += g.minCost(sc, p, 1)
	}
	return c
}

func (g *Generator) minCost(sc *scope, t gtype, depth int) int {
	if len(sc.inScope(t)) > 0 {
		return 1
	}
	return g.literalCost(t, depth)
}

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
	case tOptI32:
		return 1
	case tResI32I32:
		return 3
	case tFnI32I32:
		return 4
	}
	return 1
}
