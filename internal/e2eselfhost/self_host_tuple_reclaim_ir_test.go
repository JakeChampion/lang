package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// tupleReclaimIRCases pin the per-iteration reclamation of fresh, non-escaping
// SCALAR tuple locals in loops on the self-hosted stack-IR path. Classifying
// only all-LITERAL tuples (`(3, 4)`) as reclaimable leaks one box per iteration
// for the common `(i, 1)` loop temporary (a variable element) — the
// native backend reclaims it, the self-host did not. The fix classifies a fresh
// tuple as reclaimable when its binding annotation is an all-scalar tuple type
// (tuple_type_is_all_scalar: i32 / i64 / f64 / u32 / u64 / boolean — by-value, no
// aliasable pointer), and the loop-rebind release (emit_arr_store) now covers
// reclaimable tuples, so its box is freed each iteration (a SHALLOW box release,
// matching the leak-mode element contract).
//
// Two contracts per case:
//   - exit code pins VALUE correctness (a double-free would corrupt / crash);
//   - reclaimAssert pins the EMISSION: each program uses only tuples, so a
//     `call __fn___fern_arr_dec` (the shallow box release) is the per-iteration
//     tuple reclaim — required (>=1) or forbidden (0, escaping) as noted.
var tupleReclaimIRCases = []struct {
	name        string
	src         string
	expected    int
	mustReclaim bool
}{
	// Loop-body scalar tuple with a variable element: reclaimed each iteration.
	// sum over i in 0..3 of (i + 1) = 1+2+3+4 = 10.
	{"loop-body-scalar-tuple",
		`function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 4) { var a: (i32, i32) = (i, 1); sum = sum + a.0 + a.1; i = i + 1; } return sum; }`,
		10, true},
	// Scalar tuple in a NESTED if inside a loop: reclaimed each time the arm runs.
	// sum over i in 1..3 of (i + 1) = 2+3+4 = 9.
	{"nested-if-scalar-tuple",
		`function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 4) { if (i > 0) { var a: (i32, i32) = (i, 1); sum = sum + a.0 + a.1; } i = i + 1; } return sum; }`,
		9, true},
	// Mixed i64 / f64 scalar tuple: the wide (8-byte) elements are still by-value,
	// so the box is reclaimed. sum over i in 0..3 of (5 + 2) = 28.
	{"i64-f64-scalar-tuple",
		`function main(): i32 { var sum: i64 = 0; var i: i32 = 0; while (i < 4) { var a: (i64, f64) = (5, 2.0); sum = sum + a.0 + (a.1 as i64); i = i + 1; } return sum as i32; }`,
		28, true},
	// Memory-safety at scale: 5,000,000 iterations of a scalar-tuple loop. A leaked
	// box per iteration would exhaust the heap; a double-free would crash. exit 0
	// (sum kept mod 1000) with the reclaim present proves the balance.
	{"scalar-tuple-churn-safe",
		`function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 5000000) { var a: (i32, i32) = (i, 1); sum = (sum + a.0 + a.1) % 1000; i = i + 1; } return sum; }`,
		0, true},
	// UN-ANNOTATED scalar tuple (`var a = (i, 1)`, inferred type): reclaimed too —
	// the reclaimability check now accepts number / boolean / IDENT elements (a
	// SHALLOW box free never touches them), not just all-literal tuples, so the
	// annotation is no longer required. sum over i in 0..3 of (i + 1) = 10.
	{"unannotated-scalar-tuple",
		`function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 4) { var a = (i, 1); sum = sum + a.0 + a.1; i = i + 1; } return sum; }`,
		10, true},
	// Un-annotated churn at scale: the inferred `(i, 1)` reclaims per iteration
	// (flat heap), exit 0.
	{"unannotated-scalar-tuple-churn-safe",
		`function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 5000000) { var a = (i, 1); sum = (sum + a.0 + a.1) % 1000; i = i + 1; } return sum; }`,
		0, true},
}

// TestSelfHostTupleReclaimIRX86_64 compiles each case through the self-hosted
// x86-64 driver (asm_run, IR default-on), asserting the exit code and that the
// per-iteration tuple-box reclaim is (or isn't) emitted.
func TestSelfHostTupleReclaimIRX86_64(t *testing.T) {
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

	for _, tc := range tupleReclaimIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			// Tuple-only programs: a `call __fn___fern_arr_dec` (the shallow box
			// release) is the per-iteration tuple reclaim. The bare label
			// `__fn___fern_arr_dec:` (the helper definition) is not a call.
			reclaims := bytes.Count(asm, []byte("call __fn___fern_arr_dec"))
			if tc.mustReclaim && reclaims == 0 {
				t.Errorf("%s: expected a per-iteration tuple reclaim (call __fn___fern_arr_dec), found none — the tuple leaks", tc.name)
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
