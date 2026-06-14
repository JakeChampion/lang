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
}
