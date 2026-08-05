package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// tupleReuseIRCases exercise Perceus-style FBIP constructor reuse for tuples on
// the self-hosted stack-IR path (irlower `cross_tuple_reuse_sites` /
// `emit_cross_tuple_reuse`). When a fresh, same-arity tuple literal is built into
// a local while an earlier tuple local is dead at that point, the new tuple
// REUSES the dead donor's heap box in place (writing each element via
// `op_tuple_set` / `_w` at its width) instead of allocating a fresh box — the
// donor slot is zeroed and its box handed over, so no `call __fern_arr_box` is
// emitted for the reused tuple construction. Element coverage matches the tuple
// CONSTRUCTOR (i32 / i64 / f64 scalars plus leak-mode pointer elements: string,
// struct, array, …), each stored at its width (uniform 8-byte movq on the
// register backends; i64.store / f64.store / i32.store on wasm).
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
	// i64 elements: the donor box is overwritten with 8-byte i64 stores (i64.store
	// on wasm; movq on the register backends). Reuse fires ⇒ ONE tuple box. Value:
	// (5+7) + (30+20) = 62. A truncated store (i32.store) would corrupt the read.
	{"reuse-i64",
		`function main(): i32 { var a: (i64, i64) = (5, 7); var s: i64 = a.0 + a.1; var b: (i64, i64) = (30, 20); return (s + b.0 + b.1) as i32; }`,
		62, 1},
	// f64 elements: overwritten with 8-byte f64 stores. Reuse fires ⇒ ONE tuple
	// box. Value: (1.5+2.5) + (10.0+3.0) = 17.0 → 17.
	{"reuse-f64",
		`function main(): i32 { var a: (f64, f64) = (1.5, 2.5); var s: f64 = a.0 + a.1; var b: (f64, f64) = (10.0, 3.0); return (s + b.0 + b.1) as i32; }`,
		17, 1},
	// String (pointer) element: the donor box's pointer slot is overwritten with
	// b's string pointer. String literals are interned (no arr_box), so the only
	// allocation is the ONE reused tuple box. Value: a.1(5) → s=5; b=("yo",9),
	// 5 + 9 + len("yo")=2 = 16.
	{"reuse-string",
		`function main(): i32 { var a: (string, i32) = ("hi", 5); var s: i32 = a.1; var b: (string, i32) = ("yo", 9); return s + b.1 + b.0.len(); }`,
		16, 1},
	// Struct (pointer) element: the two struct literals each allocate their own box
	// (2), and the tuple box is REUSED (1, not 2) — so THREE boxes total, proving
	// the tuple-box reuse fires while the struct elements construct normally. Value:
	// a.1(5) → s=5; b.0=P{10,20}, b.1=9; 5 + 10 + 20 + 9 = 44.
	// `* k` (k == 1, values unchanged) keeps the two struct ELEMENTS off the
	// STATIC-CONSTANT path (#6149): placed in data they allocate nothing, and the
	// case would count 1 box instead of 3 — no longer showing that the elements
	// construct normally while the tuple box is reused.
	{"reuse-struct-elem",
		`struct P { x: i32, y: i32 } function main(): i32 { var k: i32 = 1; var a: (P, i32) = (P { x: 1 * k, y: 2 }, 5); var s: i32 = a.1; var b: (P, i32) = (P { x: 10 * k, y: 20 }, 9); return s + b.0.x + b.0.y + b.1; }`,
		44, 3},
	// Loop body: reuse now fires INSIDE the loop body too (irlower
	// lower_loop_body) — `b` reuses the dead `a`'s box every iteration, so the loop
	// allocates ONE tuple box, not two. This pins the loop-body reuse and its value
	// correctness: sum over i in 0..3 of ((i)+(i+1)) + (i)+(i*2):
	// i=0: (0+1)+(0+0)=1; i=1: (1+2)+(1+2)=6; i=2: (2+3)+(2+4)=11;
	// i=3: (3+4)+(3+6)=16; total = 34. (Dedicated loop-reuse coverage — including
	// memory-safety at 5M iterations — lives in self_host_loop_reuse_ir_test.go.)
	{"loop-body-reuse-fires",
		`function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 4) { var a: (i32, i32) = (i, i + 1); var s: i32 = a.0 + a.1; var b: (i32, i32) = (i, i * 2); sum = sum + s + b.0 + b.1; i = i + 1; } return sum; }`,
		34, 1},
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
			boxes := countUserArrBoxAllocs(asm)
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
