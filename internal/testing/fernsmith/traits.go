package fernsmith

import (
	"fmt"
	"strings"
)

// traitShape is the program's user trait: the types that implement it, and
// for each whether its impl overrides the default method.
type traitShape struct {
	impls     []gtype
	overrides []bool
}

// declareTrait writes a user trait, its impls, and three functions that use
// it, and registers those as helpers so call sites reach them:
//
//	trait Tr0 { function t0_m(self: Self, x: i32): i32;
//	            function t0_d(self: Self): i32 { return (self.t0_m(K) ^ K); } }
//	impl Tr0 for S1 { function t0_m(self: Self, x: i32): i32 { return <expr>; } }
//	impl Tr0 for i32 { ... function t0_d(self: Self): i32 { return <expr>; } }
//	function tg0[T: Tr0](a: T, b: T, x: i32): i32 { ... }   // static dispatch
//	function dg0(d: dyn Tr0, x: i32): i32 { ... }           // dynamic dispatch
//	function da0(ds: dyn Tr0[], x: i32): i32 { ... }        // a vtable per element
//	struct DH0 { d: dyn Tr0, k: i32 }                       // a trait object in a field
//
// and, optionally, a second trait whose constructor is an associated
// function, reached through the type parameter of a generic:
//
//	trait Mk0 { function mk0_new(x: i32): Self; function mk0_v(self: Self): i32; }
//	impl Mk0 for S1 { function mk0_new(x: i32): Self { return <S1 expr>; } ... }
//	function tm0[T: Mk0](a: T, x: i32): i32 { let b: T = T.mk0_new(x); ... }
//
// and, optionally, a generic trait instantiated at i32, reached both ways:
//
//	trait Tc0[T] { function tc0_get(self: Self, d: T): T; }
//	impl Tc0[i32] for S1 { function tc0_get(self: Self, d: i32): i32 { return <expr>; } }
//	function tcg0[T: Tc0[i32]](o: T, d: i32): i32 { ... }    // static dispatch
//	function dc0(o: dyn Tc0[i32], d: i32): i32 { ... }      // dynamic dispatch
//
// The impl bodies are generated expressions over `self` and `x`, so each
// implementer computes something different and a call dispatched to the
// wrong impl, or a default run where an override should be, changes the
// answer. tg0 is monomorphised per implementer; dg0 and da0 are not.
func (g *Generator) declareTrait(b *strings.Builder) {
	if !g.on(featTraits) {
		return
	}
	cands := append(sortedKeys(g.structShapes), tI32)
	// Exhaustion convention: `true` here = no trait. Every choice is drawn
	// before anything is written, so a stream that runs out among them
	// declares no trait rather than a different one.
	if g.flip(0.5) {
		return
	}
	n := 1 + g.ch.intN(minInt(len(cands), 3))
	ts := &traitShape{}
	for i := 0; i < n; i++ {
		k := g.ch.intN(len(cands))
		ts.impls = append(ts.impls, cands[k])
		cands = append(cands[:k:k], cands[k+1:]...)
		// `true` = no override, the smaller impl.
		ts.overrides = append(ts.overrides, !g.flip(0.6))
	}
	k1, k2 := g.ch.intN(64), g.ch.intN(64)
	// `true` = no Mk0, no Tc0.
	assoc := !g.flip(0.5)
	generic := !g.flip(0.5)
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
}

// declareGenericTrait writes Tc0[T], its impls at i32 for the given types,
// and tcg0 and dc0, and registers both as helpers at each implementer.
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

// declareAssocTrait writes Mk0, its impls for the given types, and tm0, and
// registers tm0 as a helper at each of them. Each impl's constructor and
// accessor are generated expressions, so tm0 at the wrong implementer, or
// T.mk0_new resolved to the wrong impl, changes the answer.
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

// maybeEmitTraitCluster writes a statement group that puts values of the
// trait's implementers behind `dyn`:
//
//	let __dyn_a0: dyn Tr0[] = [<S1 expr>, <i32 expr>];   // mixed vtables
//	let __dyn_v0: dyn Tr0 = <S1 expr>;
//	__dyn_v0 = <i32 expr>;                                // the old object dropped
//	let __dyn_h0: DH0 = DH0 { d: <S1 expr>, k: <i32> };   // optional
//	let __dyn_g0: DH0 = DH0 { ...__dyn_h0, k: <i32> };    //   shares h0's object
//	let __dyn_e0: dyn Tr0 = <i32 expr>;                   // optional; a tuple
//	let __dyn_p0: (dyn Tr0, i32) = (__dyn_e0, <i32>);     //   literal coerces nothing
//	let __dyn_c0: i32 = (match (__dyn_v0 as? S1) {        // optional
//	    Some(__dyn_s0) => <i32 expr over __dyn_s0>, None => <i32> });
//	let __dyn_r0: i32 = (da0(__dyn_a0, <i32>) + (__dyn_a0[1i32].t0_d() ^ dg0(__dyn_v0, <i32>)))
//	    + (__dyn_g0.d.t0_m(__dyn_h0.k) ^ __dyn_g0.k) + __dyn_p0.0.t0_d() + __dyn_c0;
//
// The downcast's subject is the reassigned object or an array element, and
// its target a struct implementer, which the subject may or may not be.
// Only the result joins the scope: a `dyn` value reaches a function, so a
// later closure capturing the reassigned local would be E049.
func (g *Generator) maybeEmitTraitCluster(b *strings.Builder, sc *scope) bool {
	if g.trait == nil {
		return false
	}
	// Exhaustion convention: `true` here = no cluster; every choice is drawn
	// before anything is written, as in maybeEmitOwnershipCluster.
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
	// `true` = the part is left out.
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

// structImpls is the implementers an `as?` can name: structs, not i32.
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
