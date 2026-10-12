package fernsmith

import (
	"fmt"
	"strings"
)

// isScalar reports whether t is held inline rather than in a refcounted box.
func isScalar(t gtype) bool {
	switch t {
	case tI32, tI64, tU32, tU64, tU8, tBool, tF32, tF64, tColor, tStatus:
		return true
	}
	return false
}

// maybeEmitOwnershipCluster writes a statement group that puts a nested
// value through every ownership transition Perceus has to get right, on a
// program-declared struct with at least one boxed field:
//
//	let __own_a0: S = <literal>;              // fresh, unique
//	let __own_b0: S = __own_a0;               // now shared
//	let __own_c0: S = S { ...__own_a0, fK: <expr> };  // update while shared: must copy
//	let __own_d0: F = __own_b0.fJ;            // a boxed field projected out
//	__own_a0 = <S expr>;                      // the old value dropped while b and d hold it
//
// All four names join the scope, so later expressions read through them and
// the state fold observes what they hold. A reuse that wrote into the shared
// box, a projection that skipped its retain, or a reassignment that released
// the whole value instead of one reference each changes a value the oracle
// sees — and a leak or an over-release in any of them is what the sanitizer
// leg reports.
func (g *Generator) maybeEmitOwnershipCluster(b *strings.Builder, sc *scope) bool {
	if !g.on(featNested) {
		return false
	}
	var cands []gtype
	for _, st := range sortedKeys(g.structShapes) {
		if len(boxedFields(g.structShapes[st])) > 0 {
			cands = append(cands, st)
		}
	}
	if len(cands) == 0 {
		return false
	}
	// Exhaustion convention: `true` here = no cluster. Every structural
	// choice is drawn before anything is written, and a stream that runs out
	// among them falls back to the caller's plain statement: a cluster whose
	// struct or field changed with truncation would change the types the rest
	// of the program sees, and could grow it.
	if g.flip(0.8) {
		return false
	}
	st := cands[g.ch.intN(len(cands))]
	sh := g.structShapes[st]
	f := sh.fields[g.ch.intN(len(sh.fields))]
	boxed := boxedFields(sh)
	pf := boxed[g.ch.intN(len(boxed))]
	// The reassignment is E049 when a closure later captures `a` and the
	// value can reach a function, and nothing here knows what later code
	// captures; a type that reaches no function is legal either way.
	reassign := !g.reachesFn(st, nil) && !g.flip(0.5)
	if g.ch.exhausted() {
		return false
	}
	name := sh.name
	idx := g.ownCounter
	g.ownCounter++
	a := fmt.Sprintf("__own_a%d", idx)
	alias := fmt.Sprintf("__own_b%d", idx)
	upd := fmt.Sprintf("__own_c%d", idx)
	proj := fmt.Sprintf("__own_d%d", idx)

	fmt.Fprintf(b, "let %s: %s = ", a, name)
	g.dynStructLiteral(b, sc, st, sh, 1)
	b.WriteString("; ")
	fmt.Fprintf(b, "let %s: %s = %s; ", alias, name, a)
	fmt.Fprintf(b, "let %s: %s = (%s { ...%s, %s: ", upd, name, name, a, f.name)
	g.expr(b, sc, f.t, 1)
	b.WriteString(" }); ")
	fmt.Fprintf(b, "let %s: %s = %s.%s; ", proj, g.typeName(pf.t), alias, pf.name)
	sc.declare(st, a)
	if reassign {
		fmt.Fprintf(b, "%s = ", a)
		g.expr(b, sc, st, 1)
		b.WriteString("; ")
	}
	sc.declare(st, alias)
	sc.declare(st, upd)
	sc.declare(pf.t, proj)
	return true
}

// reachesFn reports whether a value of type t can hold a function value —
// the checker's typeReachesFunc, over the generator's types.
func (g *Generator) reachesFn(t gtype, seen map[gtype]bool) bool {
	if seen[t] {
		return false
	}
	if seen == nil {
		seen = map[gtype]bool{}
	}
	seen[t] = true
	switch t {
	case tFnI32I32, tArrFnI32I32:
		return true
	}
	if sh, ok := g.structShapes[t]; ok {
		for _, f := range sh.fields {
			if g.reachesFn(f.t, seen) {
				return true
			}
		}
	}
	if sh, ok := g.enumShapes[t]; ok {
		for _, v := range sh.variants {
			for _, p := range v.payload {
				if g.reachesFn(p, seen) {
					return true
				}
			}
		}
	}
	if elem, ok := g.arrShapes[t]; ok {
		return g.reachesFn(elem, seen)
	}
	return false
}

// boxedFields lists sh's fields that own a box.
func boxedFields(sh *structShape) []structField {
	var out []structField
	for _, f := range sh.fields {
		if !isScalar(f.t) {
			out = append(out, f)
		}
	}
	return out
}

// maxFoldTerms bounds how many values the state fold reads.
const maxFoldTerms = 24

// emitStateFold writes an i32 expression that folds every i32 and boolean
// value reachable from an in-scope var — the var itself or a projection
// through it — into one checksum, the way Csmith prints a checksum of all
// global state at exit. Without it the oracle sees only what the return
// expression happened to read, and a wrong value anywhere else in the
// program, which is most of where a miscompile lands, passes unseen.
//
// Integers wrap, so the fold never traps. Writes `0i32` when there is
// nothing to read.
func (g *Generator) emitStateFold(b *strings.Builder, sc *scope) {
	terms := g.foldTerms(sc)
	if len(terms) == 0 {
		b.WriteString("0i32")
		return
	}
	b.WriteString(strings.Repeat("(", len(terms)))
	b.WriteString("0i32")
	for _, t := range terms {
		fmt.Fprintf(b, " * 31i32 + %s)", t)
	}
}

// foldTerms lists the i32-valued reads emitStateFold combines.
func (g *Generator) foldTerms(sc *scope) []string {
	var out []string
	add := func(s string) bool {
		out = append(out, s)
		return len(out) < maxFoldTerms
	}
	for _, name := range sc.inScope(tI32) {
		if !add(name) {
			return out
		}
	}
	for _, p := range g.accessPaths(sc, tI32) {
		if !add(p) {
			return out
		}
	}
	for _, name := range sc.inScope(tBool) {
		if !add(fmt.Sprintf("(if (%s) { 1i32 } else { 0i32 })", name)) {
			return out
		}
	}
	for _, p := range g.accessPaths(sc, tBool) {
		if !add(fmt.Sprintf("(if (%s) { 1i32 } else { 0i32 })", p)) {
			return out
		}
	}
	return out
}
