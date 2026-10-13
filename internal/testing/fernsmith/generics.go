package fernsmith

import (
	"fmt"
	"strings"
)

// declareGenerics writes a program-declared generic struct and enum, methods
// over each, and a generic constructor:
//
//	struct G0[T] { v: T, n: i32 }
//	function (g: G0[T]) get(): T { ... }
//	function (g: G0[T]) bump(k: i32): G0[T] { return G0 { ...g, n: ... }; }
//	enum GO0[T] { __G0_Nil, __G0_Has(T, i32) }
//	function (o: GO0[T]) or(d: T): T { match ... }
//	function (o: GO0[T]) count(): i32 { match ... }
//	function gmk0[T](v: T, n: i32): G0[T] { ... }
//
// The cluster instantiates them at a type drawn from main's pool, boxed ones
// included, so each program monomorphises them at a different payload layout
// and the functional update inside bump copies a box it shares.
func (g *Generator) declareGenerics(b *strings.Builder) {
	if !g.on(featGenericDecl) {
		return
	}
	// Exhaustion convention: `true` here = no declarations.
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

// maybeEmitGenericCluster writes a statement group over the generic
// declarations at an element type E:
//
//	let __gen_x0: E = <E expr>;
//	let __gen_a0: G0[E] = gmk0(__gen_x0, <i32>);
//	let __gen_b0: G0[E] = __gen_a0.bump(<i32>);       // or G0 { ...__gen_a0, n: <i32> }
//	let __gen_o0: GO0[E] = __G0_Nil;
//	if (<bool>) { __gen_o0 = __G0_Has(__gen_b0.get(), __gen_a0.n); }
//	let __gen_e0: E = __gen_o0.or(__gen_a0.get());
//	let __gen_r0: i32 = ((__gen_a0.n ^ __gen_b0.n) + __gen_o0.count());
//
// __gen_e0 and __gen_r0 join the scope. The others stay out of it, so no
// later closure captures the reassigned __gen_o0.
func (g *Generator) maybeEmitGenericCluster(b *strings.Builder, sc *scope) bool {
	if !g.generics {
		return false
	}
	// Exhaustion convention: `true` here = no cluster; every choice is drawn
	// before anything is written, as in maybeEmitOwnershipCluster.
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
