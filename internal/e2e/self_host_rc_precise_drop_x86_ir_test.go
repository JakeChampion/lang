package e2e

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	b.WriteString("function main(): i32 { var acc = 0;")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, " var a%d = [%d,%d,%d,%d]; var s%d = 0; var j%d = 0; while (j%d < 4) { s%d = s%d + a%d[j%d]; j%d = j%d + 1; } acc = acc + s%d;",
			i, i, i+1, i+2, i+3, i, i, i, i, i, i, i, i, i, i)
	}
	b.WriteString(" " + tail + " }")
	return b.String(), (2*n*n + 4*n) % 100
}

// TestSelfHostRcPreciseDropX86IR gates the first Phase-B Perceus optimisation
// ported to the self-host IR: drop-on-last-use for arrays. A linear, owned
// array-literal local (declared once, never reassigned, only borrow-read — never
// aliased / stored / passed / returned / captured) is released right after its
// LAST top-level use instead of at the function-exit dec-sweep, bounding the
// live set. Each case asserts the program routes "ir", computes the oracle
// value, and that the over-release detector (`__rc_underflow()`) reads 0 (the
// drop is sound — no double-free). The byte-identical fixpoint + std-test gates
// separately prove soundness on the compiler's own array literals.
func TestSelfHostRcPreciseDropX86IR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)

	probeSrc, err := os.ReadFile("../../examples/self_host/asm_pathprobe_run.fern")
	if err != nil {
		t.Fatalf("read asm_pathprobe_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_pathprobe_run.fern"), probeSrc, 0o644); err != nil {
		t.Fatalf("write asm_pathprobe_run.fern: %v", err)
	}
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	runSrc, err := os.ReadFile("../../examples/self_host/asm_ir_run.fern")
	if err != nil {
		t.Fatalf("read asm_ir_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_ir_run.fern"), runSrc, 0o644); err != nil {
		t.Fatalf("write asm_ir_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	emit := func(t *testing.T, src string) string {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, "-ir")
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		out, err := cmd.Output()
		if err != nil || len(out) == 0 {
			t.Fatalf("driver failed for %q: %v", src, err)
		}
		return string(out)
	}
	run := func(t *testing.T, asmText string) int {
		t.Helper()
		innerAsm := filepath.Join(dir, "ir_inner.s")
		innerBin := filepath.Join(dir, "ir_inner")
		if err := os.WriteFile(innerAsm, []byte(asmText), 0o644); err != nil {
			t.Fatalf("write inner asm: %v", err)
		}
		if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", innerAsm, "-o", innerBin).CombinedOutput(); err != nil {
			t.Fatalf("inner gcc: %v\n%s", err, out)
		}
		var inner *exec.Cmd
		if len(runner) == 0 {
			inner = exec.Command(innerBin)
		} else {
			inner = exec.Command(runner[0], append(append([]string{}, runner[1:]...), innerBin)...)
		}
		_ = inner.Run()
		if inner.ProcessState == nil || !inner.ProcessState.Exited() {
			t.Fatalf("inner did not exit normally")
		}
		return inner.ProcessState.ExitCode()
	}

	seqVal, seqExp := seqArrays(40, "return acc % 100;")
	seqDet, _ := seqArrays(40, "return __rc_underflow();")

	cases := []struct {
		name     string
		src      string
		expected int
	}{
		// Two independent linear array literals; each dropped right after its
		// own loop, the exit-sweep no-ops on the zeroed slots.
		{"two-arrays-value", `function main(): i32 { var a = [10, 20, 30]; var s = 0; var i = 0; while (i < 3) { s = s + a[i]; i = i + 1; } var b = [1, 2, 3, 4]; var t = 0; var k = 0; while (k < 4) { t = t + b[k]; k = k + 1; } return s + t; }`, 60 + 10},
		{"two-arrays-detector", `function main(): i32 { var a = [10, 20, 30]; var s = 0; var i = 0; while (i < 3) { s = s + a[i]; i = i + 1; } var b = [1, 2, 3, 4]; var t = 0; var k = 0; while (k < 4) { t = t + b[k]; k = k + 1; } if (s + t != 70) { return 99; } return __rc_underflow(); }`, 0},
		// 40 sequential arrays — the reclaim shape: precise drops keep peak heap
		// ~one array as the blocks recycle.
		{"reclaim-seq-value", seqVal, seqExp},
		{"reclaim-seq-detector", seqDet, 0},
		// A fresh i32[]-returning FREE call with scalar-literal args is also a
		// candidate (the builder-call win); a string[]-returning call is NOT
		// (excluded by the return-type registry — it crashed the compiler).
		{"builder-call-value", `function build(): i32[] { return [1, 2, 3, 4]; } function main(): i32 { var a = build(); var s = 0; var i = 0; while (i < 4) { s = s + a[i]; i = i + 1; } return s; }`, 10},
		{"builder-call-detector", `function build(): i32[] { return [1, 2, 3, 4]; } function main(): i32 { var a = build(); var s = 0; var i = 0; while (i < 4) { s = s + a[i]; i = i + 1; } if (s != 10) { return 99; } return __rc_underflow(); }`, 0},
		{"strarr-call-not-dropped", `function names(): string[] { return ["a", "b"]; } function main(): i32 { var xs = names(); return xs.len(); }`, 2},
		// Borrow-inference reclaim (Level 2): an array literal passed ONLY to a
		// borrowable (read-only) free function is still a precise-drop candidate —
		// the callee provably can't retain it. `sum_arr` borrows its param (only
		// `v.len()` / `v[i]`), so `t` is dropped after its last `sum_arr(t)` call.
		{"borrow-helper-value", `function sum_arr(v: i32[]): i32 { var s = 0; var i = 0; while (i < v.len()) { s = s + v[i]; i = i + 1; } return s; } function main(): i32 { var t = [1, 2, 3, 4]; var a = sum_arr(t); var b = sum_arr(t); return a + b; }`, 20},
		{"borrow-helper-detector", `function sum_arr(v: i32[]): i32 { var s = 0; var i = 0; while (i < v.len()) { s = s + v[i]; i = i + 1; } return s; } function main(): i32 { var t = [1, 2, 3, 4]; var a = sum_arr(t); var b = sum_arr(t); if (a + b != 20) { return 99; } return __rc_underflow(); }`, 0},
		// Conservative: a callee that RETURNS its param (escape) is NOT borrowable,
		// so the arg is not reclaimed early — the exit-sweep frees it. Sound either
		// way; the detector confirms no over-release.
		{"escape-helper-detector", `function keep(v: i32[]): i32[] { return v; } function main(): i32 { var t = [1, 2, 3, 4]; var u = keep(t); var a = u[0]; if (a != 1) { return 99; } return __rc_underflow(); }`, 0},
		// Inter-procedural borrow inference (single forward pass on the emit path,
		// computed once per module): `inner` is defined before `outer` and borrows
		// its param, so the pass recognises `outer`'s forwarded param as a borrow too
		// — `t` IS reclaimed after its last `outer(t)`.
		{"transitive-reclaim-value", `function inner(v: i32[]): i32 { return v[0]; } function outer(v: i32[]): i32 { return inner(v); } function main(): i32 { var t = [5, 6, 7, 8]; var a = outer(t); var b = outer(t); return a + b; }`, 10},
		{"transitive-reclaim-detector", `function inner(v: i32[]): i32 { return v[0]; } function outer(v: i32[]): i32 { return inner(v); } function main(): i32 { var t = [5, 6, 7]; var a = outer(t); if (a != 5) { return 99; } return __rc_underflow(); }`, 0},
		// Escape through a chain stays rejected: `wrap` forwards to `idf`, which
		// RETURNS its param, so `idf` is not borrowable, hence `wrap` is not, hence
		// `t` is not reclaimed — the result that aliases `t` stays valid.
		{"escape-chain-detector", `function idf(v: i32[]): i32[] { return v; } function wrap(v: i32[]): i32[] { return idf(v); } function main(): i32 { var t = [3, 4, 5]; var u = wrap(t); if (u[1] != 4) { return 99; } return __rc_underflow(); }`, 0},
		// Full convergence (least-fixpoint, iterated): `outer` is defined BEFORE the
		// borrowable `inner`, so a single forward pass would miss it — the iterated
		// fixpoint propagates inner's borrowability back to outer, reclaiming `t`.
		{"caller-before-callee-value", `function outer(v: i32[]): i32 { return inner(v); } function inner(v: i32[]): i32 { return v[0]; } function main(): i32 { var t = [5, 6, 7, 8]; var a = outer(t); var b = outer(t); return a + b; }`, 10},
		{"caller-before-callee-detector", `function outer(v: i32[]): i32 { return inner(v); } function inner(v: i32[]): i32 { return v[0]; } function main(): i32 { var t = [5, 6, 7]; var a = outer(t); if (a != 5) { return 99; } return __rc_underflow(); }`, 0},
		// Scalar-element widening: i64[] and f64[] literals are flat scalar buffers
		// (no nested pointers, like i32[]), so they are precise-drop candidates too —
		// each released right after its loop instead of at the exit sweep.
		{"i64arr-two-arrays-value", `function main(): i32 { var v: i64[] = [10, 20, 30]; var s: i64 = 0; var i = 0; while (i < 3) { s = s + v[i]; i = i + 1; } var w: i64[] = [1, 2, 3, 4]; var t: i64 = 0; var k = 0; while (k < 4) { t = t + w[k]; k = k + 1; } return (s + t) as i32; }`, 70},
		{"i64arr-two-arrays-detector", `function main(): i32 { var v: i64[] = [10, 20, 30]; var s: i64 = 0; var i = 0; while (i < 3) { s = s + v[i]; i = i + 1; } var w: i64[] = [1, 2, 3, 4]; var t: i64 = 0; var k = 0; while (k < 4) { t = t + w[k]; k = k + 1; } if ((s + t) as i32 != 70) { return 99; } return __rc_underflow(); }`, 0},
		{"f64arr-two-arrays-value", `function main(): i32 { var v: f64[] = [1.5, 2.5, 3.0]; var s: f64 = 0.0; var i = 0; while (i < 3) { s = s + v[i]; i = i + 1; } var w: f64[] = [0.5, 0.5]; var t: f64 = 0.0; var k = 0; while (k < 2) { t = t + w[k]; k = k + 1; } return (s + t) as i32; }`, 8},
		{"f64arr-two-arrays-detector", `function main(): i32 { var v: f64[] = [1.5, 2.5, 3.0]; var s: f64 = 0.0; var i = 0; while (i < 3) { s = s + v[i]; i = i + 1; } var w: f64[] = [0.5, 0.5]; var t: f64 = 0.0; var k = 0; while (k < 2) { t = t + w[k]; k = k + 1; } if ((s + t) as i32 != 8) { return 99; } return __rc_underflow(); }`, 0},
		// A fresh i64[]- / f64[]-returning FREE call with scalar-literal args is a
		// candidate too (the builder-call win, now scalar-typed); string[]-returning
		// calls stay excluded (the candidacy crash).
		{"i64arr-builder-call-value", `function build64(): i64[] { return [1, 2, 3, 4]; } function main(): i32 { var a = build64(); var s: i64 = 0; var i = 0; while (i < 4) { s = s + a[i]; i = i + 1; } return s as i32; }`, 10},
		{"i64arr-builder-call-detector", `function build64(): i64[] { return [1, 2, 3, 4]; } function main(): i32 { var a = build64(); var s: i64 = 0; var i = 0; while (i < 4) { s = s + a[i]; i = i + 1; } if (s as i32 != 10) { return 99; } return __rc_underflow(); }`, 0},
		{"f64arr-builder-call-value", `function buildf(): f64[] { return [1.5, 2.5]; } function main(): i32 { var a = buildf(); var s: f64 = 0.0; var i = 0; while (i < 2) { s = s + a[i]; i = i + 1; } return s as i32; }`, 4},
		{"f64arr-builder-call-detector", `function buildf(): f64[] { return [1.5, 2.5]; } function main(): i32 { var a = buildf(); var s: f64 = 0.0; var i = 0; while (i < 2) { s = s + a[i]; i = i + 1; } if (s as i32 != 4) { return 99; } return __rc_underflow(); }`, 0},
		// Struct in-place reuse (FBIP), functional-update self-overwrite: `var c =
		// T { ...d, f: v }` where d is a fresh, non-escaping, scalar-field struct
		// local DEAD after this update reuses d's box in place (the override field
		// is written into d's box, d's slot zeroed). The value is correct and the
		// over-release detector stays 0 (d's box is freed exactly once, through c).
		{"struct-reuse-value", `struct Point { x: i32, y: i32 } function main(): i32 { var d = Point { x: 3, y: 4 }; var c = Point { ...d, x: 10 }; return c.x + c.y; }`, 14},
		{"struct-reuse-detector", `struct Point { x: i32, y: i32 } function main(): i32 { var d = Point { x: 3, y: 4 }; var c = Point { ...d, x: 10 }; var sum = c.x + c.y; if (sum != 14) { return 99; } return __rc_underflow(); }`, 0},
		// Multi-field override: both overrides written into d's box in place; the
		// un-overridden field (y) is left untouched (already correct in d's box).
		{"struct-reuse-multi-override", `struct Trip { a: i32, b: i32, c: i32 } function main(): i32 { var d = Trip { a: 1, b: 2, c: 3 }; var e = Trip { ...d, a: 10, c: 30 }; return e.a + e.b + e.c; }`, 42},
		// NON-firing: d is used AFTER the update (`d.x` in the return), so it is NOT
		// dead at the update site — reuse must NOT fire (d's box stays live). The
		// value is still correct via the normal alloc-a-new-box path.
		{"struct-no-reuse-base-used-after-value", `struct Point { x: i32, y: i32 } function main(): i32 { var d = Point { x: 3, y: 4 }; var c = Point { ...d, x: 10 }; return c.x + c.y + d.x; }`, 17},
		{"struct-no-reuse-base-used-after-detector", `struct Point { x: i32, y: i32 } function main(): i32 { var d = Point { x: 3, y: 4 }; var c = Point { ...d, x: 10 }; var r = c.x + c.y + d.x; if (r != 17) { return 99; } return __rc_underflow(); }`, 0},
		// Array-field widening (FBIP): the reused struct now admits leak-safe array
		// fields (i32[]/boolean[]/i64[]/f64[]) alongside flat scalars. Here the i32[]
		// field is NOT overridden — it stays in d's box and MOVES to c (deep-dropped
		// once at c's reclamation). Only the scalar `tag` is rewritten in place. The
		// array contents read back correctly and the detector stays 0 (no over-release).
		{"struct-reuse-array-field-keep-value", `struct Vec { tag: i32, items: i32[] } function main(): i32 { var d = Vec { tag: 1, items: [10, 20, 30] }; var c = Vec { ...d, tag: 2 }; return c.items[0] + c.items[1] + c.items[2] + c.tag; }`, 62},
		{"struct-reuse-array-field-keep-detector", `struct Vec { tag: i32, items: i32[] } function main(): i32 { var d = Vec { tag: 1, items: [10, 20, 30] }; var c = Vec { ...d, tag: 2 }; var s = c.items[0] + c.items[1] + c.items[2] + c.tag; if (s != 62) { return 99; } return __rc_underflow(); }`, 0},
		// Array-field REPLACE: the override targets the i32[] field with a FRESH array
		// literal. The OLD array already in d's box is released (dec) before the new
		// one is written, then the new array is owned by the reused box. New contents
		// read back correctly and the detector stays 0 (old array freed exactly once,
		// new array freed exactly once — no leak, no over-release).
		{"struct-reuse-array-field-replace-value", `struct Vec { tag: i32, items: i32[] } function main(): i32 { var d = Vec { tag: 1, items: [10, 20, 30] }; var c = Vec { ...d, tag: 7, items: [100, 50] }; return c.items[0] + c.items[1] + c.tag; }`, 157},
		{"struct-reuse-array-field-replace-detector", `struct Vec { tag: i32, items: i32[] } function main(): i32 { var d = Vec { tag: 1, items: [10, 20, 30] }; var c = Vec { ...d, tag: 7, items: [100, 200] }; var s = c.items[0] + c.items[1] + c.tag; if (s != 307) { return 99; } return __rc_underflow(); }`, 0},
		// Wide-scalar (i64) field alongside an array field: the i64 override is stored
		// 8-byte (struct_set_i64) into the reused box; the non-overridden array field
		// moves to c. Value correct, detector 0.
		{"struct-reuse-i64-field-detector", `struct Box { a: i64, items: i32[] } function main(): i32 { var d = Box { a: 5, items: [1, 2, 3] }; var c = Box { ...d, a: 40 }; var av: i64 = c.a; var s = (av as i32) + c.items[0] + c.items[1] + c.items[2]; if (s != 46) { return 99; } return __rc_underflow(); }`, 0},
		// NON-firing with an array field: the base d is read AFTER the update (d.items[0]
		// in the return), so d is NOT dead at the update site — reuse must NOT fire (d's
		// box and its array stay live). Value still correct via the normal alloc path.
		{"struct-no-reuse-array-base-used-after-detector", `struct Vec { tag: i32, items: i32[] } function main(): i32 { var d = Vec { tag: 1, items: [10, 20, 30] }; var c = Vec { ...d, tag: 9 }; var r = c.tag + d.items[0]; if (r != 19) { return 99; } return __rc_underflow(); }`, 0},
		// Cross-statement FBIP: a FULL construction `var c = T { ... }` (no base)
		// reuses the heap box of an EARLIER, same-type donor `d` that is dead by the
		// construction site — d's box is rewritten field-by-field and c bound to it,
		// d's slot zeroed. Here d is read once (d.x + d.y) BEFORE c, then never again,
		// so it is dead at c and reuse fires. Value correct, detector 0 (box freed once).
		{"struct-cross-reuse-value", `struct Point { x: i32, y: i32 } function main(): i32 { var d = Point { x: 3, y: 4 }; var u = d.x + d.y; var c = Point { x: 10, y: 20 }; return c.x + c.y + u; }`, 37},
		{"struct-cross-reuse-detector", `struct Point { x: i32, y: i32 } function main(): i32 { var d = Point { x: 3, y: 4 }; var u = d.x + d.y; var c = Point { x: 10, y: 20 }; var sum = c.x + c.y + u; if (sum != 37) { return 99; } return __rc_underflow(); }`, 0},
		// Cross-statement FBIP with an array field: the donor's OLD array is released
		// (dec) before c's fresh array is written into the reused box; c then owns the
		// new array. Donor read once before c, dead after. Value correct, detector 0.
		{"struct-cross-reuse-array-value", `struct Vec { tag: i32, items: i32[] } function main(): i32 { var d = Vec { tag: 1, items: [10, 20, 30] }; var u = d.items[0]; var c = Vec { tag: 2, items: [100, 50] }; return c.items[0] + c.items[1] + c.tag + u; }`, 162},
		{"struct-cross-reuse-array-detector", `struct Vec { tag: i32, items: i32[] } function main(): i32 { var d = Vec { tag: 1, items: [10, 20, 30] }; var u = d.items[0]; var c = Vec { tag: 2, items: [100, 50] }; var s = c.items[0] + c.items[1] + c.tag + u; if (s != 162) { return 99; } return __rc_underflow(); }`, 0},
		// NON-firing cross case: the donor d is used AFTER the full construction
		// (`d.x` in the return), so d is NOT dead at the construction — reuse must NOT
		// fire and c gets a fresh box. Value still correct, detector 0.
		{"struct-cross-no-reuse-donor-used-after-value", `struct Point { x: i32, y: i32 } function main(): i32 { var d = Point { x: 3, y: 4 }; var c = Point { x: 10, y: 20 }; return c.x + c.y + d.x; }`, 33},
		{"struct-cross-no-reuse-donor-used-after-detector", `struct Point { x: i32, y: i32 } function main(): i32 { var d = Point { x: 3, y: 4 }; var c = Point { x: 10, y: 20 }; var r = c.x + c.y + d.x; if (r != 33) { return 99; } return __rc_underflow(); }`, 0},
		// Plain single-struct reclamation through the rc-headered box: a fresh,
		// non-escaping struct local is rc-dec'd at exit and FREED soundly. Before
		// the struct box gained a refcount header (struct_make via __fern_arr_box),
		// __fern_arr_dec read the word below the raw __fern_alloc block as a bogus
		// refcount and over-released, so this read 1; now it reads 0.
		{"plain-struct-reclaim-value", `struct P { x: i32, y: i32 } function compute(): i32 { var d = P { x: 3, y: 4 }; return d.x + d.y; } function main(): i32 { return compute(); }`, 7},
		{"plain-struct-reclaim-detector", `struct P { x: i32, y: i32 } function compute(): i32 { var d = P { x: 3, y: 4 }; return d.x + d.y; } function main(): i32 { var r = compute(); if (r != 7) { return 99; } return __rc_underflow(); }`, 0},
		// Two independent plain structs, both reclaimed at exit — both boxes freed,
		// detector 0.
		{"two-plain-structs-reclaim-detector", `struct P { x: i32, y: i32 } function main(): i32 { var a = P { x: 1, y: 2 }; var b = P { x: 3, y: 4 }; var s = a.x + a.y + b.x + b.y; if (s != 10) { return 99; } return __rc_underflow(); }`, 0},
		// Struct with an i32[] field: the box is rc-headered and freed soundly, and
		// the array field is deep-dropped (emit_struct_field_drops) exactly once.
		// Detector 0 proves neither the box nor the array field over-releases.
		{"struct-array-field-reclaim-value", `struct Buf { xs: i32[], n: i32 } function go(): i32 { var b = Buf { xs: [10, 20, 30], n: 3 }; return b.xs[1] + b.n; } function main(): i32 { return go(); }`, 23},
		{"struct-array-field-reclaim-detector", `struct Buf { xs: i32[], n: i32 } function main(): i32 { var b = Buf { xs: [10, 20, 30], n: 3 }; var r = b.xs[1] + b.n; if (r != 23) { return 99; } return __rc_underflow(); }`, 0},
		// Array-field from an ALIASED VARIABLE (this slice): `Box { v: a }` where
		// `a` is an rc-tracked scalar-array local. Previously bailed to the AST
		// emitter (only a FRESH literal was admitted); now the alias is admitted
		// via a Perceus dup (rc_inc) at the construction store, so the struct's
		// field owns a counted reference. The exit dec-sweep still decs `a`'s slot
		// and the struct's array field deep-drops at the box's reclamation — the
		// inc balances both decs (rc 1 →inc 2 →sweep(a) 1 →field-drop 0). Reads
		// back correctly and the over-release detector stays 0.
		{"struct-arr-field-from-var-value", `struct Box { v: i32[] } function main(): i32 { var a = [1, 2, 3]; var b = Box { v: a }; return b.v[0]; }`, 1},
		{"struct-arr-field-from-var-detector", `struct Box { v: i32[] } function main(): i32 { var a = [1, 2, 3]; var b = Box { v: a }; var r = b.v[0]; if (r != 1) { return 99; } return __rc_underflow(); }`, 0},
		// TWO-alias stress: the same array local aliased into TWO struct boxes.
		// Each construction emits its own dup, so the array's rc reaches 3 (1 →inc
		// 2 →inc 3) and is dec'd three times (sweep(a) + b's field-drop + c's
		// field-drop) — freed exactly once. Detector 0 proves the inc count is
		// correct under multiple aliases (no leak, no over-release).
		{"struct-arr-field-two-alias-detector", `struct Box { v: i32[] } function main(): i32 { var a = [1, 2, 3]; var b = Box { v: a }; var c = Box { v: a }; var r = b.v[0] + c.v[1]; if (r != 3) { return 99; } return __rc_underflow(); }`, 0},
		// Read an array OUT of a struct field (RC-frontier slice 2). The three
		// alias-creating positions — bind (`var y = h.items`), return
		// (`return h.items`), assign (`x = h.items`) — previously bailed to the AST
		// emitter; now each lowers via the IR with a Perceus dup (rc_inc) so the new
		// owner holds a counted reference, balanced against the struct's own
		// field-drop + the exit-sweep (and, for return, the caller's eventual dec).
		//
		// BIND `var y = h.items`: y becomes a second owner of the field's array.
		// inc at the field read; y is exit-swept (dec), the struct field deep-drops
		// (dec) at the box's reclamation — the inc balances both (rc 1 →inc 2
		// →sweep(y) 1 →field-drop 0). Reads back correctly, detector 0.
		{"field-arr-bind-value", `struct H { items: i32[] } function main(): i32 { var h = H { items: [10, 20, 30] }; var y = h.items; return y[0] + y[2]; }`, 40},
		{"field-arr-bind-detector", `struct H { items: i32[] } function main(): i32 { var h = H { items: [10, 20, 30] }; var y = h.items; var r = y[0] + y[2]; if (r != 40) { return 99; } return __rc_underflow(); }`, 0},
		// BIND from a BORROWED struct PARAM (`function f(h: H): i32 { var y = h.items; }`):
		// h is a borrow (never swept), the inc hands the new local its own counted
		// ref; the local is exit-swept (dec) → balanced. Detector 0.
		{"field-arr-bind-param-detector", `struct H { items: i32[] } function f(h: H): i32 { var y = h.items; var r = y[1]; return r; } function main(): i32 { var h = H { items: [5, 6, 7] }; var v = f(h); if (v != 6) { return 99; } return __rc_underflow(); }`, 0},
		// RETURN `return h.items`: the field escapes to the caller, who will own it.
		// inc so the returned ref is counted; the field is NOT moved out (the struct
		// keeps its ref). Caller indexes the returned array. Value + detector 0.
		{"field-arr-return-value", `struct H { items: i32[] } function get(h: H): i32[] { return h.items; } function main(): i32 { var h = H { items: [11, 22, 33] }; var xs = get(h); return xs[0] + xs[2]; }`, 44},
		{"field-arr-return-detector", `struct H { items: i32[] } function get(h: H): i32[] { return h.items; } function main(): i32 { var h = H { items: [11, 22, 33] }; var xs = get(h); var r = xs[0] + xs[2]; if (r != 44) { return 99; } return __rc_underflow(); }`, 0},
		// RETURN from a function that OWNS the struct (fresh local `h`, reclaimable):
		// at exit the struct field-drop decs the array (rc 1 →inc 2 →field-drop 1),
		// the caller decs to 0. The escaping array survives the field-drop. Value +
		// detector 0.
		{"field-arr-return-owned-detector", `struct H { items: i32[] } function mk(): i32[] { var h = H { items: [4, 5, 6] }; return h.items; } function main(): i32 { var xs = mk(); var r = xs[0] + xs[1] + xs[2]; if (r != 15) { return 99; } return __rc_underflow(); }`, 0},
		// ASSIGN `x = h.items` where x previously held a DIFFERENT array: emit_arr_store
		// decs the OLD array x held (cow-guarded) THEN incs the new field read. No
		// leak-of-old (freed once), no over-release of the field's array. Value +
		// detector 0.
		{"field-arr-assign-value", `struct H { items: i32[] } function main(): i32 { var h = H { items: [7, 8, 9] }; var x = [1, 2, 3]; x = h.items; return x[0] + x[2]; }`, 16},
		{"field-arr-assign-detector", `struct H { items: i32[] } function main(): i32 { var h = H { items: [7, 8, 9] }; var x = [1, 2, 3]; var pre = x[1]; x = h.items; var r = x[0] + x[2]; if (pre != 2) { return 98; } if (r != 16) { return 99; } return __rc_underflow(); }`, 0},
		// ASSIGN f64[] field: same dec-old / inc-new through emit_arr_store on the
		// 8-byte-element field. Detector 0 (the dec-old + inc-new balance for f64[] too).
		{"field-arr-assign-f64-detector", `struct H { items: f64[] } function main(): i32 { var h = H { items: [1.5, 2.5, 3.0] }; var x: f64[] = [0.5, 0.5]; x = h.items; var s = x[0] + x[2]; if ((s as i32) != 4) { return 99; } return __rc_underflow(); }`, 0},
		// Bare array FIELD-COPY as a struct-literal field value (RC-frontier slice 3,
		// following #3292 array-ident-into-field + #3308 array-out-of-field). The
		// value `s.xs` is a field READ — it lowers via struct_get to a real array
		// pointer that ALIASES the source struct's field — so the new box `s2` becomes
		// a second owner of the same array. A Perceus dup (rc_inc) at the construction
		// store gives the field its counted reference: the source struct's field-drop
		// and the new struct's field-drop each dec, the inc covers the second owner
		// (rc 1 →inc 2 →src-field-drop 1 →new-field-drop 0). Previously bailed to AST
		// (only a fresh literal / bare ident was admitted); now routes "ir". Reads
		// back correctly and the over-release detector stays 0.
		{"field-copy-scalar-value", `struct S { xs: i32[], n: i32 } function main(): i32 { var s = S { xs: [10, 20, 30], n: 3 }; var s2 = S { xs: s.xs, n: s.n }; return s2.xs[1] + s2.n; }`, 23},
		{"field-copy-scalar-detector", `struct S { xs: i32[], n: i32 } function main(): i32 { var s = S { xs: [10, 20, 30], n: 3 }; var s2 = S { xs: s.xs, n: s.n }; var r = s2.xs[1] + s2.n; if (r != 23) { return 99; } return __rc_underflow(); }`, 0},
		// i64[] field-copy: the 8-byte-element array aliased through struct_get + the
		// same alias-inc. Value + detector 0 (the inc balances the two field-drops for
		// i64[] too).
		{"field-copy-i64-detector", `struct S { xs: i64[], n: i32 } function main(): i32 { var s = S { xs: [100, 200, 300], n: 3 }; var s2 = S { xs: s.xs, n: s.n }; var v: i64 = s2.xs[0]; var r = (v as i32) + s2.n; if (r != 103) { return 99; } return __rc_underflow(); }`, 0},
		// f64[] field-copy: same through the 8-byte float-element array.
		{"field-copy-f64-detector", `struct S { xs: f64[], n: i32 } function main(): i32 { var s = S { xs: [1.5, 2.5, 3.0], n: 2 }; var s2 = S { xs: s.xs, n: s.n }; var r = (s2.xs[0] as i32) + s2.n; if (r != 3) { return 99; } return __rc_underflow(); }`, 0},
		// Array-of-struct (`S { ops: s.ops }` with `ops: Op[]`) bare field-copy: the
		// struct-array field is admitted via is_struct_array_field_type + the same
		// alias-inc, exactly like the scalar-element case. Value + detector 0.
		{"field-copy-struct-arr-value", `struct Point { x: i32, y: i32 } struct S { pts: Point[] } function main(): i32 { var s = S { pts: [Point { x: 3, y: 4 }, Point { x: 5, y: 6 }] }; var s2 = S { pts: s.pts }; return s2.pts[0].x + s2.pts[1].y; }`, 9},
		{"field-copy-struct-arr-detector", `struct Point { x: i32, y: i32 } struct S { pts: Point[] } function main(): i32 { var s = S { pts: [Point { x: 3, y: 4 }, Point { x: 5, y: 6 }] }; var s2 = S { pts: s.pts }; var r = s2.pts[0].x + s2.pts[1].y; if (r != 9) { return 99; } return __rc_underflow(); }`, 0},
		// Consumed scalar-payload enum free: a function-local `var x = V(scalar...)`
		// of an all-scalar-variant enum consumed by exactly one `match (x)` where x
		// is single-owner and DEAD after the match. The box (now rc-headered via
		// struct_make) is freed right after its match statement (dec + zero the slot)
		// — sound because the match read the scalar payloads as borrows before the
		// free. Value correct, detector 0 (box freed exactly once).
		{"consumed-enum-free-value", `enum Shape { Circle(i32), Square(i32) } function main(): i32 { var x = Circle(7); var total = 0; match (x) { Circle(r) => { total = r + r; }, Square(w) => { total = w * w; }, } return total; }`, 14},
		{"consumed-enum-free-detector", `enum Shape { Circle(i32), Square(i32) } function main(): i32 { var x = Circle(7); var total = 0; match (x) { Circle(r) => { total = r + r; }, Square(w) => { total = w * w; }, } if (total != 14) { return 99; } return __rc_underflow(); }`, 0},
		// i64-payload scalar enum is also a candidate (i64 is a flat scalar) — freed
		// once, detector 0.
		{"consumed-enum-i64-detector", `enum Big { L(i64), R(i32) } function main(): i32 { var x = L(100); var total = 0; match (x) { L(v) => { total = v as i32; }, R(n) => { total = n; }, } if (total != 100) { return 99; } return __rc_underflow(); }`, 0},
		// NON-firing: x ESCAPES through a call (`area(x)`), so it must NOT be freed
		// after the (different-function) match. The exit sweep doesn't sweep enum
		// boxes either, so x leaks (sound). Detector 0 — no over-release.
		{"escaping-enum-not-freed-detector", `enum Shape { Circle(i32), Square(i32) } function area(s: Shape): i32 { match (s) { Circle(r) => { return r + r; }, Square(w) => { return w * w; }, } return 0; } function main(): i32 { var x = Circle(7); var a = area(x); if (a != 14) { return 99; } return __rc_underflow(); }`, 0},
		// NON-firing: x is RETURNED from mk (moved to caller), so mk must NOT free it.
		// Detector 0.
		{"returned-enum-not-freed-detector", `enum Shape { Circle(i32), Square(i32) } function mk(): Shape { var x = Circle(7); return x; } function main(): i32 { var s = mk(); var total = 0; match (s) { Circle(r) => { total = r; }, Square(w) => { total = w; }, } if (total != 7) { return 99; } return __rc_underflow(); }`, 0},
		// NON-firing: a string-payload (non-scalar) variant means the box would need a
		// per-variant deep-drop, so the consumed-free does NOT fire (left to leak).
		// Detector 0.
		{"nonscalar-enum-not-freed-detector", `enum Tok { Word(string), Num(i32) } function main(): i32 { var x = Num(7); var total = 0; match (x) { Word(s) => { total = s.len(); }, Num(n) => { total = n; }, } if (total != 7) { return 99; } return __rc_underflow(); }`, 0},
		// NON-firing: x is used AFTER the match (`snd(x)`), so it is not dead — must
		// NOT be freed. Detector 0.
		{"enum-used-after-match-not-freed-detector", `enum Shape { Circle(i32), Square(i32) } function snd(s: Shape): i32 { return 1; } function main(): i32 { var x = Circle(7); var total = 0; match (x) { Circle(r) => { total = r; }, Square(w) => { total = w; }, } var g = snd(x); if (total + g != 8) { return 99; } return __rc_underflow(); }`, 0},
		// Enum-donor cross-reuse (FBIP): a consumed-and-dead scalar-enum box donated
		// to a LATER same-size full struct construction instead of being freed. Here
		// the variant `A(i32, i32)` box (3 words: shape + 2 payloads) is the donor and
		// `W { p, q }` (3 words: shape + 2 fields) is the recipient — same field count,
		// so W reuses x's box in place (shape rewritten to W, fields written, x's slot
		// zeroed). The post-match free of x is SUPPRESSED (the reuse consumes the box).
		// Value correct, detector 0 (box freed exactly once, through y).
		{"enum-donor-reuse-value", `enum E { A(i32, i32), B(i32, i32) } struct W { p: i32, q: i32 } function main(): i32 { var x = A(10, 20); var t = 0; match (x) { A(a, b) => { t = a + b; }, B(c, d) => { t = c - d; }, } var y = W { p: 3, q: 4 }; return t + y.p + y.q; }`, 37},
		{"enum-donor-reuse-detector", `enum E { A(i32, i32), B(i32, i32) } struct W { p: i32, q: i32 } function main(): i32 { var x = A(10, 20); var t = 0; match (x) { A(a, b) => { t = a + b; }, B(c, d) => { t = c - d; }, } var y = W { p: 3, q: 4 }; var s = t + y.p + y.q; if (s != 37) { return 99; } return __rc_underflow(); }`, 0},
		// Recipient with a leak-safe array field: the donor's reused box becomes W;
		// only the fresh `[7, 8]` array allocates (the box itself does not). The donor's
		// old scalar slots hold no rc value to release. Array deep-dropped once at y's
		// reclamation. Value correct, detector 0.
		{"enum-donor-reuse-array-field-value", `enum E { A(i32, i32), B(i32, i32) } struct W { p: i32, xs: i32[] } function main(): i32 { var x = A(10, 20); var t = 0; match (x) { A(a, b) => { t = a + b; }, B(c, d) => { t = c - d; }, } var y = W { p: 5, xs: [7, 8] }; return t + y.p + y.xs[0] + y.xs[1]; }`, 50},
		{"enum-donor-reuse-array-field-detector", `enum E { A(i32, i32), B(i32, i32) } struct W { p: i32, xs: i32[] } function main(): i32 { var x = A(10, 20); var t = 0; match (x) { A(a, b) => { t = a + b; }, B(c, d) => { t = c - d; }, } var y = W { p: 5, xs: [7, 8] }; var s = t + y.p + y.xs[0] + y.xs[1]; if (s != 50) { return 99; } return __rc_underflow(); }`, 0},
		// NON-firing (different size): the donor box is 3 words (shape + 2 payloads)
		// but `W { p, q, r }` is 4 words (shape + 3 fields), so no donation — x is freed
		// after its match and W gets a fresh box. Value correct, detector 0.
		{"enum-donor-no-reuse-diff-size-detector", `enum E { A(i32, i32), B(i32, i32) } struct W { p: i32, q: i32, r: i32 } function main(): i32 { var x = A(10, 20); var t = 0; match (x) { A(a, b) => { t = a + b; }, B(c, d) => { t = c - d; }, } var y = W { p: 3, q: 4, r: 5 }; var s = t + y.p + y.q + y.r; if (s != 42) { return 99; } return __rc_underflow(); }`, 0},
		// NON-firing (enum used after the match): x is matched twice, so it is NOT dead
		// after the first match — the consumed-free never classifies it, hence it is not
		// a donor either. W gets a fresh box. Value correct, detector 0.
		{"enum-donor-no-reuse-used-after-detector", `enum E { A(i32, i32), B(i32, i32) } struct W { p: i32, q: i32 } function main(): i32 { var x = A(10, 20); var t = 0; match (x) { A(a, b) => { t = a + b; }, B(c, d) => { t = c - d; }, } var y = W { p: 3, q: 4 }; var u = 0; match (x) { A(a, b) => { u = a; }, B(c, d) => { u = c; }, } var s = t + y.p + y.q + u; if (s != 47) { return 99; } return __rc_underflow(); }`, 0},
		// NON-firing (recipient escapes): the recipient y is returned (moved to the
		// caller). Donation is still sound (the box is now owned by the caller via y;
		// x's slot is zeroed so no double-free), but more importantly the value is
		// correct and the detector reads 0.
		{"enum-donor-reuse-recipient-escapes-detector", `enum E { A(i32, i32), B(i32, i32) } struct W { p: i32, q: i32 } function mk(): W { var x = A(10, 20); var t = 0; match (x) { A(a, b) => { t = a + b; }, B(c, d) => { t = c - d; }, } var y = W { p: t, q: 4 }; return y; } function main(): i32 { var w = mk(); if (w.p + w.q != 34) { return 99; } return __rc_underflow(); }`, 0},
		// RC-PAYLOAD enum free (deep-drop at the free site). The widened consumed-enum
		// free admits enums whose variants carry an RC-TRACKED payload that a single
		// __fern_rc_dec fully releases — a leak-safe scalar array (i32[]/i64[]/f64[]/
		// boolean[]) — as long as no match arm BINDS the rc payload. At the free, a
		// per-variant variant_is dispatch deep-drops exactly that variant's rc array
		// fields BEFORE the box dec, so the array is freed exactly once (no leak, no
		// double-free). (A `string` payload is NOT eligible: on this IR path a string
		// is a header-less {ptr,len} fat struct that is leak-only — never freed — so a
		// string-payload enum is classified by NEITHER free path and its box leaks;
		// those cases below confirm the detector stays 0 anyway.)
		//
		// FIRING: a leak-safe array-payload enum (i32[]) whose match IGNORES the rc
		// payload (binds `_`), consumed-and-dead. The variant_is(V) deep-drop dec's the
		// array field, then the box. Value correct, detector 0 (array freed once).
		{"rc-enum-arr-free-value", `enum E { V(i32[]), N } function go(): i32 { var x = V([1, 2, 3]); var r = 0; match (x) { V(_) => { r = 5; }, N => { r = 2; }, } return r; } function main(): i32 { return go(); }`, 5},
		{"rc-enum-arr-free-detector", `enum E { V(i32[]), N } function go(): i32 { var x = V([1, 2, 3]); var r = 0; match (x) { V(_) => { r = 5; }, N => { r = 2; }, } if (r != 5) { return 99; } return __rc_underflow(); } function main(): i32 { return go(); }`, 0},
		// FIRING (multi-variant): both variants carry an i32[] payload. The free emits a
		// variant_is dispatch per rc-bearing variant; only the live variant's array is
		// dec'd at runtime. Match binds neither payload. Value correct, detector 0.
		{"rc-enum-multi-variant-arr-detector", `enum E { A(i32[]), B(i32[]) } function go(): i32 { var x = B([4, 5]); var r = 0; match (x) { A(_) => { r = 1; }, B(_) => { r = 2; }, } if (r != 2) { return 99; } return __rc_underflow(); } function main(): i32 { return go(); }`, 0},
		// FIRING (f64[] payload widening): a leak-safe f64[] array payload is deep-dropped
		// the same way (flat 8-byte-element rc box). Value correct, detector 0.
		{"rc-enum-f64arr-free-detector", `enum E { V(f64[]), N } function go(): i32 { var x = V([1.5, 2.5]); var r = 0; match (x) { V(_) => { r = 7; }, N => { r = 2; }, } if (r != 7) { return 99; } return __rc_underflow(); } function main(): i32 { return go(); }`, 0},
		// FIRING (arm BINDS the rc payload, BORROW-ONLY): the `V(a)` arm NAMES the array
		// payload but uses it only as a borrow read (`a[0]`), which cannot outlive the
		// arm. The bind-but-no-escape widening of match_arm_binds_rc_payload (via the
		// precise-drop body_unsafe_for escape check) ADMITS this — the box is deep-dropped
		// at the free AFTER the match. Value correct, detector 0 (array released once).
		{"rc-enum-arm-binds-payload-borrow-detector", `enum E { V(i32[]), N } function go(): i32 { var x = V([10, 20, 30]); var r = 0; match (x) { V(a) => { r = a[0]; }, N => { r = 0; }, } if (r != 10) { return 99; } return __rc_underflow(); } function main(): i32 { return go(); }`, 0},
		// FIRING (borrow-only over MULTIPLE reads): the bound `a` is read twice (`a[0]`,
		// `a[1]`) plus a `.len()` borrow — all reads, no escape. Admitted; value correct,
		// detector 0 (deep-dropped once at the free).
		{"rc-enum-arm-binds-payload-borrow-value", `enum E { V(i32[]), N } function go(): i32 { var x = V([10, 20, 30]); var r = 0; match (x) { V(a) => { var n: i32 = a.len(); r = a[0] + a[1] + n; }, N => { r = 0; }, } return r; } function main(): i32 { return go(); }`, 33},
		// NON-FIRING (arm binds payload that ESCAPES): the `V(a)` arm stores the bound
		// array into the outer `out`, which is read AFTER the match — the bound payload
		// outlives the match, so the bind-but-no-escape gate REJECTS it (body_unsafe_for
		// flags the `out = a` store + later read). Falls back to the exit sweep (which
		// sweeps the live array exactly once). Value correct, detector 0 (no over-release).
		{"rc-enum-arm-binds-payload-escape-detector", `enum E { V(i32[]), N } function go(): i32 { var x = V([10, 20, 30]); var out: i32[] = []; match (x) { V(a) => { out = a; }, N => {}, } var r: i32 = out[0]; if (r != 10) { return 99; } return __rc_underflow(); } function main(): i32 { return go(); }`, 0},
		// NON-FIRING (rc payload used after match): x is matched twice, so it is NOT dead
		// after the first match — the consumed-free never classifies it; the box (and its
		// array) stays live, freed by the exit sweep. Value correct, detector 0.
		{"rc-enum-used-after-match-detector", `enum E { V(i32[]), N } function go(): i32 { var x = V([3, 4]); var a = 0; match (x) { V(_) => { a = 1; }, N => { a = 0; }, } var b = 0; match (x) { V(_) => { b = 2; }, N => { b = 0; }, } if (a + b != 3) { return 99; } return __rc_underflow(); } function main(): i32 { return go(); }`, 0},
		// In-arm consuming-match box reuse (FBIP), the marquee "functional but in-place"
		// win: `var y = match (x) { V(a, b) => W(a+1, b+1), W(c, d) => V(c, d) }` where x
		// is a fresh, sole-owner, dead-after, non-escaping ALL-SCALAR enum box and EVERY
		// arm constructs a SAME-SIZE scalar variant. x's box is reused IN PLACE
		// (op_struct_set_shape + field writes) to build y instead of allocating a fresh
		// box per arm — one fewer __fern_arr_box per arm. y owns the box on every path,
		// freed exactly once at its reclamation (no post-match free exists for an in-arm
		// scrutinee). Read-before-overwrite is structural: each arm reads its i32 payloads
		// into temps BEFORE the box is re-shaped. The V arm fires (x is a V box → reshaped
		// to W in place). Value W(4,5) → 4+5 = 9; detector 0 (box freed once).
		{"inarm-reuse-value", `enum E { V(i32, i32), W(i32, i32) } function go(): i32 { var x = V(3, 4); var y = match (x) { V(a, b) => W(a + 1, b + 1), W(c, d) => V(c, d) }; var r = match (y) { V(a, b) => a + b, W(c, d) => c + d }; return r; } function main(): i32 { return go(); }`, 9},
		{"inarm-reuse-detector", `enum E { V(i32, i32), W(i32, i32) } function go(): i32 { var x = V(3, 4); var y = match (x) { V(a, b) => W(a + 1, b + 1), W(c, d) => V(c, d) }; var r = match (y) { V(a, b) => a + b, W(c, d) => c + d }; if (r != 9) { return 99; } return __rc_underflow(); } function main(): i32 { return go(); }`, 0},
		// FIRING + heap-reuse corruption probe: allocate FRESH arrays AFTER the reusing
		// match and read them back. If the in-place reuse corrupted the heap (e.g. wrote
		// past the reused box, or freed it early so a later alloc recycled it under y),
		// the fresh array's contents would be wrong. Both the match result (r == 9) and
		// the fresh array (11+22+33 == 66) read back correctly; detector 0.
		{"inarm-reuse-corruption-probe-detector", `enum E { V(i32, i32), W(i32, i32) } function go(): i32 { var x = V(3, 4); var y = match (x) { V(a, b) => W(a + 1, b + 1), W(c, d) => V(c, d) }; var r = match (y) { V(a, b) => a + b, W(c, d) => c + d }; var fresh = [11, 22, 33]; var s = fresh[0] + fresh[1] + fresh[2]; if (r != 9) { return 90; } if (s != 66) { return 91; } return __rc_underflow(); } function main(): i32 { return go(); }`, 0},
		// NON-FIRING (different size): the matched variant V is 2-field but an arm
		// constructs a 1-field variant `W(a + b)`, so the box sizes differ — reuse must
		// NOT fire (x is freed/leaks via the normal path, each arm allocates a fresh
		// result box). Value W(7) → 7; detector 0.
		{"inarm-no-reuse-diff-size-detector", `enum E { V(i32, i32), W(i32) } function go(): i32 { var x = V(3, 4); var y = match (x) { V(a, b) => W(a + b), W(c) => V(c, c) }; var r = match (y) { V(a, b) => a + b, W(c) => c }; if (r != 7) { return 99; } return __rc_underflow(); } function main(): i32 { return go(); }`, 0},
		// NON-FIRING (x escapes / used after match): x is passed to a function AFTER the
		// reusing match, so it is NOT dead at the match site — reuse must NOT fire (x's
		// box stays live; each arm allocates a fresh result box). Value 9 + 1 = 10;
		// detector 0.
		{"inarm-no-reuse-escapes-detector", `enum E { V(i32, i32), W(i32, i32) } function use_x(e: E): i32 { return 1; } function go(): i32 { var x = V(3, 4); var y = match (x) { V(a, b) => W(a + 1, b + 1), W(c, d) => V(c, d) }; var g = use_x(x); var r = match (y) { V(a, b) => a + b, W(c, d) => c + d }; if (r + g != 10) { return 99; } return __rc_underflow(); } function main(): i32 { return go(); }`, 0},
		// WIDENED PAYLOAD WIDTHS (FBIP in-arm reuse now admits all i32-shaped scalars
		// — i32 / u32 / boolean — plus the 8-byte i64 and f64, not just i32). Each
		// FIRING `var y = match (x) { ... }` reuses x's box in place: payloads are read
		// into temps at their width (struct_get_i64 / struct_get width-64 for the 8-byte
		// cases, marked i64/f64 so the arm's ctor-arg expressions type correctly) and
		// the constructed fields written back at width (lower_i64+struct_set_i64 for i64,
		// lower_expr+struct_set width-64 for f64). The reuse dispatch intercepts the
		// firing IIFE match-EXPRESSION directly (it does NOT go through lower_iife_match's
		// i32-only gate). The result y is read back via a STATEMENT match (the generic
		// enum path handles i64/f64 payloads; the match-EXPRESSION form is still
		// i32-only, an orthogonal IR-subset gap), so the only width-sensitive lowering
		// under test is the reuse itself. Value correct + detector 0 (box freed once via y).
		// FIRING i64: V(i64,i64)→W(a+1,b+1); W(4,5) read back → 4+5 = 9.
		{"inarm-reuse-i64-value", `enum E { V(i64, i64), W(i64, i64) } function go(): i32 { var x = V(3, 4); var y = match (x) { V(a, b) => W(a + 1, b + 1), W(c, d) => V(c, d) }; var r: i64 = 0; match (y) { V(a, b) => { r = a + b; }, W(c, d) => { r = c + d; } } return r as i32; } function main(): i32 { return go(); }`, 9},
		{"inarm-reuse-i64-detector", `enum E { V(i64, i64), W(i64, i64) } function go(): i32 { var x = V(3, 4); var y = match (x) { V(a, b) => W(a + 1, b + 1), W(c, d) => V(c, d) }; var r: i64 = 0; match (y) { V(a, b) => { r = a + b; }, W(c, d) => { r = c + d; } } if (r as i32 != 9) { return 99; } return __rc_underflow(); } function main(): i32 { return go(); }`, 0},
		// FIRING i64 corruption probe: fresh array allocated AFTER the reusing match
		// reads back intact (reuse didn't clobber the heap / free early).
		{"inarm-reuse-i64-corruption-probe-detector", `enum E { V(i64, i64), W(i64, i64) } function go(): i32 { var x = V(3, 4); var y = match (x) { V(a, b) => W(a + 1, b + 1), W(c, d) => V(c, d) }; var r: i64 = 0; match (y) { V(a, b) => { r = a + b; }, W(c, d) => { r = c + d; } } var fresh = [11, 22, 33]; var s = fresh[0] + fresh[1] + fresh[2]; if (r as i32 != 9) { return 90; } if (s != 66) { return 91; } return __rc_underflow(); } function main(): i32 { return go(); }`, 0},
		// FIRING f64: V(f64,f64)→W(a+1.0,b+1.0); W(4.0,5.0) read back → 4+5 = 9.
		{"inarm-reuse-f64-value", `enum E { V(f64, f64), W(f64, f64) } function go(): i32 { var x = V(3.0, 4.0); var y = match (x) { V(a, b) => W(a + 1.0, b + 1.0), W(c, d) => V(c, d) }; var r: f64 = 0.0; match (y) { V(a, b) => { r = a + b; }, W(c, d) => { r = c + d; } } return r as i32; } function main(): i32 { return go(); }`, 9},
		{"inarm-reuse-f64-detector", `enum E { V(f64, f64), W(f64, f64) } function go(): i32 { var x = V(3.0, 4.0); var y = match (x) { V(a, b) => W(a + 1.0, b + 1.0), W(c, d) => V(c, d) }; var r: f64 = 0.0; match (y) { V(a, b) => { r = a + b; }, W(c, d) => { r = c + d; } } if (r as i32 != 9) { return 99; } return __rc_underflow(); } function main(): i32 { return go(); }`, 0},
		// FIRING boolean: V(boolean,boolean)→W(!a,!b); V(true,false) → W(false,true);
		// read back → false?10:0 + true?1:0 = 1.
		{"inarm-reuse-boolean-value", `enum E { V(boolean, boolean), W(boolean, boolean) } function go(): i32 { var x = V(true, false); var y = match (x) { V(a, b) => W(!a, !b), W(c, d) => V(c, d) }; var r = 0; match (y) { V(a, b) => { r = 0; }, W(c, d) => { if (c) { r = r + 10; } if (d) { r = r + 1; } } } return r; } function main(): i32 { return go(); }`, 1},
		{"inarm-reuse-boolean-detector", `enum E { V(boolean, boolean), W(boolean, boolean) } function go(): i32 { var x = V(true, false); var y = match (x) { V(a, b) => W(!a, !b), W(c, d) => V(c, d) }; var r = 0; match (y) { V(a, b) => { r = 0; }, W(c, d) => { if (c) { r = r + 10; } if (d) { r = r + 1; } } } if (r != 1) { return 99; } return __rc_underflow(); } function main(): i32 { return go(); }`, 0},
		// FIRING u32: V(u32,u32)→W(a+1,b+1); W(4,5) read back → 4+5 = 9 (i32-shaped).
		{"inarm-reuse-u32-detector", `enum E { V(u32, u32), W(u32, u32) } function go(): i32 { var x = V(3, 4); var y = match (x) { V(a, b) => W(a + 1, b + 1), W(c, d) => V(c, d) }; var r = 0; match (y) { V(a, b) => { r = (a + b) as i32; }, W(c, d) => { r = (c + d) as i32; } } if (r != 9) { return 99; } return __rc_underflow(); } function main(): i32 { return go(); }`, 0},
		// (u64 is deliberately NOT admitted by the in-arm reuse gate —
		// struct_field_is_i64 / struct_field_width classify a u64 field as i32-shaped,
		// so the 8-byte read/write path is never selected and the value would truncate.
		// inarm_reuse_payload_type_ok excludes it. A u64-payload program can't route the
		// IR path here at all: with reuse not firing, the firing IIFE `var y = match(x)`
		// falls to lower_iife_match, whose match-EXPRESSION binding is itself i32-only
		// — so no route=="ir" u64 case is expressible in this harness. The exclusion is
		// covered structurally by the gate + the firing i64/f64 cases above.)
		//
		// ARRAY PAYLOAD WIDENING (FBIP in-arm reuse now admits leak-safe scalar-array
		// payload fields — i32[]/i64[]/f64[]/boolean[] — alongside scalars). For each
		// RESULT array field the reuse emits a COW-GUARDED write (mirror the cross-struct
		// reuse / emit_arr_store guard): load the donor's OLD array at that slot, compare
		// to the NEW value, `if (old != new) arr_dec(old)`, store new. Two sound shapes:
		//   - MOVE: the arm moves a BOUND array payload into the same result slot
		//     (`V(a, xs) => W(a+1, xs)`). The new value IS the old array pointer (the
		//     binding temp aliases the box slot, unchanged since reshape), so old==new →
		//     NO dec → the array is reused in place (the strongest FBIP win: no arr_box
		//     for the array either).
		//   - REPLACE: the arm builds a FRESH array (`V(a, xs) => W(a, [7,8])`). old!=new
		//     → the donor's old array is dropped, the fresh one written and owned by the
		//     reused box.
		// The donor's array payloads in `var x = V(...)` must be fresh array literals
		// (sole ownership), and the per-position array-ness must match between pattern
		// and result variant; a permutation/swap or a stray borrow of the moved array is
		// gated out (see inarm_reuse_match_ok). Read-before-overwrite holds: the array
		// pointer is read into a temp at arm entry before any field write.
		//
		// FIRING MOVE: V(i32,i32[])→W(a+1, xs) — xs MOVED into the result, reused in
		// place. y read back: W's array [10,20,30] sums 60, +tag 4 = 64. Detector 0.
		{"inarm-reuse-array-move-value", `enum E { V(i32, i32[]), W(i32, i32[]) } function go(): i32 { var x = V(3, [10, 20, 30]); var y = match (x) { V(a, xs) => W(a + 1, xs), W(b, ys) => V(b, ys) }; var r = 0; match (y) { V(a, xs) => { r = a + xs[0] + xs[1] + xs[2]; }, W(c, ds) => { r = c + ds[0] + ds[1] + ds[2]; } } return r; } function main(): i32 { return go(); }`, 64},
		{"inarm-reuse-array-move-detector", `enum E { V(i32, i32[]), W(i32, i32[]) } function go(): i32 { var x = V(3, [10, 20, 30]); var y = match (x) { V(a, xs) => W(a + 1, xs), W(b, ys) => V(b, ys) }; var r = 0; match (y) { V(a, xs) => { r = a + xs[0] + xs[1] + xs[2]; }, W(c, ds) => { r = c + ds[0] + ds[1] + ds[2]; } } if (r != 64) { return 99; } return __rc_underflow(); } function main(): i32 { return go(); }`, 0},
		// FIRING MOVE + heap-reuse corruption probe: a FRESH array allocated AFTER the
		// reusing match reads back intact (the move didn't free the array early or
		// clobber the heap; a stale free would let the fresh alloc recycle y's array).
		{"inarm-reuse-array-move-corruption-probe-detector", `enum E { V(i32, i32[]), W(i32, i32[]) } function go(): i32 { var x = V(3, [10, 20, 30]); var y = match (x) { V(a, xs) => W(a + 1, xs), W(b, ys) => V(b, ys) }; var r = 0; match (y) { V(a, xs) => { r = a + xs[0] + xs[1] + xs[2]; }, W(c, ds) => { r = c + ds[0] + ds[1] + ds[2]; } } var fresh = [11, 22, 33]; var s = fresh[0] + fresh[1] + fresh[2]; if (r != 64) { return 90; } if (s != 66) { return 91; } return __rc_underflow(); } function main(): i32 { return go(); }`, 0},
		// FIRING REPLACE: the V arm builds a FRESH array result field (`W(a, [7, 8])`).
		// The donor's old array [10,20,30] is dropped (old!=new), the fresh [7,8] written
		// and owned by the reused box. y read back: tag 3 + 7 + 8 = 18. Detector 0 (old
		// array freed once, fresh array freed once — no leak, no over-release).
		{"inarm-reuse-array-replace-value", `enum E { V(i32, i32[]), W(i32, i32[]) } function go(): i32 { var x = V(3, [10, 20, 30]); var y = match (x) { V(a, xs) => W(a, [7, 8]), W(b, ys) => V(b, ys) }; var r = 0; match (y) { V(a, xs) => { r = a + xs[0] + xs[1]; }, W(c, ds) => { r = c + ds[0] + ds[1]; } } return r; } function main(): i32 { return go(); }`, 18},
		{"inarm-reuse-array-replace-detector", `enum E { V(i32, i32[]), W(i32, i32[]) } function go(): i32 { var x = V(3, [10, 20, 30]); var y = match (x) { V(a, xs) => W(a, [7, 8]), W(b, ys) => V(b, ys) }; var r = 0; match (y) { V(a, xs) => { r = a + xs[0] + xs[1]; }, W(c, ds) => { r = c + ds[0] + ds[1]; } } if (r != 18) { return 99; } return __rc_underflow(); } function main(): i32 { return go(); }`, 0},
		// FIRING REPLACE corruption probe: a fresh array after the replacing match reads
		// back intact (the dropped old array was freed exactly once; no double-free / no
		// dangling slot the fresh alloc could recycle wrongly).
		{"inarm-reuse-array-replace-corruption-probe-detector", `enum E { V(i32, i32[]), W(i32, i32[]) } function go(): i32 { var x = V(3, [10, 20, 30]); var y = match (x) { V(a, xs) => W(a, [7, 8]), W(b, ys) => V(b, ys) }; var r = 0; match (y) { V(a, xs) => { r = a + xs[0] + xs[1]; }, W(c, ds) => { r = c + ds[0] + ds[1]; } } var fresh = [11, 22, 33]; var s = fresh[0] + fresh[1] + fresh[2]; if (r != 18) { return 90; } if (s != 66) { return 91; } return __rc_underflow(); } function main(): i32 { return go(); }`, 0},
		// FIRING MOVE with a leading i64 scalar alongside the array field: the i64 scalar
		// is written 8-byte (struct_set_i64), the array MOVED via the cow-guard. Mixed
		// scalar+array reuse. y read back: (a+1) as i32 = 6 + 10+20 = 36. Detector 0.
		{"inarm-reuse-array-mixed-i64-detector", `enum E { V(i64, i32[]), W(i64, i32[]) } function go(): i32 { var x = V(5, [10, 20]); var y = match (x) { V(a, xs) => W(a + 1, xs), W(b, ys) => V(b, ys) }; var r = 0; match (y) { V(a, xs) => { r = (a as i32) + xs[0] + xs[1]; }, W(c, ds) => { r = (c as i32) + ds[0] + ds[1]; } } if (r != 36) { return 99; } return __rc_underflow(); } function main(): i32 { return go(); }`, 0},
		// (A permutation/swap `V(p, q) => V(q, p)` and an i64[]/f64[] array payload are
		// gated OUT of the firing path here: the swap is rejected by the per-position
		// move gate — the arg at slot di is bound from a DIFFERENT slot, so a cow-guard
		// would wrongly dec a still-needed array — and an i64[]/f64[] enum-payload
		// program falls outside the IR subset for an orthogonal reason (the y read-back
		// via the i32-only match-EXPRESSION / 8-byte-element enum-array path), so neither
		// routes "ir" in this harness. The swap rejection is covered structurally by
		// inarm_reuse_match_ok's per-position binding check; its non-firing is verified
		// by an arr_box-count probe in the slice report.)
		//
		// MATCH-EXPRESSION PAYLOAD-BINDING WIDENING (lower_iife_match / lower_i64):
		// a `var r = match (x) { V(a) => ... }` IIFE match-EXPRESSION that BINDS a
		// non-i32 enum payload now routes "ir" (was "ast"). The underlying StmtMatch
		// path already binds + deep-drops i64 / f64 / leak-safe-array payloads at
		// width; the IIFE gate was merely over-conservative (i32-only). Admitted:
		//   - i64 / f64 payload returned DIRECTLY (`V(a) => a`) — the result temp is
		//     classified to the matching width from the variant's payload field type
		//     (iife_payload_result_width), since the bare ident isn't in scope to
		//     infer at classify time. An annotated `var r: i64 =` binding routes
		//     through lower_i64, which also admits the payload-i64 IIFE match now.
		//   - a scalar-element array payload (i32[]/boolean[]) bound and read as a
		//     BORROW (`V(xs) => xs[0]` / `xs.len()`) — result stays i32; the array
		//     isn't returned bare (that would need an array temp the IIFE doesn't
		//     classify, so it stays gated to AST). The 8-byte-element arrays
		//     (i64[]/f64[]) are NOT admitted — the StmtMatch payload-binding mark
		//     doesn't carry the element width, so an element read would mis-width
		//     (the statement-match path has the same limitation); they stay on AST.
		// i64 payload returned directly into an i64 result temp; value (7) correct.
		{"iife-match-i64-payload-value", `enum E { V(i64), W(i64) } function main(): i32 { var x: E = E.V(7); var r: i64 = match (x) { V(a) => a, W(b) => b }; return r as i32; }`, 7},
		// f64 payload returned directly into an f64 result temp; value (3) correct.
		{"iife-match-f64-payload-value", `enum E { V(f64), W(f64) } function main(): i32 { var x: E = E.V(3.5); var r: f64 = match (x) { V(a) => a, W(b) => b }; return r as i32; }`, 3},
		// i32[] payload bound, read element [0]; result is i32 (the element).
		{"iife-match-arr-payload-elem-value", `enum E { V(i32[]), W(i32[]) } function main(): i32 { var x: E = E.V([42, 7]); var r = match (x) { V(xs) => xs[0], W(ys) => ys[0] }; return r; }`, 42},
		// i32[] payload bound, read .len() (single-expr arm); result is i32.
		{"iife-match-arr-payload-len-value", `enum E { V(i32[]), W(i32[]) } function main(): i32 { var x: E = E.V([1, 2, 3]); var r = match (x) { V(xs) => xs.len(), W(ys) => ys.len() }; return r; }`, 3},
		// i32[] payload BOUND but unused (arm returns a literal); still routes "ir".
		{"iife-match-arr-payload-unused-value", `enum E { V(i32[]), W(i32[]) } function main(): i32 { var x: E = E.V([1, 2]); var r = match (x) { V(xs) => 5, W(ys) => 9 }; return r; }`, 5},
		// Leak-safety detector: the array payload's box is bound + read through the
		// IIFE path then dropped; the over-release detector reads 0 (freed exactly
		// once — no leak, no double-free).
		{"iife-match-arr-payload-detector", `enum E { V(i32[]), W(i32[]) } function go(): i32 { var x: E = E.V([10, 20, 30]); var r = match (x) { V(xs) => xs[0], W(ys) => ys[0] }; if (r != 10) { return 99; } return __rc_underflow(); } function main(): i32 { return go(); }`, 0},
		// boolean[] payload bound, read .len() (single-expr arm); result is i32.
		{"iife-match-boolarr-payload-len-value", `enum E { V(boolean[]), N } function main(): i32 { var x: E = E.V([true, false, true]); var r = match (x) { V(xs) => xs.len(), N => 0 }; return r; }`, 3},
		// STRUCT / STRING / TUPLE / ENUM payload BORROW widening (lower_iife_match's
		// iife_payload_borrow_i32): a `var r = match (e) { V(p) => p.x, ... }` IIFE
		// match-EXPRESSION binding a STRUCT / STRING / TUPLE / nominal-ENUM payload now
		// routes "ir" (was "ast") WHEN the arm reads it as a borrow whose result is
		// provably i32 (`p.x` of an i32 field, `s.len()`, `t.0` of an i32 element, an
		// enum method returning i32, `s[i]` char code, or an i32 COMPOSITION of these).
		// The default i32 result temp then holds the right value; a non-i32 borrow
		// result (string field / i64 element / bare payload) stays gated to AST (its
		// temp would mis-classify at the i32 width inferred before the binding is in
		// scope). The structural classifier proves i32 without a trial scope, recovering
		// each leaf's type directly from the payload type. The underlying StmtMatch path
		// already binds + leak-drops these payloads at width.
		// Struct payload read by an i32 FIELD: result is the i32 field value.
		{"iife-match-struct-payload-field-value", `struct P { x: i32, y: i32 } enum E { V(P), N } function main(): i32 { var e: E = E.V(P { x: 5, y: 9 }); var r = match (e) { V(p) => p.x + p.y, N => 0 }; return r; }`, 14},
		// String payload read by .len(): result is the i32 length.
		{"iife-match-string-payload-len-value", `enum E { V(string), N } function main(): i32 { var e: E = E.V("hello"); var r = match (e) { V(s) => s.len(), N => 0 }; return r; }`, 5},
		// String payload indexed `s[i]`: result is the i32 char code ('H' = 72).
		{"iife-match-string-payload-index-value", `enum E { V(string), N } function main(): i32 { var e: E = E.V("Hi"); var r = match (e) { V(s) => s[0], N => 0 }; return r; }`, 72},
		// Tuple payload read by ELEMENT `t.0` / `t.1` (i32 elements): result is i32.
		{"iife-match-tuple-payload-elem-value", `enum E { V((i32, i32)), N } function main(): i32 { var e: E = E.V((3, 4)); var r = match (e) { V(t) => t.0 + t.1, N => 0 }; return r; }`, 7},
		// Enum payload with a METHOD returning i32 (`t.sum()`): result is i32 — the
		// method's i32 return is recovered by exclusion from the wider-result registries.
		{"iife-match-enum-payload-method-value", `enum Tree { Leaf(i32), Node(Tree, Tree) } function (t: Tree) sum(): i32 { return match (t) { Leaf(v) => v, Node(l, r) => l.sum() + r.sum() } } enum E { V(Tree), N } function main(): i32 { var tr: Tree = Tree.Leaf(7); var e: E = E.V(tr); var r = match (e) { V(t) => t.sum(), N => 0 }; return r; }`, 7},
		// Leak-safety detector: the STRING payload box is bound + borrow-read through the
		// IIFE path then leaked-with-the-enum; the over-release detector reads 0 (the
		// borrowed payload is never double-freed through the IIFE path).
		{"iife-match-struct-payload-detector", `struct P { x: i32 } enum E { V(P), N } function go(): i32 { var e: E = E.V(P { x: 42 }); var r = match (e) { V(p) => p.x, N => 0 }; if (r != 42) { return 99; } return __rc_underflow(); } function main(): i32 { return go(); }`, 0},
		// Leak-safety detector for the STRING-payload borrow (the box is reclaimed
		// exactly once — no leak, no double-free through the inlined match path).
		{"iife-match-string-payload-detector", `enum E { V(string), N } function go(): i32 { var e: E = E.V("world"); var r = match (e) { V(s) => s.len(), N => 0 }; if (r != 5) { return 99; } return __rc_underflow(); } function main(): i32 { return go(); }`, 0},
		// ARROW LAMBDA (`(params): R => expr`) — the self-host parser now parses the
		// concise arrow form into the SAME ExprLambda the verbose `function (params):
		// R { return expr; }` produces, so it rides the existing lambda-lift + IR
		// lowering with no codegen changes. Each case routes "ir" and is oracle-checked
		// against the native interpreter. Previously the self-host parser had NO arrow
		// syntax at all, so these mis-parsed and bailed to the AST emitter.
		// Non-capturing binding, called once: __lam_N(5) = 6.
		{"arrow-lambda-noncap-binding", `function main(): i32 { var f = (x: i32): i32 => x + 1; return f(5); }`, 6},
		// Capturing binding (captures outer `n`): param-lifted with n threaded as an
		// argument — (5)+10 = 15.
		{"arrow-lambda-capturing-binding", `function main(): i32 { var n: i32 = 10; var f = (x: i32): i32 => x + n; return f(5); }`, 15},
		// Multi-param arrow: add(3, 4) = 7.
		{"arrow-lambda-multi-param", `function main(): i32 { var add = (a: i32, b: i32): i32 => a + b; return add(3, 4); }`, 7},
		// Empty-param arrow: () => 42 → 42.
		{"arrow-lambda-empty-params", `function main(): i32 { var f = (): i32 => 42; return f(); }`, 42},
		// No-capture arrow passed as a CALL ARGUMENT — hoisted to a __lam_N function
		// value (the slice-1 const_func path): ap((y) => y*2, 5) = 10.
		{"arrow-lambda-as-arg", `function ap(f: (i32) => i32, x: i32): i32 { return f(x); } function main(): i32 { return ap((y: i32): i32 => y * 2, 5); }`, 10},
		// Verbose `function (...)` and arrow forms must still BOTH route ir — a
		// grouping `(a + b)` next to an arrow lambda confirms the lookahead doesn't
		// mis-class an ordinary parenthesised expression: (3 + 4) + ((x) => x)(1) = 8.
		{"arrow-lambda-vs-grouping", `function main(): i32 { var a: i32 = 3; var b: i32 = 4; var id = (x: i32): i32 => x; return (a + b) + id(1); }`, 8},

		// VALUE-PRODUCING `.with` (this slice). `a.with(i, v)` in expression position
		// is a FRESH clone of `a` with element i replaced — `a` is UNMUTATED. The
		// oracle reads BOTH `b[1]` (the replaced value 99) AND `a[1]` (the original 2):
		// if `.with` mutated `a` in place, `a[1]` would be 99 and the sum 198, not 101.
		// 99 + 2 = 101 proves the clone is independent. (Detector cases below confirm
		// no over-release.)
		{"with-value-expr-unmutated", `function main(): i32 { var a = [1, 2, 3]; var b = a.with(1, 99); return b[1] + a[1]; }`, 101},
		{"with-value-expr-detector", `function main(): i32 { var a = [1, 2, 3]; var b = a.with(1, 99); if (b[1] + a[1] != 101) { return 99; } return __rc_underflow(); }`, 0},
		// Whole clone is correct: sum of b (1+99+3) plus a unchanged (1+2+3) = 103+6.
		{"with-value-expr-full", `function main(): i32 { var a = [1, 2, 3]; var b = a.with(1, 99); var sb = b[0] + b[1] + b[2]; var sa = a[0] + a[1] + a[2]; return sb + sa; }`, 109},
		// i64[] / f64[] element clones.
		{"with-value-i64-detector", `function main(): i32 { var a: i64[] = [10, 20, 30]; var b = a.with(0, 100); if ((b[0] + a[0]) as i32 != 110) { return 99; } return __rc_underflow(); }`, 0},
		{"with-value-f64-detector", `function main(): i32 { var a: f64[] = [1.5, 2.5, 3.0]; var b = a.with(2, 9.0); if (((b[2] + a[2]) as i32) != 12) { return 99; } return __rc_underflow(); }`, 0},

		// VALUE-PRODUCING `.append` in expression position: clones the receiver then
		// grows the clone — the receiver is UNMUTATED. b has 4 elements (1,2,3,4),
		// a still has 3 (1,2,3): b[3] + a.len() = 4 + 3 = 7.
		{"append-value-field-detector", `struct Buf { xs: i32[], n: i32 } function main(): i32 { var s = Buf { xs: [1, 2, 3], n: 3 }; var t = Buf { xs: s.xs.append(4), n: s.n + 1 }; var r = t.xs[3] + s.xs.len() + t.n; if (r != 11) { return 99; } return __rc_underflow(); }`, 0},

		// STRUCT-FIELD `.with` — the headline BAIL→ir flip: the pervasive immutable-
		// update idiom `State { xs: s.xs.with(i, v), n: s.n }`. The base `s.xs` field
		// is cloned (borrowed for the copy, not aliased), so the new struct owns a
		// fresh array with NO alias-inc. `t.xs[1]` reads 99, `s.xs[1]` still reads 2
		// (the original struct's array is unmutated): 99 + 2 = 101.
		{"struct-field-with-value", `struct State { xs: i32[], n: i32 } function main(): i32 { var s = State { xs: [1, 2, 3], n: 3 }; var t = State { xs: s.xs.with(1, 99), n: s.n }; return t.xs[1] + s.xs[1]; }`, 101},
		{"struct-field-with-detector", `struct State { xs: i32[], n: i32 } function main(): i32 { var s = State { xs: [1, 2, 3], n: 3 }; var t = State { xs: s.xs.with(1, 99), n: s.n }; if (t.xs[1] + s.xs[1] != 101) { return 99; } return __rc_underflow(); }`, 0},
		// Read back the whole updated struct: t.xs = [1,99,3] (sum 103) + t.n 3 = 106;
		// s unchanged: s.xs sum 6 + s.n 3 = 9. Total 115.
		{"struct-field-with-full", `struct State { xs: i32[], n: i32 } function main(): i32 { var s = State { xs: [1, 2, 3], n: 3 }; var t = State { xs: s.xs.with(1, 99), n: s.n }; var st = t.xs[0] + t.xs[1] + t.xs[2] + t.n; var ss = s.xs[0] + s.xs[1] + s.xs[2] + s.n; return st + ss; }`, 115},
		// STRUCT-FIELD `.append`: `State { xs: s.xs.append(v), n: s.n + 1 }`. t.xs has
		// 4 elements; s.xs still 3. t.xs sum (1+2+3+4)=10 + t.n 4 = 14; s.xs sum 6 +
		// s.n 3 = 9 → 23.
		{"struct-field-append-value", `struct State { xs: i32[], n: i32 } function main(): i32 { var s = State { xs: [1, 2, 3], n: 3 }; var t = State { xs: s.xs.append(4), n: s.n + 1 }; var st = t.xs[0] + t.xs[1] + t.xs[2] + t.xs[3] + t.n; var ss = s.xs[0] + s.xs[1] + s.xs[2] + s.n; return st + ss; }`, 23},
		{"struct-field-append-detector", `struct State { xs: i32[], n: i32 } function main(): i32 { var s = State { xs: [1, 2, 3], n: 3 }; var t = State { xs: s.xs.append(4), n: s.n + 1 }; if (t.xs[3] != 4) { return 98; } if (s.xs.len() != 3) { return 97; } return __rc_underflow(); }`, 0},
		// i64[]-field and f64[]-field struct updates.
		{"struct-field-with-i64-detector", `struct State { xs: i64[], n: i32 } function main(): i32 { var s = State { xs: [10, 20, 30], n: 3 }; var t = State { xs: s.xs.with(0, 100), n: s.n }; if (((t.xs[0] + s.xs[0]) as i32) != 110) { return 99; } return __rc_underflow(); }`, 0},
		{"struct-field-with-f64-detector", `struct State { xs: f64[], n: i32 } function main(): i32 { var s = State { xs: [1.5, 2.5, 3.0], n: 3 }; var t = State { xs: s.xs.with(2, 9.0), n: s.n }; if ((((t.xs[2] + s.xs[2]) as i32)) != 12) { return 99; } return __rc_underflow(); }`, 0},
		// Chained immutable updates: t = update(s), u = update(t). Each clones; all
		// three structs independent. u.xs = [1,99,3] then [1,99,42]; s/t unmutated.
		{"struct-field-with-chain-detector", `struct State { xs: i32[], n: i32 } function main(): i32 { var s = State { xs: [1, 2, 3], n: 3 }; var t = State { xs: s.xs.with(1, 99), n: s.n }; var u = State { xs: t.xs.with(2, 42), n: t.n }; if (u.xs[1] != 99) { return 98; } if (u.xs[2] != 42) { return 97; } if (s.xs[1] != 2) { return 96; } if (t.xs[2] != 3) { return 95; } return __rc_underflow(); }`, 0},

		// STRING-RESULT match-EXPRESSION payload widening (lower_iife_match /
		// iife_payload_result_kind + iife_payload_string_bindable): a `var s = match
		// (e) { V(x) => x, ... }` IIFE that RETURNS a string value derived from the
		// bound payload now routes "ir" (was "ast"). The result temp can't be
		// classified by expr_is_str at classify time (the binding isn't in scope), so
		// the string KIND is recovered from the variant's payload field type — a BARE
		// string payload (`V(x) => x`), a string struct/enum FIELD (`V(p) => p.name`),
		// or a string tuple ELEMENT (`V(t) => t.0`) — and the temp is marked a string
		// slot so the StmtMatch arm-body store/load is a string pointer. (An i64/f64
		// field/element result still bails to AST — the wide-field StmtMatch store
		// doesn't round-trip; only the BARE i64/f64 payload is admitted, as before.)
		// Each case oracle-checks the result string's length as the exit code.
		// Bare string payload returned: result is the 5-char payload "hello".
		{"iife-match-string-bare-payload-value", `enum E { V(string), N } function main(): i32 { var e: E = E.V("hello"); var s: string = match (e) { V(x) => x, N => "z" }; return s.len(); }`, 5},
		// The non-payload arm (literal "zz") is taken: result is the 2-char "zz".
		{"iife-match-string-bare-payload-other-arm", `enum E { V(string), N } function main(): i32 { var e: E = E.N; var s: string = match (e) { V(x) => x, N => "zz" }; return s.len(); }`, 2},
		// Struct string FIELD read: result is the 5-char field value "world".
		{"iife-match-string-struct-field-value", `struct P { name: string } enum E { V(P), N } function main(): i32 { var e: E = E.V(P { name: "world" }); var s: string = match (e) { V(p) => p.name, N => "zz" }; return s.len(); }`, 5},
		// Tuple string ELEMENT read (`t.0`): result is the 3-char element "hey".
		{"iife-match-string-tuple-elem-value", `enum E { V((string, i32)), N } function main(): i32 { var e: E = E.V(("hey", 3)); var s: string = match (e) { V(t) => t.0, N => "z" }; return s.len(); }`, 3},
		// Leak-safety detector: the string-payload box is bound + returned through the
		// IIFE path; the over-release detector reads 0 (the result string is reclaimed
		// exactly once — no leak, no double-free through the inlined match path).
		{"iife-match-string-bare-payload-detector", `enum E { V(string), N } function go(): i32 { var e: E = E.V("hello"); var s: string = match (e) { V(x) => x, N => "z" }; if (s.len() != 5) { return 99; } return __rc_underflow(); } function main(): i32 { return go(); }`, 0},
		// Struct-string-field result detector: the borrowed string field is returned
		// and reclaimed exactly once (no over-release through the IIFE path).
		{"iife-match-string-struct-field-detector", `struct P { name: string } enum E { V(P), N } function go(): i32 { var e: E = E.V(P { name: "world" }); var s: string = match (e) { V(p) => p.name, N => "zz" }; if (s.len() != 5) { return 99; } return __rc_underflow(); } function main(): i32 { return go(); }`, 0},
		// String-payload CONCAT result (`V(x) => x + "!"`): the bound string payload is
		// read BORROW-ONLY inside a `+` that allocates a fresh string. The result temp
		// classifies str (expr_is_str sees the string literal operand); the concat
		// borrow gate (iife_string_concat_borrow_only) admits the borrow-only read.
		// Result "hi!" has length 3.
		{"iife-match-string-concat-suffix-value", `enum E { V(string), N } function main(): i32 { var e: E = E.V("hi"); var s: string = match (e) { V(x) => x + "!", N => "z" }; return s.len(); }`, 3},
		// Prefix concat (`"k=" + x`): the literal is the LEFT operand; the payload the
		// right. Result "k=hi" has length 4.
		{"iife-match-string-concat-prefix-value", `enum E { V(string), N } function main(): i32 { var e: E = E.V("hi"); var s: string = match (e) { V(x) => "k=" + x, N => "z" }; return s.len(); }`, 4},
		// Prefix + suffix (`">" + x + "<"`): a nested `+` tree, every payload-mentioning
		// leaf a borrow-only read. Result ">hi<" has length 4.
		{"iife-match-string-concat-wrap-value", `enum E { V(string), N } function main(): i32 { var e: E = E.V("hi"); var s: string = match (e) { V(x) => ">" + x + "<", N => "z" }; return s.len(); }`, 4},
		// Struct string-FIELD concat (`V(p) => p.name + "!"`): the borrow leaf is a
		// string field read of the leak-safe struct payload. Result "world!" length 6.
		{"iife-match-string-concat-struct-field-value", `struct P { name: string } enum E { V(P), N } function main(): i32 { var e: E = E.V(P { name: "world" }); var s: string = match (e) { V(p) => p.name + "!", N => "zz" }; return s.len(); }`, 6},
		// Tuple string-ELEMENT concat (`V(t) => t.0 + "!"`): the borrow leaf is a
		// string tuple-element read. Result "hey!" length 4.
		{"iife-match-string-concat-tuple-elem-value", `enum E { V((string, i32)), N } function main(): i32 { var e: E = E.V(("hey", 3)); var s: string = match (e) { V(t) => t.0 + "!", N => "z" }; return s.len(); }`, 4},
		// The non-payload arm of a concat match is taken: the result is the N-arm
		// literal "none" (length 4), unaffected by the V-arm concat shape.
		{"iife-match-string-concat-other-arm", `enum E { V(string), N } function main(): i32 { var e: E = E.N; var s: string = match (e) { V(x) => x + "!", N => "none" }; return s.len(); }`, 4},
		// Over-release detector for the concat shape: the borrowed payload is READ (a
		// borrow) and the concat allocates a FRESH string bound to s — reclaimed exactly
		// once. The detector reads 0 (no leak, no double-free of the payload or result).
		{"iife-match-string-concat-detector", `enum E { V(string), N } function go(): i32 { var e: E = E.V("hi"); var s: string = match (e) { V(x) => x + "!", N => "z" }; if (s.len() != 3) { return 99; } return __rc_underflow(); } function main(): i32 { return go(); }`, 0},
		// Struct-field concat detector: the borrowed string field feeds a fresh concat;
		// the field box and the result are each accounted exactly once (detector 0).
		{"iife-match-string-concat-struct-field-detector", `struct P { name: string } enum E { V(P), N } function go(): i32 { var e: E = E.V(P { name: "world" }); var s: string = match (e) { V(p) => p.name + "!", N => "zz" }; if (s.len() != 6) { return 99; } return __rc_underflow(); } function main(): i32 { return go(); }`, 0},
		// COMPOSITE match-EXPRESSION result (the IIFE composite-result slice). A
		// match-expression whose result is a struct / nominal-enum / tuple value
		// returned WHOLE — a bare bound payload `V(q) => q` and/or a fresh same-type
		// construction in another arm — now types the result temp + the bound local
		// so a later `p.field` / `match (p)` / `p.0` resolves on the IR path. Each
		// arm must agree on the composite type (mismatched arms still bail to AST).
		//
		// BARE STRUCT payload returned whole, used via `p.field`. The leak-safe
		// struct pointer rides one slot; the temp + `p` are marked struct P.
		{"iife-match-struct-payload-field-value", `struct P { x: i32 } enum E { V(P), N } function main(): i32 { var e: E = E.V(P { x: 7 }); var p: P = match (e) { V(q) => q, N => P { x: 0 } }; return p.x; }`, 7},
		// The OTHER (constructor) arm is taken: the N-arm builds a fresh P, same type.
		{"iife-match-struct-payload-other-arm", `struct P { x: i32 } enum E { V(P), N } function main(): i32 { var e: E = E.N; var p: P = match (e) { V(q) => q, N => P { x: 42 } }; return p.x; }`, 42},
		// UNANNOTATED struct binding, payload arm FIRST (`iife_leaf_value` sees a bare
		// ident not in scope, so `p` is typed via the composite-result fallback).
		{"iife-match-struct-payload-unannotated-value", `struct P { x: i32 } enum E { V(P), N } function main(): i32 { var e: E = E.V(P { x: 5 }); var p = match (e) { V(q) => q, N => P { x: 0 } }; return p.x; }`, 5},
		// Over-release detector: the struct payload is BORROWED from the enum box (a
		// leak-only struct, never reclaimed), the constructed N-arm struct leaks too —
		// neither over-releases. Detector reads 0.
		{"iife-match-struct-payload-detector", `struct P { x: i32 } enum E { V(P), N } function go(): i32 { var e: E = E.V(P { x: 7 }); var p: P = match (e) { V(q) => q, N => P { x: 0 } }; if (p.x != 7) { return 99; } return __rc_underflow(); } function main(): i32 { return go(); }`, 0},
		// BARE ENUM payload returned whole, used via a nested `match (p)`. The inner
		// enum pointer rides one slot; the temp + `p` are marked enum Inner.
		{"iife-match-enum-payload-match-value", `enum Inner { A(i32), B } enum Outer { W(Inner), Z } function main(): i32 { var o: Outer = Outer.W(Inner.A(5)); var p: Inner = match (o) { W(q) => q, Z => Inner.B }; return match (p) { A(n) => n, B => 99 }; }`, 5},
		// The Z-arm constructs a fresh Inner.B of the same enum type; nested match on it.
		{"iife-match-enum-payload-other-arm", `enum Inner { A(i32), B } enum Outer { W(Inner), Z } function main(): i32 { var o: Outer = Outer.Z; var p: Inner = match (o) { W(q) => q, Z => Inner.B }; return match (p) { A(n) => n, B => 8 }; }`, 8},
		// Enum-payload over-release detector: enum boxes are leak-only on this IR path
		// (the exit sweep never frees them), so neither the borrowed payload nor the
		// constructed arm over-releases. Detector reads 0.
		{"iife-match-enum-payload-detector", `enum Inner { A(i32), B } enum Outer { W(Inner), Z } function go(): i32 { var o: Outer = Outer.W(Inner.A(5)); var p: Inner = match (o) { W(q) => q, Z => Inner.B }; var r = match (p) { A(n) => n, B => 99 }; if (r != 5) { return 99; } return __rc_underflow(); } function main(): i32 { return go(); }`, 0},
		// BARE TUPLE payload returned whole, used via `p.0` / `p.1`. The tuple pointer
		// rides one slot; the temp + `p` are marked with the element tags.
		{"iife-match-tuple-payload-elem-value", `enum E { V((i32, i32)), N } function main(): i32 { var e: E = E.V((3, 4)); var p: (i32, i32) = match (e) { V(q) => q, N => (0, 0) }; return p.0 + p.1; }`, 7},
		// UNANNOTATED tuple binding (the element tags come from the composite-result
		// fallback, payload arm first).
		{"iife-match-tuple-payload-unannotated-value", `enum E { V((i32, i32)), N } function main(): i32 { var e: E = E.V((10, 20)); var p = match (e) { V(q) => q, N => (0, 0) }; return p.0 + p.1; }`, 30},
		// Tuple-payload over-release detector: the borrowed tuple is leak-only; neither
		// arm over-releases. Detector reads 0.
		{"iife-match-tuple-payload-detector", `enum E { V((i32, i32)), N } function go(): i32 { var e: E = E.V((3, 4)); var p: (i32, i32) = match (e) { V(q) => q, N => (0, 0) }; if (p.0 + p.1 != 7) { return 99; } return __rc_underflow(); } function main(): i32 { return go(); }`, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			route := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, []byte(tc.src))))
			if route != "ir" {
				t.Errorf("%s routed through %q path, want \"ir\"", tc.name, route)
			}
			if got := run(t, emit(t, tc.src)); got != tc.expected {
				t.Errorf("precise-drop x86 IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}

	// (The former `field-with-call-stays-ast` negative is now obsolete: a
	// `.with(i,v)` / `.append(v)` array-field value lowers via IR through the
	// value-producing clone path added in this slice — covered by the
	// `struct-field-with-*` / `struct-field-append-*` positive cases above.)
}
