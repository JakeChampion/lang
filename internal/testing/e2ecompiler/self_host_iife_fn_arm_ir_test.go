package e2ecompiler

import (
	"testing"
)

var iifeFnArmCases = []struct {
	name string
	src  string
	exit int
}{
	{"nested-match-expr-fn-arm", "enum Status { Active, Inactive } function gen_f0(p1: Status): (i32) => i32 { return (match (p1) { Active => (match (p1) { Active => ((a: i32) => a), Inactive => ((b: i32) => 95) }), Inactive => ((c: i32) => 72) }); } function main(): i32 { let f: (i32) => i32 = gen_f0(Status.Active); return f(7); }", 7},
	{"ifexpr-lambda-arms-var-called", "function main(): i32 { let n: i32 = 1; let w: (i32) => i32 = (if (n > 0) { ((a: i32) => 89) } else { ((b: i32) => b) }); return w(3); }", 89},
	{"matchexpr-lambda-arms-returned", "enum Status { Active, Inactive } function gen(p1: Status): (i32) => i32 { return (match (p1) { Active => ((a: i32) => 5), Inactive => ((c: i32) => 72) }); } function main(): i32 { let f: (i32) => i32 = gen(Status.Inactive); return f(7); }", 72},
	{"nocapture-iife-called-directly", "function main(): i32 { let w: (i32) => i32 = (if (true) { ((a: i32) => 89) } else { ((b: i32) => b) }); return w(3); }", 89},

	{"nocapture-iife-arms-captured-by-closure", "function main(): i32 { let v1: (i32) => i32 = (if (true) { ((a: i32) => 5i32) } else { ((b: i32) => 26i32) }); let v4: (i32) => i32 = ((y: i32) => v1(y)); return v4(3i32) & 63i32; }", 5},
	{"nocapture-iife-arms-else-branch-captured", "function main(): i32 { let v1: (i32) => i32 = (if (false) { ((a: i32) => 5i32) } else { ((b: i32) => 26i32) }); let v4: (i32) => i32 = ((y: i32) => v1(y)); return v4(3i32) & 63i32; }", 26},
	{"nocapture-matchexpr-arms-captured-by-closure", "enum S { A, B } function main(): i32 { let v1: (i32) => i32 = (match (S.A) { A => ((x: i32) => 7i32), B => ((y: i32) => 26i32) }); let v4: (i32) => i32 = ((z: i32) => v1(z)); return v4(3i32) & 63i32; }", 7},
	{"nocapture-iife-arms-through-passthrough", "function id[T](x: T): T { return x; } function main(): i32 { let v1: (i32) => i32 = id((if (true) { ((a: i32) => 5i32) } else { ((b: i32) => 26i32) })); return v1(3i32) & 63i32; }", 5},
	{"nocapture-iife-arms-through-passthrough-captured", "function id[T](x: T): T { return x; } function main(): i32 { let v1: (i32) => i32 = id((if (true) { ((a: i32) => a) } else { ((b: i32) => 26i32) })); let v4: (i32) => i32 = ((y: i32) => v1(v1(y))); return v4(9i32) & 63i32; }", 9},

	{"ifexpr-closure-local-arms-called", "function main(): i32 { let v0: (i32) => i32 = ((a: i32) => 89); let n: i32 = 1; let w: (i32) => i32 = (if (n > 0) { v0 } else { v0 }); return w(3); }", 89},
	{"matchexpr-closure-local-arms-called", "enum S { A, B } function main(): i32 { let v0: (i32) => i32 = ((a: i32) => 89); let v1: (i32) => i32 = ((b: i32) => 7); let e: S = S.A; let w: (i32) => i32 = (match (e) { A => v0, B => v1 }); return w(3); }", 89},
	{"ifexpr-bare-fnname-arms-stay-plain", "function inc(x: i32): i32 { return x + 1; } function dbl(x: i32): i32 { return x * 2; } function main(): i32 { let n: i32 = 1; let w: (i32) => i32 = (if (n > 0) { inc } else { dbl }); return w(41); }", 42},

	{"mixed-arms-array-element", "function main(): i32 { let v0: (i32) => i32 = ((a: i32) => 89); let xs: ((i32) => i32)[] = [v0, (if (true) { v0 } else { ((b: i32) => b) }), v0]; return xs[1i32](3i32); }", 89},
	{"mixed-arms-var-called", "function main(): i32 { let v0: (i32) => i32 = ((a: i32) => 89); let n: i32 = 1; let w: (i32) => i32 = (if (n > 0) { v0 } else { ((b: i32) => b) }); return w(3); }", 89},
	{"mixed-arms-field-read-into-struct-field", "struct S { f: (i32) => i32 } function main(): i32 { let a: S = S { f: (x: i32) => 36 }; let s: S = S { f: if (false) { a.f } else { (y: i32) => y + 3 } }; return s.f(1); }", 4},
	{"mixed-arms-field-read-in-nested-fn", "struct S { b: boolean, f: (i32) => i32 } function main(): i32 { let a: S = S { b: true, f: (x: i32) => 36 }; function lf(): S { return S { b: true, f: if (a.b) { a.f } else { (y: i32) => y } }; } return lf().f(1); }", 36},
	{"mixed-arms-element-read-into-struct-field", "struct S { f: (i32) => i32 } function main(): i32 { let fs: ((i32) => i32)[] = [(x: i32) => 36]; let s: S = S { f: if (true) { fs[0] } else { (y: i32) => y } }; return s.f(1); }", 36},
	{"mixed-arms-returned-then-called", "function gen(n: i32): (i32) => i32 { let v0: (i32) => i32 = ((a: i32) => 89); return (if (n > 0) { v0 } else { ((b: i32) => b) }); } function main(): i32 { let f: (i32) => i32 = gen(1); return f(3); }", 89},

	{"capturing-arm-lambda", "function main(): i32 { let n: i32 = 7i32; let v2: (i32) => i32 = (if (true) { ((x: i32) => (x + n)) } else { ((x: i32) => 41i32) }); return v2(1i32) & 63i32; }", 8},
	{"capturing-arms-distinct-captures", "function main(): i32 { let n: i32 = 7i32; let m: i32 = 20i32; let c: boolean = false; let v2: (i32) => i32 = (if (c) { ((x: i32) => (x + n)) } else { ((x: i32) => (x + m)) }); return v2(1i32) & 63i32; }", 21},
	{"matchexpr-capturing-arms", "enum S { A, B } function main(): i32 { let n: i32 = 9i32; let e: S = S.B; let f: (i32) => i32 = (match (e) { A => ((x: i32) => (x + n)), B => ((y: i32) => (y * n)) }); return f(3i32) & 63i32; }", 27},
	{"capturing-arm-string-capture", "function main(): i32 { let s: string = \"abcd\"; let f: (i32) => i32 = (if (true) { ((x: i32) => (x + s.len())) } else { ((y: i32) => y) }); return f(3i32) & 63i32; }", 7},
	{"capturing-arm-bound-then-returned", "function gen(n: i32): (i32) => i32 { let w: (i32) => i32 = (if (n > 0) { ((x: i32) => (x + n)) } else { ((y: i32) => (y - n)) }); return w; } function main(): i32 { let f: (i32) => i32 = gen(5i32); return f(4i32) & 63i32; }", 9},

	{"sole-return-iife-capturing-arms", "function gen(n: i32): (i32) => i32 { return (if (n > 0) { ((x: i32) => (x + n)) } else { ((y: i32) => (y - n)) }); } function main(): i32 { let f: (i32) => i32 = gen(5i32); return f(4i32) & 63i32; }", 9},
	{"sole-return-matchexpr-capturing-arms", "enum S { A, B } function gen(n: i32, e: S): (i32) => i32 { return (match (e) { A => ((x: i32) => (x + n)), B => ((y: i32) => (y * n)) }); } function main(): i32 { let f: (i32) => i32 = gen(5i32, S.B); return f(4i32) & 63i32; }", 20},

	{"tuplematch-capturing-arms-in-loop", "function main(): i32 { let base: i32 = 5; let t: i32 = 0; let i: i32 = 0; while (i < 6) { let k: i32 = i; let g: (i32) => i32 = (match ((i % 3, 0)) { (0, _) => ((x: i32) => x - base), (1, _) => ((x: i32) => k), _ => ((x: i32) => x + 1) }); t = t + g(3) % 7; i = i + 1; } return t % 101; }", 9},
	{"tuplematch-arm-captures-its-binder", "function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 4) { let h: (i32) => i32 = (match ((i % 2, i)) { (0, k) => ((x: i32) => x * k), _ => ((x: i32) => x) }); t = t + h(2); i = i + 1; } return t; }", 8},
	{"structmatch-capturing-arms-returned", "struct P { a: i32, b: i32 } function pick(p: P, base: i32): (i32) => i32 { return match (p) { P { a: 0, b } => ((x: i32) => x - base + b), P { a: 1, b } => ((x: i32) => b), _ => ((x: i32) => x + 1) }; } function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 6) { let g: (i32) => i32 = pick(P { a: i % 3, b: i }, 5); t = t + g(3); i = i + 1; } return t; }", 12},

	{"arm-array-capturing-lambda", "function main(): i32 { let v1: i32 = 3i32; let xs: ((i32) => i32)[] = (if (true) { [((x: i32) => (x + v1))] } else { [((y: i32) => y)] }); return xs[0i32](1i32) & 63i32; }", 4},
	{"arm-array-else-branch-taken", "function main(): i32 { let v1: i32 = 3i32; let c: boolean = false; let xs: ((i32) => i32)[] = (if (c) { [((x: i32) => (x + v1))] } else { [((y: i32) => (y + 10i32))] }); return xs[0i32](1i32) & 63i32; }", 11},
	{"matchexpr-arm-array-capturing", "enum S { A, B } function main(): i32 { let v1: i32 = 3i32; let e: S = S.B; let xs: ((i32) => i32)[] = (match (e) { A => [((x: i32) => (x + v1))], B => [((y: i32) => (y * v1))] }); return xs[0i32](2i32) & 63i32; }", 6},
	{"arm-array-mixed-cap-and-fnname", "function inc(x: i32): i32 { return x + 1i32; } function main(): i32 { let v1: i32 = 3i32; let xs: ((i32) => i32)[] = (if (true) { [((x: i32) => (x + v1)), inc] } else { [inc, inc] }); return (xs[0i32](1i32) + xs[1i32](1i32)) & 63i32; }", 6},
	{"arm-array-named-local-sibling", "function main(): i32 { let v1: i32 = 3i32; let ys: ((i32) => i32)[] = [((z: i32) => z)]; let xs: ((i32) => i32)[] = (if (true) { [((x: i32) => (x + v1))] } else { ys }); return xs[0i32](1i32) & 63i32; }", 4},
	{"arm-array-named-local-else-taken", "function main(): i32 { let v1: i32 = 3i32; let c: boolean = false; let ys: ((i32) => i32)[] = [((z: i32) => (z + 10i32))]; let xs: ((i32) => i32)[] = (if (c) { [((x: i32) => (x + v1))] } else { ys }); return xs[0i32](1i32) & 63i32; }", 11},
	{"arm-array-named-local-then-arm", "function main(): i32 { let v1: i32 = 3i32; let c: boolean = true; let ys: ((i32) => i32)[] = [((z: i32) => (z + 10i32))]; let xs: ((i32) => i32)[] = (if (c) { ys } else { [((x: i32) => (x + v1))] }); return xs[0i32](1i32) & 63i32; }", 11},
	{"arm-array-named-local-both-arrays-used", "function main(): i32 { let v1: i32 = 3i32; let ys: ((i32) => i32)[] = [((z: i32) => z)]; let xs: ((i32) => i32)[] = (if (true) { [((x: i32) => (x + v1))] } else { ys }); return ((xs[0i32](1i32) * 10i32) + ys[0i32](2i32)) & 63i32; }", 42},
	{"matchexpr-arm-array-named-local", "enum S { A, B } function main(): i32 { let v1: i32 = 3i32; let e: S = S.A; let ys: ((i32) => i32)[] = [((z: i32) => (z + 10i32))]; let xs: ((i32) => i32)[] = (match (e) { A => [((x: i32) => (x + v1))], B => ys }); return xs[0i32](1i32) & 63i32; }", 4},
	{"nested-iife-arm-array-named-local", "function main(): i32 { let v1: i32 = 3i32; let ys: ((i32) => i32)[] = [((z: i32) => (z + 10i32))]; let zs: ((i32) => i32)[] = [((w: i32) => (w * 2i32))]; let c: boolean = true; let xs: ((i32) => i32)[] = (if (c) { [((x: i32) => (x + v1))] } else { (if (c) { ys } else { zs }) }); return xs[0i32](1i32) & 63i32; }", 4},
	{"arm-array-param-name", "function pick(ys: ((i32) => i32)[], c: boolean): i32 { let v1: i32 = 3i32; let xs: ((i32) => i32)[] = (if (c) { [((x: i32) => (x + v1))] } else { ys }); return xs[0i32](1i32) & 63i32; } function main(): i32 { let zs: ((i32) => i32)[] = [((z: i32) => z)]; return pick(zs, true); }", 4},
	{"arm-array-param-name-else-taken", "function pick(ys: ((i32) => i32)[], c: boolean): i32 { let v1: i32 = 3i32; let xs: ((i32) => i32)[] = (if (c) { [((x: i32) => (x + v1))] } else { ys }); return xs[0i32](1i32) & 63i32; } function main(): i32 { let zs: ((i32) => i32)[] = [((z: i32) => (z + 10i32))]; return pick(zs, false); }", 11},
	{"arm-array-call-result", "function mk(): ((i32) => i32)[] { return [((z: i32) => z)]; } function main(): i32 { let v1: i32 = 3i32; let xs: ((i32) => i32)[] = (if (true) { [((x: i32) => (x + v1))] } else { mk() }); return xs[0i32](1i32) & 63i32; }", 4},
	{"arm-array-call-result-taken", "function mk(): ((i32) => i32)[] { return [((z: i32) => (z + 20i32))]; } function main(): i32 { let v1: i32 = 3i32; let c: boolean = false; let xs: ((i32) => i32)[] = (if (c) { [((x: i32) => (x + v1))] } else { mk() }); return xs[0i32](1i32) & 63i32; }", 21},
	{"arm-array-named-local-unrelated-fnptr-array", "function inc(x: i32): i32 { return x + 1i32; } function main(): i32 { let v1: i32 = 3i32; let ps: ((i32) => i32)[] = [inc]; let ys: ((i32) => i32)[] = [((z: i32) => z)]; let xs: ((i32) => i32)[] = (if (true) { [((x: i32) => (x + v1))] } else { ys }); return ((xs[0i32](1i32) * 10i32) + ps[0i32](1i32)) & 63i32; }", 42},
	{"arm-array-named-local-foreach-over-name", "function main(): i32 { let v1: i32 = 3i32; let ys: ((i32) => i32)[] = [((z: i32) => (z + 1i32))]; let xs: ((i32) => i32)[] = (if (true) { [((x: i32) => (x + v1))] } else { ys }); let t: i32 = 0i32; for f in ys { t = t + f(1i32); } return (xs[0i32](1i32) + t) & 63i32; }", 6},
	{"arm-array-named-local-append-to-name", "function main(): i32 { let v1: i32 = 3i32; let ys: ((i32) => i32)[] = [((z: i32) => (z + 1i32))]; let ws: ((i32) => i32)[] = ys.append(((q: i32) => (q + 5i32))); let xs: ((i32) => i32)[] = (if (true) { [((x: i32) => (x + v1))] } else { ys }); return (xs[0i32](1i32) + ws[1i32](1i32)) & 63i32; }", 10},

	{"arm-array-nocapture", "function main(): i32 { let xs: ((i32) => i32)[] = (if (true) { [((x: i32) => (x + 3i32))] } else { [((y: i32) => y)] }); return xs[0i32](1i32) & 63i32; }", 4},
	{"arm-array-bare-fnnames", "function inc(x: i32): i32 { return x + 1i32; } function dbl(x: i32): i32 { return x * 2i32; } function main(): i32 { let xs: ((i32) => i32)[] = (if (true) { [inc] } else { [dbl] }); return xs[0i32](41i32) & 63i32; }", 42},
	{"arm-array-iife-element-capturing", "function gen(p1: i32, c: boolean): i32 { let xs: ((i32) => i32)[] = [((a: i32) => a), (if (c) { ((x: i32) => (x + p1)) } else { ((y: i32) => p1) })]; return xs[1i32](3i32); } function main(): i32 { return gen(4i32, true) & 63i32; }", 7},
	{"arm-array-iife-element-payload-capture", "function main(): i32 { let v2: Option[i32] = Some(5i32); let xs: ((i32) => i32)[] = [((z: i32) => z), (match (v2) { Some(p) => ((x: i32) => (x + p)), None => ((y: i32) => y) })]; return xs[1i32](2i32) & 63i32; }", 7},
	{"struct-field-iife-arms", "struct H { f: (i32) => i32 } function main(): i32 { let n: i32 = 4i32; let h: H = H { f: (if (true) { ((x: i32) => (x + n)) } else { ((y: i32) => y) }) }; return h.f(3i32) & 63i32; }", 7},
	{"call-arg-iife-arms", "function apply(f: (i32) => i32, v: i32): i32 { return f(v); } function main(): i32 { let n: i32 = 6i32; return apply((if (true) { ((x: i32) => (x + n)) } else { ((y: i32) => y) }), 3i32) & 63i32; }", 9},
	{"arm-array-iife-element-nocapture", "function main(): i32 { let xs: ((i32) => i32)[] = [((a: i32) => (a + 1i32)), (if (true) { ((x: i32) => (x * 2i32)) } else { ((y: i32) => y) })]; return (xs[0i32](1i32) + xs[1i32](3i32)) & 63i32; }", 8},

	{"curry-tail-return-unchanged", "function curry(a: i32): (i32) => ((i32) => i32) { return ((b: i32) => ((c: i32) => (a + b + c))); } function main(): i32 { let g: (i32) => ((i32) => i32) = curry(1i32); let h: (i32) => i32 = g(2i32); return h(3i32) & 63i32; }", 6},

	{"arm-passthrough-holds-lambda", "function pick[T](cond: boolean, a: T, b: T): T { return if (cond) { a } else { b }; } function main(): i32 { let v0: (i32) => i32 = ((a: i32) => 40i32); let n: i32 = 2i32; let t: boolean = false; let xs: ((i32) => i32)[] = [(if (t) { v0 } else { pick(t, v0, ((x: i32) => (x + n))) })]; return xs[0i32](3i32) & 63i32; }", 5},

	{"nested-iife-arms-capturing", "function main(): i32 { let n: i32 = 5i32; let f: (i32) => i32 = (if (true) { (if (false) { ((a: i32) => 20i32) } else { ((b: i32) => (b + n)) }) } else { (if (true) { ((c: i32) => 40i32) } else { ((d: i32) => 3i32) }) }); return f(2i32) & 63i32; }", 7},
	{"nested-iife-arms-else-branch-taken", "function main(): i32 { let n: i32 = 5i32; let c: boolean = false; let f: (i32) => i32 = (if (c) { (if (false) { ((a: i32) => 20i32) } else { ((b: i32) => (b + n)) }) } else { (if (true) { ((cc: i32) => (n * 4i32)) } else { ((d: i32) => 3i32) }) }); return f(2i32) & 63i32; }", 20},
	{"nested-matchexpr-iife-arms-capturing", "enum S { A, B } function main(): i32 { let n: i32 = 9i32; let e: S = S.A; let f: (i32) => i32 = (match (e) { A => (match (e) { A => ((x: i32) => (x * n)), B => ((y: i32) => y) }), B => (match (e) { A => ((z: i32) => 1i32), B => ((w: i32) => 2i32) }) }); return f(3i32) & 63i32; }", 27},
	{"nested-iife-arms-three-deep", "function main(): i32 { let n: i32 = 6i32; let f: (i32) => i32 = (if (true) { (if (true) { (if (false) { ((a: i32) => 20i32) } else { ((b: i32) => (b + n)) }) } else { (if (true) { ((c: i32) => 1i32) } else { ((d: i32) => 2i32) }) }) } else { (if (true) { ((e: i32) => 3i32) } else { ((g: i32) => 4i32) }) }); return f(1i32) & 63i32; }", 7},
	{"nested-iife-arms-bound-then-returned", "function gen(n: i32, c: boolean): (i32) => i32 { let w: (i32) => i32 = (if (c) { (if (false) { ((a: i32) => 20i32) } else { ((b: i32) => (b + n)) }) } else { (if (true) { ((cc: i32) => n) } else { ((d: i32) => 3i32) }) }); return w; } function main(): i32 { let f: (i32) => i32 = gen(5i32, true); return f(2i32) & 63i32; }", 7},
	{"nested-iife-arms-mixed-closure-local", "function main(): i32 { let v0: (i32) => i32 = ((a: i32) => 41i32); let n: i32 = 5i32; let c: boolean = true; let f: (i32) => i32 = (if (c) { (if (false) { v0 } else { ((b: i32) => (b + n)) }) } else { (if (c) { v0 } else { v0 }) }); return f(2i32) & 63i32; }", 7},
	{"nested-iife-closure-local-arms-unchanged", "function main(): i32 { let v0: (i32) => i32 = ((a: i32) => 41i32); let c: boolean = true; let f: (i32) => i32 = (if (c) { (if (c) { v0 } else { v0 }) } else { (if (c) { v0 } else { v0 }) }); return f(3i32) & 63i32; }", 41},
	{"nested-iife-bare-fnname-arms-unchanged", "function inc(x: i32): i32 { return x + 1i32; } function dbl(x: i32): i32 { return x * 2i32; } function main(): i32 { let c: boolean = true; let f: (i32) => i32 = (if (c) { (if (c) { inc } else { dbl }) } else { (if (c) { dbl } else { inc }) }); return f(40i32) & 63i32; }", 41},

	{"arm-array-fnname-beside-call-result", "function dbl(x: i32): i32 { return x * 2i32; } function mk(): ((i32) => i32)[] { return [((z: i32) => (z + 7i32))]; } function run(xs: ((i32) => i32)[]): i32 { return xs[0i32](4i32); } function pick(c: boolean): i32 { let xs: ((i32) => i32)[] = (if (c) { [dbl] } else { mk() }); return (xs[0i32](1i32) + run(xs)) & 63i32; } function main(): i32 { return pick(true) + pick(false) * 2i32; }", 48},
	{"arm-array-fnnames-only", "function dbl(x: i32): i32 { return x * 2i32; } function inc(x: i32): i32 { return x + 1i32; } function run(xs: ((i32) => i32)[]): i32 { return xs[0i32](4i32); } function pick(c: boolean): i32 { let xs: ((i32) => i32)[] = (if (c) { [dbl] } else { [inc] }); return (xs[0i32](1i32) + run(xs)) & 63i32; } function main(): i32 { return pick(true) + pick(false) * 10i32; }", 80},
	{"arm-array-nocapture-lambda-beside-param", "function run(xs: ((i32) => i32)[]): i32 { return xs[0i32](4i32); } function pick(ys: ((i32) => i32)[], c: boolean): i32 { let xs: ((i32) => i32)[] = (if (c) { [((x: i32) => (x + 1i32))] } else { ys }); return (xs[0i32](1i32) + run(xs)) & 63i32; } function main(): i32 { let zs: ((i32) => i32)[] = [((z: i32) => z)]; return pick(zs, true) + pick(zs, false) * 10i32; }", 57},
}

func TestSelfHostIIFEFnArmIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range iifeFnArmCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

func TestSelfHostIIFEFnArmIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	for _, tc := range iifeFnArmCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", tc.src)); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

func TestSelfHostIIFEFnArmWasmIR(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range iifeFnArmCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runWasm(t, cli.emit(t, "wasm32-wasi", tc.src)); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
