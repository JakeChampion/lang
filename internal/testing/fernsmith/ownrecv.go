package fernsmith

import (
	"fmt"
	"strings"
)

func (g *Generator) declareOwnRecv(b *strings.Builder) {
	if !g.on(featOwnRecv) {
		return
	}
	if g.flip(0.5) || g.ch.exhausted() {
		return
	}
	g.ownRecv = true
	b.WriteString("struct OB0 { xs: i32[], n: i32 }\n")
	b.WriteString("function (own b: OB0) push(x: i32): OB0 { return OB0 { xs: b.xs.append(x), n: ((b.n * 3i32) + x) }; }\n")
}

func (g *Generator) maybeEmitOwnRecvCluster(b *strings.Builder, sc *scope) bool {
	if !g.ownRecv {
		return false
	}
	if g.flip(0.8) {
		return false
	}
	alias := !g.flip(0.5)
	pushes := g.ch.intN(3)
	loop := 0
	if !g.flip(0.5) {
		loop = 1 + g.ch.intN(4)
	}
	if g.ch.exhausted() {
		return false
	}
	idx := g.ownRecvCounter
	g.ownRecvCounter++
	ob := fmt.Sprintf("__orv_b%d", idx)
	a := fmt.Sprintf("__orv_a%d", idx)
	i := fmt.Sprintf("__orv_i%d", idx)
	c := fmt.Sprintf("__orv_c%d", idx)
	r := fmt.Sprintf("__orv_r%d", idx)

	fmt.Fprintf(b, "let %s: OB0 = OB0 { xs: [", ob)
	g.expr(b, sc, tI32, 1)
	b.WriteString(", ")
	g.expr(b, sc, tI32, 1)
	b.WriteString("], n: ")
	g.expr(b, sc, tI32, 1)
	b.WriteString(" }; ")
	if alias {
		fmt.Fprintf(b, "let %s: i32[] = %s.xs; ", a, ob)
	}
	for k := 0; k < pushes; k++ {
		fmt.Fprintf(b, "%s = %s.push(", ob, ob)
		g.expr(b, sc, tI32, 1)
		b.WriteString("); ")
	}
	if loop > 0 {
		fmt.Fprintf(b, "let %s: i32 = 0i32; while (%s < %di32) { %s = %s.push(%s); %s = %s + 1i32; } ",
			i, i, loop, ob, ob, i, i, i)
	}
	fmt.Fprintf(b, "let %s: OB0 = %s.push(", c, ob)
	g.expr(b, sc, tI32, 1)
	b.WriteString("); ")
	fmt.Fprintf(b, "let %s: i32 = (((%s.xs.len() * 31i32) + (%s.xs[(%s.xs.len() - 1i32)] ^ %s.n))", r, c, c, c, c)
	if alias {
		fmt.Fprintf(b, " + (%s.len() ^ %s[1i32])", a, a)
	}
	b.WriteString("); ")
	sc.declare(tI32, r)
	return true
}
