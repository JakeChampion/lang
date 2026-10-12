package fernsmith

import "sort"

// span is one element of a list the generator drew from a corpus: the bytes
// [start, end) it consumed, and count, the byte its list's length came from.
// Deleting the element's bytes and lowering that count by one keeps the later
// decisions on the bytes they read before, where a cut anywhere but the end
// would shift every one; a pick whose range the element was part of, a scope
// or the helpers, can still read differently.
type span struct{ count, start, end int }

// pos is the next corpus byte the generator reads, or -1 when it draws from a
// random source rather than a corpus.
func (g *Generator) pos() int {
	if c, ok := g.ch.(*byteChooser); ok {
		return c.pos
	}
	return -1
}

// listCount draws a list's length below max and reports the corpus byte it
// came from, or -1 when no byte was read: a list of at most one choice, an
// exhausted corpus, or a random source.
func (g *Generator) listCount(max int) (int, int) {
	at := g.pos()
	n := g.ch.intN(max)
	if at < 0 || g.pos() == at {
		return n, -1
	}
	return n, at
}

// markSpan records the element that began at start.
func (g *Generator) markSpan(count, start int) {
	if count >= 0 && start >= 0 && g.pos() > start {
		g.spans = append(g.spans, span{count: count, start: start, end: g.pos()})
	}
}

// exprSpan is an expression of type t drawn from the bytes [start, end). lead
// is whether its first byte chose between a leaf and a larger production,
// which every expression above the depth limit reads.
type exprSpan struct {
	start, end int
	t          gtype
	lead       bool
}

// markExpr records the expression that began at start.
func (g *Generator) markExpr(start int, t gtype, lead bool) {
	if start >= 0 && g.pos() > start {
		g.exprs = append(g.exprs, exprSpan{start: start, end: g.pos(), t: t, lead: lead})
	}
}

// mainBytesSpans is GenMainBytes with the list elements and expressions it
// drew.
func mainBytesSpans(data []byte) (string, []span, []exprSpan) {
	g := newByteGen(data, DefaultConfig())
	src := g.MainProgram()
	return src, g.spans, g.exprs
}

// withoutSpan is data less the element s, its list one shorter.
func withoutSpan(data []byte, s span) []byte {
	out := make([]byte, 0, len(data)-(s.end-s.start))
	out = append(out, data[:s.start]...)
	out = append(out, data[s.end:]...)
	out[s.count]--
	return out
}

// hoisted is data with the expression outer replaced by inner, a smaller
// expression of the same type inside it, or nil when their first bytes do not
// line up: an expression at the depth limit reads no leading byte, so it
// stands in for one above the limit behind a byte that picks a leaf, and for
// one at the limit only when it is a leaf itself.
func hoisted(data []byte, outer, inner exprSpan) []byte {
	body := data[inner.start:inner.end]
	switch {
	case outer.lead && !inner.lead:
		body = append([]byte{0}, body...)
	case !outer.lead && inner.lead:
		if float64(body[0])/256 >= leafChance {
			return nil
		}
		body = body[1:]
	}
	out := make([]byte, 0, len(data)-(outer.end-outer.start)+len(body))
	out = append(out, data[:outer.start]...)
	out = append(out, body...)
	return append(out, data[outer.end:]...)
}

// reductions is every corpus one structural move from data makes, in the order
// ShrinkMainBytes tries them: deleting a helper or statement, largest first,
// then replacing an expression, largest first, by one inside it of its type,
// smallest first.
func reductions(data []byte) [][]byte {
	_, spans, exprs := mainBytesSpans(data)
	var out [][]byte
	sort.SliceStable(spans, func(i, j int) bool {
		return spans[i].end-spans[i].start > spans[j].end-spans[j].start
	})
	for _, s := range spans {
		if data[s.count] > 0 {
			out = append(out, withoutSpan(data, s))
		}
	}
	bySize := func(es []exprSpan, larger bool) {
		sort.SliceStable(es, func(i, j int) bool {
			return (es[i].end-es[i].start > es[j].end-es[j].start) == larger
		})
	}
	bySize(exprs, true)
	for i, outer := range exprs {
		var inner []exprSpan
		for _, e := range exprs[i+1:] {
			if e.t == outer.t && e.start >= outer.start && e.end <= outer.end && e != outer {
				inner = append(inner, e)
			}
		}
		bySize(inner, false)
		for _, e := range inner {
			if cand := hoisted(data, outer, e); cand != nil {
				out = append(out, cand)
			}
		}
	}
	return out
}

// ShrinkMainBytes reduces a corpus for which fails holds to a smaller one for
// which it still holds, through GenMainBytes' structure. Each round first cuts
// bytes off the end, the minimisation contract every byte-driven entry point
// keeps, then takes the first structural move (reductions) that still fails;
// it stops when a round finds none. A move is kept when fails holds and the
// program it generates is no longer, and since every program a corpus
// generates type-checks, any move is safe to try. fails runs once per
// distinct program.
func ShrinkMainBytes(data []byte, fails func([]byte) bool) []byte {
	cur := data
	src := GenMainBytes(cur)
	passed := map[string]bool{}
	try := func(cand []byte) bool {
		s := GenMainBytes(cand)
		if len(s) > len(src) || (len(cand) >= len(cur) && len(s) == len(src)) || passed[s] {
			return false
		}
		if !fails(cand) {
			passed[s] = true
			return false
		}
		cur, src = cand, s
		return true
	}
	for shrunk := true; shrunk; {
		shrunk = false
		for step := len(cur) / 2; step >= 1; {
			if step < len(cur) && try(cur[:len(cur)-step]) {
				continue
			}
			step /= 2
		}
		for _, cand := range reductions(cur) {
			if try(cand) {
				shrunk = true
				break
			}
		}
	}
	return cur
}
