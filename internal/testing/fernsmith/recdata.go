package fernsmith

import (
	"fmt"
	"strings"
)

// declareRecData declares recursive types and the helpers that build, rebuild
// and fold them: a list, a binary search tree, a rose tree whose children are
// an array of itself, a generic list of strings, and a struct linked through
// an Option of itself. A rebuild of a unique value can reuse its boxes,
// and one of a value still held elsewhere must copy, so a helper sometimes
// keeps the input alive past the rebuild.
func (g *Generator) declareRecData(b *strings.Builder) {
	if !g.on(featRecData) || g.flip(0.3) {
		return
	}
	list, tree, rose, strs, linked := true, !g.flip(0.4), !g.flip(0.4), !g.flip(0.5), !g.flip(0.6)
	if g.ch.exhausted() {
		return
	}
	if list {
		g.declareRecList(b)
	}
	if tree {
		g.declareRecTree(b)
	}
	if rose {
		g.declareRecRose(b)
	}
	if strs {
		g.declareRecStrings(b)
	}
	if linked {
		g.declareRecLinked(b)
	}
}

func (g *Generator) declareRecList(b *strings.Builder) {
	b.WriteString("enum RL0 { RNil0, RCons0(i32, RL0) }\n")
	b.WriteString("function rl0_build(n: i32, k: i32): RL0 { let l: RL0 = RNil0; let i: i32 = 0i32; " +
		"while (i < n) { l = RCons0(((i * k) ^ i), l); i = i + 1i32; } return l; }\n")
	b.WriteString("function rl0_map(l: RL0, d: i32): RL0 { match (l) { " +
		"RCons0(h, t) => { return RCons0((h ^ d), rl0_map(t, d)); }, RNil0 => { return RNil0; } } }\n")
	b.WriteString("function rl0_keep(l: RL0, m: i32): RL0 { match (l) { " +
		"RCons0(h, t) => { if ((h & m) == 0i32) { return rl0_keep(t, m); } return RCons0(h, rl0_keep(t, m)); }, " +
		"RNil0 => { return RNil0; } } }\n")
	b.WriteString("function rl0_rev(l: RL0): RL0 { let out: RL0 = RNil0; let c: RL0 = l; let go: boolean = true; " +
		"while (go) { match (c) { RCons0(h, t) => { out = RCons0(h, out); c = t; }, RNil0 => { go = false; } } } return out; }\n")
	b.WriteString("function rl0_pairs(l: RL0): RL0 { match (l) { " +
		"RCons0(a, RCons0(b, t)) => { return RCons0((a + b), rl0_pairs(t)); }, " +
		"RCons0(a, RNil0()) => { return RCons0(a, RNil0); }, RNil0 => { return RNil0; } } }\n")
	b.WriteString("function rl0_sum(l: RL0): i32 { let s: i32 = 0i32; let c: RL0 = l; let go: boolean = true; " +
		"while (go) { match (c) { RCons0(h, t) => { s = ((s * 31i32) + h); c = t; }, RNil0 => { go = false; } } } return s; }\n")

	// A long list goes only through the loops, never the recursive rebuilds.
	long := !g.flip(0.25)
	size := "63i32"
	if long {
		size = "2047i32"
	}
	n := 1 + g.ch.intN(4)
	fmt.Fprintf(b, "function rl0_go(n: i32, k: i32): i32 { let a: RL0 = rl0_build((n & %s), (k & 255i32)); let c: RL0 = a; ", size)
	held := []string{"a"}
	for i := 0; i < n; i++ {
		prev := "c"
		keep := !g.flip(0.4)
		if keep {
			fmt.Fprintf(b, "let h%d: RL0 = c; ", i)
			held = append(held, fmt.Sprintf("h%d", i))
		}
		switch op := g.ch.intN(4); {
		case op == 0 || long:
			fmt.Fprintf(b, "c = rl0_rev(%s); ", prev)
		case op == 3:
			fmt.Fprintf(b, "c = rl0_pairs(%s); ", prev)
		case op == 1:
			fmt.Fprintf(b, "c = rl0_map(%s, ((k * %di32) & 255i32)); ", prev, 3+2*i)
		default:
			fmt.Fprintf(b, "c = rl0_keep(%s, %di32); ", prev, 1<<uint(i%4))
		}
	}
	b.WriteString("return (rl0_sum(c)")
	for _, h := range held {
		fmt.Fprintf(b, " ^ (rl0_sum(%s) * 7i32)", h)
	}
	b.WriteString("); }\n")
	g.helpers = append(g.helpers, helperSig{name: "rl0_go", params: []gtype{tI32, tI32}, retType: tI32})
}

func (g *Generator) declareRecTree(b *strings.Builder) {
	b.WriteString("enum RT0 { RTLeaf0, RTNode0(RT0, i32, RT0) }\n")
	b.WriteString("function rt0_ins(t: RT0, x: i32): RT0 { match (t) { " +
		"RTNode0(l, v, r) => { if (x < v) { return RTNode0(rt0_ins(l, x), v, r); } return RTNode0(l, v, rt0_ins(r, x)); }, " +
		"RTLeaf0 => { return RTNode0(RTLeaf0, x, RTLeaf0); } } }\n")
	b.WriteString("function rt0_sum(t: RT0, d: i32): i32 { match (t) { " +
		"RTNode0(l, v, r) => { return (((rt0_sum(l, (d + 1i32)) * 7i32) + (v * d)) + rt0_sum(r, (d + 1i32))); }, " +
		"RTLeaf0 => { return d; } } }\n")
	b.WriteString("function rt0_go(n: i32, k: i32): i32 { let t: RT0 = RTLeaf0; let i: i32 = 0i32; " +
		"while (i < (n & 31i32)) { t = rt0_ins(t, (((i * k) ^ (i * 7i32)) & 127i32)); i = i + 1i32; } ")
	if !g.flip(0.5) {
		b.WriteString("let u: RT0 = t; t = rt0_ins(t, (k & 127i32)); return (rt0_sum(t, 1i32) ^ (rt0_sum(u, 2i32) * 3i32)); }\n")
	} else {
		b.WriteString("return rt0_sum(t, 1i32); }\n")
	}
	g.helpers = append(g.helpers, helperSig{name: "rt0_go", params: []gtype{tI32, tI32}, retType: tI32})
}

func (g *Generator) declareRecRose(b *strings.Builder) {
	b.WriteString("enum RR0 { RRLeaf0(i32), RRose0(i32, RR0[]) }\n")
	if g.flip(0.5) {
		b.WriteString("function rr0_build(d: i32, k: i32): RR0 { if (d <= 0i32) { return RRLeaf0(k); } " +
			"return RRose0(k, [rr0_build((d - 1i32), ((k * 3i32) & 255i32)), rr0_build((d - 1i32), ((k + 5i32) & 255i32))]); }\n")
	} else {
		b.WriteString("function rr0_build(d: i32, k: i32): RR0 { if (d <= 0i32) { return RRose0(k, []); } " +
			"let cs: RR0[] = []; let i: i32 = 0i32; while (i < ((k & 1i32) + 2i32)) { " +
			"cs = cs.append(rr0_build((d - 1i32), ((k + i) & 255i32))); i = i + 1i32; } return RRose0(k, cs); }\n")
	}
	b.WriteString("function rr0_sum(t: RR0): i32 { match (t) { RRLeaf0(v) => { return v; }, " +
		"RRose0(v, cs) => { let s: i32 = v; for c in cs { s = ((s * 5i32) + rr0_sum(c)); } return (s ^ cs.len()); } } }\n")
	b.WriteString("function rr0_map(t: RR0, m: i32): RR0 { match (t) { RRLeaf0(v) => { return RRLeaf0((v ^ m)); }, " +
		"RRose0(v, cs) => { let out: RR0[] = []; for c in cs { out = out.append(rr0_map(c, m)); } return RRose0((v + m), out); } } }\n")
	b.WriteString("function rr0_go(d: i32, k: i32): i32 { let t: RR0 = rr0_build((d & 3i32), (k & 255i32)); ")
	switch g.ch.intN(3) {
	case 0:
		b.WriteString("return rr0_sum(t); }\n")
	case 1:
		b.WriteString("let u: RR0 = rr0_map(t, 9i32); return (rr0_sum(u) ^ (rr0_sum(t) * 3i32)); }\n")
	default:
		b.WriteString("t = rr0_map(t, (k & 15i32)); return rr0_sum(t); }\n")
	}
	g.helpers = append(g.helpers, helperSig{name: "rr0_go", params: []gtype{tI32, tI32}, retType: tI32})
}

// A list generic in its payload, instantiated at string, so a rebuild and a
// drop each release counted children as well as the cells.
func (g *Generator) declareRecStrings(b *strings.Builder) {
	b.WriteString("enum GL0[T] { GNil0, GCons0(T, GL0[T]) }\n")
	b.WriteString("function gl0_len[T](l: GL0[T]): i32 { match (l) { GCons0(_, t) => { return (1i32 + gl0_len(t)); }, GNil0 => { return 0i32; } } }\n")
	b.WriteString("function gs0_bytes(l: GL0[string]): i32 { match (l) { GCons0(s, t) => { return ((s.len() * 3i32) + gs0_bytes(t)); }, GNil0 => { return 0i32; } } }\n")
	b.WriteString("function gs0_map(l: GL0[string], d: string): GL0[string] { match (l) { " +
		"GCons0(s, t) => { return GCons0((s + d), gs0_map(t, d)); }, GNil0 => { return GNil0; } } }\n")
	b.WriteString("function gs0_go(n: i32, k: i32): i32 { let g: GL0[string] = GNil0; let i: i32 = 0i32; " +
		"while (i < (n & 15i32)) { g = GCons0((i ^ k).to_string(), g); i = i + 1i32; } ")
	if !g.flip(0.5) {
		b.WriteString("let h: GL0[string] = g; g = gs0_map(g, \"q\"); return (gs0_bytes(g) + (gs0_bytes(h) * 5i32) + gl0_len(h)); }\n")
	} else {
		b.WriteString("g = gs0_map(g, (k & 7i32).to_string()); return (gs0_bytes(g) + gl0_len(g)); }\n")
	}
	g.helpers = append(g.helpers, helperSig{name: "gs0_go", params: []gtype{tI32, tI32}, retType: tI32})
}

// A struct that reaches itself through an Option field.
func (g *Generator) declareRecLinked(b *strings.Builder) {
	b.WriteString("struct RN0 { v: i32, next: Option[RN0] }\n")
	b.WriteString("function rn0_sum(r: RN0): i32 { match (r.next) { Some(nx) => { return ((r.v * 3i32) + rn0_sum(nx)); }, None => { return r.v; } } }\n")
	b.WriteString("function rn0_go(n: i32, k: i32): i32 { let h: RN0 = RN0 { v: k, next: None }; let i: i32 = 1i32; " +
		"while (i < (n & 31i32)) { h = RN0 { v: (i ^ k), next: Some(h) }; i = i + 1i32; } ")
	if !g.flip(0.5) {
		b.WriteString("let t: RN0 = RN0 { ...h, v: (h.v + 1i32) }; return (rn0_sum(t) ^ rn0_sum(h)); }\n")
	} else {
		b.WriteString("return rn0_sum(h); }\n")
	}
	g.helpers = append(g.helpers, helperSig{name: "rn0_go", params: []gtype{tI32, tI32}, retType: tI32})
}
