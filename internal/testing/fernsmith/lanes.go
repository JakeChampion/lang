package fernsmith

import (
	"fmt"
	"strings"
)

// laneKind is one SIMD lane type: its name, its lane count and its scalar.
type laneKind struct {
	name, scalar string
	n            int
}

var laneKinds = []laneKind{{"f64x2", "f64", 2}, {"f32x4", "f32", 4}}

// declareLanes declares the helpers a lane cluster calls: lane values in and
// out of a call, and through an array.
func (g *Generator) declareLanes(b *strings.Builder) {
	if !g.profile.floatsAllowed() || !g.on(featLanes) || g.flip(0.4) || g.ch.exhausted() {
		return
	}
	g.lanes = true
	for _, k := range laneKinds {
		fmt.Fprintf(b, "@noinline function ln_%s(a: %s, b: %s): %s { return ((a * b) - __%s_max(a, b)); }\n",
			k.name, k.name, k.name, k.name, k.name)
		fmt.Fprintf(b, "function ln_%s_sum(xs: %s[]): %s { let acc: %s = __%s_splat(0.0); let i: i32 = 0i32; "+
			"while ((i + %d) <= xs.len()) { acc = (acc + __%s_load(xs, i)); i = i + %d; } return acc; }\n",
			k.name, k.scalar, k.name, k.name, k.name, k.n, k.name, k.n)
	}
}

// maybeEmitLaneCluster builds lane values from the program's i32 state, so
// each lane is a small integer and every operation on it is exact, then
// folds each lane back into an i32 local.
func (g *Generator) maybeEmitLaneCluster(b *strings.Builder, sc *scope) bool {
	if !g.lanes || g.flip(0.75) {
		return false
	}
	k := laneKinds[g.ch.intN(len(laneKinds))]
	if g.ch.exhausted() {
		return false
	}
	idx := g.laneCounter
	g.laneCounter++
	a := fmt.Sprintf("__ln_a%d", idx)
	c := fmt.Sprintf("__ln_c%d", idx)
	r := fmt.Sprintf("__ln_r%d", idx)
	xs := fmt.Sprintf("__ln_xs%d", idx)

	fmt.Fprintf(b, "let %s: %s = __%s_of(", a, k.name, k.name)
	for i := 0; i < k.n; i++ {
		if i > 0 {
			b.WriteString(", ")
		}
		g.laneScalar(b, sc, k)
	}
	b.WriteString("); ")
	names := []string{a}
	if !g.flip(0.5) {
		fmt.Fprintf(b, "let %s: %s[] = [", xs, k.scalar)
		for i := 0; i < 2*k.n; i++ {
			if i > 0 {
				b.WriteString(", ")
			}
			g.laneScalar(b, sc, k)
		}
		fmt.Fprintf(b, "]; let %s_v: %s = __%s_load(%s, %d); ", xs, k.name, k.name, xs, g.ch.intN(k.n+1))
		names = append(names, xs+"_v")
	}
	fmt.Fprintf(b, "let %s: %s = ", c, k.name)
	mul := false
	g.laneExpr(b, k, names, 3, &mul)
	b.WriteString("; ")
	if len(names) > 1 && !g.flip(0.5) {
		fmt.Fprintf(b, "let %s_w: %s[] = __%s_with(%s, %d, %s); ", xs, k.scalar, k.name, xs, g.ch.intN(k.n+1), c)
		fmt.Fprintf(b, "%s = (%s + ln_%s_sum(%s_w)); ", c, c, k.name, xs)
	}
	fmt.Fprintf(b, "let %s: i32 = 0i32", r)
	for i := 0; i < k.n; i++ {
		fmt.Fprintf(b, " + ((__%s_lane(%s, %d) as i32) * %di32)", k.name, c, i, 2*i+1)
	}
	b.WriteString("; ")
	sc.declare(tI32, r)
	return true
}

// laneScalar is an i32 of the program's state, masked to a small range and
// converted to the lane's scalar type.
func (g *Generator) laneScalar(b *strings.Builder, sc *scope, k laneKind) {
	b.WriteString("((((")
	g.expr(b, sc, tI32, 1)
	fmt.Fprintf(b, ") & 63i32) - 32i32) as %s)", k.scalar)
}

func (g *Generator) laneExpr(b *strings.Builder, k laneKind, names []string, depth int, mul *bool) {
	if depth <= 0 || g.flip(0.3) {
		if g.flip(0.7) {
			b.WriteString(names[g.ch.intN(len(names))])
		} else {
			fmt.Fprintf(b, "__%s_splat(%d.0)", k.name, g.ch.intN(17)-8)
		}
		return
	}
	switch op := g.ch.intN(9); op {
	case 0, 1, 2:
		sym := []string{" + ", " - ", " * "}[op]
		if op == 2 {
			if *mul {
				sym = " + "
			}
			*mul = true
		}
		b.WriteString("(")
		g.laneExpr(b, k, names, depth-1, mul)
		b.WriteString(sym)
		g.laneExpr(b, k, names, depth-1, mul)
		b.WriteString(")")
	case 3, 4:
		fmt.Fprintf(b, "__%s_%s(", k.name, []string{"min", "max"}[op-3])
		g.laneExpr(b, k, names, depth-1, mul)
		b.WriteString(", ")
		g.laneExpr(b, k, names, depth-1, mul)
		b.WriteString(")")
	case 5:
		fmt.Fprintf(b, "__%s_%s(", k.name, []string{"abs", "floor"}[g.ch.intN(2)])
		g.laneExpr(b, k, names, depth-1, mul)
		b.WriteString(")")
	case 6:
		b.WriteString("(-")
		g.laneExpr(b, k, names, depth-1, mul)
		b.WriteString(")")
	case 7:
		fmt.Fprintf(b, "__%s_select(", k.name)
		g.laneExpr(b, k, names, depth-1, mul)
		b.WriteString(", ")
		g.laneExpr(b, k, names, depth-1, mul)
		fmt.Fprintf(b, ", __%s_lt(", k.name)
		g.laneExpr(b, k, names, depth-1, mul)
		b.WriteString(", ")
		g.laneExpr(b, k, names, depth-1, mul)
		b.WriteString("))")
	default:
		if *mul {
			b.WriteString(names[0])
			return
		}
		*mul = true
		fmt.Fprintf(b, "ln_%s(", k.name)
		g.laneExpr(b, k, names, depth-1, mul)
		b.WriteString(", ")
		g.laneExpr(b, k, names, depth-1, mul)
		b.WriteString(")")
	}
}
