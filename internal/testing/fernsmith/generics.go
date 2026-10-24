package fernsmith

import (
	"fmt"
	"strings"
)

func (g *Generator) declareGenerics(b *strings.Builder) {
	if !g.on(featGenericDecl) {
		return
	}
	if g.flip(0.5) || g.ch.exhausted() {
		return
	}
	g.generics = true
	b.WriteString("struct G0[T] { v: T, n: i32 }\n")
	b.WriteString("function (g: G0[T]) get(): T { return g.v; }\n")
	b.WriteString("function (g: G0[T]) bump(k: i32): G0[T] { return G0 { ...g, n: ((g.n * 3i32) + k) }; }\n")
	b.WriteString("enum GO0[T] { __G0_Nil, __G0_Has(T, i32) }\n")
	b.WriteString("function (o: GO0[T]) or(d: T): T { match (o) { __G0_Has(x, _) => { return x; }, __G0_Nil => { return d; } } }\n")
	b.WriteString("function (o: GO0[T]) count(): i32 { match (o) { __G0_Has(_, k) => { return k; }, __G0_Nil => { return 0i32; } } }\n")
	b.WriteString("function gmk0[T](v: T, n: i32): G0[T] { return G0 { v: v, n: n }; }\n")
}

func (g *Generator) maybeEmitGenericCluster(b *strings.Builder, sc *scope) bool {
	if !g.generics {
		return false
	}
	if g.flip(0.8) {
		return false
	}
	e := g.pickMainVarType()
	spread := !g.flip(0.5)
	if g.ch.exhausted() {
		return false
	}
	idx := g.genCounter
	g.genCounter++
	en := g.typeName(e)
	x := fmt.Sprintf("__gen_x%d", idx)
	a := fmt.Sprintf("__gen_a%d", idx)
	bb := fmt.Sprintf("__gen_b%d", idx)
	o := fmt.Sprintf("__gen_o%d", idx)
	ev := fmt.Sprintf("__gen_e%d", idx)
	r := fmt.Sprintf("__gen_r%d", idx)

	fmt.Fprintf(b, "let %s: %s = ", x, en)
	g.expr(b, sc, e, 1)
	fmt.Fprintf(b, "; let %s: G0[%s] = gmk0(%s, ", a, en, x)
	g.expr(b, sc, tI32, 1)
	if spread {
		fmt.Fprintf(b, "); let %s: G0[%s] = G0 { ...%s, n: ", bb, en, a)
		g.expr(b, sc, tI32, 1)
		b.WriteString(" }; ")
	} else {
		fmt.Fprintf(b, "); let %s: G0[%s] = %s.bump(", bb, en, a)
		g.expr(b, sc, tI32, 1)
		b.WriteString("); ")
	}
	fmt.Fprintf(b, "let %s: GO0[%s] = __G0_Nil; if (", o, en)
	g.boolExpr(b, sc, 1)
	fmt.Fprintf(b, ") { %s = __G0_Has(%s.get(), %s.n); } ", o, bb, a)
	fmt.Fprintf(b, "let %s: %s = %s.or(%s.get()); ", ev, en, o, a)
	fmt.Fprintf(b, "let %s: i32 = ((%s.n ^ %s.n) + %s.count()); ", r, a, bb, o)
	sc.declare(e, ev)
	sc.declare(tI32, r)
	return true
}
