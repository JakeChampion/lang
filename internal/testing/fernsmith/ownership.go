package fernsmith

import (
	"fmt"
	"strings"
)

func isScalar(t gtype) bool {
	switch t {
	case tI32, tI64, tU32, tU64, tU8, tBool, tF32, tF64, tColor, tStatus:
		return true
	}
	return false
}

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
	if g.flip(0.8) {
		return false
	}
	st := cands[g.ch.intN(len(cands))]
	sh := g.structShapes[st]
	f := sh.fields[g.ch.intN(len(sh.fields))]
	boxed := boxedFields(sh)
	pf := boxed[g.ch.intN(len(boxed))]
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

func boxedFields(sh *structShape) []structField {
	var out []structField
	for _, f := range sh.fields {
		if !isScalar(f.t) {
			out = append(out, f)
		}
	}
	return out
}

const maxFoldTerms = 24

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
	for t := gtype(0); t < g.nextNominal; t++ {
		form := g.foldForm(t)
		if form == "" {
			continue
		}
		for _, name := range sc.inScope(t) {
			if !add(fmt.Sprintf(form, name)) {
				return out
			}
		}
	}
	return out
}

// foldForm is how the state fold reads a local of type t as an i32, or ""
// when it does not. A float is left out: its conversion of a NaN is not
// the same on every backend.
func (g *Generator) foldForm(t gtype) string {
	switch t {
	case tI64, tU32, tU64, tU8:
		return "(%s as i32)"
	case tString, tMapI32I32:
		return "%s.len()"
	case tOptI32:
		return "(match (%s) { Some(__fv) => __fv, None => 0i32 })"
	case tResI32I32:
		return "(match (%s) { Ok(__fv) => __fv, Err(__fv) => __fv })"
	}
	if _, ok := g.arrayElemOf(t); ok {
		return "%s.len()"
	}
	var variants []enumVariant
	switch t {
	case tColor:
		variants = []enumVariant{{name: "Red"}, {name: "Green"}, {name: "Blue"}}
	case tStatus:
		variants = []enumVariant{{name: "Active"}, {name: "Inactive"}, {name: "Pending"}}
	default:
		if sh, ok := g.enumShapes[t]; ok {
			variants = sh.variants
		}
	}
	if len(variants) == 0 {
		return ""
	}
	// An enum reads as the number of its variant.
	var b strings.Builder
	b.WriteString("(match (%s) { ")
	for i, v := range variants {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(v.name)
		if len(v.payload) > 0 {
			b.WriteString("(" + strings.TrimSuffix(strings.Repeat("_, ", len(v.payload)), ", ") + ")")
		}
		fmt.Fprintf(&b, " => %di32", i+1)
	}
	b.WriteString(" })")
	return b.String()
}

func (g *Generator) maybeEmitClosureCluster(b *strings.Builder, sc *scope) bool {
	if !g.on(featClosure) {
		return false
	}
	if g.flip(0.85) {
		return false
	}
	op := []string{"+", "*", "^"}[g.ch.intN(3)]
	escaping := !g.flip(0.5)
	if g.ch.exhausted() {
		return false
	}
	idx := g.cloCounter
	g.cloCounter++
	c := fmt.Sprintf("__clo_c%d", idx)
	f := fmt.Sprintf("__clo_f%d", idx)
	r := fmt.Sprintf("__clo_r%d", idx)
	x := fmt.Sprintf("__lam_x%d", g.lambdaCounter)
	g.lambdaCounter++

	fmt.Fprintf(b, "let %s: i32 = ", c)
	g.expr(b, sc, tI32, 1)
	b.WriteString("; ")
	sc.declare(tI32, c)
	inner := newScope(sc)
	inner.declare(tI32, x)
	fmt.Fprintf(b, "let %s: (i32) => i32 = ((%s: i32) => ((%s - %s) %s ", f, x, x, c, op)
	prevRet := g.currentReturnType
	g.currentReturnType = tI32
	g.inClosure++
	g.expr(b, inner, tI32, 1)
	g.inClosure--
	g.currentReturnType = prevRet
	b.WriteString(")); ")
	sc.declare(tFnI32I32, f)
	fmt.Fprintf(b, "let %s: i32 = ", r)
	if escaping {
		fmt.Fprintf(b, "[%s][0i32](", f)
	} else {
		fmt.Fprintf(b, "%s(", f)
	}
	g.expr(b, sc, tI32, 1)
	b.WriteString("); ")
	sc.declare(tI32, r)
	return true
}
