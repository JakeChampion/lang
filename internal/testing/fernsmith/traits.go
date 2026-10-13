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
	for _, t := range ts.impls {
		g.helpers = append(g.helpers,
			helperSig{name: "tg0", params: []gtype{t, t, tI32}, retType: tI32},
			helperSig{name: "dg0", params: []gtype{t, tI32}, retType: tI32})
	}
}

// maybeEmitTraitCluster writes a statement group that puts values of the
// trait's implementers behind `dyn`:
//
//	let __dyn_a0: dyn Tr0[] = [<S1 expr>, <i32 expr>];   // mixed vtables
//	let __dyn_v0: dyn Tr0 = <S1 expr>;
//	__dyn_v0 = <i32 expr>;                                // the old object dropped
//	let __dyn_r0: i32 = (da0(__dyn_a0, <i32>) + (__dyn_a0[1i32].t0_d() ^ dg0(__dyn_v0, <i32>)));
//
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
	fmt.Fprintf(b, "; let %s: i32 = (da0(%s, ", r, arr)
	g.expr(b, sc, tI32, 1)
	fmt.Fprintf(b, ") + (%s[%di32].t0_d() ^ dg0(%s, ", arr, at, v)
	g.expr(b, sc, tI32, 1)
	b.WriteString("))); ")
	sc.declare(tI32, r)
	return true
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
