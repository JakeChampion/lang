package fernsmith

import (
	"sort"
	"strings"
)

type span struct{ count, start, end int }

func (g *Generator) pos() int {
	if c, ok := g.ch.(*byteChooser); ok {
		return c.pos
	}
	return -1
}

func (g *Generator) listCount(max int) (int, int) {
	at := g.pos()
	n := g.ch.intN(max)
	if at < 0 || g.pos() == at {
		return n, -1
	}
	return n, at
}

func (g *Generator) markSpan(count, start int) {
	if count >= 0 && start >= 0 && g.pos() > start {
		g.spans = append(g.spans, span{count: count, start: start, end: g.pos()})
	}
}

type exprSpan struct {
	start, end int
	t          gtype
	lead       bool
}

func (g *Generator) markExpr(start int, t gtype, lead bool) {
	if start >= 0 && g.pos() > start {
		g.exprs = append(g.exprs, exprSpan{start: start, end: g.pos(), t: t, lead: lead})
	}
}

func mainBytesSpans(data []byte) (string, []span, []exprSpan) {
	g := newByteGen(data, DefaultConfig())
	src := g.MainProgram()
	return src, g.spans, g.exprs
}

func withoutSpan(data []byte, s span) []byte {
	out := make([]byte, 0, len(data)-(s.end-s.start))
	out = append(out, data[:s.start]...)
	out = append(out, data[s.end:]...)
	out[s.count]--
	return out
}

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

// ShrinkSource deletes the program's lines, and the statements inside each
// line, one at a time, last first, keeping each deletion after which fails
// still holds, until no deletion does. It reaches what ShrinkMainBytes cannot:
// the declarations every program's prelude carries whatever its corpus.
func ShrinkSource(src string, fails func(string) bool) string {
	lines := strings.Split(src, "\n")
	try := func(i int, line string, drop bool) bool {
		cand := append([]string{}, lines[:i]...)
		if !drop {
			cand = append(cand, line)
		}
		cand = append(cand, lines[i+1:]...)
		if !fails(strings.Join(cand, "\n")) {
			return false
		}
		lines = cand
		return true
	}
	for shrunk := true; shrunk; {
		shrunk = false
		for i := len(lines) - 1; i >= 0; i-- {
			if strings.TrimSpace(lines[i]) == "" {
				continue
			}
			if !strings.HasPrefix(lines[i], "function main(") && try(i, "", true) {
				shrunk = true
				continue
			}
			cuts := stmtCuts(lines[i])
			for k := len(cuts) - 2; k >= 0; k-- {
				line := lines[i]
				if try(i, line[:cuts[k]]+line[cuts[k+1]:], false) {
					shrunk = true
				}
			}
		}
	}
	return strings.Join(lines, "\n")
}

// stmtCuts is where each statement of line's outermost body starts, then where
// the last one ends: after its opening brace and after each `; ` at depth one.
func stmtCuts(line string) []int {
	var cuts []int
	depth := 0
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '(', '[', '{':
			depth++
			if depth == 1 && line[i] == '{' && cuts == nil {
				cuts = append(cuts, skipSpace(line, i+1))
			}
		case ')', ']', '}':
			depth--
		case ';':
			if depth == 1 && cuts != nil {
				cuts = append(cuts, skipSpace(line, i+1))
			}
		}
	}
	return cuts
}

func skipSpace(s string, i int) int {
	if i < len(s) && s[i] == ' ' {
		return i + 1
	}
	return i
}
