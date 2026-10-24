package fernsmith

import (
	"fmt"
	"strings"
)

type traitShape struct {
	impls     []gtype
	overrides []bool
}

func (g *Generator) declareTrait(b *strings.Builder) {
	if !g.on(featTraits) {
		return
	}
	cands := append(sortedKeys(g.structShapes), tI32)
	if g.flip(0.5) {
		return
	}
	n := 1 + g.ch.intN(minInt(len(cands), 3))
	ts := &traitShape{}
	for i := 0; i < n; i++ {
		k := g.ch.intN(len(cands))
		ts.impls = append(ts.impls, cands[k])
		cands = append(cands[:k:k], cands[k+1:]...)
		ts.overrides = append(ts.overrides, !g.flip(0.6))
	}
	k1, k2 := g.ch.intN(64), g.ch.intN(64)
	assoc := !g.flip(0.5)
	generic := !g.flip(0.5)
	var items []gtype
	if !g.flip(0.5) {
		for range ts.impls {
			items = append(items, []gtype{tI32, tI64, tString}[g.ch.intN(3)])
		}
	}
	if g.ch.exhausted() {
		return
	}
	g.trait = ts

	fmt.Fprintf(b, "trait Tr0 { function t0_m(self: Self, x: i32): i32; "+
		"function t0_d(self: Self): i32 { return (self.t0_m(%di32) ^ %di32); } }\n", k1, k2)
	prevRet := g.currentReturnType
	g.currentReturnType = tI32
	for i, t := range ts.impls {
		fmt.Fprintf(b, "impl Tr0 for %s { function t0_m(self: Self, x: i32): i32 { return ", g.typeName(t))
		sc := newScope(nil)
		sc.declare(t, "self")
		sc.declare(tI32, "x")
		g.expr(b, sc, tI32, 1)
		b.WriteString("; }")
		if ts.overrides[i] {
			b.WriteString(" function t0_d(self: Self): i32 { return ")
			sc := newScope(nil)
			sc.declare(t, "self")
			g.expr(b, sc, tI32, 1)
			b.WriteString("; }")
		}
		b.WriteString(" }\n")
	}
	g.currentReturnType = prevRet
	b.WriteString("function tg0[T: Tr0](a: T, b: T, x: i32): i32 { return ((a.t0_m(x) * 3i32) + b.t0_d()); }\n")
	b.WriteString("function dg0(d: dyn Tr0, x: i32): i32 { return (d.t0_m(x) - d.t0_d()); }\n")
	b.WriteString("function da0(ds: dyn Tr0[], x: i32): i32 { let t: i32 = x; " +
		"for d in ds { t = ((t * 31i32) + d.t0_m(x)); } return (t ^ ds.len()); }\n")
	b.WriteString("struct DH0 { d: dyn Tr0, k: i32 }\n")
	for _, t := range ts.impls {
		g.helpers = append(g.helpers,
			helperSig{name: "tg0", params: []gtype{t, t, tI32}, retType: tI32},
			helperSig{name: "dg0", params: []gtype{t, tI32}, retType: tI32})
	}
	if assoc {
		g.declareAssocTrait(b, ts.impls)
	}
	if generic {
		g.declareGenericTrait(b, ts.impls)
	}
	if items != nil {
		g.declareAssocTypeTrait(b, ts.impls, items)
	}
}

func (g *Generator) declareAssocTypeTrait(b *strings.Builder, impls, items []gtype) {
	b.WriteString("trait Pr0 { type Item; function pr0_get(self: Self): Self::Item; }\n")
	prevRet := g.currentReturnType
	for i, t := range impls {
		fmt.Fprintf(b, "impl Pr0 for %s { type Item = %s; function pr0_get(self: Self): Self::Item { return ",
			g.typeName(t), g.typeName(items[i]))
		g.currentReturnType = items[i]
		sc := newScope(nil)
		sc.declare(t, "self")
		g.expr(b, sc, items[i], 1)
		b.WriteString("; } }\n")
	}
	g.currentReturnType = prevRet
	b.WriteString("function tp0[H: Pr0](h: H): H::Item { return h.pr0_get(); }\n")
	b.WriteString("function dp0(d: dyn Pr0[Item = i32]): i32 { return (d.pr0_get() * 9i32); }\n")
	for i, t := range impls {
		g.helpers = append(g.helpers, helperSig{name: "tp0", params: []gtype{t}, retType: items[i]})
		if items[i] == tI32 {
			g.helpers = append(g.helpers, helperSig{name: "dp0", params: []gtype{t}, retType: tI32})
		}
	}
}

func (g *Generator) declareGenericTrait(b *strings.Builder, impls []gtype) {
	b.WriteString("trait Tc0[T] { function tc0_get(self: Self, d: T): T; }\n")
	prevRet := g.currentReturnType
	g.currentReturnType = tI32
	for _, t := range impls {
		fmt.Fprintf(b, "impl Tc0[i32] for %s { function tc0_get(self: Self, d: i32): i32 { return ", g.typeName(t))
		sc := newScope(nil)
		sc.declare(t, "self")
		sc.declare(tI32, "d")
		g.expr(b, sc, tI32, 1)
		b.WriteString("; } }\n")
	}
	g.currentReturnType = prevRet
	b.WriteString("function tcg0[T: Tc0[i32]](o: T, d: i32): i32 { return (o.tc0_get(d) - (d * 5i32)); }\n")
	b.WriteString("function dc0(o: dyn Tc0[i32], d: i32): i32 { return (o.tc0_get(d) ^ 77i32); }\n")
	for _, t := range impls {
		g.helpers = append(g.helpers,
			helperSig{name: "tcg0", params: []gtype{t, tI32}, retType: tI32},
			helperSig{name: "dc0", params: []gtype{t, tI32}, retType: tI32})
	}
}

func (g *Generator) declareAssocTrait(b *strings.Builder, impls []gtype) {
	b.WriteString("trait Mk0 { function mk0_new(x: i32): Self; function mk0_v(self: Self): i32; }\n")
	prevRet := g.currentReturnType
	for _, t := range impls {
		fmt.Fprintf(b, "impl Mk0 for %s { function mk0_new(x: i32): Self { return ", g.typeName(t))
		g.currentReturnType = t
		sc := newScope(nil)
		sc.declare(tI32, "x")
		g.expr(b, sc, t, 1)
		b.WriteString("; } function mk0_v(self: Self): i32 { return ")
		g.currentReturnType = tI32
		sc = newScope(nil)
		sc.declare(t, "self")
		g.expr(b, sc, tI32, 1)
		b.WriteString("; } }\n")
	}
	g.currentReturnType = prevRet
	b.WriteString("function tm0[T: Mk0](a: T, x: i32): i32 { let b: T = T.mk0_new(x); " +
		"return ((a.mk0_v() * 7i32) ^ b.mk0_v()); }\n")
	for _, t := range impls {
		g.helpers = append(g.helpers, helperSig{name: "tm0", params: []gtype{t, tI32}, retType: tI32})
	}
}

func (g *Generator) maybeEmitTraitCluster(b *strings.Builder, sc *scope) bool {
	if g.trait == nil {
		return false
	}
	if g.flip(0.8) {
		return false
	}
	impls := g.trait.impls
	elems := make([]gtype, 2+g.ch.intN(2))
	for i := range elems {
		elems[i] = impls[g.ch.intN(len(impls))]
	}
	first, second := impls[g.ch.intN(len(impls))], impls[g.ch.intN(len(impls))]
	at := g.ch.intN(len(elems))
	var held, tupled, downTo gtype
	hold := !g.flip(0.5)
	if hold {
		held = impls[g.ch.intN(len(impls))]
	}
	tuple := !g.flip(0.5)
	if tuple {
		tupled = impls[g.ch.intN(len(impls))]
	}
	structs := structImpls(impls)
	down := len(structs) > 0 && !g.flip(0.3)
	downFromArr := false
	if down {
		downTo = structs[g.ch.intN(len(structs))]
		downFromArr = !g.flip(0.5)
	}
	if g.ch.exhausted() {
		return false
	}
	idx := g.dynCounter
	g.dynCounter++
	arr := fmt.Sprintf("__dyn_a%d", idx)
	v := fmt.Sprintf("__dyn_v%d", idx)
	r := fmt.Sprintf("__dyn_r%d", idx)

	fmt.Fprintf(b, "let %s: dyn Tr0[] = [", arr)
	for i, t := range elems {
		if i > 0 {
			b.WriteString(", ")
		}
		g.expr(b, sc, t, 1)
	}
	b.WriteString("]; ")
	fmt.Fprintf(b, "let %s: dyn Tr0 = ", v)
	g.expr(b, sc, first, 1)
	fmt.Fprintf(b, "; %s = ", v)
	g.expr(b, sc, second, 1)
	b.WriteString("; ")
	tail := ""
	if hold {
		h, h2 := fmt.Sprintf("__dyn_h%d", idx), fmt.Sprintf("__dyn_g%d", idx)
		fmt.Fprintf(b, "let %s: DH0 = DH0 { d: ", h)
		g.expr(b, sc, held, 1)
		b.WriteString(", k: ")
		g.expr(b, sc, tI32, 1)
		fmt.Fprintf(b, " }; let %s: DH0 = DH0 { ...%s, k: ", h2, h)
		g.expr(b, sc, tI32, 1)
		b.WriteString(" }; ")
		tail += fmt.Sprintf(" + (%s.d.t0_m(%s.k) ^ %s.k)", h2, h, h2)
	}
	if tuple {
		p, e := fmt.Sprintf("__dyn_p%d", idx), fmt.Sprintf("__dyn_e%d", idx)
		fmt.Fprintf(b, "let %s: dyn Tr0 = ", e)
		g.expr(b, sc, tupled, 1)
		fmt.Fprintf(b, "; let %s: (dyn Tr0, i32) = (%s, ", p, e)
		g.expr(b, sc, tI32, 1)
		b.WriteString("); ")
		tail += fmt.Sprintf(" + (%s.0.t0_d() - %s.1)", p, p)
	}
	if down {
		c, bind := fmt.Sprintf("__dyn_c%d", idx), fmt.Sprintf("__dyn_s%d", idx)
		subject := v
		if downFromArr {
			subject = fmt.Sprintf("%s[%di32]", arr, at)
		}
		fmt.Fprintf(b, "let %s: i32 = (match (%s as? %s) { Some(%s) => ", c, subject, g.typeName(downTo), bind)
		inner := newScope(sc)
		inner.declare(downTo, bind)
		g.expr(b, inner, tI32, 1)
		b.WriteString(", None => ")
		g.expr(b, sc, tI32, 1)
		b.WriteString(" }); ")
		tail += " + " + c
	}
	fmt.Fprintf(b, "let %s: i32 = ((da0(%s, ", r, arr)
	g.expr(b, sc, tI32, 1)
	fmt.Fprintf(b, ") + (%s[%di32].t0_d() ^ dg0(%s, ", arr, at, v)
	g.expr(b, sc, tI32, 1)
	fmt.Fprintf(b, ")))%s); ", tail)
	sc.declare(tI32, r)
	return true
}

func structImpls(impls []gtype) []gtype {
	var out []gtype
	for _, t := range impls {
		if t != tI32 {
			out = append(out, t)
		}
	}
	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
