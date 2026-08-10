package e2eselfhost

import (
	"testing"
)

// TestSelfHostOuterWriteCaptureIRX86_64 pins the capture clause where the
// LAMBDA ONLY READS and the OUTER body reassigns afterwards (#6539).
//
// The language's answer is written down rather than inferred: captures are "by
// value at creation" but "scalar captures are copied and mutable"
// (docs/LANGUAGE-REVIEW-2026-07.md), so the closure must observe the value at
// CALL time. `box_mutated_scalar_captures` delivers that by rewriting the
// capture to a shared cell.
//
// That pass has two arms, and only one of them was covered before:
//
//   - #2850 / SH-057 — the LAMBDA writes the capture. Pinned by
//     TestSelfHostMutableScalarCaptureInterp, every case of which writes from
//     inside the lambda.
//   - #5394 — the lambda only reads; the OUTER body reassigns. That is these
//     cases, and nothing exercised it.
//
// The pass scanned `fd.body` alone and never descended into a loop / if / match
// body, so the in-loop binding below — much the commoner spelling — was never
// seen and kept its construction-time snapshot, returning 0.
//
// `toplevel` is the CONTROL and the reason this is a reach fix rather than a
// broken cell rewrite: the same binding over the same capture, one block
// shallower, was already correct. Without it the natural conclusion is that the
// cell machinery is wrong, which points the fix somewhere far more invasive.
//
// These deliberately do NOT live in the two neighbouring capture suites:
//
//   - TestSelfHostMutableScalarCaptureInterp drives the self-host INTERPRETER,
//     which does not implement the #5394 arm at all — both shapes fail there,
//     the top-level control included. That is a separate gap, filed as #6578.
//   - TestSelfHostCaptureLambdaX86IR requires every case to lift to `__lam_0`.
//     The in-loop lambda is stored into an array, so it becomes a `__mkclo$`
//     closure box instead and trips that precondition while computing the right
//     answer.
func TestSelfHostOuterWriteCaptureIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range []struct {
		name string
		src  string
		want int
	}{
		// CONTROL: top-level binding. Correct before the fix and after.
		{
			"toplevel",
			`function main(): i32 { var i: i32 = 0; var f: (i32) => i32 = ((x: i32) => x + i); i = 3; return f(0); }`,
			3,
		},
		// The regression: the identical binding one block deeper.
		{
			"in-loop",
			`function main(): i32 { var fs: ((i32) => i32)[] = []; var i: i32 = 0; while (i < 3) { var g: (i32) => i32 = ((x: i32) => x + i); fs = fs.append(g); i = i + 1; } return (fs[0])(0); }`,
			3,
		},
		// Two blocks deep, and through an `if` rather than a loop — the walk has
		// to recurse generally, not special-case `while`.
		{
			"nested-if-in-loop",
			`function main(): i32 { var fs: ((i32) => i32)[] = []; var i: i32 = 0; while (i < 2) { if (i >= 0) { var g: (i32) => i32 = ((x: i32) => x + i); fs = fs.append(g); } i = i + 1; } return (fs[0])(0); }`,
			2,
		},
		// OPERAND POSITIONS. The two shapes above are `var g = <lambda>`, which
		// the scan matches because the init IS a lambda. These are lambdas the
		// init merely CONTAINS — a struct-literal field, a call argument — and
		// they were invisible at any depth, top level included, until the scan
		// looked inside operands. `struct-literal-field` is this issue's
		// original reproducer.
		{
			"struct-literal-field",
			`struct C { f: (i32) => i32, n: i32 } function main(): i32 { var cs: C[] = []; var i: i32 = 0; while (i < 2) { cs = cs.append(C { f: ((x: i32) => x + i), n: i }); i = i + 1; } return (cs[0].f)(0); }`,
			2,
		},
		{
			"call-argument",
			`function take(f: (i32) => i32): i32 { return f(0); } function main(): i32 { var i: i32 = 0; var fs: ((i32) => i32)[] = []; fs = fs.append(((x: i32) => x + i)); i = 4; return take(fs[0]); }`,
			4,
		},
		// A read-only capture nothing ever reassigns must stay unboxed and keep
		// answering with the value it closed over — the guard against widening
		// the scan into boxing every capture it can now see.
		{
			"readonly-capture-control",
			`function main(): i32 { var fs: ((i32) => i32)[] = []; var k: i32 = 5; var i: i32 = 0; while (i < 2) { var g: (i32) => i32 = ((x: i32) => x + k); fs = fs.append(g); i = i + 1; } return (fs[0])(0); }`,
			5,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, nil)
			progBin := buildBin(t, gcc, dir, "outerwrite_"+tc.name, asm)
			_, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Errorf("%s: exited %d, want %d — a mutated scalar capture must be read at "+
					"CALL time, not snapshotted when the closure is built", tc.name, exit, tc.want)
			}
		})
	}
}
