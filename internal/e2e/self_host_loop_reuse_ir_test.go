package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// loopReuseIRCases exercise Perceus FBIP constructor reuse fired INSIDE a LOOP
// BODY on the self-hosted stack-IR path (irlower `lower_loop_body`). Native's
// high-value same-block reuse pass reclaims loop-churn allocations: a fresh
// struct / tuple construction that reuses an earlier dead same-block donor's box
// fires PER ITERATION (zero-alloc each turn) instead of only at the function-body
// top level. The recipient slot is loop-carried, so the reuse emitter releases
// its prior (previous-iteration) box (`emit_reuse_recip_prior_release`) before the
// in-place overwrite — one alloc + one free per iteration, balanced.
//
// Two contracts per case:
//   - exit code pins VALUE correctness (a reuse that mis-stored, double-freed, or
//     stranded a live read would corrupt or crash);
//   - boxAssert pins the EMISSION contract: a reuse loop allocates ONE tuple/struct
//     box (the per-iteration donor, handed to the reuser), a donor-live control
//     allocates TWO. A regression to no-loop-reuse bumps the reuse cases to 2.
var loopReuseIRCases = []struct {
	name      string
	src       string
	expected  int
	boxAssert int // exact `call __fern_arr_box` count in the program
}{
	// Struct loop reuse: `a` is dead by the time `b` is built each iteration, so b
	// reuses a's box in place — ONE allocation. sum over i in 0..3 of
	// (i + (i+1)) + (i*2 + 3): i=0:1+3=4; i=1:3+5=8; i=2:5+7=12; i=3:7+9=16 = 40.
	{"loop-struct-reuse",
		`struct P { x: i32, y: i32 } function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 4) { var a: P = P { x: i, y: i + 1 }; var s: i32 = a.x + a.y; var b: P = P { x: i * 2, y: 3 }; sum = sum + s + b.x + b.y; i = i + 1; } return sum; }`,
		40, 1},
	// Struct donor-live control: `a` is read AFTER `b` is built, so reuse is
	// suppressed and both allocate (TWO boxes). Same value shape: sum over i in
	// 0..3 of (i + (i+1)) + (i + 3) = 4+... i=0:1+3=4;i=1:3+4=7;i=2:5+5=10;i=3:7+6=13 = 34.
	{"loop-struct-donor-live",
		`struct P { x: i32, y: i32 } function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 4) { var a: P = P { x: i, y: i + 1 }; var b: P = P { x: i, y: 3 }; sum = sum + a.x + a.y + b.x + b.y; i = i + 1; } return sum; }`,
		34, 2},
	// Tuple loop reuse: b reuses a's box in place each iteration — ONE allocation.
	// sum over i in 0..3 of (i + (i+1)) + (i + 3) = 34.
	{"loop-tuple-reuse",
		`function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 4) { var a: (i32, i32) = (i, i + 1); var s: i32 = a.0 + a.1; var b: (i32, i32) = (i, 3); sum = sum + s + b.0 + b.1; i = i + 1; } return sum; }`,
		34, 1},
	// Tuple donor-live control: no reuse, TWO boxes.
	{"loop-tuple-donor-live",
		`function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 4) { var a: (i32, i32) = (i, i + 1); var b: (i32, i32) = (i, 3); sum = sum + a.0 + a.1 + b.0 + b.1; i = i + 1; } return sum; }`,
		34, 2},
	// Memory-safety at scale: five million iterations of struct loop reuse. A
	// per-iteration double-free would crash (SIGSEGV) and a leaked recipient box
	// would exhaust the heap; the exit code 0 (sum kept mod 1000 = 0) with a
	// single static allocation proves the per-iteration alloc/free stays balanced.
	{"loop-struct-churn-safe",
		`struct P { x: i32, y: i32 } function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 5000000) { var a: P = P { x: i, y: i + 1 }; var s: i32 = a.x + a.y; var b: P = P { x: i, y: 3 }; sum = (sum + b.x + b.y) % 1000; i = i + 1; } return sum; }`,
		0, 1},
	// Functional-update (self-overwrite) reuse in a loop: `c = P { ...d, y: 3 }`
	// reuses the dead `d`'s box in place each iteration — ONE allocation. The
	// immutable-state-threading loop shape. sum over i in 0..3 of (d.x=i) + 3 =
	// (0+3)+(1+3)+(2+3)+(3+3) = 18.
	{"loop-funcupdate-reuse",
		`struct P { x: i32, y: i32 } function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 4) { var d: P = P { x: i, y: 0 }; var c: P = P { ...d, y: 3 }; sum = sum + c.x + c.y; i = i + 1; } return sum; }`,
		18, 1},
	// Functional-update memory safety at scale: 5M iterations, balanced alloc/free
	// (the recipient's prior box freed each turn), exit 0 (sum mod 1000).
	{"loop-funcupdate-churn-safe",
		`struct P { x: i32, y: i32 } function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 5000000) { var d: P = P { x: i, y: 0 }; var c: P = P { ...d, y: 3 }; sum = (sum + c.x + c.y) % 1000; i = i + 1; } return sum; }`,
		0, 1},
}

// TestSelfHostLoopReuseIRX86_64 compiles each case through the self-hosted x86-64
// driver (asm_run, IR default-on), asserting the exit code and the exact box
// allocation count (the loop-reuse emission contract).
func TestSelfHostLoopReuseIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	for _, tc := range loopReuseIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			boxes := bytes.Count(asm, []byte("call __fern_arr_box"))
			if boxes != tc.boxAssert {
				t.Errorf("%s: expected %d box allocations (call __fern_arr_box), found %d — loop-reuse emission contract regressed", tc.name, tc.boxAssert, boxes)
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}
