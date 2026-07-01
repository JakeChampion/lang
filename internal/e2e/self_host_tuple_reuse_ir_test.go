package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// tupleReuseIRCases exercise Perceus-style FBIP constructor reuse for tuples on
// the self-hosted stack-IR path (irlower `cross_tuple_reuse_sites` /
// `emit_cross_tuple_reuse`). When a fresh, same-arity, all-i32 tuple literal is
// built into a local while an earlier tuple local is dead at that point, the new
// tuple REUSES the dead donor's heap box in place (writing each element via
// `op_tuple_set`) instead of allocating a fresh box — the donor slot is zeroed
// and its box handed over, so no `call __fern_arr_box` is emitted for the
// reused construction.
//
// Two contracts are pinned per case:
//   - exit code pins VALUE correctness: the reused box must be OVERWRITTEN with
//     the new tuple's elements. A reuse that forgot to store would leave the
//     donor's stale values and corrupt the result; a double-free / dangling
//     handover would crash. The donor-live and arity-mismatch cases pin that
//     reuse is correctly SUPPRESSED (writing a 3-tuple into a 2-slot donor box
//     would overflow the heap; taking over a still-live donor would corrupt it).
//   - boxAssert pins the EMISSION contract. Every case uses only tuples, so each
//     `call __fern_arr_box` is a tuple allocation. A reuse-firing case allocates
//     exactly ONE box (the donor's, later handed to the reuser); the no-reuse
//     cases allocate TWO. A regression to leak-only (never reuse) would bump the
//     reuse case to 2; a regression to reuse-when-unsafe would drop a no-reuse
//     case to 1 (with a value corruption / overflow to match).
var tupleReuseIRCases = []struct {
	name      string
	src       string
	expected  int
	boxAssert int // exact `call __fern_arr_box` count expected in the program
}{
	// Reuse fires: `a` is dead after `s` is computed, so the fresh same-arity
	// all-i32 tuple `b` reuses a's box in place. Only ONE box is allocated. The
	// value pins that b's box was overwritten: s = 5+7 = 12, b = (2,3), so
	// 12 + 2 + 3 = 17. A reuse that failed to store would read a's stale (5,7)
	// and yield 12 + 5 + 7 = 24.
	{"reuse-fires",
		`function main(): i32 { var a: (i32, i32) = (5, 7); var s: i32 = a.0 + a.1; var b: (i32, i32) = (2, 3); return s + b.0 + b.1; }`,
		17, 1},
	// Donor still live: `a` is read AFTER `b` is constructed, so a is NOT a valid
	// donor and reuse must be suppressed — both tuples allocate (TWO boxes). A
	// spurious reuse here would hand a's box to b and corrupt the later a.0 / a.1
	// reads. Value: (5+7) + (2+3) = 17.
	{"donor-live-no-reuse",
		`function main(): i32 { var a: (i32, i32) = (5, 7); var b: (i32, i32) = (2, 3); return a.0 + a.1 + b.0 + b.1; }`,
		17, 2},
	// Arity mismatch: `a` is a 2-tuple (2-slot box), `b` is a 3-tuple. Even though
	// a is dead, reuse must be suppressed — writing three elements into a's
	// two-slot box would overflow the heap. Both allocate (TWO boxes). Value:
	// (5+7) + (1+2+3) = 18.
	{"arity-mismatch-no-reuse",
		`function main(): i32 { var a: (i32, i32) = (5, 7); var s: i32 = a.0 + a.1; var b: (i32, i32, i32) = (1, 2, 3); return s + b.0 + b.1 + b.2; }`,
		18, 2},
	// Loop body: reuse is a function-body-level rewrite, so it does not fire
	// inside a loop body — each iteration allocates both tuples (TWO static box
	// call sites). This pins that the loop path is unaffected and stays correct:
	// sum over i in 0..3 of ((i)+(i+1)) + (i)+(i*2) = (1+3+5+7) ... expanded:
	// i=0: (0+1)+(0+0)=1; i=1: (1+2)+(1+2)=6; i=2: (2+3)+(2+4)=11;
	// i=3: (3+4)+(3+6)=16; total = 34.
	{"loop-body-value-correct",
		`function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 4) { var a: (i32, i32) = (i, i + 1); var s: i32 = a.0 + a.1; var b: (i32, i32) = (i, i * 2); sum = sum + s + b.0 + b.1; i = i + 1; } return sum; }`,
		34, 2},
}

// TestSelfHostTupleReuseIRX86_64 compiles each case through the self-hosted
// x86-64 driver (asm_run, IR default-on), asserts the exit code, and asserts the
// exact tuple-box allocation count (the reuse emission contract).
func TestSelfHostTupleReuseIRX86_64(t *testing.T) {
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

	for _, tc := range tupleReuseIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			// Every case uses only tuples, so each `call __fern_arr_box` (the raw
			// runtime allocator symbol) is a tuple-box allocation. The
			// `__fern_arr_box:` definition label is not a call and is not counted.
			boxes := bytes.Count(asm, []byte("call __fern_arr_box"))
			if boxes != tc.boxAssert {
				t.Errorf("%s: expected %d tuple-box allocations (call __fern_arr_box), found %d — reuse emission contract regressed", tc.name, tc.boxAssert, boxes)
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
