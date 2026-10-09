package e2ecompiler

import (
	"fmt"
	"strings"
	"testing"
)

// seqArrays builds a function with n SEQUENTIAL array-literal locals, each read
// (borrow-only) in its own loop then never used again — the shape Perceus
// drop-on-last-use targets: with precise drops, each array's buffer is released
// right after its loop and recycled by the next array's allocation, so peak heap
// stays ~one array instead of n. Returns the program and its expected
// (sum % 100). sum = Σ_{i<n} (i + (i+1) + (i+2) + (i+3)) = 2n² + 4n.
func seqArrays(n int, tail string) (string, int) {
	var b strings.Builder
	b.WriteString("function main(): i32 { let acc = 0;")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, " let a%d = [%d,%d,%d,%d]; let s%d = 0; let j%d = 0; while (j%d < 4) { s%d = s%d + a%d[j%d]; j%d = j%d + 1; } acc = acc + s%d;",
			i, i, i+1, i+2, i+3, i, i, i, i, i, i, i, i, i, i)
	}
	b.WriteString(" " + tail + " }")
	return b.String(), (2*n*n + 4*n) % 100
}

// TestSelfHostRcPreciseDropX86IR pins drop-on-last-use and in-place reuse on
// x86-64, compiled through the self-hosted CLI. Each
// case asserts the value the program computes, or that the over-release
// detector (`__rc_underflow_count()`) reads 0, so the drop is sound. The
// byte-identical fixpoint and std-test gates separately prove soundness on the
// compiler's own sources.
func TestSelfHostRcPreciseDropX86IR(t *testing.T) {
	boxedProbes(t)
	cli := newStrictCLI(t)
	emit := func(t *testing.T, src string) string {
		t.Helper()
		return cli.emit(t, "x86-64-linux", src)
	}

	seqVal, seqExp := seqArrays(40, "return acc % 100;")
	seqDet, _ := seqArrays(40, "return __rc_underflow_count();")

	cases := []struct {
		name     string
		src      string
		expected int
	}{
		// Two independent linear array literals; each dropped right after its
		// own loop.
		{"two-arrays-value", `function main(): i32 { let a = [10, 20, 30]; let s = 0; let i = 0; while (i < 3) { s = s + a[i]; i = i + 1; } let b = [1, 2, 3, 4]; let t = 0; let k = 0; while (k < 4) { t = t + b[k]; k = k + 1; } return s + t; }`, 60 + 10},
		{"two-arrays-detector", `function main(): i32 { let a = [10, 20, 30]; let s = 0; let i = 0; while (i < 3) { s = s + a[i]; i = i + 1; } let b = [1, 2, 3, 4]; let t = 0; let k = 0; while (k < 4) { t = t + b[k]; k = k + 1; } if (s + t != 70) { return 99; } return __rc_underflow_count(); }`, 0},
		// 40 sequential arrays — the reclaim shape: precise drops keep peak heap
		// ~one array as the blocks recycle.
		{"reclaim-seq-value", seqVal, seqExp},
		{"reclaim-seq-detector", seqDet, 0},
		// A fresh i32[]-returning FREE call with scalar-literal args is also a
		// candidate (the builder-call win). The string[]-returning call is a
		// value check: treating it as a candidate crashed the compiler.
		{"builder-call-value", `function build(): i32[] { return [1, 2, 3, 4]; } function main(): i32 { let a = build(); let s = 0; let i = 0; while (i < 4) { s = s + a[i]; i = i + 1; } return s; }`, 10},
		{"builder-call-detector", `function build(): i32[] { return [1, 2, 3, 4]; } function main(): i32 { let a = build(); let s = 0; let i = 0; while (i < 4) { s = s + a[i]; i = i + 1; } if (s != 10) { return 99; } return __rc_underflow_count(); }`, 0},
		{"strarr-call-not-dropped", `function names(): string[] { return ["a", "b"]; } function main(): i32 { let xs = names(); return xs.len(); }`, 2},
		// Borrow-inference reclaim (Level 2): an array literal passed ONLY to a
		// borrowable (read-only) free function is still a precise-drop candidate —
		// the callee provably can't retain it. `sum_arr` borrows its param (only
		// `v.len()` / `v[i]`), so `t` is dropped after its last `sum_arr(t)` call.
		{"borrow-helper-value", `function sum_arr(v: i32[]): i32 { let s = 0; let i = 0; while (i < v.len()) { s = s + v[i]; i = i + 1; } return s; } function main(): i32 { let t = [1, 2, 3, 4]; let a = sum_arr(t); let b = sum_arr(t); return a + b; }`, 20},
		{"borrow-helper-detector", `function sum_arr(v: i32[]): i32 { let s = 0; let i = 0; while (i < v.len()) { s = s + v[i]; i = i + 1; } return s; } function main(): i32 { let t = [1, 2, 3, 4]; let a = sum_arr(t); let b = sum_arr(t); if (a + b != 20) { return 99; } return __rc_underflow_count(); }`, 0},
		// Conservative: a callee that RETURNS its param (escape) is NOT borrowable,
		// so the arg is not reclaimed early. The detector confirms no over-release.
		{"escape-helper-detector", `@noinline function keep(v: i32[]): i32[] { return v; } function main(): i32 { let t = [1, 2, 3, 4]; let u = keep(t); let a = u[0]; if (a != 1) { return 99; } return __rc_underflow_count(); }`, 0},
		// Inter-procedural borrow inference (single forward pass on the emit path,
		// computed once per module): `inner` is defined before `outer` and borrows
		// its param, so the pass recognises `outer`'s forwarded param as a borrow too
		// — `t` IS reclaimed after its last `outer(t)`.
		{"transitive-reclaim-value", `function inner(v: i32[]): i32 { return v[0]; } function outer(v: i32[]): i32 { return inner(v); } function main(): i32 { let t = [5, 6, 7, 8]; let a = outer(t); let b = outer(t); return a + b; }`, 10},
		{"transitive-reclaim-detector", `function inner(v: i32[]): i32 { return v[0]; } function outer(v: i32[]): i32 { return inner(v); } function main(): i32 { let t = [5, 6, 7]; let a = outer(t); if (a != 5) { return 99; } return __rc_underflow_count(); }`, 0},
		// Escape through a chain stays rejected: `wrap` forwards to `idf`, which
		// RETURNS its param, so `idf` is not borrowable, hence `wrap` is not, hence
		// `t` is not reclaimed — the result that aliases `t` stays valid.
		{"escape-chain-detector", `@noinline function idf(v: i32[]): i32[] { return v; } function wrap(v: i32[]): i32[] { return idf(v); } function main(): i32 { let t = [3, 4, 5]; let u = wrap(t); if (u[1] != 4) { return 99; } return __rc_underflow_count(); }`, 0},
		// Full convergence (least-fixpoint, iterated): `outer` is defined BEFORE the
		// borrowable `inner`, so a single forward pass would miss it — the iterated
		// fixpoint propagates inner's borrowability back to outer, reclaiming `t`.
		{"caller-before-callee-value", `function outer(v: i32[]): i32 { return inner(v); } function inner(v: i32[]): i32 { return v[0]; } function main(): i32 { let t = [5, 6, 7, 8]; let a = outer(t); let b = outer(t); return a + b; }`, 10},
		{"caller-before-callee-detector", `function outer(v: i32[]): i32 { return inner(v); } function inner(v: i32[]): i32 { return v[0]; } function main(): i32 { let t = [5, 6, 7]; let a = outer(t); if (a != 5) { return 99; } return __rc_underflow_count(); }`, 0},
		// Scalar-element widening: i64[] and f64[] literals are flat scalar buffers
		// (no nested pointers, like i32[]), so they are precise-drop candidates too —
		// each released right after its loop.
		{"i64arr-two-arrays-value", `function main(): i32 { let v: i64[] = [10, 20, 30]; let s: i64 = 0; let i = 0; while (i < 3) { s = s + v[i]; i = i + 1; } let w: i64[] = [1, 2, 3, 4]; let t: i64 = 0; let k = 0; while (k < 4) { t = t + w[k]; k = k + 1; } return (s + t) as i32; }`, 70},
		{"i64arr-two-arrays-detector", `function main(): i32 { let v: i64[] = [10, 20, 30]; let s: i64 = 0; let i = 0; while (i < 3) { s = s + v[i]; i = i + 1; } let w: i64[] = [1, 2, 3, 4]; let t: i64 = 0; let k = 0; while (k < 4) { t = t + w[k]; k = k + 1; } if ((s + t) as i32 != 70) { return 99; } return __rc_underflow_count(); }`, 0},
		{"f64arr-two-arrays-value", `function main(): i32 { let v: f64[] = [1.5, 2.5, 3.0]; let s: f64 = 0.0; let i = 0; while (i < 3) { s = s + v[i]; i = i + 1; } let w: f64[] = [0.5, 0.5]; let t: f64 = 0.0; let k = 0; while (k < 2) { t = t + w[k]; k = k + 1; } return (s + t) as i32; }`, 8},
		{"f64arr-two-arrays-detector", `function main(): i32 { let v: f64[] = [1.5, 2.5, 3.0]; let s: f64 = 0.0; let i = 0; while (i < 3) { s = s + v[i]; i = i + 1; } let w: f64[] = [0.5, 0.5]; let t: f64 = 0.0; let k = 0; while (k < 2) { t = t + w[k]; k = k + 1; } if ((s + t) as i32 != 8) { return 99; } return __rc_underflow_count(); }`, 0},
		// A fresh i64[]- / f64[]-returning FREE call with scalar-literal args is a
		// candidate too (the builder-call win).
		{"i64arr-builder-call-value", `function build64(): i64[] { return [1, 2, 3, 4]; } function main(): i32 { let a = build64(); let s: i64 = 0; let i = 0; while (i < 4) { s = s + a[i]; i = i + 1; } return s as i32; }`, 10},
		{"i64arr-builder-call-detector", `function build64(): i64[] { return [1, 2, 3, 4]; } function main(): i32 { let a = build64(); let s: i64 = 0; let i = 0; while (i < 4) { s = s + a[i]; i = i + 1; } if (s as i32 != 10) { return 99; } return __rc_underflow_count(); }`, 0},
		{"f64arr-builder-call-value", `function buildf(): f64[] { return [1.5, 2.5]; } function main(): i32 { let a = buildf(); let s: f64 = 0.0; let i = 0; while (i < 2) { s = s + a[i]; i = i + 1; } return s as i32; }`, 4},
		{"f64arr-builder-call-detector", `function buildf(): f64[] { return [1.5, 2.5]; } function main(): i32 { let a = buildf(); let s: f64 = 0.0; let i = 0; while (i < 2) { s = s + a[i]; i = i + 1; } if (s as i32 != 4) { return 99; } return __rc_underflow_count(); }`, 0},
		// Struct in-place reuse (FBIP), functional-update self-overwrite: `let c =
		// T { ...d, f: v }` where d is a fresh, non-escaping, scalar-field struct
		// local DEAD after this update reuses d's box in place (the override field
		// is written into d's box, d's slot zeroed). The value is correct and the
		// over-release detector stays 0 (d's box is freed exactly once, through c).
		{"struct-reuse-value", `struct Point { x: i32, y: i32 } function main(): i32 { let d = Point { x: 3, y: 4 }; let c = Point { ...d, x: 10 }; return c.x + c.y; }`, 14},
		{"struct-reuse-detector", `struct Point { x: i32, y: i32 } function main(): i32 { let d = Point { x: 3, y: 4 }; let c = Point { ...d, x: 10 }; let sum = c.x + c.y; if (sum != 14) { return 99; } return __rc_underflow_count(); }`, 0},
		// Multi-field override: both overrides written into d's box in place; the
		// un-overridden field (y) is left untouched (already correct in d's box).
		{"struct-reuse-multi-override", `struct Trip { a: i32, b: i32, c: i32 } function main(): i32 { let d = Trip { a: 1, b: 2, c: 3 }; let e = Trip { ...d, a: 10, c: 30 }; return e.a + e.b + e.c; }`, 42},
		// NON-firing: d is used AFTER the update (`d.x` in the return), so it is NOT
		// dead at the update site — reuse must NOT fire (d's box stays live). The
		// value is still correct via the normal alloc-a-new-box path.
		{"struct-no-reuse-base-used-after-value", `struct Point { x: i32, y: i32 } function main(): i32 { let d = Point { x: 3, y: 4 }; let c = Point { ...d, x: 10 }; return c.x + c.y + d.x; }`, 17},
		{"struct-no-reuse-base-used-after-detector", `struct Point { x: i32, y: i32 } function main(): i32 { let d = Point { x: 3, y: 4 }; let c = Point { ...d, x: 10 }; let r = c.x + c.y + d.x; if (r != 17) { return 99; } return __rc_underflow_count(); }`, 0},
		// Array fields (i32[]/boolean[]/i64[]/f64[]) in a reused struct (FBIP). Here
		// the i32[] field is NOT overridden — it stays in d's box and MOVES to c
		// (deep-dropped once at c's reclamation). Only the scalar `tag` is rewritten
		// in place. The array contents read back correctly and the detector stays 0
		// (no over-release).
		{"struct-reuse-array-field-keep-value", `struct Vec { tag: i32, items: i32[] } function main(): i32 { let d = Vec { tag: 1, items: [10, 20, 30] }; let c = Vec { ...d, tag: 2 }; return c.items[0] + c.items[1] + c.items[2] + c.tag; }`, 62},
		{"struct-reuse-array-field-keep-detector", `struct Vec { tag: i32, items: i32[] } function main(): i32 { let d = Vec { tag: 1, items: [10, 20, 30] }; let c = Vec { ...d, tag: 2 }; let s = c.items[0] + c.items[1] + c.items[2] + c.tag; if (s != 62) { return 99; } return __rc_underflow_count(); }`, 0},
		// Array-field REPLACE: the override targets the i32[] field with a FRESH array
		// literal. The OLD array already in d's box is released (dec) before the new
		// one is written, then the new array is owned by the reused box. New contents
		// read back correctly and the detector stays 0 (old array freed exactly once,
		// new array freed exactly once — no leak, no over-release).
		{"struct-reuse-array-field-replace-value", `struct Vec { tag: i32, items: i32[] } function main(): i32 { let d = Vec { tag: 1, items: [10, 20, 30] }; let c = Vec { ...d, tag: 7, items: [100, 50] }; return c.items[0] + c.items[1] + c.tag; }`, 157},
		{"struct-reuse-array-field-replace-detector", `struct Vec { tag: i32, items: i32[] } function main(): i32 { let d = Vec { tag: 1, items: [10, 20, 30] }; let c = Vec { ...d, tag: 7, items: [100, 200] }; let s = c.items[0] + c.items[1] + c.tag; if (s != 307) { return 99; } return __rc_underflow_count(); }`, 0},
		// Wide-scalar (i64) field alongside an array field: the i64 override is stored
		// 8-byte into the reused box; the non-overridden array field moves to c.
		// Value correct, detector 0.
		{"struct-reuse-i64-field-detector", `struct Box { a: i64, items: i32[] } function main(): i32 { let d = Box { a: 5, items: [1, 2, 3] }; let c = Box { ...d, a: 40 }; let av: i64 = c.a; let s = (av as i32) + c.items[0] + c.items[1] + c.items[2]; if (s != 46) { return 99; } return __rc_underflow_count(); }`, 0},
		// NON-firing with an array field: the base d is read AFTER the update (d.items[0]
		// in the return), so d is NOT dead at the update site — reuse must NOT fire (d's
		// box and its array stay live). Value still correct via the normal alloc path.
		{"struct-no-reuse-array-base-used-after-detector", `struct Vec { tag: i32, items: i32[] } function main(): i32 { let d = Vec { tag: 1, items: [10, 20, 30] }; let c = Vec { ...d, tag: 9 }; let r = c.tag + d.items[0]; if (r != 19) { return 99; } return __rc_underflow_count(); }`, 0},
		// Cross-statement FBIP: a FULL construction `let c = T { ... }` (no base)
		// reuses the heap box of an EARLIER, same-type donor `d` that is dead by the
		// construction site — d's box is rewritten field-by-field and c bound to it,
		// d's slot zeroed. Here d is read once (d.x + d.y) BEFORE c, then never again,
		// so it is dead at c and reuse fires. Value correct, detector 0 (box freed once).
		{"struct-cross-reuse-value", `struct Point { x: i32, y: i32 } function main(): i32 { let d = Point { x: 3, y: 4 }; let u = d.x + d.y; let c = Point { x: 10, y: 20 }; return c.x + c.y + u; }`, 37},
		{"struct-cross-reuse-detector", `struct Point { x: i32, y: i32 } function main(): i32 { let d = Point { x: 3, y: 4 }; let u = d.x + d.y; let c = Point { x: 10, y: 20 }; let sum = c.x + c.y + u; if (sum != 37) { return 99; } return __rc_underflow_count(); }`, 0},
		// Cross-statement FBIP with an array field: the donor's OLD array is released
		// (dec) before c's fresh array is written into the reused box; c then owns the
		// new array. Donor read once before c, dead after. Value correct, detector 0.
		{"struct-cross-reuse-array-value", `struct Vec { tag: i32, items: i32[] } function main(): i32 { let d = Vec { tag: 1, items: [10, 20, 30] }; let u = d.items[0]; let c = Vec { tag: 2, items: [100, 50] }; return c.items[0] + c.items[1] + c.tag + u; }`, 162},
		{"struct-cross-reuse-array-detector", `struct Vec { tag: i32, items: i32[] } function main(): i32 { let d = Vec { tag: 1, items: [10, 20, 30] }; let u = d.items[0]; let c = Vec { tag: 2, items: [100, 50] }; let s = c.items[0] + c.items[1] + c.tag + u; if (s != 162) { return 99; } return __rc_underflow_count(); }`, 0},
		// CALL-RESULT donor (#4356 divergence 3): d is bound from a function that
		// returns a fresh struct, so it is a cross-reuse donor exactly like a
		// literal binding — the box is recycled into c, freed exactly once.
		// Value correct, detector 0.
		{"struct-cross-reuse-callret-donor-value", `struct Point { x: i32, y: i32 } function mk(a: i32): Point { return Point { x: a, y: a + 1 }; } function main(): i32 { let d: Point = mk(3); let u = d.x + d.y; let c = Point { x: 10, y: 20 }; return c.x + c.y + u; }`, 37},
		{"struct-cross-reuse-callret-donor-detector", `struct Point { x: i32, y: i32 } function mk(a: i32): Point { return Point { x: a, y: a + 1 }; } function main(): i32 { let d: Point = mk(3); let u = d.x + d.y; let c = Point { x: 10, y: 20 }; let sum = c.x + c.y + u; if (sum != 37) { return 99; } return __rc_underflow_count(); }`, 0},
		// CALL-RESULT donor with an array field: mk's return is strict-fresh
		// (fresh scalar-array literal field), so the donor's OLD array is
		// released at the reuse and c owns the new one. Value + detector 0.
		{"struct-cross-reuse-callret-array-detector", `struct Vec { tag: i32, items: i32[] } function mkv(t: i32): Vec { return Vec { tag: t, items: [10, 20, 30] }; } function main(): i32 { let d: Vec = mkv(1); let u = d.items[0]; let c = Vec { tag: 2, items: [100, 50] }; let s = c.items[0] + c.items[1] + c.tag + u; if (s != 162) { return 99; } return __rc_underflow_count(); }`, 0},
		// NON-firing: the callee returns its PARAM (not strict-fresh — not in
		// the registry), so the call-result binding is NOT a donor; c gets a
		// fresh box and nothing double-frees. Value correct, detector 0.
		{"struct-cross-no-reuse-nonfresh-callee-detector", `struct Point { x: i32, y: i32 } function pick(p: Point): Point { return p; } function main(): i32 { let s0 = Point { x: 3, y: 4 }; let d: Point = pick(s0); let u = d.x + d.y; let c = Point { x: 10, y: 20 }; let s = c.x + c.y + u; if (s != 37) { return 99; } return __rc_underflow_count(); }`, 0},
		// No type annotation on the call binding (`let d = mk(3)`): value correct,
		// detector 0.
		{"struct-cross-no-reuse-unannotated-callret-detector", `struct Point { x: i32, y: i32 } function mk(a: i32): Point { return Point { x: a, y: a + 1 }; } function main(): i32 { let d = mk(3); let u = d.x + d.y; let c = Point { x: 10, y: 20 }; let s = c.x + c.y + u; if (s != 37) { return 99; } return __rc_underflow_count(); }`, 0},
		// ENUM-field cross-reuse (#4356 divergence 1): the donor's old payload-
		// carrying enum box is flat-dec-freed on the reuse arm and the recycled
		// struct box solely owns the recipient's fresh ctor value; the sentinel
		// (payloadless Off) round-trips too. Value correct, detector 0.
		{"struct-cross-reuse-enum-field-value", `enum St { On(i32), Off } struct M { tag: i32, st: St } function main(): i32 { let d = M { tag: 1, st: On(5) }; let u: i32 = 0; match (d.st) { On(v) => { u = v + d.tag; }, Off => { u = d.tag; } } let c = M { tag: 2, st: On(10) }; let r: i32 = 0; match (c.st) { On(v) => { r = v + c.tag + u; }, Off => { r = 0; } } return r; }`, 18},
		{"struct-cross-reuse-enum-field-detector", `enum St { On(i32), Off } struct M { tag: i32, st: St } function main(): i32 { let d = M { tag: 1, st: On(5) }; let u: i32 = 0; match (d.st) { On(v) => { u = v + d.tag; }, Off => { u = d.tag; } } let c = M { tag: 2, st: On(10) }; let r: i32 = 0; match (c.st) { On(v) => { r = v + c.tag + u; }, Off => { r = 0; } } if (r != 18) { return 99; } return __rc_underflow_count(); }`, 0},
		// ENUM-field corruption probe: a fresh array allocated AFTER the enum-field
		// reuse reads back intact (the old enum box was freed exactly once; the
		// recycled struct box was not double-released).
		{"struct-cross-reuse-enum-field-corruption-detector", `enum St { On(i32), Off } struct M { tag: i32, st: St } function main(): i32 { let d = M { tag: 1, st: On(5) }; let u: i32 = 0; match (d.st) { On(v) => { u = v; }, Off => { u = 0; } } let c = M { tag: 2, st: Off }; let fresh = [11, 22, 33]; let s = fresh[0] + fresh[1] + fresh[2]; let r: i32 = 0; match (c.st) { On(v) => { r = v; }, Off => { r = c.tag; } } if (u != 5) { return 90; } if (s != 66) { return 91; } if (r != 2) { return 92; } return __rc_underflow_count(); }`, 0},
		// NON-firing: the donor's enum field value is an ALIAS (a bare ident, not
		// a fresh ctor), so the reuse must not free a box the alias still holds.
		// Value + detector 0.
		{"struct-cross-no-reuse-aliased-enum-donor-detector", `enum St { On(i32), Off } struct M { tag: i32, st: St } function main(): i32 { let e0: St = On(7); let d = M { tag: 1, st: e0 }; let u: i32 = 0; match (d.st) { On(v) => { u = v + d.tag; }, Off => { u = d.tag; } } let c = M { tag: 2, st: Off }; let w: i32 = 0; match (e0) { On(v) => { w = v; }, Off => { w = 0; } } let r: i32 = 0; match (c.st) { On(v) => { r = v; }, Off => { r = c.tag + u + w; } } if (r != 17) { return 99; } return __rc_underflow_count(); }`, 0},
		// NON-firing: the RECIPIENT's enum value is an alias — the recipient walk
		// rejects, fresh box, nothing double-frees. Value + detector 0.
		{"struct-cross-no-reuse-aliased-enum-recipient-detector", `enum St { On(i32), Off } struct M { tag: i32, st: St } function main(): i32 { let d = M { tag: 1, st: On(5) }; let u: i32 = 0; match (d.st) { On(v) => { u = v + d.tag; }, Off => { u = d.tag; } } let e1: St = On(9); let c = M { tag: 2, st: e1 }; let r: i32 = 0; match (c.st) { On(v) => { r = v + c.tag + u; }, Off => { r = 0; } } if (r != 17) { return 99; } return __rc_underflow_count(); }`, 0},
		// SELF-OVERWRITE enum override (#4356 divergence 1): the base's old On(5)
		// box is flat-dec-freed on the reuse arm before On(9) overwrites; the
		// carried variant moves with the box. Values + detector 0.
		{"struct-selfoverwrite-enum-override-value", `enum St { On(i32), Off } struct M { tag: i32, st: St } function main(): i32 { let d = M { tag: 1, st: On(5) }; let c = M { ...d, st: On(9) }; let r: i32 = 0; match (c.st) { On(v) => { r = v + c.tag; }, Off => { r = 0; } } return r; }`, 10},
		{"struct-selfoverwrite-enum-override-detector", `enum St { On(i32), Off } struct M { tag: i32, st: St } function main(): i32 { let d = M { tag: 1, st: On(5) }; let c = M { ...d, st: On(9) }; let r: i32 = 0; match (c.st) { On(v) => { r = v + c.tag; }, Off => { r = 0; } } if (r != 10) { return 99; } return __rc_underflow_count(); }`, 0},
		// SELF-OVERWRITE carried enum field: not in the override list — moves
		// with the reused box; the payload survives the rebuild. Detector 0.
		{"struct-selfoverwrite-enum-carried-detector", `enum St { On(i32), Off } struct M { tag: i32, st: St } function main(): i32 { let d = M { tag: 1, st: On(5) }; let c = M { ...d, tag: 2 }; let r: i32 = 0; match (c.st) { On(v) => { r = v + c.tag; }, Off => { r = 0; } } if (r != 7) { return 99; } return __rc_underflow_count(); }`, 0},
		// NON-firing: the base's enum field value is an ALIAS — no reuse of the
		// base, nothing double-frees.
		{"struct-selfoverwrite-no-reuse-aliased-enum-base-detector", `enum St { On(i32), Off } struct M { tag: i32, st: St } function main(): i32 { let e0: St = On(7); let d = M { tag: 1, st: e0 }; let c = M { ...d, st: On(9) }; let w: i32 = 0; match (e0) { On(v) => { w = v; }, Off => { w = 0; } } let r: i32 = 0; match (c.st) { On(v) => { r = v + c.tag + w; }, Off => { r = 0; } } if (r != 17) { return 99; } return __rc_underflow_count(); }`, 0},
		// STRING-field reuse (#4356 divergence 1): the donor's old fresh concat
		// buffer is __fern_str_free'd on the reuse arm; values stay correct and
		// nothing over-releases (str_free is rc-aware; literal .rodata data is
		// heap-guard-skipped).
		{"struct-string-field-reuse-value", `struct N { id: i32, name: string } function main(): i32 { let d = N { id: 1, name: "ab" + "c" }; let u: i32 = d.name.len() as i32 + d.id; let c = N { id: 2, name: "wxyz" + "q" }; return c.name.len() as i32 + c.id + u; }`, 11},
		{"struct-string-field-reuse-detector", `struct N { id: i32, name: string } function main(): i32 { let d = N { id: 1, name: "ab" + "c" }; let u: i32 = d.name.len() as i32 + d.id; let c = N { id: 2, name: "wxyz" + "q" }; let s: i32 = c.name.len() as i32 + c.id + u; if (s != 11) { return 99; } return __rc_underflow_count(); }`, 0},
		// STRING corruption probe: a fresh array after the string-field reuse
		// reads back intact and the recipient's string content is right.
		{"struct-string-field-corruption-detector", `struct N { id: i32, name: string } function main(): i32 { let d = N { id: 1, name: "ab" + "c" }; let u: i32 = d.name.len() as i32; let c = N { id: 2, name: "wxyz" + "q" }; let fresh = [11, 22, 33]; let s = fresh[0] + fresh[1] + fresh[2]; if (u != 3) { return 90; } if (s != 66) { return 91; } if (c.name[0] != 119) { return 92; } return __rc_underflow_count(); }`, 0},
		// MAP IDENTITY-CARRYING method: `mm.insert(1,1)` returns mm's OWN mapbox,
		// and as a struct-lit field value it carries the box past mm's last use —
		// reclaiming mm there frees a box the program still reads through c.m
		// (SIGSEGV). Value 3 + 1 = 4; corruption probe: a fresh array after the
		// struct-lit reads back intact and c.m survives.
		{"map-callvalue-struct-field-value", `import "core/map";
struct C { id: i32, m: Map[i32, i32] } function main(): i32 { let mm: Map[i32, i32] = map_new(4); let c = C { id: 3, m: mm.insert(1, 1) }; return c.id + c.m.len(); }`, 4},
		{"map-callvalue-struct-field-corruption-detector", `import "core/map";
struct C { id: i32, m: Map[i32, i32] } function main(): i32 { let mm: Map[i32, i32] = map_new(4); let c = C { id: 3, m: mm.insert(1, 1) }; let fresh = [11, 22, 33]; let s = fresh[0] + fresh[1] + fresh[2]; if (s != 66) { return 91; } if (c.id + c.m.len() != 4) { return 92; } return __rc_underflow_count(); }`, 0},
		// The own-param sibling (the shape that first surfaced the UAF while
		// testing #5087): the struct with the aliased mapbox flows through an
		// `own` param donor site — value stays correct with the map excluded.
		{"map-callvalue-own-param-field-value", `import "core/map";
struct C { id: i32, m: Map[i32, i32] } function f(own d: C): i32 { let u: i32 = d.id + d.m.len(); let mm: Map[i32, i32] = map_new(4); let c = C { id: 10, m: mm.insert(1, 5) }; return c.id + c.m.len() + u; } function main(): i32 { let m0: Map[i32, i32] = map_new(4); let c0 = C { id: 3, m: m0.insert(1, 1) }; return f(c0); }`, 15},
		// The `.without` sibling: its (Map, existed) tuple wraps the SAME mapbox,
		// so the destructured m2 aliases mm — reclaiming mm at its last use (the
		// without statement) would free the box before m2.get_or reads it
		// (SIGSEGV). Value 7 (absent key → default; interp-oracle-checked),
		// detector 0.
		{"map-without-alias-bind-value", `import "core/map";
function main(): i32 { let mm: Map[i32, i32] = map_new(8); let (m2, e) = mm.without(1); return m2.get_or(2, 7); }`, 7},
		{"map-without-alias-bind-detector", `import "core/map";
function main(): i32 { let mm: Map[i32, i32] = map_new(8); let (m2, e) = mm.without(1); let r: i32 = m2.get_or(2, 7); if (r != 7) { return 99; } return __rc_underflow_count(); }`, 0},
		// NON-firing: the donor's string field is a bare-ident ALIAS, so the
		// aliased buffer stays valid.
		{"struct-string-no-reuse-aliased-donor-detector", `struct N { id: i32, name: string } function main(): i32 { let nm: string = "ab" + "c"; let d = N { id: 1, name: nm }; let u: i32 = d.name.len() as i32 + d.id; let c = N { id: 2, name: "wxyz" + "q" }; let s: i32 = c.name.len() as i32 + c.id + u + nm.len() as i32; if (s != 14) { return 99; } return __rc_underflow_count(); }`, 0},
		// CROSS-TYPE class pairing (#4356 divergence 2): donor A and recipient B
		// share only the box class (same field count, slot-uniform boxes); field
		// kinds are position-SWAPPED, so the reuse arm's release must walk A's
		// OWN layout (dec the old array at A's slot 1) before B's fields
		// overwrite. A recipient-layout walk would rc_dec A's scalar n as a
		// pointer — the detector/value pair pins the donor-layout walk.
		{"struct-cross-type-class-pairing-value", `struct A { n: i32, xs: i32[] } struct B { ys: i32[], m: i32 } function main(): i32 { let d = A { n: 3, xs: [10, 20] }; let u: i32 = d.n + d.xs[0]; let c = B { ys: [7, 8, 9], m: 2 }; return c.ys[2] + c.m + u; }`, 24},
		{"struct-cross-type-class-pairing-detector", `struct A { n: i32, xs: i32[] } struct B { ys: i32[], m: i32 } function main(): i32 { let d = A { n: 3, xs: [10, 20] }; let u: i32 = d.n + d.xs[0]; let c = B { ys: [7, 8, 9], m: 2 }; let s: i32 = c.ys[2] + c.m + u; if (s != 24) { return 99; } return __rc_underflow_count(); }`, 0},
		// CROSS-TYPE corruption probe: a fresh array after the swapped-kind reuse
		// reads back intact (the donor's old array freed exactly once, at the
		// right slot; no scalar was mis-dec'd as a pointer).
		{"struct-cross-type-corruption-detector", `struct A { n: i32, xs: i32[] } struct B { ys: i32[], m: i32 } function main(): i32 { let d = A { n: 3, xs: [10, 20] }; let u: i32 = d.xs[1]; let c = B { ys: [40, 2], m: 5 }; let fresh = [11, 22, 33]; let s = fresh[0] + fresh[1] + fresh[2]; if (u != 20) { return 90; } if (s != 66) { return 91; } if (c.ys[0] + c.ys[1] + c.m != 47) { return 92; } return __rc_underflow_count(); }`, 0},
		// NON-firing: different field COUNTS = different box class — never paired.
		{"struct-cross-no-reuse-class-mismatch-detector", `struct A { n: i32, xs: i32[] } struct C3 { a: i32, b: i32, cc: i32 } function main(): i32 { let d = A { n: 3, xs: [10, 20] }; let u: i32 = d.n + d.xs[0]; let c = C3 { a: 1, b: 2, cc: 3 }; let s: i32 = c.a + c.b + c.cc + u; if (s != 19) { return 99; } return __rc_underflow_count(); }`, 0},
		// NON-firing cross case: the donor d is used AFTER the full construction
		// (`d.x` in the return), so d is NOT dead at the construction — reuse must NOT
		// fire and c gets a fresh box. Value still correct, detector 0.
		{"struct-cross-no-reuse-donor-used-after-value", `struct Point { x: i32, y: i32 } function main(): i32 { let d = Point { x: 3, y: 4 }; let c = Point { x: 10, y: 20 }; return c.x + c.y + d.x; }`, 33},
		{"struct-cross-no-reuse-donor-used-after-detector", `struct Point { x: i32, y: i32 } function main(): i32 { let d = Point { x: 3, y: 4 }; let c = Point { x: 10, y: 20 }; let r = c.x + c.y + d.x; if (r != 33) { return 99; } return __rc_underflow_count(); }`, 0},
		// Plain single-struct reclamation through the rc-headered box: a fresh,
		// non-escaping struct local is rc-dec'd at scope end and FREED once
		// (detector 0; a box without a refcount header over-releases and reads 1).
		{"plain-struct-reclaim-value", `struct P { x: i32, y: i32 } function compute(): i32 { let d = P { x: 3, y: 4 }; return d.x + d.y; } function main(): i32 { return compute(); }`, 7},
		{"plain-struct-reclaim-detector", `struct P { x: i32, y: i32 } function compute(): i32 { let d = P { x: 3, y: 4 }; return d.x + d.y; } function main(): i32 { let r = compute(); if (r != 7) { return 99; } return __rc_underflow_count(); }`, 0},
		// Two independent plain structs, both reclaimed at exit — both boxes freed,
		// detector 0.
		{"two-plain-structs-reclaim-detector", `struct P { x: i32, y: i32 } function main(): i32 { let a = P { x: 1, y: 2 }; let b = P { x: 3, y: 4 }; let s = a.x + a.y + b.x + b.y; if (s != 10) { return 99; } return __rc_underflow_count(); }`, 0},
		// Precise drop for a fresh SCALAR-ONLY struct local, last-used at a later
		// top-level statement: its box is freed right after that use (a single
		// shallow __fern_rc_dec — no field to walk), bounding the live set. A
		// scalar-only struct carries no rc field, so freeing the box early cannot
		// dangle any reference. Mirrors the array precise drop.
		//
		// FIRING: `p` is built, last-used inside an `if`, then dead — freed after the
		// if-statement. f(5): p.x+p.y = 3, c = 3, return 3+5 = 8.
		{"scalar-struct-precise-if-value", `struct P { x: i32, y: i32 } function f(n: i32): i32 { let p = P { x: 1, y: 2 }; let c = 0; if (n > 0) { c = p.x + p.y; } return c + n; } function main(): i32 { return f(5); }`, 8},
		{"scalar-struct-precise-if-detector", `struct P { x: i32, y: i32 } function f(n: i32): i32 { let p = P { x: 1, y: 2 }; let c = 0; if (n > 0) { c = p.x + p.y; } return c + n; } function main(): i32 { let r = f(5); if (r != 8) { return 99; } return __rc_underflow_count(); }`, 0},
		// FIRING + heap-reuse corruption probe: a FRESH array allocated AFTER the
		// precise drop reads back intact. If the early free were unsound (double-free
		// / freed a still-live box) the fresh alloc could recycle p's box and corrupt
		// the read. c == 7 and 11+22+33 == 66; detector 0.
		{"scalar-struct-precise-corruption-probe-detector", `struct P { x: i32, y: i32 } function go(): i32 { let p = P { x: 3, y: 4 }; let c = 0; let i = 0; while (i < 1) { c = p.x + p.y; i = i + 1; } let fresh = [11, 22, 33]; let s = fresh[0] + fresh[1] + fresh[2]; if (c != 7) { return 90; } if (s != 66) { return 91; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// ARRAY-FIELD struct precise-drop: a fresh `Buf { xs: [..], n }` last-used
		// in a nested block is DEEP-DROPPED (its array field freed) + box-freed
		// right after that statement (the struct sibling of the array / tuple
		// precise drops). f(5): b built, used in the if (c=b.xs[1]+b.n=23), freed
		// after the if; return 23+5=28.
		{"struct-arrfield-precise-if-value", `struct Buf { xs: i32[], n: i32 } function f(m: i32): i32 { let b = Buf { xs: [10, 20, 30], n: 3 }; let c = 0; if (m > 0) { c = b.xs[1] + b.n; } return c + m; } function main(): i32 { return f(5); }`, 28},
		{"struct-arrfield-precise-if-detector", `struct Buf { xs: i32[], n: i32 } function f(m: i32): i32 { let b = Buf { xs: [10, 20, 30], n: 3 }; let c = 0; if (m > 0) { c = b.xs[1] + b.n; } return c + m; } function main(): i32 { let r = f(5); if (r != 28) { return 99; } return __rc_underflow_count(); }`, 0},
		// Reordered fields (`Buf { n: 3, xs: [..] }`): fields map by NAME, so the
		// array field is still the one dropped. Value + detector 0.
		{"struct-arrfield-reordered-precise-if-detector", `struct Buf { xs: i32[], n: i32 } function f(m: i32): i32 { let b = Buf { n: 3, xs: [10, 20, 30] }; let c = 0; if (m > 0) { c = b.xs[0] + b.n; } return c + m; } function main(): i32 { let r = f(5); if (r != 18) { return 99; } return __rc_underflow_count(); }`, 0},
		// PRECISE drop + corruption probe: a fresh array allocated AFTER the array-field
		// struct's precise deep-drop reads back intact (the array field + box were
		// freed soundly, exactly once — no early free of a still-live buffer).
		{"struct-arrfield-precise-corruption-probe-detector", `struct Buf { xs: i32[], n: i32 } function go(): i32 { let b = Buf { xs: [1, 2, 3], n: 5 }; let c = 0; let i = 0; while (i < 1) { c = b.xs[0] + b.xs[2] + b.n; i = i + 1; } let fresh = [11, 22, 33]; let s = fresh[0] + fresh[1] + fresh[2]; if (c != 9) { return 90; } if (s != 66) { return 91; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// GUARD: a scalar-only struct local that is ALSO a cross-reuse donor (`a` dead
		// before the same-type full construction `b`) must NOT be precise-dropped —
		// cross-reuse recycles a's box into b, so an early free would race it. The
		// emission-site donor guard skips the precise drop here; the box is freed
		// exactly once (through b at exit). sa(3) + sb(7) = 10; detector 0.
		{"scalar-struct-precise-cross-reuse-donor-guard-detector", `struct P { x: i32, y: i32 } function main(): i32 { let a = P { x: 1, y: 2 }; let sa = a.x + a.y; let b = P { x: 3, y: 4 }; let sb = b.x + b.y; if (sa + sb != 10) { return 99; } return __rc_underflow_count(); }`, 0},
		// Fresh SCALAR-TUPLE local reclaim: tuple_make boxes via __fern_arr_box
		// (rc-headered, like struct_make), so a fresh, non-escaping `let t = (3, 4)`
		// (all scalar-literal elements) is freed with a shallow __fern_rc_dec. Value
		// + the over-release detector (`__rc_underflow_count()==0`) pin that the box
		// is freed exactly once.
		{"scalar-tuple-reclaim-value", `function go(): i32 { let t: (i32, i32) = (3, 4); return t.0 + t.1; } function main(): i32 { return go(); }`, 7},
		{"scalar-tuple-reclaim-detector", `function go(): i32 { let t: (i32, i32) = (3, 4); let r = t.0 + t.1; return r; } function main(): i32 { let v = go(); if (v != 7) { return 99; } return __rc_underflow_count(); }`, 0},
		// 3-element scalar tuple, same shallow free.
		{"scalar-tuple-3elem-detector", `function go(): i32 { let t: (i32, i32, i32) = (10, 20, 30); let r = t.0 + t.1 + t.2; return r; } function main(): i32 { let v = go(); if (v != 60) { return 99; } return __rc_underflow_count(); }`, 0},
		// Heap-reuse corruption probe: a FRESH array allocated AFTER the tuple's last
		// use reads back intact. If the tuple box were freed unsoundly (double-free /
		// freed a still-live box) the fresh alloc could recycle it and corrupt the
		// read. sum 7 + 11+22+33 == 66; detector 0.
		{"scalar-tuple-corruption-probe-detector", `function go(): i32 { let t: (i32, i32) = (3, 4); let s = t.0 + t.1; let fresh = [11, 22, 33]; let f = fresh[0] + fresh[1] + fresh[2]; if (s != 7) { return 90; } if (f != 66) { return 91; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// NON-firing (escapes): a tuple RETURNED whole (`return t`) escapes to the
		// caller, so it must NOT be freed in mk. The caller reads it. Value +
		// detector 0 (no over-release).
		{"scalar-tuple-escapes-not-freed-detector", `function mk(): (i32, i32) { let t: (i32, i32) = (5, 6); return t; } function main(): i32 { let t = mk(); let r = t.0 + t.1; if (r != 11) { return 99; } return __rc_underflow_count(); }`, 0},
		// PRECISE drop for an early-dead scalar tuple: `t` is last-used inside the
		// `if`, so its box is freed right after the if-statement, bounding the live
		// set. f(5): t.0+t.1 = 7, c = 7, ret 7+5=12.
		{"scalar-tuple-precise-if-value", `function f(n: i32): i32 { let t: (i32, i32) = (3, 4); let c = 0; if (n > 0) { c = t.0 + t.1; } return c + n; } function main(): i32 { return f(5); }`, 12},
		{"scalar-tuple-precise-if-detector", `function f(n: i32): i32 { let t: (i32, i32) = (3, 4); let c = 0; if (n > 0) { c = t.0 + t.1; } return c + n; } function main(): i32 { let r = f(5); if (r != 12) { return 99; } return __rc_underflow_count(); }`, 0},
		// PRECISE drop + heap-reuse corruption probe: a FRESH array allocated AFTER
		// the tuple's precise drop reads back intact (the early free was sound — no
		// double-free / dangling slot the fresh alloc could recycle). c 7 + 66; det 0.
		{"scalar-tuple-precise-corruption-probe-detector", `function go(): i32 { let t: (i32, i32) = (3, 4); let c = 0; let i = 0; while (i < 1) { c = t.0 + t.1; i = i + 1; } let fresh = [11, 22, 33]; let s = fresh[0] + fresh[1] + fresh[2]; if (c != 7) { return 90; } if (s != 66) { return 91; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// Fresh scalar OPTION reclaim: a fresh, single-owner, dead-after
		// `let o = Some(scalar)` / `None` consumed by exactly one `match (o)` has its
		// rc-headered box freed right after that match (the Option sibling of the
		// consumed-scalar-enum free). Value + `__rc_underflow_count()==0` pin the box
		// is freed exactly once (the match read its tag/payload as borrows first).
		{"option-reclaim-value", `function go(): i32 { let o: Option[i32] = Some(7); let r = 0; match (o) { Some(v) => { r = v; }, None => { r = 0; }, } return r; } function main(): i32 { return go(); }`, 7},
		{"option-reclaim-detector", `function go(): i32 { let o: Option[i32] = Some(7); let r = 0; match (o) { Some(v) => { r = v; }, None => { r = 0; }, } if (r != 7) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// None box (payloadless, rc-headered) is freed the same way.
		{"option-none-detector", `function go(): i32 { let o: Option[i32] = None; let r = 0; match (o) { Some(v) => { r = v; }, None => { r = 5; }, } if (r != 5) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// Un-annotated Some(scalar-literal).
		{"option-unannotated-literal-detector", `function go(): i32 { let o = Some(9); let r = 0; match (o) { Some(v) => { r = v; }, None => { r = 0; }, } if (r != 9) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// i64-payload Option: 8-byte payload, same box free.
		{"option-i64-payload-detector", `function go(): i32 { let o: Option[i64] = Some(100); let r: i64 = 0; match (o) { Some(v) => { r = v; }, None => { r = 0; }, } if (r as i32 != 100) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// Heap-reuse corruption probe: a fresh array allocated AFTER the option's
		// consuming match reads back intact (the box free was sound). r 7 + 66; det 0.
		{"option-corruption-probe-detector", `function go(): i32 { let o: Option[i32] = Some(7); let r = 0; match (o) { Some(v) => { r = v; }, None => { r = 0; }, } let fresh = [11, 22, 33]; let s = fresh[0] + fresh[1] + fresh[2]; if (r != 7) { return 90; } if (s != 66) { return 91; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// NON-firing (used after the match): `o` is matched twice, so it is not dead
		// after the first match — must NOT be freed there. Detector 0 (no over-release).
		{"option-used-after-match-detector", `function go(): i32 { let o: Option[i32] = Some(7); let a = 0; match (o) { Some(v) => { a = v; }, None => { a = 0; }, } let b = 0; match (o) { Some(v) => { b = v; }, None => { b = 0; }, } if (a + b != 14) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// NON-firing (escapes / returned): a returned Option is moved to the caller, so
		// mk must NOT free it. Detector 0.
		{"option-escapes-not-freed-detector", `function mk(): Option[i32] { let o: Option[i32] = Some(7); return o; } function main(): i32 { let o = mk(); let r = 0; match (o) { Some(v) => { r = v; }, None => { r = 0; }, } if (r != 7) { return 99; } return __rc_underflow_count(); }`, 0},
		// Fresh scalar RESULT reclaim: Ok / Err share the same rc-headered box as
		// Some, so a fresh, single-owner, dead-after `let r = Ok(scalar)` /
		// `Err(scalar)` consumed by exactly one `match (r)` has its box freed right
		// after that match — the Result sibling of the option free. Value +
		// `__rc_underflow_count()==0` pin the box is freed exactly once.
		{"result-ok-reclaim-value", `function go(): i32 { let r: Result[i32, i32] = Ok(7); let x = 0; match (r) { Ok(v) => { x = v; }, Err(e) => { x = e; }, } return x; } function main(): i32 { return go(); }`, 7},
		{"result-ok-reclaim-detector", `function go(): i32 { let r: Result[i32, i32] = Ok(7); let x = 0; match (r) { Ok(v) => { x = v; }, Err(e) => { x = e; }, } if (x != 7) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// Err(scalar) box is freed the same way.
		{"result-err-reclaim-detector", `function go(): i32 { let r: Result[i32, i32] = Err(4); let x = 0; match (r) { Ok(v) => { x = v; }, Err(e) => { x = e; }, } if (x != 4) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// Distinct Ok/Err scalar types: Ok reads T (i32), Err reads E (boolean); the
		// constructed Ok(i32) box holds only T's payload.
		{"result-distinct-types-detector", `function go(): i32 { let r: Result[i32, boolean] = Ok(9); let x = 0; match (r) { Ok(v) => { x = v; }, Err(e) => { x = 1; }, } if (x != 9) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// i64-payload Result Ok: 8-byte payload, same box free.
		{"result-i64-payload-detector", `function go(): i32 { let r: Result[i64, i32] = Ok(100); let x: i64 = 0; match (r) { Ok(v) => { x = v; }, Err(e) => { x = 0; }, } if (x as i32 != 100) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// Heap-reuse corruption probe: a fresh array allocated AFTER the result's
		// consuming match reads back intact (the box free was sound). x 7 + 66; det 0.
		{"result-corruption-probe-detector", `function go(): i32 { let r: Result[i32, i32] = Ok(7); let x = 0; match (r) { Ok(v) => { x = v; }, Err(e) => { x = e; }, } let fresh = [11, 22, 33]; let s = fresh[0] + fresh[1] + fresh[2]; if (x != 7) { return 90; } if (s != 66) { return 91; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// NON-firing (used after the match): `r` is matched twice, so it is not dead
		// after the first match — must NOT be freed there. Detector 0 (no over-release).
		{"result-used-after-match-detector", `function go(): i32 { let r: Result[i32, i32] = Ok(7); let a = 0; match (r) { Ok(v) => { a = v; }, Err(e) => { a = e; }, } let b = 0; match (r) { Ok(v) => { b = v; }, Err(e) => { b = e; }, } if (a + b != 14) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// NON-firing (escapes / returned): a returned Result is moved to the caller, so
		// mk must NOT free it. Detector 0.
		{"result-escapes-not-freed-detector", `function mk(): Result[i32, i32] { let r: Result[i32, i32] = Ok(7); return r; } function main(): i32 { let r = mk(); let x = 0; match (r) { Ok(v) => { x = v; }, Err(e) => { x = e; }, } if (x != 7) { return 99; } return __rc_underflow_count(); }`, 0},
		// RC-PAYLOAD (scalar array) Option/Result deep-drop free: a
		// `let o = Some([..])` / `Ok([..])` / `Err([..])` with a flat scalar-array
		// payload DEEP-DROPS its payload then frees the box, right after its single
		// consuming match. A borrow-only arm binding (`Some(v) => v[i] / v.len()`)
		// ends before the post-match free. Value + `__rc_underflow_count()==0` pin
		// the box AND the payload array are each freed exactly once.
		{"option-arr-payload-value", `function go(): i32 { let o: Option[i32[]] = Some([10, 20, 30]); let r = 0; match (o) { Some(v) => { r = v[0] + v[2]; }, None => { r = 0; }, } return r; } function main(): i32 { return go(); }`, 40},
		{"option-arr-payload-detector", `function go(): i32 { let o: Option[i32[]] = Some([10, 20, 30]); let r = 0; match (o) { Some(v) => { r = v[0] + v[2]; }, None => { r = 0; }, } if (r != 40) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// Un-annotated Some([..]) (Option infers the single type param).
		{"option-arr-unannotated-detector", `function go(): i32 { let o = Some([1, 2, 3]); let r = 0; match (o) { Some(v) => { r = v[1]; }, None => { r = 0; }, } if (r != 2) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// A borrow that reads .len() as well as indexing still fires + stays clean.
		{"option-arr-len-borrow-detector", `function go(): i32 { let o: Option[i32[]] = Some([4, 5, 6, 7]); let r = 0; match (o) { Some(v) => { r = v.len() + v[0]; }, None => { r = 0; }, } if (r != 8) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// f64[] payload: the 8-byte-element array is still a flat scalar buffer, so the
		// single-dec payload release + box free are the same. Value + detector 0.
		{"option-f64arr-payload-detector", `function go(): i32 { let o: Option[f64[]] = Some([1.5, 2.5]); let r: f64 = 0.0; match (o) { Some(v) => { r = v[0] + v[1]; }, None => { r = 0.0; }, } if ((r as i32) != 4) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// Result Ok([..]): the array payload of an Ok box (tag 0) is deep-dropped the
		// same way — the Result sibling of the option array-payload free.
		{"result-ok-arr-payload-value", `function go(): i32 { let r: Result[i32[], i32] = Ok([5, 6, 7]); let x = 0; match (r) { Ok(v) => { x = v[0] + v[2]; }, Err(e) => { x = e; }, } return x; } function main(): i32 { return go(); }`, 12},
		{"result-ok-arr-payload-detector", `function go(): i32 { let r: Result[i32[], i32] = Ok([5, 6, 7]); let x = 0; match (r) { Ok(v) => { x = v[0] + v[2]; }, Err(e) => { x = e; }, } if (x != 12) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// Result Err([..]): the Err payload (tag 1) is the array, deep-dropped with
		// the box.
		{"result-err-arr-payload-detector", `function go(): i32 { let r: Result[i32, i32[]] = Err([3, 4]); let x = 0; match (r) { Ok(v) => { x = v; }, Err(e) => { x = e[0] + e[1]; }, } if (x != 7) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// Heap-reuse corruption probe: a fresh array allocated AFTER the option's
		// consuming match + payload deep-drop reads back intact (the payload + box
		// frees were sound — no double-free / dangling slot the alloc could recycle).
		{"option-arr-corruption-probe-detector", `function go(): i32 { let o: Option[i32[]] = Some([1, 2, 3]); let r = 0; match (o) { Some(v) => { r = v[0] + v[2]; }, None => { r = 0; }, } let fresh = [11, 22, 33]; let s = fresh[0] + fresh[1] + fresh[2]; if (r != 4) { return 90; } if (s != 66) { return 91; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// NON-firing (binding escapes the arm): the Some arm STORES its payload `v` to
		// an outer array var, so the box's array must NOT be freed after the match
		// — it moved out. Detector 0 (no over-release).
		{"option-arr-binding-escapes-detector", `function go(): i32 { let o: Option[i32[]] = Some([7, 8, 9]); let out: i32[] = [0]; match (o) { Some(v) => { out = v; }, None => {} } let r = out[0] + out[2]; if (r != 16) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// NON-firing (used after the match): `o` matched twice, so it is not dead after
		// the first match — must NOT be freed there. Detector 0.
		{"option-arr-used-after-detector", `function go(): i32 { let o: Option[i32[]] = Some([1, 2, 3]); let a = 0; match (o) { Some(v) => { a = v[0]; }, None => { a = 0; }, } let b = 0; match (o) { Some(v) => { b = v[1]; }, None => { b = 0; }, } if (a + b != 3) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// NON-firing (escapes / returned): a returned Option is moved to the caller, so
		// mk must NOT free it (nor its array). Detector 0.
		{"option-arr-escapes-not-freed-detector", `function mk(): Option[i32[]] { let o: Option[i32[]] = Some([1, 2, 3]); return o; } function main(): i32 { let o = mk(); let r = 0; match (o) { Some(v) => { r = v[0] + v[2]; }, None => { r = 0; }, } if (r != 4) { return 99; } return __rc_underflow_count(); }`, 0},
		// STRUCT-PAYLOAD Option/Result deep-drop free: a `let o = Some(P{..})` /
		// `Ok(P{..})` with a FRESH struct-LITERAL payload frees the payload box (and,
		// for an array-field struct, releases its fields through P's drop) then the
		// option box, right after its single consuming match. The fresh literal is
		// sole-owned (rc==1). Borrow-only arm binding (`Some(p) => p.x + p.y`).
		// Value + `__rc_underflow_count()==0` pin the payload box AND the option box
		// freed once each.
		{"option-struct-payload-value", `struct P { x: i32, y: i32 } function go(): i32 { let o: Option[P] = Some(P { x: 3, y: 4 }); let r = 0; match (o) { Some(p) => { r = p.x + p.y; }, None => { r = 0; }, } return r; } function main(): i32 { return go(); }`, 7},
		{"option-struct-payload-detector", `struct P { x: i32, y: i32 } function go(): i32 { let o: Option[P] = Some(P { x: 3, y: 4 }); let r = 0; match (o) { Some(p) => { r = p.x + p.y; }, None => { r = 0; }, } if (r != 7) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// Un-annotated Some(P{..}) (Option infers the single param from the literal).
		{"option-struct-payload-unannotated-detector", `struct P { x: i32, y: i32 } function go(): i32 { let o = Some(P { x: 3, y: 4 }); let r = 0; match (o) { Some(p) => { r = p.x + p.y; }, None => { r = 0; }, } if (r != 7) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// Result Ok(P{..}) struct payload freed the same way (tag 0).
		{"result-ok-struct-payload-detector", `struct P { x: i32, y: i32 } function go(): i32 { let r: Result[P, i32] = Ok(P { x: 5, y: 6 }); let v = 0; match (r) { Ok(p) => { v = p.x + p.y; }, Err(e) => { v = e; }, } if (v != 11) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// Array-FIELD struct payload: `Some(Buf{xs:[..], n})` — the Buf box, its
		// array field and the option box must each be released at most once.
		// Value + detector 0.
		{"option-struct-arrfield-payload-value", `struct Buf { xs: i32[], n: i32 } function go(): i32 { let o: Option[Buf] = Some(Buf { xs: [10, 20, 30], n: 3 }); let r = 0; match (o) { Some(b) => { r = b.xs[1] + b.n; }, None => { r = 0; } } return r; } function main(): i32 { return go(); }`, 23},
		{"option-struct-arrfield-payload-detector", `struct Buf { xs: i32[], n: i32 } function go(): i32 { let o: Option[Buf] = Some(Buf { xs: [10, 20, 30], n: 3 }); let r = 0; match (o) { Some(b) => { r = b.xs[1] + b.n; }, None => { r = 0; } } if (r != 23) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// Wildcard arm (no binding / no field read) — still shallow-freed cleanly.
		{"option-struct-arrfield-wildcard-detector", `struct Buf { xs: i32[], n: i32 } function go(): i32 { let o: Option[Buf] = Some(Buf { xs: [10, 20, 30], n: 3 }); let r = 0; match (o) { Some(_) => { r = 5; }, None => { r = 0; } } if (r != 5) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// Heap-reuse corruption probe: fresh arrays allocated AFTER the array-field
		// struct payload's consuming match read back intact (the box frees were sound
		// and did NOT free the still-machinery-owned array field early).
		{"option-struct-arrfield-corruption-probe-detector", `struct Buf { xs: i32[], n: i32 } function go(): i32 { let o: Option[Buf] = Some(Buf { xs: [10, 20, 30], n: 3 }); let r = 0; match (o) { Some(b) => { r = b.xs[0] + b.xs[2]; }, None => { r = 0; } } let fresh = [11, 22, 33]; let s = fresh[0] + fresh[1] + fresh[2]; if (r != 40) { return 90; } if (s != 66) { return 91; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// Heap-reuse corruption probe: a fresh array allocated AFTER the struct-payload
		// deep-drop reads back intact (the payload box + option box frees were sound).
		{"option-struct-payload-corruption-probe-detector", `struct P { x: i32, y: i32 } function go(): i32 { let o: Option[P] = Some(P { x: 3, y: 4 }); let r = 0; match (o) { Some(p) => { r = p.x + p.y; }, None => { r = 0; }, } let fresh = [11, 22, 33]; let s = fresh[0] + fresh[1] + fresh[2]; if (r != 7) { return 90; } if (s != 66) { return 91; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// NON-firing (bare-ident struct payload): `Some(p)` where p is a local aliases
		// p's box — NOT a fresh literal, so freeing it with the option would
		// double-free with p's own reclamation. Detector 0.
		{"option-struct-bareident-not-freed-detector", `struct P { x: i32, y: i32 } function go(): i32 { let p = P { x: 3, y: 4 }; let o: Option[P] = Some(p); let r = 0; match (o) { Some(q) => { r = q.x + q.y; }, None => { r = 0; }, } if (r != 7) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// NON-firing (binding escapes): the Some arm STORES its struct payload `p` to
		// an outer var, so the option's payload must NOT be freed after the match.
		// Detector 0 (no over-release).
		{"option-struct-binding-escapes-detector", `struct P { x: i32, y: i32 } function go(): i32 { let o: Option[P] = Some(P { x: 3, y: 4 }); let keep = P { x: 0, y: 0 }; match (o) { Some(p) => { keep = p; }, None => {} } let r = keep.x + keep.y; if (r != 7) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// PRECISE drop for a fresh scalar Option/Result last-used inside a NESTED block
		// (an if-body match, no top-level consuming match): the rc-headered box is
		// shallow-freed right after the whole if-statement — the option sibling of
		// the scalar struct/tuple precise-if drops. FIRING: f(5): o=Some(3), the if
		// runs the match, c=3, return 3+5=8.
		{"option-precise-if-value", `function f(n: i32): i32 { let o: Option[i32] = Some(3); let c = 0; if (n > 0) { match (o) { Some(v) => { c = v; }, None => { c = 0; } } } return c + n; } function main(): i32 { return f(5); }`, 8},
		{"option-precise-if-detector", `function f(n: i32): i32 { let o: Option[i32] = Some(3); let c = 0; if (n > 0) { match (o) { Some(v) => { c = v; }, None => { c = 0; } } } return c + n; } function main(): i32 { let r = f(5); if (r != 8) { return 99; } return __rc_underflow_count(); }`, 0},
		// A None box (payloadless, rc-headered via opt_none) is precise-dropped the same.
		{"option-precise-none-detector", `function f(n: i32): i32 { let o: Option[i32] = None; let c = 0; if (n > 0) { match (o) { Some(v) => { c = v; }, None => { c = 9; } } } return c + n; } function main(): i32 { let r = f(5); if (r != 14) { return 99; } return __rc_underflow_count(); }`, 0},
		// Result last-used in a nested if-match — precise-dropped the same way.
		{"result-precise-if-detector", `function f(n: i32): i32 { let r: Result[i32, i32] = Ok(4); let c = 0; if (n > 0) { match (r) { Ok(v) => { c = v; }, Err(e) => { c = e; } } } return c + n; } function main(): i32 { let z = f(5); if (z != 9) { return 99; } return __rc_underflow_count(); }`, 0},
		// PRECISE drop + heap-reuse corruption probe: a FRESH array allocated AFTER the
		// option's precise drop reads back intact (the early box free was sound — no
		// double-free / dangling slot the fresh alloc could recycle). c 3 + 66; det 0.
		{"option-precise-corruption-probe-detector", `function go(): i32 { let o: Option[i32] = Some(3); let c = 0; let i = 0; while (i < 1) { match (o) { Some(v) => { c = v; }, None => {} } i = i + 1; } let fresh = [11, 22, 33]; let s = fresh[0] + fresh[1] + fresh[2]; if (c != 3) { return 90; } if (s != 66) { return 91; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// An option consumed by a top-level match: the box is freed exactly once.
		// Detector 0 (no double-free).
		{"option-toplevel-match-not-double-freed-detector", `function go(): i32 { let o: Option[i32] = Some(7); let r = 0; match (o) { Some(v) => { r = v; }, None => { r = 0; } } if (r != 7) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// PRECISE drop for an rc-PAYLOAD Option last-used in a NESTED block — the
		// rc-payload sibling of the scalar-option precise-if drop above. An
		// `Option[i32[]]` / `Option[P]` / `Option[Buf]` with no top-level match and a
		// borrow-only nested payload binding has its payload + box freed right after
		// the enclosing statement.
		// FIRING: f(5): o=Some([10,20,30]), the if runs the match, c=v[0]+v[2]=40,
		// return 40+5=45.
		{"option-arr-precise-if-value", `function f(n: i32): i32 { let o: Option[i32[]] = Some([10, 20, 30]); let c = 0; if (n > 0) { match (o) { Some(v) => { c = v[0] + v[2]; }, None => {} } } return c + n; } function main(): i32 { return f(5); }`, 45},
		{"option-arr-precise-if-detector", `function f(n: i32): i32 { let o: Option[i32[]] = Some([10, 20, 30]); let c = 0; if (n > 0) { match (o) { Some(v) => { c = v[0] + v[2]; }, None => {} } } return c + n; } function main(): i32 { let r = f(5); if (r != 45) { return 99; } return __rc_underflow_count(); }`, 0},
		// Scalar-struct payload option, nested-block last use — payload box + option box
		// freed after the if.
		{"option-struct-precise-if-detector", `struct P { x: i32, y: i32 } function f(n: i32): i32 { let o: Option[P] = Some(P { x: 3, y: 4 }); let c = 0; if (n > 0) { match (o) { Some(p) => { c = p.x + p.y; }, None => {} } } return c + n; } function main(): i32 { let r = f(5); if (r != 12) { return 99; } return __rc_underflow_count(); }`, 0},
		// Array-field struct payload option, nested-block last use — shallow box free
		// (the array field is machinery-owned, as in the consume-by-match case).
		{"option-arrfield-struct-precise-if-detector", `struct Buf { xs: i32[], n: i32 } function f(k: i32): i32 { let o: Option[Buf] = Some(Buf { xs: [10, 20, 30], n: 3 }); let c = 0; if (k > 0) { match (o) { Some(b) => { c = b.xs[1] + b.n; }, None => {} } } return c + k; } function main(): i32 { let r = f(5); if (r != 28) { return 99; } return __rc_underflow_count(); }`, 0},
		// PRECISE drop + corruption probe: fresh arrays after the array-payload option's
		// precise drop read back intact (the early payload + box free was sound).
		{"option-arr-precise-corruption-probe-detector", `function go(): i32 { let o: Option[i32[]] = Some([1, 2, 3]); let c = 0; let i = 0; while (i < 1) { match (o) { Some(v) => { c = v[0] + v[2]; }, None => {} } i = i + 1; } let fresh = [11, 22, 33]; let s = fresh[0] + fresh[1] + fresh[2]; if (c != 4) { return 90; } if (s != 66) { return 91; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// NON-firing (binding escapes in the nested block): the Some arm STORES its
		// array payload `v` to an outer var, so the option must NOT be freed (v
		// moved out). Detector 0.
		{"option-arr-precise-binding-escapes-detector", `function go(): i32 { let o: Option[i32[]] = Some([7, 8, 9]); let out: i32[] = [0]; if (true) { match (o) { Some(v) => { out = v; }, None => {} } } let r = out[0] + out[2]; if (r != 16) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// RESULT precise-drop parity: a scalar RESULT box is freed like the scalar
		// Option one.
		// f(5): Ok(4), c=4, 4+5=9.
		{"result-scalar-precise-if-value", `function f(n: i32): i32 { let r: Result[i32, i32] = Ok(4); let c = 0; if (n > 0) { match (r) { Ok(v) => { c = v; }, Err(e) => { c = e; } } } return c + n; } function main(): i32 { return f(5); }`, 9},
		{"result-scalar-precise-if-detector", `function f(n: i32): i32 { let r: Result[i32, i32] = Ok(4); let c = 0; if (n > 0) { match (r) { Ok(v) => { c = v; }, Err(e) => { c = e; } } } return c + n; } function main(): i32 { let z = f(5); if (z != 9) { return 99; } return __rc_underflow_count(); }`, 0},
		// rc-PAYLOAD Result precise-drop: an Ok([..]) box and its array freed after
		// the nested if.
		{"result-arr-ok-precise-if-detector", `function f(n: i32): i32 { let r: Result[i32[], i32] = Ok([10, 20, 30]); let c = 0; if (n > 0) { match (r) { Ok(v) => { c = v[0] + v[2]; }, Err(e) => { c = e; } } } return c + n; } function main(): i32 { let z = f(5); if (z != 45) { return 99; } return __rc_underflow_count(); }`, 0},
		// rc-PAYLOAD Result precise-drop, ERR-array side: an Err([..]) box (offset-8 is
		// the Err array pointer) freed the same way — the case the slot type alone can't
		// distinguish from an Ok-scalar box, resolved by the recorded kind.
		{"result-arr-err-precise-if-detector", `function f(n: i32): i32 { let r: Result[i32, i32[]] = Err([3, 4, 5]); let c = 0; if (n > 0) { match (r) { Ok(v) => { c = v; }, Err(e) => { c = e[0] + e[2]; } } } return c + n; } function main(): i32 { let z = f(5); if (z != 13) { return 99; } return __rc_underflow_count(); }`, 0},
		// rc-PAYLOAD Result precise-drop, scalar-STRUCT Ok payload.
		{"result-struct-precise-if-detector", `struct P { x: i32, y: i32 } function f(n: i32): i32 { let r: Result[P, i32] = Ok(P { x: 3, y: 4 }); let c = 0; if (n > 0) { match (r) { Ok(p) => { c = p.x + p.y; }, Err(e) => { c = e; } } } return c + n; } function main(): i32 { let z = f(5); if (z != 12) { return 99; } return __rc_underflow_count(); }`, 0},
		// SCALAR user-enum precise-drop in a nested block — the enum sibling of the
		// scalar-option precise-if drop, for an UN-annotated `let x = Circle(7)` with
		// no top-level match. FIRING: f(5): x=Circle(7), c=r+r=14, return 14+5=19.
		{"enum-scalar-precise-if-value", `enum Shape { Circle(i32), Square(i32) } function f(n: i32): i32 { let x = Circle(7); let c = 0; if (n > 0) { match (x) { Circle(r) => { c = r + r; }, Square(w) => { c = w; } } } return c + n; } function main(): i32 { return f(5); }`, 19},
		{"enum-scalar-precise-if-detector", `enum Shape { Circle(i32), Square(i32) } function f(n: i32): i32 { let x = Circle(7); let c = 0; if (n > 0) { match (x) { Circle(r) => { c = r + r; }, Square(w) => { c = w; } } } return c + n; } function main(): i32 { let z = f(5); if (z != 19) { return 99; } return __rc_underflow_count(); }`, 0},
		// Annotated variant (`let x: Shape = Square(6)`) — same free.
		{"enum-annotated-precise-if-detector", `enum Shape { Circle(i32), Square(i32) } function f(n: i32): i32 { let x: Shape = Square(6); let c = 0; if (n > 0) { match (x) { Circle(r) => { c = r; }, Square(w) => { c = w * 2; } } } return c + n; } function main(): i32 { let z = f(5); if (z != 17) { return 99; } return __rc_underflow_count(); }`, 0},
		// PRECISE drop + corruption probe: a fresh array after the enum's precise drop
		// reads back intact (the early box free was sound).
		{"enum-scalar-precise-corruption-probe-detector", `enum Shape { Circle(i32), Square(i32) } function go(): i32 { let x = Circle(7); let c = 0; let i = 0; while (i < 1) { match (x) { Circle(r) => { c = r; }, Square(w) => { c = w; } } i = i + 1; } let fresh = [11, 22, 33]; let s = fresh[0] + fresh[1] + fresh[2]; if (c != 7) { return 90; } if (s != 66) { return 91; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// An enum consumed by a top-level match: freed exactly once.
		{"enum-toplevel-match-not-double-freed-detector", `enum Shape { Circle(i32), Square(i32) } function go(): i32 { let x = Circle(7); let t = 0; match (x) { Circle(r) => { t = r + r; }, Square(w) => { t = w * w; } } if (t != 14) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// rc-PAYLOAD user-enum precise-drop in a nested block — the rc-payload-enum
		// sibling of the rc-payload-option precise drop. A `Poly([..])` (array-payload
		// variant) with no top-level match and a borrow-only arm binding has its runtime
		// variant deep-dropped + box freed after the enclosing statement.
		// FIRING: f(5): x=Poly([10,20,30]), c=a[0]+a[2]=40, return 40+5=45.
		{"enum-arr-precise-if-value", `enum Shape { Poly(i32[]), Dot(i32) } function f(n: i32): i32 { let x = Poly([10, 20, 30]); let c = 0; if (n > 0) { match (x) { Poly(a) => { c = a[0] + a[2]; }, Dot(d) => { c = d; } } } return c + n; } function main(): i32 { return f(5); }`, 45},
		{"enum-arr-precise-if-detector", `enum Shape { Poly(i32[]), Dot(i32) } function f(n: i32): i32 { let x = Poly([10, 20, 30]); let c = 0; if (n > 0) { match (x) { Poly(a) => { c = a[0] + a[2]; }, Dot(d) => { c = d; } } } return c + n; } function main(): i32 { let z = f(5); if (z != 45) { return 99; } return __rc_underflow_count(); }`, 0},
		// STRUCT payload enum variant (`V(Buf)` with Buf{xs:i32[],n}): the drop
		// releases the payload struct's array field through Buf's drop, then frees
		// the payload + box. Value + detector 0.
		{"enum-struct-precise-if-detector", `struct Buf { xs: i32[], n: i32 } enum E { V(Buf), W(i32) } function f(n: i32): i32 { let x = V(Buf { xs: [10, 20, 30], n: 3 }); let c = 0; if (n > 0) { match (x) { V(b) => { c = b.xs[1] + b.n; }, W(k) => { c = k; } } } return c + n; } function main(): i32 { let z = f(5); if (z != 28) { return 99; } return __rc_underflow_count(); }`, 0},
		// PRECISE drop + corruption probe: a fresh array after the rc-payload enum's
		// precise deep-drop reads back intact (payload + box freed soundly, exactly once).
		{"enum-arr-precise-corruption-probe-detector", `enum Shape { Poly(i32[]), Dot(i32) } function go(): i32 { let x = Poly([1, 2, 3]); let c = 0; let i = 0; while (i < 1) { match (x) { Poly(a) => { c = a[0] + a[2]; }, Dot(d) => { c = d; } } i = i + 1; } let fresh = [11, 22, 33]; let s = fresh[0] + fresh[1] + fresh[2]; if (c != 4) { return 90; } if (s != 66) { return 91; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// NON-firing (arm binds an rc payload that ESCAPES): the Poly arm STORES its
		// array payload `a` to an outer var, so the enum must NOT be freed with its
		// payload (a moved out). Detector 0.
		{"enum-arr-precise-binding-escapes-detector", `enum Shape { Poly(i32[]), Dot(i32) } function go(): i32 { let x = Poly([7, 8, 9]); let out: i32[] = [0]; if (true) { match (x) { Poly(a) => { out = a; }, Dot(d) => {} } } let r = out[0] + out[2]; if (r != 16) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// Struct with an i32[] field: the box is rc-headered and freed soundly, and
		// the array field is deep-dropped exactly once.
		// Detector 0 proves neither the box nor the array field over-releases.
		{"struct-array-field-reclaim-value", `struct Buf { xs: i32[], n: i32 } function go(): i32 { let b = Buf { xs: [10, 20, 30], n: 3 }; return b.xs[1] + b.n; } function main(): i32 { return go(); }`, 23},
		{"struct-array-field-reclaim-detector", `struct Buf { xs: i32[], n: i32 } function main(): i32 { let b = Buf { xs: [10, 20, 30], n: 3 }; let r = b.xs[1] + b.n; if (r != 23) { return 99; } return __rc_underflow_count(); }`, 0},
		// Array-field from an ALIASED VARIABLE: `Box { v: a }` where `a` is an
		// rc-tracked scalar-array local. A Perceus dup (rc_inc) at the construction
		// store gives the struct's field a counted reference, so `a`'s own release
		// and the field's deep-drop each dec once (rc 1 →inc 2 →a 1 →field-drop 0).
		// Reads back correctly and the over-release detector stays 0.
		{"struct-arr-field-from-var-value", `struct Box { v: i32[] } function main(): i32 { let a = [1, 2, 3]; let b = Box { v: a }; return b.v[0]; }`, 1},
		{"struct-arr-field-from-var-detector", `struct Box { v: i32[] } function main(): i32 { let a = [1, 2, 3]; let b = Box { v: a }; let r = b.v[0]; if (r != 1) { return 99; } return __rc_underflow_count(); }`, 0},
		// TWO-alias stress: the same array local aliased into TWO struct boxes.
		// Each construction emits its own dup, so the array's rc reaches 3 (1 →inc
		// 2 →inc 3) and is dec'd three times (a + b's field-drop + c's field-drop)
		// — freed exactly once. Detector 0 proves the inc count is correct under
		// multiple aliases (no leak, no over-release).
		{"struct-arr-field-two-alias-detector", `struct Box { v: i32[] } function main(): i32 { let a = [1, 2, 3]; let b = Box { v: a }; let c = Box { v: a }; let r = b.v[0] + c.v[1]; if (r != 3) { return 99; } return __rc_underflow_count(); }`, 0},
		// Read an array OUT of a struct field. The three alias-creating positions —
		// bind (`let y = h.items`), return (`return h.items`), assign
		// (`x = h.items`) — each take a Perceus dup (rc_inc) so the new owner holds
		// a counted reference, balanced against the struct's own field-drop (and,
		// for return, the caller's eventual dec).
		//
		// BIND `let y = h.items`: y becomes a second owner of the field's array.
		// inc at the field read; y's release and the struct field's deep-drop each
		// dec once (rc 1 →inc 2 →y 1 →field-drop 0). Reads back correctly,
		// detector 0.
		{"field-arr-bind-value", `struct H { items: i32[] } function main(): i32 { let h = H { items: [10, 20, 30] }; let y = h.items; return y[0] + y[2]; }`, 40},
		{"field-arr-bind-detector", `struct H { items: i32[] } function main(): i32 { let h = H { items: [10, 20, 30] }; let y = h.items; let r = y[0] + y[2]; if (r != 40) { return 99; } return __rc_underflow_count(); }`, 0},
		// BIND from a BORROWED struct PARAM (`function f(h: H): i32 { let y = h.items; }`):
		// h is a borrow (never swept), the inc hands the new local its own counted
		// ref; the local is exit-swept (dec) → balanced. Detector 0.
		{"field-arr-bind-param-detector", `struct H { items: i32[] } function f(h: H): i32 { let y = h.items; let r = y[1]; return r; } function main(): i32 { let h = H { items: [5, 6, 7] }; let v = f(h); if (v != 6) { return 99; } return __rc_underflow_count(); }`, 0},
		// RETURN `return h.items`: the field escapes to the caller, who will own it.
		// inc so the returned ref is counted; the field is NOT moved out (the struct
		// keeps its ref). Caller indexes the returned array. Value + detector 0.
		{"field-arr-return-value", `struct H { items: i32[] } function get(h: H): i32[] { return h.items; } function main(): i32 { let h = H { items: [11, 22, 33] }; let xs = get(h); return xs[0] + xs[2]; }`, 44},
		{"field-arr-return-detector", `struct H { items: i32[] } function get(h: H): i32[] { return h.items; } function main(): i32 { let h = H { items: [11, 22, 33] }; let xs = get(h); let r = xs[0] + xs[2]; if (r != 44) { return 99; } return __rc_underflow_count(); }`, 0},
		// RETURN from a function that OWNS the struct (fresh local `h`, reclaimable):
		// at exit the struct field-drop decs the array (rc 1 →inc 2 →field-drop 1),
		// the caller decs to 0. The escaping array survives the field-drop. Value +
		// detector 0.
		{"field-arr-return-owned-detector", `struct H { items: i32[] } function mk(): i32[] { let h = H { items: [4, 5, 6] }; return h.items; } function main(): i32 { let xs = mk(); let r = xs[0] + xs[1] + xs[2]; if (r != 15) { return 99; } return __rc_underflow_count(); }`, 0},
		// ASSIGN `x = h.items` where x previously held a DIFFERENT array: the store
		// decs the OLD array x held (cow-guarded) THEN incs the new field read. No
		// leak-of-old (freed once), no over-release of the field's array. Value +
		// detector 0.
		{"field-arr-assign-value", `struct H { items: i32[] } function main(): i32 { let h = H { items: [7, 8, 9] }; let x = [1, 2, 3]; x = h.items; return x[0] + x[2]; }`, 16},
		{"field-arr-assign-detector", `struct H { items: i32[] } function main(): i32 { let h = H { items: [7, 8, 9] }; let x = [1, 2, 3]; let pre = x[1]; x = h.items; let r = x[0] + x[2]; if (pre != 2) { return 98; } if (r != 16) { return 99; } return __rc_underflow_count(); }`, 0},
		// ASSIGN f64[] field: same dec-old / inc-new on the 8-byte-element field.
		// Detector 0 (the dec-old + inc-new balance for f64[] too).
		{"field-arr-assign-f64-detector", `struct H { items: f64[] } function main(): i32 { let h = H { items: [1.5, 2.5, 3.0] }; let x: f64[] = [0.5, 0.5]; x = h.items; let s = x[0] + x[2]; if ((s as i32) != 4) { return 99; } return __rc_underflow_count(); }`, 0},
		// Bare array FIELD-COPY as a struct-literal field value (#3292, #3308). The
		// value `s.xs` is a field READ — it lowers via struct_get to a real array
		// pointer that ALIASES the source struct's field — so the new box `s2` becomes
		// a second owner of the same array. A Perceus dup (rc_inc) at the construction
		// store gives the field its counted reference: the source struct's field-drop
		// and the new struct's field-drop each dec, the inc covers the second owner
		// (rc 1 →inc 2 →src-field-drop 1 →new-field-drop 0). Reads back correctly
		// and the over-release detector stays 0.
		{"field-copy-scalar-value", `struct S { xs: i32[], n: i32 } function main(): i32 { let s = S { xs: [10, 20, 30], n: 3 }; let s2 = S { xs: s.xs, n: s.n }; return s2.xs[1] + s2.n; }`, 23},
		{"field-copy-scalar-detector", `struct S { xs: i32[], n: i32 } function main(): i32 { let s = S { xs: [10, 20, 30], n: 3 }; let s2 = S { xs: s.xs, n: s.n }; let r = s2.xs[1] + s2.n; if (r != 23) { return 99; } return __rc_underflow_count(); }`, 0},
		// i64[] field-copy: the 8-byte-element array aliased through struct_get + the
		// same alias-inc. Value + detector 0 (the inc balances the two field-drops for
		// i64[] too).
		{"field-copy-i64-detector", `struct S { xs: i64[], n: i32 } function main(): i32 { let s = S { xs: [100, 200, 300], n: 3 }; let s2 = S { xs: s.xs, n: s.n }; let v: i64 = s2.xs[0]; let r = (v as i32) + s2.n; if (r != 103) { return 99; } return __rc_underflow_count(); }`, 0},
		// f64[] field-copy: same through the 8-byte float-element array.
		{"field-copy-f64-detector", `struct S { xs: f64[], n: i32 } function main(): i32 { let s = S { xs: [1.5, 2.5, 3.0], n: 2 }; let s2 = S { xs: s.xs, n: s.n }; let r = (s2.xs[0] as i32) + s2.n; if (r != 3) { return 99; } return __rc_underflow_count(); }`, 0},
		// Array-of-struct (`S { ops: s.ops }` with `ops: Op[]`) bare field-copy: the
		// same alias-inc as the scalar-element case. Value + detector 0.
		{"field-copy-struct-arr-value", `struct Point { x: i32, y: i32 } struct S { pts: Point[] } function main(): i32 { let s = S { pts: [Point { x: 3, y: 4 }, Point { x: 5, y: 6 }] }; let s2 = S { pts: s.pts }; return s2.pts[0].x + s2.pts[1].y; }`, 9},
		{"field-copy-struct-arr-detector", `struct Point { x: i32, y: i32 } struct S { pts: Point[] } function main(): i32 { let s = S { pts: [Point { x: 3, y: 4 }, Point { x: 5, y: 6 }] }; let s2 = S { pts: s.pts }; let r = s2.pts[0].x + s2.pts[1].y; if (r != 9) { return 99; } return __rc_underflow_count(); }`, 0},
		// Consumed scalar-payload enum free: a function-local `let x = V(scalar...)`
		// of an all-scalar-variant enum consumed by exactly one `match (x)` where x
		// is single-owner and DEAD after the match. The rc-headered box is freed
		// right after its match statement — sound because the match read the
		// scalar payloads as borrows before the free. Value correct, detector 0
		// (box freed exactly once).
		{"consumed-enum-free-value", `enum Shape { Circle(i32), Square(i32) } function main(): i32 { let x = Circle(7); let total = 0; match (x) { Circle(r) => { total = r + r; }, Square(w) => { total = w * w; }, } return total; }`, 14},
		{"consumed-enum-free-detector", `enum Shape { Circle(i32), Square(i32) } function main(): i32 { let x = Circle(7); let total = 0; match (x) { Circle(r) => { total = r + r; }, Square(w) => { total = w * w; }, } if (total != 14) { return 99; } return __rc_underflow_count(); }`, 0},
		// i64-payload scalar enum is also a candidate (i64 is a flat scalar) — freed
		// once, detector 0.
		{"consumed-enum-i64-detector", `enum Big { L(i64), R(i32) } function main(): i32 { let x = L(100); let total = 0; match (x) { L(v) => { total = v as i32; }, R(n) => { total = n; }, } if (total != 100) { return 99; } return __rc_underflow_count(); }`, 0},
		// NON-firing: x ESCAPES through a call (`area(x)`), so it must NOT be freed
		// after the (different-function) match. Detector 0 — no over-release.
		{"escaping-enum-not-freed-detector", `enum Shape { Circle(i32), Square(i32) } function area(s: Shape): i32 { match (s) { Circle(r) => { return r + r; }, Square(w) => { return w * w; }, } return 0; } function main(): i32 { let x = Circle(7); let a = area(x); if (a != 14) { return 99; } return __rc_underflow_count(); }`, 0},
		// NON-firing: x is RETURNED from mk (moved to caller), so mk must NOT free it.
		// Detector 0.
		{"returned-enum-not-freed-detector", `enum Shape { Circle(i32), Square(i32) } function mk(): Shape { let x = Circle(7); return x; } function main(): i32 { let s = mk(); let total = 0; match (s) { Circle(r) => { total = r; }, Square(w) => { total = w; }, } if (total != 7) { return 99; } return __rc_underflow_count(); }`, 0},
		// FIRING (#4355): a string-payload variant is rc-droppable (the per-variant
		// deep-drop releases it via __fern_str_free) — the live Num(7) box is freed
		// after its match (the Word arm of the variant_is dispatch skips at
		// runtime). Value correct, detector 0.
		{"string-payload-enum-freed-detector", `enum Tok { Word(string), Num(i32) } function main(): i32 { let x = Num(7); let total = 0; match (x) { Word(s) => { total = s.len(); }, Num(n) => { total = n; }, } if (total != 7) { return 99; } return __rc_underflow_count(); }`, 0},
		// NON-firing: x is used AFTER the match (`snd(x)`), so it is not dead — must
		// NOT be freed. Detector 0.
		{"enum-used-after-match-not-freed-detector", `enum Shape { Circle(i32), Square(i32) } function snd(s: Shape): i32 { return 1; } function main(): i32 { let x = Circle(7); let total = 0; match (x) { Circle(r) => { total = r; }, Square(w) => { total = w; }, } let g = snd(x); if (total + g != 8) { return 99; } return __rc_underflow_count(); }`, 0},
		// Enum-donor cross-reuse (FBIP): a consumed-and-dead scalar-enum box donated
		// to a LATER same-size full struct construction instead of being freed. Here
		// the variant `A(i32, i32)` box (3 words: shape + 2 payloads) is the donor and
		// `W { p, q }` (3 words: shape + 2 fields) is the recipient — same field count,
		// so W reuses x's box in place (shape rewritten to W, fields written, x's slot
		// zeroed). The post-match free of x is SUPPRESSED (the reuse consumes the box).
		// Value correct, detector 0 (box freed exactly once, through y).
		{"enum-donor-reuse-value", `enum E { A(i32, i32), B(i32, i32) } struct W { p: i32, q: i32 } function main(): i32 { let x = A(10, 20); let t = 0; match (x) { A(a, b) => { t = a + b; }, B(c, d) => { t = c - d; }, } let y = W { p: 3, q: 4 }; return t + y.p + y.q; }`, 37},
		{"enum-donor-reuse-detector", `enum E { A(i32, i32), B(i32, i32) } struct W { p: i32, q: i32 } function main(): i32 { let x = A(10, 20); let t = 0; match (x) { A(a, b) => { t = a + b; }, B(c, d) => { t = c - d; }, } let y = W { p: 3, q: 4 }; let s = t + y.p + y.q; if (s != 37) { return 99; } return __rc_underflow_count(); }`, 0},
		// Recipient with an array field: the donor's reused box becomes W; only the
		// fresh `[7, 8]` array allocates (the box itself does not). The donor's old
		// scalar slots hold no rc value to release. Array deep-dropped once at y's
		// reclamation. Value correct, detector 0.
		{"enum-donor-reuse-array-field-value", `enum E { A(i32, i32), B(i32, i32) } struct W { p: i32, xs: i32[] } function main(): i32 { let x = A(10, 20); let t = 0; match (x) { A(a, b) => { t = a + b; }, B(c, d) => { t = c - d; }, } let y = W { p: 5, xs: [7, 8] }; return t + y.p + y.xs[0] + y.xs[1]; }`, 50},
		{"enum-donor-reuse-array-field-detector", `enum E { A(i32, i32), B(i32, i32) } struct W { p: i32, xs: i32[] } function main(): i32 { let x = A(10, 20); let t = 0; match (x) { A(a, b) => { t = a + b; }, B(c, d) => { t = c - d; }, } let y = W { p: 5, xs: [7, 8] }; let s = t + y.p + y.xs[0] + y.xs[1]; if (s != 50) { return 99; } return __rc_underflow_count(); }`, 0},
		// NON-firing (different size): the donor box is 3 words (shape + 2 payloads)
		// but `W { p, q, r }` is 4 words (shape + 3 fields), so no donation — x is freed
		// after its match and W gets a fresh box. Value correct, detector 0.
		{"enum-donor-no-reuse-diff-size-detector", `enum E { A(i32, i32), B(i32, i32) } struct W { p: i32, q: i32, r: i32 } function main(): i32 { let x = A(10, 20); let t = 0; match (x) { A(a, b) => { t = a + b; }, B(c, d) => { t = c - d; }, } let y = W { p: 3, q: 4, r: 5 }; let s = t + y.p + y.q + y.r; if (s != 42) { return 99; } return __rc_underflow_count(); }`, 0},
		// NON-firing (enum used after the match): x is matched twice, so it is NOT dead
		// after the first match — the consumed-free never classifies it, hence it is not
		// a donor either. W gets a fresh box. Value correct, detector 0.
		{"enum-donor-no-reuse-used-after-detector", `enum E { A(i32, i32), B(i32, i32) } struct W { p: i32, q: i32 } function main(): i32 { let x = A(10, 20); let t = 0; match (x) { A(a, b) => { t = a + b; }, B(c, d) => { t = c - d; }, } let y = W { p: 3, q: 4 }; let u = 0; match (x) { A(a, b) => { u = a; }, B(c, d) => { u = c; }, } let s = t + y.p + y.q + u; if (s != 47) { return 99; } return __rc_underflow_count(); }`, 0},
		// NON-firing (recipient escapes): the recipient y is returned (moved to the
		// caller). Donation is still sound (the box is now owned by the caller via y;
		// x's slot is zeroed so no double-free), but more importantly the value is
		// correct and the detector reads 0.
		{"enum-donor-reuse-recipient-escapes-detector", `enum E { A(i32, i32), B(i32, i32) } struct W { p: i32, q: i32 } function mk(): W { let x = A(10, 20); let t = 0; match (x) { A(a, b) => { t = a + b; }, B(c, d) => { t = c - d; }, } let y = W { p: t, q: 4 }; return y; } function main(): i32 { let w = mk(); if (w.p + w.q != 34) { return 99; } return __rc_underflow_count(); }`, 0},
		// RC-PAYLOAD enum free (deep-drop at the free site): an enum whose variants
		// carry an RC-TRACKED payload — a scalar array (i32[]/i64[]/f64[]/boolean[])
		// or a fresh `string` (#4355) — is freed after its consuming match by a
		// per-variant variant_is dispatch that deep-drops exactly that variant's rc
		// fields BEFORE the box dec, so the payload is freed exactly once (no leak,
		// no double-free).
		//
		// FIRING: an array-payload enum (i32[]) whose match IGNORES the rc payload
		// (binds `_`), consumed-and-dead. The variant_is(V) deep-drop dec's the
		// array field, then the box. Value correct, detector 0 (array freed once).
		{"rc-enum-arr-free-value", `enum E { V(i32[]), N } function go(): i32 { let x = V([1, 2, 3]); let r = 0; match (x) { V(_) => { r = 5; }, N => { r = 2; }, } return r; } function main(): i32 { return go(); }`, 5},
		{"rc-enum-arr-free-detector", `enum E { V(i32[]), N } function go(): i32 { let x = V([1, 2, 3]); let r = 0; match (x) { V(_) => { r = 5; }, N => { r = 2; }, } if (r != 5) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// FIRING (multi-variant): both variants carry an i32[] payload. The free emits a
		// variant_is dispatch per rc-bearing variant; only the live variant's array is
		// dec'd at runtime. Match binds neither payload. Value correct, detector 0.
		{"rc-enum-multi-variant-arr-detector", `enum E { A(i32[]), B(i32[]) } function go(): i32 { let x = B([4, 5]); let r = 0; match (x) { A(_) => { r = 1; }, B(_) => { r = 2; }, } if (r != 2) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// FIRING (f64[] payload): an f64[] array payload is deep-dropped the same way
		// (flat 8-byte-element rc box). Value correct, detector 0.
		{"rc-enum-f64arr-free-detector", `enum E { V(f64[]), N } function go(): i32 { let x = V([1.5, 2.5]); let r = 0; match (x) { V(_) => { r = 7; }, N => { r = 2; }, } if (r != 7) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// FIRING (arm BINDS the rc payload, BORROW-ONLY): the `V(a)` arm NAMES the array
		// payload but uses it only as a borrow read (`a[0]`), which cannot outlive the
		// arm, so the box is deep-dropped at the free AFTER the match. Value correct,
		// detector 0 (array released once).
		{"rc-enum-arm-binds-payload-borrow-detector", `enum E { V(i32[]), N } function go(): i32 { let x = V([10, 20, 30]); let r = 0; match (x) { V(a) => { r = a[0]; }, N => { r = 0; }, } if (r != 10) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// FIRING (borrow-only over MULTIPLE reads): the bound `a` is read twice (`a[0]`,
		// `a[1]`) plus a `.len()` borrow — all reads, no escape. Admitted; value correct,
		// detector 0 (deep-dropped once at the free).
		{"rc-enum-arm-binds-payload-borrow-value", `enum E { V(i32[]), N } function go(): i32 { let x = V([10, 20, 30]); let r = 0; match (x) { V(a) => { let n: i32 = a.len(); r = a[0] + a[1] + n; }, N => { r = 0; }, } return r; } function main(): i32 { return go(); }`, 33},
		// FIRING with a MOVED payload (#4400, Koka consuming match): the `V(a)` arm
		// stores the bound array into the outer `out`, which is read AFTER the match.
		// The box is freed while the payload's dec is SKIPPED — ownership moved to
		// `out`, which reads valid bytes after the free site. Value correct,
		// detector 0.
		{"rc-enum-arm-binds-payload-escape-detector", `enum E { V(i32[]), N } function go(): i32 { let x = V([10, 20, 30]); let out: i32[] = []; match (x) { V(a) => { out = a; }, N => {}, } let r: i32 = out[0]; if (r != 10) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// NON-FIRING (rc payload used after match): x is matched twice, so it is NOT
		// dead after the first match; the box (and its array) stays live through the
		// second. Value correct, detector 0.
		{"rc-enum-used-after-match-detector", `enum E { V(i32[]), N } function go(): i32 { let x = V([3, 4]); let a = 0; match (x) { V(_) => { a = 1; }, N => { a = 0; }, } let b = 0; match (x) { V(_) => { b = 2; }, N => { b = 0; }, } if (a + b != 3) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// In-arm consuming-match box reuse (FBIP), the main "functional but in-place"
		// win: `let y = match (x) { V(a, b) => W(a+1, b+1), W(c, d) => V(c, d) }` where x
		// is a fresh, sole-owner, dead-after, non-escaping ALL-SCALAR enum box and EVERY
		// arm constructs a SAME-SIZE scalar variant. x's box is reused IN PLACE
		// (op_struct_set_shape + field writes) to build y instead of allocating a fresh
		// box per arm — one fewer __fern_arr_box per arm. y owns the box on every path,
		// freed exactly once at its reclamation (no post-match free exists for an in-arm
		// scrutinee). Read-before-overwrite is structural: each arm reads its i32 payloads
		// into temps BEFORE the box is re-shaped. The V arm fires (x is a V box → reshaped
		// to W in place). Value W(4,5) → 4+5 = 9; detector 0 (box freed once).
		{"inarm-reuse-value", `enum E { V(i32, i32), W(i32, i32) } function go(): i32 { let x = V(3, 4); let y = match (x) { V(a, b) => W(a + 1, b + 1), W(c, d) => V(c, d) }; let r = match (y) { V(a, b) => a + b, W(c, d) => c + d }; return r; } function main(): i32 { return go(); }`, 9},
		{"inarm-reuse-detector", `enum E { V(i32, i32), W(i32, i32) } function go(): i32 { let x = V(3, 4); let y = match (x) { V(a, b) => W(a + 1, b + 1), W(c, d) => V(c, d) }; let r = match (y) { V(a, b) => a + b, W(c, d) => c + d }; if (r != 9) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// FIRING + heap-reuse corruption probe: allocate FRESH arrays AFTER the reusing
		// match and read them back. If the in-place reuse corrupted the heap (e.g. wrote
		// past the reused box, or freed it early so a later alloc recycled it under y),
		// the fresh array's contents would be wrong. Both the match result (r == 9) and
		// the fresh array (11+22+33 == 66) read back correctly; detector 0.
		{"inarm-reuse-corruption-probe-detector", `enum E { V(i32, i32), W(i32, i32) } function go(): i32 { let x = V(3, 4); let y = match (x) { V(a, b) => W(a + 1, b + 1), W(c, d) => V(c, d) }; let r = match (y) { V(a, b) => a + b, W(c, d) => c + d }; let fresh = [11, 22, 33]; let s = fresh[0] + fresh[1] + fresh[2]; if (r != 9) { return 90; } if (s != 66) { return 91; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// NON-FIRING (different size): the matched variant V is 2-field but an arm
		// constructs a 1-field variant `W(a + b)`, so the box sizes differ — reuse must
		// NOT fire (x is freed/leaks via the normal path, each arm allocates a fresh
		// result box). Value W(7) → 7; detector 0.
		{"inarm-no-reuse-diff-size-detector", `enum E { V(i32, i32), W(i32) } function go(): i32 { let x = V(3, 4); let y = match (x) { V(a, b) => W(a + b), W(c) => V(c, c) }; let r = match (y) { V(a, b) => a + b, W(c) => c }; if (r != 7) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// NON-FIRING (x escapes / used after match): x is passed to a function AFTER the
		// reusing match, so it is NOT dead at the match site — reuse must NOT fire (x's
		// box stays live; each arm allocates a fresh result box). Value 9 + 1 = 10;
		// detector 0.
		{"inarm-no-reuse-escapes-detector", `enum E { V(i32, i32), W(i32, i32) } function use_x(e: E): i32 { return 1; } function go(): i32 { let x = V(3, 4); let y = match (x) { V(a, b) => W(a + 1, b + 1), W(c, d) => V(c, d) }; let g = use_x(x); let r = match (y) { V(a, b) => a + b, W(c, d) => c + d }; if (r + g != 10) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// PAYLOAD WIDTHS in FBIP in-arm reuse: i32-shaped scalars (i32 / u32 /
		// boolean) plus the 8-byte i64 and f64. Each FIRING
		// `let y = match (x) { ... }` reuses x's box in place: payloads are read at
		// their width and the constructed fields written back at width. The result
		// y is read back via a STATEMENT match. Value correct + detector 0 (box
		// freed once via y).
		// FIRING i64: V(i64,i64)→W(a+1,b+1); W(4,5) read back → 4+5 = 9.
		{"inarm-reuse-i64-value", `enum E { V(i64, i64), W(i64, i64) } function go(): i32 { let x = V(3, 4); let y = match (x) { V(a, b) => W(a + 1, b + 1), W(c, d) => V(c, d) }; let r: i64 = 0; match (y) { V(a, b) => { r = a + b; }, W(c, d) => { r = c + d; } } return r as i32; } function main(): i32 { return go(); }`, 9},
		{"inarm-reuse-i64-detector", `enum E { V(i64, i64), W(i64, i64) } function go(): i32 { let x = V(3, 4); let y = match (x) { V(a, b) => W(a + 1, b + 1), W(c, d) => V(c, d) }; let r: i64 = 0; match (y) { V(a, b) => { r = a + b; }, W(c, d) => { r = c + d; } } if (r as i32 != 9) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// FIRING i64 corruption probe: fresh array allocated AFTER the reusing match
		// reads back intact (reuse didn't clobber the heap / free early).
		{"inarm-reuse-i64-corruption-probe-detector", `enum E { V(i64, i64), W(i64, i64) } function go(): i32 { let x = V(3, 4); let y = match (x) { V(a, b) => W(a + 1, b + 1), W(c, d) => V(c, d) }; let r: i64 = 0; match (y) { V(a, b) => { r = a + b; }, W(c, d) => { r = c + d; } } let fresh = [11, 22, 33]; let s = fresh[0] + fresh[1] + fresh[2]; if (r as i32 != 9) { return 90; } if (s != 66) { return 91; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// FIRING f64: V(f64,f64)→W(a+1.0,b+1.0); W(4.0,5.0) read back → 4+5 = 9.
		{"inarm-reuse-f64-value", `enum E { V(f64, f64), W(f64, f64) } function go(): i32 { let x = V(3.0, 4.0); let y = match (x) { V(a, b) => W(a + 1.0, b + 1.0), W(c, d) => V(c, d) }; let r: f64 = 0.0; match (y) { V(a, b) => { r = a + b; }, W(c, d) => { r = c + d; } } return r as i32; } function main(): i32 { return go(); }`, 9},
		{"inarm-reuse-f64-detector", `enum E { V(f64, f64), W(f64, f64) } function go(): i32 { let x = V(3.0, 4.0); let y = match (x) { V(a, b) => W(a + 1.0, b + 1.0), W(c, d) => V(c, d) }; let r: f64 = 0.0; match (y) { V(a, b) => { r = a + b; }, W(c, d) => { r = c + d; } } if (r as i32 != 9) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// FIRING boolean: V(boolean,boolean)→W(!a,!b); V(true,false) → W(false,true);
		// read back → false?10:0 + true?1:0 = 1.
		{"inarm-reuse-boolean-value", `enum E { V(boolean, boolean), W(boolean, boolean) } function go(): i32 { let x = V(true, false); let y = match (x) { V(a, b) => W(!a, !b), W(c, d) => V(c, d) }; let r = 0; match (y) { V(a, b) => { r = 0; }, W(c, d) => { if (c) { r = r + 10; } if (d) { r = r + 1; } } } return r; } function main(): i32 { return go(); }`, 1},
		{"inarm-reuse-boolean-detector", `enum E { V(boolean, boolean), W(boolean, boolean) } function go(): i32 { let x = V(true, false); let y = match (x) { V(a, b) => W(!a, !b), W(c, d) => V(c, d) }; let r = 0; match (y) { V(a, b) => { r = 0; }, W(c, d) => { if (c) { r = r + 10; } if (d) { r = r + 1; } } } if (r != 1) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// FIRING u32: V(u32,u32)→W(a+1,b+1); W(4,5) read back → 4+5 = 9 (i32-shaped).
		{"inarm-reuse-u32-detector", `enum E { V(u32, u32), W(u32, u32) } function go(): i32 { let x = V(3, 4); let y = match (x) { V(a, b) => W(a + 1, b + 1), W(c, d) => V(c, d) }; let r = 0; match (y) { V(a, b) => { r = (a + b) as i32; }, W(c, d) => { r = (c + d) as i32; } } if (r != 9) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// ARRAY PAYLOADS in FBIP in-arm reuse (scalar-array payload fields —
		// i32[]/i64[]/f64[]/boolean[] — alongside scalars). For each RESULT array
		// field the reuse emits a COW-GUARDED write: load the donor's OLD array at
		// that slot, compare to the NEW value, `if (old != new) arr_dec(old)`,
		// store new. Two sound shapes:
		//   - MOVE: the arm moves a BOUND array payload into the same result slot
		//     (`V(a, xs) => W(a+1, xs)`). The new value IS the old array pointer (the
		//     binding temp aliases the box slot, unchanged since reshape), so old==new →
		//     NO dec → the array is reused in place (the strongest FBIP win: no arr_box
		//     for the array either).
		//   - REPLACE: the arm builds a FRESH array (`V(a, xs) => W(a, [7,8])`). old!=new
		//     → the donor's old array is dropped, the fresh one written and owned by the
		//     reused box.
		// The donor's array payloads in `let x = V(...)` must be fresh array literals
		// (sole ownership), and the per-position array-ness must match between pattern
		// and result variant; a permutation/swap or a stray borrow of the moved array
		// must not reuse. Read-before-overwrite holds: the array pointer is read into
		// a temp at arm entry before any field write.
		//
		// FIRING MOVE: V(i32,i32[])→W(a+1, xs) — xs MOVED into the result, reused in
		// place. y read back: W's array [10,20,30] sums 60, +tag 4 = 64. Detector 0.
		{"inarm-reuse-array-move-value", `enum E { V(i32, i32[]), W(i32, i32[]) } function go(): i32 { let x = V(3, [10, 20, 30]); let y = match (x) { V(a, xs) => W(a + 1, xs), W(b, ys) => V(b, ys) }; let r = 0; match (y) { V(a, xs) => { r = a + xs[0] + xs[1] + xs[2]; }, W(c, ds) => { r = c + ds[0] + ds[1] + ds[2]; } } return r; } function main(): i32 { return go(); }`, 64},
		{"inarm-reuse-array-move-detector", `enum E { V(i32, i32[]), W(i32, i32[]) } function go(): i32 { let x = V(3, [10, 20, 30]); let y = match (x) { V(a, xs) => W(a + 1, xs), W(b, ys) => V(b, ys) }; let r = 0; match (y) { V(a, xs) => { r = a + xs[0] + xs[1] + xs[2]; }, W(c, ds) => { r = c + ds[0] + ds[1] + ds[2]; } } if (r != 64) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// FIRING MOVE + heap-reuse corruption probe: a FRESH array allocated AFTER the
		// reusing match reads back intact (the move didn't free the array early or
		// clobber the heap; a stale free would let the fresh alloc recycle y's array).
		{"inarm-reuse-array-move-corruption-probe-detector", `enum E { V(i32, i32[]), W(i32, i32[]) } function go(): i32 { let x = V(3, [10, 20, 30]); let y = match (x) { V(a, xs) => W(a + 1, xs), W(b, ys) => V(b, ys) }; let r = 0; match (y) { V(a, xs) => { r = a + xs[0] + xs[1] + xs[2]; }, W(c, ds) => { r = c + ds[0] + ds[1] + ds[2]; } } let fresh = [11, 22, 33]; let s = fresh[0] + fresh[1] + fresh[2]; if (r != 64) { return 90; } if (s != 66) { return 91; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// FIRING REPLACE: the V arm builds a FRESH array result field (`W(a, [7, 8])`).
		// The donor's old array [10,20,30] is dropped (old!=new), the fresh [7,8] written
		// and owned by the reused box. y read back: tag 3 + 7 + 8 = 18. Detector 0 (old
		// array freed once, fresh array freed once — no leak, no over-release).
		{"inarm-reuse-array-replace-value", `enum E { V(i32, i32[]), W(i32, i32[]) } function go(): i32 { let x = V(3, [10, 20, 30]); let y = match (x) { V(a, xs) => W(a, [7, 8]), W(b, ys) => V(b, ys) }; let r = 0; match (y) { V(a, xs) => { r = a + xs[0] + xs[1]; }, W(c, ds) => { r = c + ds[0] + ds[1]; } } return r; } function main(): i32 { return go(); }`, 18},
		{"inarm-reuse-array-replace-detector", `enum E { V(i32, i32[]), W(i32, i32[]) } function go(): i32 { let x = V(3, [10, 20, 30]); let y = match (x) { V(a, xs) => W(a, [7, 8]), W(b, ys) => V(b, ys) }; let r = 0; match (y) { V(a, xs) => { r = a + xs[0] + xs[1]; }, W(c, ds) => { r = c + ds[0] + ds[1]; } } if (r != 18) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// FIRING REPLACE corruption probe: a fresh array after the replacing match reads
		// back intact (the dropped old array was freed exactly once; no double-free / no
		// dangling slot the fresh alloc could recycle wrongly).
		{"inarm-reuse-array-replace-corruption-probe-detector", `enum E { V(i32, i32[]), W(i32, i32[]) } function go(): i32 { let x = V(3, [10, 20, 30]); let y = match (x) { V(a, xs) => W(a, [7, 8]), W(b, ys) => V(b, ys) }; let r = 0; match (y) { V(a, xs) => { r = a + xs[0] + xs[1]; }, W(c, ds) => { r = c + ds[0] + ds[1]; } } let fresh = [11, 22, 33]; let s = fresh[0] + fresh[1] + fresh[2]; if (r != 18) { return 90; } if (s != 66) { return 91; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// FIRING MOVE with a leading i64 scalar alongside the array field: the i64 scalar
		// is written 8-byte, the array MOVED via the cow-guard. Mixed scalar+array
		// reuse. y read back: (a+1) as i32 = 6 + 10+20 = 36. Detector 0.
		{"inarm-reuse-array-mixed-i64-detector", `enum E { V(i64, i32[]), W(i64, i32[]) } function go(): i32 { let x = V(5, [10, 20]); let y = match (x) { V(a, xs) => W(a + 1, xs), W(b, ys) => V(b, ys) }; let r = 0; match (y) { V(a, xs) => { r = (a as i32) + xs[0] + xs[1]; }, W(c, ds) => { r = (c as i32) + ds[0] + ds[1]; } } if (r != 36) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// A match-EXPRESSION (`let r = match (x) { V(a) => ... }`) that BINDS a
		// non-i32 enum payload:
		//   - an i64 / f64 payload returned DIRECTLY (`V(a) => a`), also into an
		//     annotated `let r: i64 =` binding;
		//   - a scalar-element array payload (i32[]/boolean[]) bound and read as a
		//     BORROW (`V(xs) => xs[0]` / `xs.len()`), with an i32 result.
		// i64 payload returned directly into an i64 result; value (7) correct.
		{"iife-match-i64-payload-value", `enum E { V(i64), W(i64) } function main(): i32 { let x: E = E.V(7); let r: i64 = match (x) { V(a) => a, W(b) => b }; return r as i32; }`, 7},
		// f64 payload returned directly into an f64 result temp; value (3) correct.
		{"iife-match-f64-payload-value", `enum E { V(f64), W(f64) } function main(): i32 { let x: E = E.V(3.5); let r: f64 = match (x) { V(a) => a, W(b) => b }; return r as i32; }`, 3},
		// i32[] payload bound, read element [0]; result is i32 (the element).
		{"iife-match-arr-payload-elem-value", `enum E { V(i32[]), W(i32[]) } function main(): i32 { let x: E = E.V([42, 7]); let r = match (x) { V(xs) => xs[0], W(ys) => ys[0] }; return r; }`, 42},
		// i32[] payload bound, read .len() (single-expr arm); result is i32.
		{"iife-match-arr-payload-len-value", `enum E { V(i32[]), W(i32[]) } function main(): i32 { let x: E = E.V([1, 2, 3]); let r = match (x) { V(xs) => xs.len(), W(ys) => ys.len() }; return r; }`, 3},
		// i32[] payload BOUND but unused (arm returns a literal).
		{"iife-match-arr-payload-unused-value", `enum E { V(i32[]), W(i32[]) } function main(): i32 { let x: E = E.V([1, 2]); let r = match (x) { V(xs) => 5, W(ys) => 9 }; return r; }`, 5},
		// Leak-safety detector: the array payload's box is bound + read through the
		// IIFE path then dropped; the over-release detector reads 0 (freed exactly
		// once — no leak, no double-free).
		{"iife-match-arr-payload-detector", `enum E { V(i32[]), W(i32[]) } function go(): i32 { let x: E = E.V([10, 20, 30]); let r = match (x) { V(xs) => xs[0], W(ys) => ys[0] }; if (r != 10) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// boolean[] payload bound, read .len() (single-expr arm); result is i32.
		{"iife-match-boolarr-payload-len-value", `enum E { V(boolean[]), N } function main(): i32 { let x: E = E.V([true, false, true]); let r = match (x) { V(xs) => xs.len(), N => 0 }; return r; }`, 3},
		// STRUCT / STRING / TUPLE / ENUM payload BORROW reads: a
		// `let r = match (e) { V(p) => p.x, ... }` match-EXPRESSION binding a
		// STRUCT / STRING / TUPLE / nominal-ENUM payload and reading it as a borrow
		// with an i32 result (`p.x` of an i32 field, `s.len()`, `t.0` of an i32
		// element, an enum method returning i32, `s[i]` char code, or an i32
		// COMPOSITION of these).
		// Struct payload read by an i32 FIELD: result is the i32 field value.
		{"iife-match-struct-payload-field-value", `struct P { x: i32, y: i32 } enum E { V(P), N } function main(): i32 { let e: E = E.V(P { x: 5, y: 9 }); let r = match (e) { V(p) => p.x + p.y, N => 0 }; return r; }`, 14},
		// String payload read by .len(): result is the i32 length.
		{"iife-match-string-payload-len-value", `enum E { V(string), N } function main(): i32 { let e: E = E.V("hello"); let r = match (e) { V(s) => s.len(), N => 0 }; return r; }`, 5},
		// String payload indexed `s[i]`: result is the i32 char code ('H' = 72).
		{"iife-match-string-payload-index-value", `enum E { V(string), N } function main(): i32 { let e: E = E.V("Hi"); let r = match (e) { V(s) => (s[0] as i32), N => 0 }; return r; }`, 72},
		// Tuple payload read by ELEMENT `t.0` / `t.1` (i32 elements): result is i32.
		{"iife-match-tuple-payload-elem-value", `enum E { V((i32, i32)), N } function main(): i32 { let e: E = E.V((3, 4)); let r = match (e) { V(t) => t.0 + t.1, N => 0 }; return r; }`, 7},
		// Enum payload with a METHOD returning i32 (`t.sum()`): result is i32 — the
		// method's i32 return is recovered by exclusion from the wider-result registries.
		{"iife-match-enum-payload-method-value", `enum Tree { Leaf(i32), Node(Tree, Tree) } function (t: Tree) sum(): i32 { return match (t) { Leaf(v) => v, Node(l, r) => l.sum() + r.sum() }; } enum E { V(Tree), N } function main(): i32 { let tr: Tree = Tree.Leaf(7); let e: E = E.V(tr); let r = match (e) { V(t) => t.sum(), N => 0 }; return r; }`, 7},
		// Leak-safety detector: the STRING payload box is bound + borrow-read through the
		// IIFE path then leaked-with-the-enum; the over-release detector reads 0 (the
		// borrowed payload is never double-freed through the IIFE path).
		{"iife-match-struct-payload-detector", `struct P { x: i32 } enum E { V(P), N } function go(): i32 { let e: E = E.V(P { x: 42 }); let r = match (e) { V(p) => p.x, N => 0 }; if (r != 42) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// Leak-safety detector for the STRING-payload borrow (the box is reclaimed
		// exactly once — no leak, no double-free through the inlined match path).
		{"iife-match-string-payload-detector", `enum E { V(string), N } function go(): i32 { let e: E = E.V("world"); let r = match (e) { V(s) => s.len(), N => 0 }; if (r != 5) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// ARROW LAMBDA (`(params): R => expr`) — the self-host parser parses the
		// concise arrow form into the SAME ExprLambda the verbose `function (params):
		// R { return expr; }` produces. Each case is oracle-checked against the
		// native interpreter.
		// Non-capturing binding, called once: __lam_N(5) = 6.
		{"arrow-lambda-noncap-binding", `function main(): i32 { let f = (x: i32): i32 => x + 1; return f(5); }`, 6},
		// Capturing binding (captures outer `n`): param-lifted with n threaded as an
		// argument — (5)+10 = 15.
		{"arrow-lambda-capturing-binding", `function main(): i32 { let n: i32 = 10; let f = (x: i32): i32 => x + n; return f(5); }`, 15},
		// Multi-param arrow: add(3, 4) = 7.
		{"arrow-lambda-multi-param", `function main(): i32 { let add = (a: i32, b: i32): i32 => a + b; return add(3, 4); }`, 7},
		// Empty-param arrow: () => 42 → 42.
		{"arrow-lambda-empty-params", `function main(): i32 { let f = (): i32 => 42; return f(); }`, 42},
		// No-capture arrow passed as a CALL ARGUMENT — hoisted to a __lam_N function
		// value (the slice-1 const_func path): ap((y) => y*2, 5) = 10.
		{"arrow-lambda-as-arg", `function ap(f: (i32) => i32, x: i32): i32 { return f(x); } function main(): i32 { return ap((y: i32): i32 => y * 2, 5); }`, 10},
		// Verbose `function (...)` and arrow forms together — a grouping `(a + b)`
		// next to an arrow lambda confirms the lookahead doesn't mis-class an
		// ordinary parenthesised expression: (3 + 4) + ((x) => x)(1) = 8.
		{"arrow-lambda-vs-grouping", `function main(): i32 { let a: i32 = 3; let b: i32 = 4; let id = (x: i32): i32 => x; return (a + b) + id(1); }`, 8},

		// VALUE-PRODUCING `.with` (this slice). `a.with(i, v)` in expression position
		// is a FRESH clone of `a` with element i replaced — `a` is UNMUTATED. The
		// oracle reads BOTH `b[1]` (the replaced value 99) AND `a[1]` (the original 2):
		// if `.with` mutated `a` in place, `a[1]` would be 99 and the sum 198, not 101.
		// 99 + 2 = 101 proves the clone is independent. (Detector cases below confirm
		// no over-release.)
		{"with-value-expr-unmutated", `function main(): i32 { let a = [1, 2, 3]; let b = a.with(1, 99); return b[1] + a[1]; }`, 101},
		{"with-value-expr-detector", `function main(): i32 { let a = [1, 2, 3]; let b = a.with(1, 99); if (b[1] + a[1] != 101) { return 99; } return __rc_underflow_count(); }`, 0},
		// Whole clone is correct: sum of b (1+99+3) plus a unchanged (1+2+3) = 103+6.
		{"with-value-expr-full", `function main(): i32 { let a = [1, 2, 3]; let b = a.with(1, 99); let sb = b[0] + b[1] + b[2]; let sa = a[0] + a[1] + a[2]; return sb + sa; }`, 109},
		// i64[] / f64[] element clones.
		{"with-value-i64-detector", `function main(): i32 { let a: i64[] = [10, 20, 30]; let b = a.with(0, 100); if ((b[0] + a[0]) as i32 != 110) { return 99; } return __rc_underflow_count(); }`, 0},
		{"with-value-f64-detector", `function main(): i32 { let a: f64[] = [1.5, 2.5, 3.0]; let b = a.with(2, 9.0); if (((b[2] + a[2]) as i32) != 12) { return 99; } return __rc_underflow_count(); }`, 0},

		// VALUE-PRODUCING `.append` in expression position: clones the receiver then
		// grows the clone — the receiver is UNMUTATED. b has 4 elements (1,2,3,4),
		// a still has 3 (1,2,3): b[3] + a.len() = 4 + 3 = 7.
		{"append-value-field-detector", `struct Buf { xs: i32[], n: i32 } function main(): i32 { let s = Buf { xs: [1, 2, 3], n: 3 }; let t = Buf { xs: s.xs.append(4), n: s.n + 1 }; let r = t.xs[3] + s.xs.len() + t.n; if (r != 11) { return 99; } return __rc_underflow_count(); }`, 0},

		// STRUCT-FIELD `.with` — the main BAIL→ir flip: the pervasive immutable-
		// update idiom `State { xs: s.xs.with(i, v), n: s.n }`. The base `s.xs` field
		// is cloned (borrowed for the copy, not aliased), so the new struct owns a
		// fresh array with NO alias-inc. `t.xs[1]` reads 99, `s.xs[1]` still reads 2
		// (the original struct's array is unmutated): 99 + 2 = 101.
		{"struct-field-with-value", `struct State { xs: i32[], n: i32 } function main(): i32 { let s = State { xs: [1, 2, 3], n: 3 }; let t = State { xs: s.xs.with(1, 99), n: s.n }; return t.xs[1] + s.xs[1]; }`, 101},
		{"struct-field-with-detector", `struct State { xs: i32[], n: i32 } function main(): i32 { let s = State { xs: [1, 2, 3], n: 3 }; let t = State { xs: s.xs.with(1, 99), n: s.n }; if (t.xs[1] + s.xs[1] != 101) { return 99; } return __rc_underflow_count(); }`, 0},
		// Read back the whole updated struct: t.xs = [1,99,3] (sum 103) + t.n 3 = 106;
		// s unchanged: s.xs sum 6 + s.n 3 = 9. Total 115.
		{"struct-field-with-full", `struct State { xs: i32[], n: i32 } function main(): i32 { let s = State { xs: [1, 2, 3], n: 3 }; let t = State { xs: s.xs.with(1, 99), n: s.n }; let st = t.xs[0] + t.xs[1] + t.xs[2] + t.n; let ss = s.xs[0] + s.xs[1] + s.xs[2] + s.n; return st + ss; }`, 115},
		// STRUCT-FIELD `.append`: `State { xs: s.xs.append(v), n: s.n + 1 }`. t.xs has
		// 4 elements; s.xs still 3. t.xs sum (1+2+3+4)=10 + t.n 4 = 14; s.xs sum 6 +
		// s.n 3 = 9 → 23.
		{"struct-field-append-value", `struct State { xs: i32[], n: i32 } function main(): i32 { let s = State { xs: [1, 2, 3], n: 3 }; let t = State { xs: s.xs.append(4), n: s.n + 1 }; let st = t.xs[0] + t.xs[1] + t.xs[2] + t.xs[3] + t.n; let ss = s.xs[0] + s.xs[1] + s.xs[2] + s.n; return st + ss; }`, 23},
		{"struct-field-append-detector", `struct State { xs: i32[], n: i32 } function main(): i32 { let s = State { xs: [1, 2, 3], n: 3 }; let t = State { xs: s.xs.append(4), n: s.n + 1 }; if (t.xs[3] != 4) { return 98; } if (s.xs.len() != 3) { return 97; } return __rc_underflow_count(); }`, 0},
		// i64[]-field and f64[]-field struct updates.
		{"struct-field-with-i64-detector", `struct State { xs: i64[], n: i32 } function main(): i32 { let s = State { xs: [10, 20, 30], n: 3 }; let t = State { xs: s.xs.with(0, 100), n: s.n }; if (((t.xs[0] + s.xs[0]) as i32) != 110) { return 99; } return __rc_underflow_count(); }`, 0},
		{"struct-field-with-f64-detector", `struct State { xs: f64[], n: i32 } function main(): i32 { let s = State { xs: [1.5, 2.5, 3.0], n: 3 }; let t = State { xs: s.xs.with(2, 9.0), n: s.n }; if ((((t.xs[2] + s.xs[2]) as i32)) != 12) { return 99; } return __rc_underflow_count(); }`, 0},
		// Chained immutable updates: t = update(s), u = update(t). Each clones; all
		// three structs independent. u.xs = [1,99,3] then [1,99,42]; s/t unmutated.
		{"struct-field-with-chain-detector", `struct State { xs: i32[], n: i32 } function main(): i32 { let s = State { xs: [1, 2, 3], n: 3 }; let t = State { xs: s.xs.with(1, 99), n: s.n }; let u = State { xs: t.xs.with(2, 42), n: t.n }; if (u.xs[1] != 99) { return 98; } if (u.xs[2] != 42) { return 97; } if (s.xs[1] != 2) { return 96; } if (t.xs[2] != 3) { return 95; } return __rc_underflow_count(); }`, 0},

		// STRING-RESULT match-EXPRESSION: a `let s = match (e) { V(x) => x, ... }`
		// that RETURNS a string derived from the bound payload — a BARE string
		// payload (`V(x) => x`), a string struct/enum FIELD (`V(p) => p.name`), or a
		// string tuple ELEMENT (`V(t) => t.0`). Each case oracle-checks the result
		// string's length as the exit code.
		// Bare string payload returned: result is the 5-char payload "hello".
		{"iife-match-string-bare-payload-value", `enum E { V(string), N } function main(): i32 { let e: E = E.V("hello"); let s: string = match (e) { V(x) => x, N => "z" }; return s.len(); }`, 5},
		// The non-payload arm (literal "zz") is taken: result is the 2-char "zz".
		{"iife-match-string-bare-payload-other-arm", `enum E { V(string), N } function main(): i32 { let e: E = E.N; let s: string = match (e) { V(x) => x, N => "zz" }; return s.len(); }`, 2},
		// Struct string FIELD read: result is the 5-char field value "world".
		{"iife-match-string-struct-field-value", `struct P { name: string } enum E { V(P), N } function main(): i32 { let e: E = E.V(P { name: "world" }); let s: string = match (e) { V(p) => p.name, N => "zz" }; return s.len(); }`, 5},
		// Tuple string ELEMENT read (`t.0`): result is the 3-char element "hey".
		{"iife-match-string-tuple-elem-value", `enum E { V((string, i32)), N } function main(): i32 { let e: E = E.V(("hey", 3)); let s: string = match (e) { V(t) => t.0, N => "z" }; return s.len(); }`, 3},
		// Leak-safety detector: the string-payload box is bound + returned through the
		// IIFE path; the over-release detector reads 0 (the result string is reclaimed
		// exactly once — no leak, no double-free through the inlined match path).
		{"iife-match-string-bare-payload-detector", `enum E { V(string), N } function go(): i32 { let e: E = E.V("hello"); let s: string = match (e) { V(x) => x, N => "z" }; if (s.len() != 5) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// Struct-string-field result detector: the borrowed string field is returned
		// and reclaimed exactly once (no over-release through the IIFE path).
		{"iife-match-string-struct-field-detector", `struct P { name: string } enum E { V(P), N } function go(): i32 { let e: E = E.V(P { name: "world" }); let s: string = match (e) { V(p) => p.name, N => "zz" }; if (s.len() != 5) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// String-payload CONCAT result (`V(x) => x + "!"`): the bound string payload is
		// read BORROW-ONLY inside a `+` that allocates a fresh string.
		// Result "hi!" has length 3.
		{"iife-match-string-concat-suffix-value", `enum E { V(string), N } function main(): i32 { let e: E = E.V("hi"); let s: string = match (e) { V(x) => x + "!", N => "z" }; return s.len(); }`, 3},
		// Prefix concat (`"k=" + x`): the literal is the LEFT operand; the payload the
		// right. Result "k=hi" has length 4.
		{"iife-match-string-concat-prefix-value", `enum E { V(string), N } function main(): i32 { let e: E = E.V("hi"); let s: string = match (e) { V(x) => "k=" + x, N => "z" }; return s.len(); }`, 4},
		// Prefix + suffix (`">" + x + "<"`): a nested `+` tree, every payload-mentioning
		// leaf a borrow-only read. Result ">hi<" has length 4.
		{"iife-match-string-concat-wrap-value", `enum E { V(string), N } function main(): i32 { let e: E = E.V("hi"); let s: string = match (e) { V(x) => ">" + x + "<", N => "z" }; return s.len(); }`, 4},
		// Struct string-FIELD concat (`V(p) => p.name + "!"`): the borrow leaf is a
		// string field read of the struct payload. Result "world!" length 6.
		{"iife-match-string-concat-struct-field-value", `struct P { name: string } enum E { V(P), N } function main(): i32 { let e: E = E.V(P { name: "world" }); let s: string = match (e) { V(p) => p.name + "!", N => "zz" }; return s.len(); }`, 6},
		// Tuple string-ELEMENT concat (`V(t) => t.0 + "!"`): the borrow leaf is a
		// string tuple-element read. Result "hey!" length 4.
		{"iife-match-string-concat-tuple-elem-value", `enum E { V((string, i32)), N } function main(): i32 { let e: E = E.V(("hey", 3)); let s: string = match (e) { V(t) => t.0 + "!", N => "z" }; return s.len(); }`, 4},
		// The non-payload arm of a concat match is taken: the result is the N-arm
		// literal "none" (length 4), unaffected by the V-arm concat shape.
		{"iife-match-string-concat-other-arm", `enum E { V(string), N } function main(): i32 { let e: E = E.N; let s: string = match (e) { V(x) => x + "!", N => "none" }; return s.len(); }`, 4},
		// Over-release detector for the concat shape: the borrowed payload is READ (a
		// borrow) and the concat allocates a FRESH string bound to s — reclaimed exactly
		// once. The detector reads 0 (no leak, no double-free of the payload or result).
		{"iife-match-string-concat-detector", `enum E { V(string), N } function go(): i32 { let e: E = E.V("hi"); let s: string = match (e) { V(x) => x + "!", N => "z" }; if (s.len() != 3) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// Struct-field concat detector: the borrowed string field feeds a fresh concat;
		// the field box and the result are each accounted exactly once (detector 0).
		{"iife-match-string-concat-struct-field-detector", `struct P { name: string } enum E { V(P), N } function go(): i32 { let e: E = E.V(P { name: "world" }); let s: string = match (e) { V(p) => p.name + "!", N => "zz" }; if (s.len() != 6) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// COMPOSITE match-EXPRESSION result: a match-expression whose result is a
		// struct / nominal-enum / tuple value returned WHOLE — a bare bound payload
		// `V(q) => q` and/or a fresh same-type construction in another arm — so a
		// later `p.field` / `match (p)` / `p.0` resolves on the bound local.
		//
		// BARE STRUCT payload returned whole, used via `p.field`.
		{"iife-match-struct-payload-field-value", `struct P { x: i32 } enum E { V(P), N } function main(): i32 { let e: E = E.V(P { x: 7 }); let p: P = match (e) { V(q) => q, N => P { x: 0 } }; return p.x; }`, 7},
		// The OTHER (constructor) arm is taken: the N-arm builds a fresh P, same type.
		{"iife-match-struct-payload-other-arm", `struct P { x: i32 } enum E { V(P), N } function main(): i32 { let e: E = E.N; let p: P = match (e) { V(q) => q, N => P { x: 42 } }; return p.x; }`, 42},
		// UNANNOTATED struct binding, payload arm FIRST.
		{"iife-match-struct-payload-unannotated-value", `struct P { x: i32 } enum E { V(P), N } function main(): i32 { let e: E = E.V(P { x: 5 }); let p = match (e) { V(q) => q, N => P { x: 0 } }; return p.x; }`, 5},
		// Over-release detector: the struct payload is BORROWED from the enum box;
		// neither it nor the constructed N-arm struct over-releases. Detector
		// reads 0.
		{"iife-match-struct-payload-detector", `struct P { x: i32 } enum E { V(P), N } function go(): i32 { let e: E = E.V(P { x: 7 }); let p: P = match (e) { V(q) => q, N => P { x: 0 } }; if (p.x != 7) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// BARE ENUM payload returned whole, used via a nested `match (p)`. The inner
		// enum pointer occupies one slot; the temp + `p` are marked enum Inner.
		{"iife-match-enum-payload-match-value", `enum Inner { A(i32), B } enum Outer { W(Inner), Z } function main(): i32 { let o: Outer = Outer.W(Inner.A(5)); let p: Inner = match (o) { W(q) => q, Z => Inner.B }; return match (p) { A(n) => n, B => 99 }; }`, 5},
		// The Z-arm constructs a fresh Inner.B of the same enum type; nested match on it.
		{"iife-match-enum-payload-other-arm", `enum Inner { A(i32), B } enum Outer { W(Inner), Z } function main(): i32 { let o: Outer = Outer.Z; let p: Inner = match (o) { W(q) => q, Z => Inner.B }; return match (p) { A(n) => n, B => 8 }; }`, 8},
		// Enum-payload over-release detector: neither the borrowed payload nor the
		// constructed arm over-releases. Detector reads 0.
		{"iife-match-enum-payload-detector", `enum Inner { A(i32), B } enum Outer { W(Inner), Z } function go(): i32 { let o: Outer = Outer.W(Inner.A(5)); let p: Inner = match (o) { W(q) => q, Z => Inner.B }; let r = match (p) { A(n) => n, B => 99 }; if (r != 5) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// BARE TUPLE payload returned whole, used via `p.0` / `p.1`. The tuple pointer
		// occupies one slot; the temp + `p` are marked with the element tags.
		{"iife-match-tuple-payload-elem-value", `enum E { V((i32, i32)), N } function main(): i32 { let e: E = E.V((3, 4)); let p: (i32, i32) = match (e) { V(q) => q, N => (0, 0) }; return p.0 + p.1; }`, 7},
		// UNANNOTATED tuple binding, payload arm first.
		{"iife-match-tuple-payload-unannotated-value", `enum E { V((i32, i32)), N } function main(): i32 { let e: E = E.V((10, 20)); let p = match (e) { V(q) => q, N => (0, 0) }; return p.0 + p.1; }`, 30},
		// Tuple-payload over-release detector: neither arm over-releases. Detector
		// reads 0.
		{"iife-match-tuple-payload-detector", `enum E { V((i32, i32)), N } function go(): i32 { let e: E = E.V((3, 4)); let p: (i32, i32) = match (e) { V(q) => q, N => (0, 0) }; if (p.0 + p.1 != 7) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// Reclamation soundness for a struct carrying rc-tracked fields (string /
		// string[] / nested-struct / struct-array): it constructs, field-accesses
		// and reclaims with its rc-headered box freed exactly once. These detector
		// cases pin that nothing over-releases (`__rc_underflow_count() == 0`).
		{"nonleaksafe-string-field-reclaim-detector", `struct P { name: string, n: i32 } function go(): i32 { let p = P { name: "hello", n: 7 }; return p.n; } function main(): i32 { let r = go(); if (r != 7) { return 99; } return __rc_underflow_count(); }`, 0},
		{"nonleaksafe-strarr-field-reclaim-detector", `struct Doc { lines: string[], n: i32 } function go(): i32 { let d = Doc { lines: ["a", "b", "c"], n: 3 }; return d.n; } function main(): i32 { let r = go(); if (r != 3) { return 99; } return __rc_underflow_count(); }`, 0},
		// Nested-struct field (the doc's `Outer { inner: Inner { xs: [..] }, n }` shape):
		// `o.inner.xs[i]` reads through two box levels; both boxes reclaim soundly.
		{"nonleaksafe-nested-struct-reclaim-detector", `struct Inner { xs: i32[] } struct Outer { inner: Inner, n: i32 } function go(): i32 { let o = Outer { inner: Inner { xs: [10, 20, 30] }, n: 5 }; return o.inner.xs[1] + o.n; } function main(): i32 { let r = go(); if (r != 25) { return 99; } return __rc_underflow_count(); }`, 0},
		// Struct-ARRAY field (the doc's `o.items[i].v` shape): the array slab + element
		// struct boxes reclaim soundly (slab freed once).
		{"nonleaksafe-struct-array-reclaim-detector", `struct Item { v: i32 } struct Bag { items: Item[], n: i32 } function go(): i32 { let b = Bag { items: [Item { v: 10 }, Item { v: 20 }], n: 2 }; return b.items[0].v + b.items[1].v + b.n; } function main(): i32 { let r = go(); if (r != 32) { return 99; } return __rc_underflow_count(); }`, 0},
		// REASSIGN frees the old rc-headered box: rebinding `p` to a new struct releases
		// the previous box (a string-field struct) — exactly once, detector 0.
		{"nonleaksafe-reassign-frees-old-detector", `struct P { name: string } function main(): i32 { let p = P { name: "first" }; p = P { name: "second" }; if (p.name.len() != 6) { return 99; } return __rc_underflow_count(); }`, 0},
		// STRING-FIELD DEEP-DROP. A reclaimable struct's `string` field is dec'd on
		// reclamation. The field values here are a fresh inline concat `a + b`, the
		// case/shape transforms `.to_ascii_upper()` / `.to_ascii_lower()` /
		// `.repeat(n)` (+ their str_* free-fn spellings), a string LITERAL (.rodata
		// static), an aliased local / param / field read, and the aliasing
		// transforms `.trim()` (a zero-copy view) / `.replace()` (receiver-identity
		// on empty/no-match). Detector 0 across every shape proves no double-free.
		//
		// Fresh LITERAL field: static .rodata, so the drop must not free it.
		{"strdrop-literal-field-detector", `struct P { name: string, n: i32 } function go(): i32 { let p = P { name: "hello", n: 4 }; return p.n; } function main(): i32 { let r = go(); if (r != 4) { return 99; } return __rc_underflow_count(); }`, 0},
		// ALIASED string local into a field: inc'd at construction → field-drop decs
		// the dup, the local's reference survives (leaks, sound). No over-release.
		{"strdrop-aliased-local-detector", `struct P { name: string } function go(): i32 { let s = "shared"; let p = P { name: s }; let r = s.len() + p.name.len(); return r; } function main(): i32 { let r = go(); if (r != 12) { return 99; } return __rc_underflow_count(); }`, 0},
		// SAME string local aliased into TWO structs. Only the LAST use may be
		// moved, so the first literal incs and the second takes the local's own
		// reference over: rc reaches 2, the two field-drops bring it to 0, and the
		// local's exit dec is elided. Balanced — no leak, no over-release.
		//
		// This is the case that made the first #6545 attempt unsound. Admitting a
		// string field to the construction-move analysis while the emitter still
		// answered "is this local moved?" BY NAME elided the retain at BOTH
		// literals, so one buffer was released twice.
		{"strdrop-two-alias-detector", `struct P { name: string } function go(): i32 { let s = "x"; let a = P { name: s }; let b = P { name: s }; let r = a.name.len() + b.name.len(); return r; } function main(): i32 { let r = go(); if (r != 2) { return 99; } return __rc_underflow_count(); }`, 0},
		// The ARRAY sibling of the case above, and the one that shows the
		// name-keyed elision was never string-specific: array fields have been in
		// the construction-move set all along, so this over-released on every
		// build before the retain was keyed on the move SITE (#6545).
		{"arrdrop-two-alias-detector", `struct Q { v: i32[] } function go(): i32 { let s: i32[] = [1, 2]; let a: Q = Q { v: s }; let b: Q = Q { v: s }; let r: i32 = a.v.len() + b.v.len(); return r; } function main(): i32 { let r: i32 = go(); if (r != 4) { return 99; } return __rc_underflow_count(); }`, 0},
		// A field read OUT of a reclaimable struct, then RETURNED (escapes): the
		// returned string must survive the field-drop (a use-after-free /
		// over-release shows here).
		{"strdrop-return-field-detector", `struct P { name: string } function mk(): string { let p = P { name: "hi" }; return p.name; } function main(): i32 { let s = mk(); if (s.len() != 2) { return 99; } return __rc_underflow_count(); }`, 0},
		// A CONCAT field value (a fresh heap string, op_str_concat) is FREED: owned
		// by the struct with no inc, reclaimed exactly once by the field-drop. The
		// struct reads back correctly and the detector stays 0 (no over-release of
		// the fresh box).
		{"strdrop-concat-field-detector", `struct P { name: string } function go(): i32 { let a = "ab"; let p = P { name: a + "c" }; return p.name.len(); } function main(): i32 { let r = go(); if (r != 3) { return 99; } return __rc_underflow_count(); }`, 0},
		// FRESH-STRING TRANSFORMS (this slice) — each allocates a fresh, exclusively-
		// owned data buffer + box, so the field-drop FREES it with no construction
		// inc (the leak shrinks further). Value reads back correctly; detector 0
		// proves the fresh box is reclaimed exactly once.
		//
		// `.to_ascii_upper()` field value: fresh cased buffer, freed once.
		{"strdrop-toupper-field-value", `import "std/string";
struct P { name: string } function go(): i32 { let s = "ab"; let p = P { name: s.to_ascii_upper() }; return p.name.len(); } function main(): i32 { let r = go(); if (r != 2) { return 99; } return __rc_underflow_count(); }`, 0},
		// `.to_ascii_lower()` field value: fresh cased buffer, freed once.
		{"strdrop-tolower-field-detector", `import "std/string";
struct P { name: string } function go(): i32 { let s = "AB"; let p = P { name: s.to_ascii_lower() }; return p.name.len(); } function main(): i32 { let r = go(); if (r != 2) { return 99; } return __rc_underflow_count(); }`, 0},
		// `.repeat(n)` field value: a fresh n-copy buffer, freed once.
		{"strdrop-repeat-field-detector", `import "std/string";
struct P { name: string } function go(): i32 { let s = "ab"; let p = P { name: s.repeat(3) }; return p.name.len(); } function main(): i32 { let r = go(); if (r != 6) { return 99; } return __rc_underflow_count(); }`, 0},
		// FRESH ALLOC BUILTINS (this slice) — fixed fresh-allocating semantics, no user
		// override, so the field-drop FREES the result with no construction inc.
		//
		// `string_from_bytes_unchecked(arr)`: packs bytes into a fresh box, freed once.
		{"strdrop-string-from-bytes-field-detector", `struct P { name: string } function go(): i32 { let b: u8[] = [104, 105]; let p = P { name: string_from_bytes_unchecked(b) }; return p.name.len(); } function main(): i32 { let r = go(); if (r != 2) { return 99; } return __rc_underflow_count(); }`, 0},
		// `i32_to_string(n)` is deliberately EXCLUDED (stays inc'd → leaks): its box's
		// `data` points into the MIDDLE of a 32-byte scratch buffer, not at an alloc
		// boundary, so reclaiming it over-releases. This case proves the EXCLUSION is
		// sound — inc'd, detector 0 (no over-release despite the un-freeable box).
		// EXCLUDED (aliasing) transforms stay inc'd → field-drop decs the dup only.
		//
		// `.trim()` is a zero-copy VIEW into the receiver's buffer: inc'd (NOT freed),
		// so freeing the view never reaches the parent's data. Leaks, detector 0.
		{"strdrop-trim-field-excluded-detector", `import "std/string";
struct P { name: string } function go(): i32 { let s = "  ab  "; let p = P { name: s.trim() }; return p.name.len(); } function main(): i32 { let r = go(); if (r != 2) { return 99; } return __rc_underflow_count(); }`, 0},
		// `.replace(a, b)` returns the receiver box unchanged on no-match: inc'd (NOT
		// freed) so an alias of the receiver is never over-released. Detector 0.
		{"strdrop-replace-field-excluded-detector", `import "std/string";
struct P { name: string } function go(): i32 { let s = "abXab"; let p = P { name: s.replace("X", "Y") }; return p.name.len(); } function main(): i32 { let r = go(); if (r != 5) { return 99; } return __rc_underflow_count(); }`, 0},
		// i64-RESULT match-EXPRESSION fed by a struct-FIELD read of the bound payload
		// (`V(p) => p.v`, v:i64): the result is the full i64 field. Value-checked
		// via `as i32`.
		{"iife-match-i64-struct-field-value", `struct P { v: i64 } enum E { V(P), N } function main(): i32 { let e: E = E.V(P { v: 42 }); let r: i64 = match (e) { V(p) => p.v, N => 0 }; return r as i32; }`, 42},
		// The N (no-payload) arm taken: the i64 constant arm stores via the i64 path too.
		{"iife-match-i64-struct-field-other-arm", `struct P { v: i64 } enum E { V(P), N } function main(): i32 { let e: E = E.N; let r: i64 = match (e) { V(p) => p.v, N => 7 }; return r as i32; }`, 7},
		// Over-release detector: the struct payload is borrowed; neither arm
		// over-releases. Detector reads 0.
		{"iife-match-i64-struct-field-detector", `struct P { v: i64 } enum E { V(P), N } function go(): i32 { let e: E = E.V(P { v: 42 }); let r: i64 = match (e) { V(p) => p.v, N => 0 }; if (r as i32 != 42) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// f64-RESULT match-EXPRESSION fed by a struct-FIELD read (`V(p) => p.v`, v:f64).
		// The temp is marked f64; the field read is struct_get width-64 stored into the
		// f64 slot. `as i32` truncates 42.5 toward zero → 42.
		{"iife-match-f64-struct-field-value", `struct P { v: f64 } enum E { V(P), N } function main(): i32 { let e: E = E.V(P { v: 42.5 }); let r: f64 = match (e) { V(p) => p.v, N => 0.0 }; return r as i32; }`, 42},
		{"iife-match-f64-struct-field-detector", `struct P { v: f64 } enum E { V(P), N } function go(): i32 { let e: E = E.V(P { v: 42.5 }); let r: f64 = match (e) { V(p) => p.v, N => 0.0 }; if (r as i32 != 42) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// MAP local reclaim: a fresh `let m = Map{..}` used only via read methods
		// (get_or), non-escaping + non-iterated, has its keys + values buffers freed
		// (rc-guarded __fern_rc_dec) at scope exit — the raw 16-byte mapbox leaks. The
		// buffers are sole-owned (rc==1), so the detector stays 0.
		{"map-reclaim-value", `import "core/map";
function go(): i32 { let m = Map { 1: 10, 2: 20 }; return m.get_or(1, 0) + m.get_or(2, 0); } function main(): i32 { return go(); }`, 30},
		{"map-reclaim-detector", `import "core/map";
function go(): i32 { let m = Map { 1: 10, 2: 20 }; let r = m.get_or(1, 0) + m.get_or(2, 0); if (r != 30) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// Heap-reuse corruption probe: fresh arrays allocated AFTER the map's buffer
		// reclaim read back intact (the buffers were freed soundly, exactly once — no
		// still-live buffer freed early that the fresh alloc could recycle).
		{"map-reclaim-corruption-probe-detector", `import "core/map";
function go(): i32 { let m = Map { 1: 10, 2: 20 }; let r = m.get_or(1, 0) + m.get_or(2, 0); let fresh = [11, 22, 33]; let s = fresh[0] + fresh[1] + fresh[2]; if (r != 30) { return 90; } if (s != 66) { return 91; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// NON-firing (escapes / passed to a fn): a map passed as an arg must keep its
		// buffers until the callee has read them. Detector 0.
		{"map-passed-not-freed-detector", `import "core/map";
function sum(m: Map[i32, i32]): i32 { return m.get_or(1, 0) + m.get_or(2, 0); } function go(): i32 { let m = Map { 1: 10, 2: 20 }; let r = sum(m); if (r != 30) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// FIRING (for..in): a map iterated by `for (k, v) in m` is RECLAIMED after
		// the loop — its iter is a scoped loop temp (dead by the free) and scalar
		// k/v are copies. Value correct + detector 0 (freed once).
		{"map-foreach-reclaimed-detector", `import "core/map";
function go(): i32 { let m = Map { 1: 10, 2: 20 }; let t = 0; for (k, v) in m { t = t + v; } if (t != 30) { return 99; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// FIRING + corruption probe: a fresh array after a `for..in m` loop's reclaim
		// reads back intact (the buffers were freed soundly after the loop, once).
		{"map-foreach-corruption-probe-detector", `import "core/map";
function go(): i32 { let m = Map { 1: 10, 2: 20 }; let t = 0; for (k, v) in m { t = t + v; } let fresh = [11, 22, 33]; let s = fresh[0] + fresh[1] + fresh[2]; if (t != 30) { return 90; } if (s != 66) { return 91; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// MAP precise-drop: a fresh map last-used in a NESTED block has its buffers
		// freed right after that statement, bounding peak heap, and freed only once.
		// f(5): m built, used in the if (c=35), freed after the if; return 35+5=40.
		{"map-precise-if-value", `import "core/map";
function f(n: i32): i32 { let m = Map { 1: 10, 2: 25 }; let c = 0; if (n > 0) { c = m.get_or(1, 0) + m.get_or(2, 0); } return c + n; } function main(): i32 { return f(5); }`, 40},
		{"map-precise-if-detector", `import "core/map";
function f(n: i32): i32 { let m = Map { 1: 10, 2: 25 }; let c = 0; if (n > 0) { c = m.get_or(1, 0) + m.get_or(2, 0); } return c + n; } function main(): i32 { let r = f(5); if (r != 40) { return 99; } return __rc_underflow_count(); }`, 0},
		// CONDITIONALLY-DECLARED map: `m` is declared inside an if-branch and
		// dropped on the taken path only. Detector 0 proves neither a double-free
		// nor a null-deref.
		{"map-conditional-decl-detector", `import "core/map";
function f(n: i32): i32 { let t = 0; if (n > 0) { let m = Map { 1: 10, 2: 20 }; t = m.get_or(1, 0) + m.get_or(2, 0); } if (t != 30) { return 99; } return __rc_underflow_count(); } function main(): i32 { return f(5); }`, 0},
		// PRECISE drop + heap-reuse corruption probe: fresh arrays allocated AFTER the
		// map's precise buffer-free read back intact (the buffers were freed soundly,
		// exactly once — no still-live buffer freed early that the alloc could recycle).
		{"map-precise-corruption-probe-detector", `import "core/map";
function go(): i32 { let m = Map { 1: 10, 2: 20 }; let c = 0; let i = 0; while (i < 1) { c = m.get_or(1, 0) + m.get_or(2, 0); i = i + 1; } let fresh = [11, 22, 33]; let s = fresh[0] + fresh[1] + fresh[2]; if (c != 30) { return 90; } if (s != 66) { return 91; } return __rc_underflow_count(); } function main(): i32 { return go(); }`, 0},
		// MAP-typed struct fields in box reuse (#4356 slice 8): the map read
		// through the reused box (a carried copy, a fresh override, and the
		// cross-family recipient) is intact, the detector stays clean (nothing
		// over-released), and a fresh array allocated next to the reuse reads back
		// unpoisoned.
		{"map-field-reuse-carried-value", `import "core/map";
struct P { id: i32, m: Map[i32, i32] } function main(): i32 { let d = P { id: 1, m: Map { 1: 10 } }; let c = P { ...d, id: 2 }; return c.m.get_or(1, 0) + c.id; }`, 12},
		{"map-field-reuse-carried-detector", `import "core/map";
struct P { id: i32, m: Map[i32, i32] } function main(): i32 { let d = P { id: 1, m: Map { 1: 10 } }; let c = P { ...d, id: 2 }; let s: i32 = c.m.get_or(1, 0) + c.id; if (s != 12) { return 99; } return __rc_underflow_count(); }`, 0},
		{"map-field-reuse-override-value", `import "core/map";
struct P { id: i32, m: Map[i32, i32] } function main(): i32 { let d = P { id: 1, m: Map { 1: 10 } }; let c = P { ...d, m: Map { 1: 39 } }; return c.m.get_or(1, 0) + c.id; }`, 40},
		{"map-field-reuse-override-detector", `import "core/map";
struct P { id: i32, m: Map[i32, i32] } function main(): i32 { let d = P { id: 1, m: Map { 1: 10 } }; let c = P { ...d, m: Map { 1: 39 } }; let s: i32 = c.m.get_or(1, 0) + c.id; if (s != 40) { return 99; } return __rc_underflow_count(); }`, 0},
		{"map-field-reuse-cross-value", `import "core/map";
struct P { id: i32, m: Map[i32, i32] } function main(): i32 { let d = P { id: 1, m: Map { 1: 10 } }; let u: i32 = d.m.get_or(1, 0) + d.id; let c = P { id: 2, m: Map { 1: 7 } }; return c.m.get_or(1, 0) + c.id + u; }`, 20},
		{"map-field-reuse-cross-detector", `import "core/map";
struct P { id: i32, m: Map[i32, i32] } function main(): i32 { let d = P { id: 1, m: Map { 1: 10 } }; let u: i32 = d.m.get_or(1, 0) + d.id; let c = P { id: 2, m: Map { 1: 7 } }; let s: i32 = c.m.get_or(1, 0) + c.id + u; if (s != 20) { return 99; } return __rc_underflow_count(); }`, 0},
		{"map-field-reuse-corruption-probe-detector", `import "core/map";
struct P { id: i32, m: Map[i32, i32] } function main(): i32 { let d = P { id: 1, m: Map { 1: 10 } }; let c = P { ...d, m: Map { 1: 39 } }; let fresh = [11, 22, 33]; let s: i32 = fresh[0] + fresh[1] + fresh[2]; if (c.m.get_or(1, 0) + c.id != 40) { return 90; } if (s != 66) { return 91; } return __rc_underflow_count(); }`, 0},
		// TUPLE / OPTION struct fields in box reuse (#4356 slice 9): intact reads
		// through the reused box (carried, override, cross) with clean detectors
		// and an unpoisoned fresh array.
		{"tuple-field-reuse-carried-value", `struct P { id: i32, pr: (i32, i32) } function main(): i32 { let d = P { id: 1, pr: (10, 20) }; let c = P { ...d, id: 2 }; return c.pr.0 + c.pr.1 + c.id; }`, 32},
		{"tuple-field-reuse-override-detector", `struct P { id: i32, pr: (i32, i32) } function main(): i32 { let d = P { id: 1, pr: (10, 20) }; let c = P { ...d, pr: (7, 8) }; let s: i32 = c.pr.0 + c.pr.1 + c.id; if (s != 16) { return 99; } return __rc_underflow_count(); }`, 0},
		{"tuple-field-reuse-cross-detector", `struct P { id: i32, pr: (i32, i32) } function main(): i32 { let d = P { id: 1, pr: (10, 20) }; let u: i32 = d.pr.0 + d.id; let c = P { id: 2, pr: (7, 8) }; let s: i32 = c.pr.0 + c.pr.1 + c.id + u; if (s != 28) { return 99; } return __rc_underflow_count(); }`, 0},
		{"tuple-field-reuse-corruption-probe-detector", `struct P { id: i32, pr: (i32, i32) } function main(): i32 { let d = P { id: 1, pr: (10, 20) }; let c = P { ...d, pr: (7, 8) }; let fresh = [11, 22, 33]; let s: i32 = fresh[0] + fresh[1] + fresh[2]; if (c.pr.0 + c.pr.1 + c.id != 16) { return 90; } if (s != 66) { return 91; } return __rc_underflow_count(); }`, 0},
		{"option-field-reuse-carried-value", `struct Q { id: i32, o: Option[i32] } function main(): i32 { let d = Q { id: 1, o: Some(10) }; let c = Q { ...d, id: 2 }; let r: i32 = 0; match (c.o) { Some(v) => { r = v + c.id; }, None => { r = 0; } } return r; }`, 12},
		{"option-field-reuse-override-detector", `struct Q { id: i32, o: Option[i32] } function main(): i32 { let d = Q { id: 1, o: Some(10) }; let c = Q { ...d, o: Some(30) }; let r: i32 = 0; match (c.o) { Some(v) => { r = v + c.id; }, None => { r = 0; } } if (r != 31) { return 99; } return __rc_underflow_count(); }`, 0},
		// OWN-PARAM struct donors (#4356 slice 10 / divergence 3): a construction in
		// a function with an `own` struct param reuses that param's (moved-in, sole-
		// owned, dead-after) box. All-scalar donor+recipient, so no old-field
		// release; the value read from the reused box is intact, the detector clean
		// (nothing over-released), and a fresh array next to the reuse reads back
		// unpoisoned. Same type + cross-type (A→B same box class).
		{"own-param-donor-value", `struct P { x: i32, y: i32 } function bump(own d: P): i32 { let u: i32 = d.x + d.y; let c = P { x: 10, y: 20 }; return c.x + c.y + u; } function main(): i32 { return bump(P { x: 3, y: 4 }); }`, 37},
		{"own-param-donor-detector", `struct P { x: i32, y: i32 } function bump(own d: P): i32 { let u: i32 = d.x + d.y; let c = P { x: 10, y: 20 }; let s: i32 = c.x + c.y + u; if (s != 37) { return 99; } return __rc_underflow_count(); } function main(): i32 { return bump(P { x: 3, y: 4 }); }`, 0},
		{"own-param-donor-cross-detector", `struct A { n: i32, m: i32 } struct B { p: i32, q: i32 } function f(own d: A): i32 { let u: i32 = d.n + d.m; let c = B { p: 10, q: 20 }; let s: i32 = c.p + c.q + u; if (s != 37) { return 99; } return __rc_underflow_count(); } function main(): i32 { return f(A { n: 3, m: 4 }); }`, 0},
		{"own-param-donor-corruption-probe-detector", `struct P { x: i32, y: i32 } function bump(own d: P): i32 { let u: i32 = d.x + d.y; let c = P { x: 10, y: 20 }; let fresh = [11, 22, 33]; let s: i32 = fresh[0] + fresh[1] + fresh[2]; if (c.x + c.y + u != 37) { return 90; } if (s != 66) { return 91; } return __rc_underflow_count(); } function main(): i32 { return bump(P { x: 3, y: 4 }); }`, 0},
		// OWN-PARAM donors with RC-POINTER fields (#4356 slice 11): the donor
		// param's old array / nested-struct field is rc-GUARDED-released on the
		// reuse arm. The detectors are the double-free guard — a mis-balanced
		// release of the donor's old array (or its nested inner box) would
		// over-free (detector > 0); the value cases confirm the reused box's
		// fresh array / inner reads back intact.
		{"own-param-donor-array-value", `struct H { id: i32, items: i32[] } function bump(own d: H): i32 { let u: i32 = d.id + d.items[0]; let c = H { id: 5, items: [7, 8, 9] }; return c.id + c.items[0] + c.items[2] + u; } function main(): i32 { return bump(H { id: 1, items: [10, 20] }); }`, 32},
		{"own-param-donor-array-detector", `struct H { id: i32, items: i32[] } function bump(own d: H): i32 { let u: i32 = d.id + d.items[0]; let c = H { id: 5, items: [7, 8, 9] }; let s: i32 = c.id + c.items[0] + c.items[2] + u; if (s != 32) { return 99; } return __rc_underflow_count(); } function main(): i32 { return bump(H { id: 1, items: [10, 20] }); }`, 0},
		{"own-param-donor-nested-value", `struct Inner { a: i32, b: i32 } struct Outer { id: i32, inner: Inner } function bump(own d: Outer): i32 { let u: i32 = d.id + d.inner.a; let c = Outer { id: 5, inner: Inner { a: 7, b: 8 } }; return c.id + c.inner.a + c.inner.b + u; } function main(): i32 { return bump(Outer { id: 1, inner: Inner { a: 2, b: 3 } }); }`, 23},
		{"own-param-donor-nested-detector", `struct Inner { a: i32, b: i32 } struct Outer { id: i32, inner: Inner } function bump(own d: Outer): i32 { let u: i32 = d.id + d.inner.a; let c = Outer { id: 5, inner: Inner { a: 7, b: 8 } }; let s: i32 = c.id + c.inner.a + c.inner.b + u; if (s != 23) { return 99; } return __rc_underflow_count(); } function main(): i32 { return bump(Outer { id: 1, inner: Inner { a: 2, b: 3 } }); }`, 0},
		{"own-param-donor-array-corruption-probe-detector", `struct H { id: i32, items: i32[] } function bump(own d: H): i32 { let u: i32 = d.id + d.items[0]; let c = H { id: 5, items: [7, 8, 9] }; let fresh = [11, 22, 33]; let s: i32 = fresh[0] + fresh[1] + fresh[2]; if (c.id + c.items[0] + c.items[2] + u != 32) { return 90; } if (s != 66) { return 91; } return __rc_underflow_count(); } function main(): i32 { return bump(H { id: 1, items: [10, 20] }); }`, 0},
		// OWN-PARAM base in the SELF-OVERWRITE family (#4356 slice 12): `let c =
		// T { ...d, f: v }` over an `own d` param reuses its box in place. Detectors
		// guard the override release (array/nested) and the carried-array move; a
		// mis-balanced release or a dropped carried box would over-free / corrupt.
		{"own-param-selfoverwrite-scalar-value", `struct P { x: i32, y: i32 } function bump(own d: P): i32 { let c = P { ...d, x: 10 }; return c.x + c.y; } function main(): i32 { return bump(P { x: 3, y: 4 }); }`, 14},
		{"own-param-selfoverwrite-array-detector", `struct H { id: i32, items: i32[] } function bump(own d: H): i32 { let c = H { ...d, items: [7, 8, 9] }; let s: i32 = c.id + c.items[0] + c.items[2]; if (s != 17) { return 99; } return __rc_underflow_count(); } function main(): i32 { return bump(H { id: 1, items: [10, 20] }); }`, 0},
		{"own-param-selfoverwrite-carried-value", `struct H { id: i32, items: i32[] } function bump(own d: H): i32 { let c = H { ...d, id: 5 }; return c.id + c.items[0] + c.items[1]; } function main(): i32 { return bump(H { id: 1, items: [10, 20] }); }`, 35},
		{"own-param-selfoverwrite-carried-detector", `struct H { id: i32, items: i32[] } function bump(own d: H): i32 { let c = H { ...d, id: 5 }; let s: i32 = c.id + c.items[0] + c.items[1]; if (s != 35) { return 99; } return __rc_underflow_count(); } function main(): i32 { return bump(H { id: 1, items: [10, 20] }); }`, 0},
		{"own-param-selfoverwrite-corruption-probe-detector", `struct H { id: i32, items: i32[] } function bump(own d: H): i32 { let c = H { ...d, items: [7, 8, 9] }; let fresh = [11, 22, 33]; let s: i32 = fresh[0] + fresh[1] + fresh[2]; if (c.id + c.items[0] + c.items[2] != 17) { return 90; } if (s != 66) { return 91; } return __rc_underflow_count(); } function main(): i32 { return bump(H { id: 1, items: [10, 20] }); }`, 0},
		// IN-PLACE enum self-reassign reuse (FBIP): a loop-carried array-payload
		// enum `let b = V0([..]); while(..) { b = V1([..]); b = V2([..]); }` whose
		// enum has UNIFORM variant layout (every variant one array field) reuses
		// b's box IN PLACE across each reassign — the old payload array is released,
		// the box is re-shaped to the new variant, and the fresh payload written into
		// the SAME box. The over-release detector (`__rc_underflow_count()==0`) is
		// the soundness gate for the in-place mutation: a mis-balanced old-payload
		// release or a corrupted reshape would double-free (detector > 0).
		// wildcard-churn value = 2 (last b is Swap).
		{"enum-reassign-inplace-value", `enum Bag { Keep(i32[]), Swap(i32[]) } function churn(n: i32): i32 { let b: Bag = Keep([0, 0, 0, 0]); let i = 0; while (i < n) { b = Keep([i, i, i, i]); b = Swap([i, i, i, i]); i = i + 1; } match (b) { Keep(_) => { return 1; }, Swap(_) => { return 2; }, } return 0; } function main(): i32 { return churn(5); }`, 2},
		{"enum-reassign-inplace-detector", `enum Bag { Keep(i32[]), Swap(i32[]) } function churn(n: i32): i32 { let b: Bag = Keep([0, 0, 0, 0]); let i = 0; while (i < n) { b = Keep([i, i, i, i]); b = Swap([i, i, i, i]); i = i + 1; } let r = 0; match (b) { Keep(_) => { r = 1; }, Swap(_) => { r = 2; }, } if (r != 2) { return 99; } return __rc_underflow_count(); } function main(): i32 { return churn(5); }`, 0},
		// IN-PLACE + heap-reuse corruption probe: a fresh array allocated in the same
		// scope as the reassigns reads back intact (a mis-freed superseded payload, or a
		// box freed while still live, would poison the recycled buffer). acc = 10*(7+8)=150.
		{"enum-reassign-inplace-corruption-probe-detector", `enum Bag { Keep(i32[]), Swap(i32[]) } function churn(n: i32): i32 { let b: Bag = Keep([9, 9]); let i = 0; let acc = 0; while (i < n) { b = Keep([1, 2]); b = Swap([3, 4]); let fresh: i32[] = [7, 8]; acc = acc + fresh[0] + fresh[1]; i = i + 1; } let r = 0; match (b) { Keep(_) => { r = 1; }, Swap(_) => { r = 0; }, } if (acc != 150) { return 90; } return __rc_underflow_count(); } function main(): i32 { return churn(10); }`, 0},
		// PAYLOAD-BOUND: the payload IS bound (`Keep(a) => a[0]`). Value stays
		// correct + detector clean. churn(3): last b = Swap([2,..]) -> a[1] = 2.
		{"enum-reassign-bound-fallback-value", `enum Bag { Keep(i32[]), Swap(i32[]) } function churn(n: i32): i32 { let b: Bag = Keep([0, 0, 0, 0]); let i = 0; while (i < n) { b = Keep([i, i, i, i]); b = Swap([i, i, i, i]); match (b) { Keep(a) => { }, Swap(a) => { }, } i = i + 1; } match (b) { Keep(a) => { return a[0]; }, Swap(a) => { return a[1]; }, } return 0; } function main(): i32 { return churn(3); }`, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := cli.runX86(t, emit(t, tc.src)); got != tc.expected {
				t.Errorf("precise-drop x86 IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}

	// #4350: the self-overwrite reuse site is RUNTIME-GUARDED — the emitted asm
	// must carry the uniqueness probe and the token-degrade allocator (reused =
	// __fern_rc_is_unique(d); box = __fern_alloc_reuse(token, nfields)). The
	// degrade arm is unreachable from any valid program today (only sole-owner
	// donors reuse), so it is pinned structurally here — and at scale by the
	// self-compile fixpoints, which run every self-overwrite site in the
	// compiler's own source through the guard — rather than by an end-to-end
	// case.
	t.Run("self-overwrite-guard-emitted", func(t *testing.T) {
		asm := emit(t, `struct Point { x: i32, y: i32 } function main(): i32 { let d = Point { x: 3, y: 4 }; let c = Point { ...d, x: 10 }; return c.x + c.y; }`)
		if rcIsUniqueSites(asm) == 0 {
			t.Error("self-overwrite reuse site emitted no __fern_rc_is_unique guard")
		}
		if !strings.Contains(asm, "call __fn___fern_alloc_reuse") {
			t.Error("self-overwrite reuse site emitted no __fern_alloc_reuse token-degrade call")
		}
	})

	// #4350 slice 5: the in-arm consuming-match reuse is RUNTIME-GUARDED too —
	// same token shape, with __fern_alloc_reuse called PER ARM (sized by the
	// matched variant). The degrade arm is unreachable from any statically
	// admitted program (sole-owner donors only), so it is pinned structurally
	// here and at scale by the self-compile fixpoints.
	t.Run("inarm-reuse-guard-emitted", func(t *testing.T) {
		asm := emit(t, `enum E { V(i32, i32), W(i32, i32) } function go(): i32 { let x = V(3, 4); let y = match (x) { V(a, b) => W(a + 1, b + 1), W(c, d) => V(c, d) }; let r = match (y) { V(a, b) => a + b, W(c, d) => c + d }; return r; } function main(): i32 { return go(); }`)
		if rcIsUniqueSites(asm) == 0 {
			t.Error("in-arm match reuse site emitted no __fern_rc_is_unique guard")
		}
		if !strings.Contains(asm, "call __fn___fern_alloc_reuse") {
			t.Error("in-arm match reuse site emitted no __fern_alloc_reuse token-degrade call")
		}
	})
}
