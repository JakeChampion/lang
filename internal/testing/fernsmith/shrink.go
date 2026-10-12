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

// mainBytesSpans is GenMainBytes with the list elements it drew.
func mainBytesSpans(data []byte) (string, []span) {
	g := newByteGen(data, DefaultConfig())
	src := g.MainProgram()
	return src, g.spans
}

// withoutSpan is data less the element s, its list one shorter.
func withoutSpan(data []byte, s span) []byte {
	out := make([]byte, 0, len(data)-(s.end-s.start))
	out = append(out, data[:s.start]...)
	out = append(out, data[s.end:]...)
	out[s.count]--
	return out
}

// ShrinkMainBytes reduces a corpus for which fails holds to a smaller one for
// which it still holds, through GenMainBytes' structure. It alternates two
// moves until neither shortens the program: cutting bytes off the end, the
// minimisation contract every byte-driven entry point keeps, and deleting one
// helper or statement whole, which truncation cannot reach when a later part of
// the program is what fails. A move is kept when fails still holds and the
// program it generates is no longer; every move shortens the corpus, so the
// loop ends.
func ShrinkMainBytes(data []byte, fails func([]byte) bool) []byte {
	cur := data
	src := GenMainBytes(cur)
	try := func(cand []byte) bool {
		s := GenMainBytes(cand)
		if len(s) > len(src) || !fails(cand) {
			return false
		}
		cur, src = cand, s
		return true
	}
	for shrunk := true; shrunk; {
		shrunk = false
		for step := len(cur) / 2; step >= 1; {
			if step < len(cur) && try(cur[:len(cur)-step]) {
				shrunk = true
				continue
			}
			step /= 2
		}
		_, spans := mainBytesSpans(cur)
		sort.SliceStable(spans, func(i, j int) bool {
			return spans[i].end-spans[i].start > spans[j].end-spans[j].start
		})
		for _, s := range spans {
			if cur[s.count] > 0 && try(withoutSpan(cur, s)) {
				shrunk = true
				break
			}
		}
	}
	return cur
}
