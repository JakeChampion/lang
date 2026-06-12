package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// structRCIRCases exercise the conservative escape-gated struct reclamation on
// the stack-IR path: a fresh, non-escaping struct local (only field-read) is
// freed at scope exit / loop-rebind via a shallow rc-dec of its box
// (`call __fn___fern_arr_dec`, the generic size-classed box release), while a
// struct that ESCAPES — returned, or passed as a call argument — is left to leak
// so its box never dangles. The earlier shallow-RC slice freed escaping boxes
// and use-after-free'd the self-compile; these cases pin that:
//   - exit codes pin VALUE correctness (a double-free / use-after-free corrupts);
//   - freeAssert pins the EMISSION contract (no array appears in any case, so any
//     `call __fn___fern_arr_dec` is a struct free): +1 requires ≥1 struct free
//     (can't regress to leak-only), -1 requires zero (the escape case can't
//     regress to a spurious free → double-free).
var structRCIRCases = []struct {
	name      string
	src       string
	expected  int
	freeAssert int // +1: must free a struct; -1: must NOT free any struct; 0: don't check
}{
	// Loop-rebind: a fresh struct built each iteration, only field-read, is freed
	// each iteration (the prior box released on rebind) and at exit. sum over
	// i in 0..4 of (i + (i+1)) = 1+3+5+7+9 = 25.
	{"loop-rebind-reclaimed",
		`struct P { x: i32, y: i32 } function main(): i32 { var sum: i32 = 0; var i: i32 = 0; while (i < 5) { var p: P = P { x: i, y: i + 1 }; sum = sum + p.x + p.y; i = i + 1; } return sum; }`,
		25, 1},
	// Single fresh struct, only field-read: freed once at scope exit.
	{"single-reclaimed",
		`struct P { x: i32, y: i32 } function main(): i32 { var p: P = P { x: 30, y: 12 }; return p.x + p.y; }`,
		42, 1},
	// Escape via call argument: p is passed to sumit (borrowed by the callee), so
	// p escapes and is NOT reclaimed — zero struct frees. A regression that freed
	// it here would double-free (p is freed by main AND lives through the call).
	// total = (0+10)+(1+10)+(2+10) = 33.
	{"escape-call-arg-not-freed",
		`struct P { x: i32, y: i32 } function sumit(p: P): i32 { return p.x + p.y; } function main(): i32 { var total: i32 = 0; var i: i32 = 0; while (i < 3) { var p: P = P { x: i, y: 10 }; total = total + sumit(p); i = i + 1; } return total; }`,
		33, -1},
	// Escape via return: make() returns its fresh local, so it is NOT freed in
	// make (the caller's reference survives); main's q is call-bound (not a fresh
	// literal) so also not reclaimed. A premature free in make would corrupt q.
	// 7 + 9 = 16.
	{"escape-return-survives",
		`struct P { x: i32, y: i32 } function make(): P { var p: P = P { x: 7, y: 9 }; return p; } function main(): i32 { var q: P = make(); return q.x + q.y; }`,
		16, 0},
	// Escape via struct-literal field value: q is built into r (a field value), so
	// q escapes and is not reclaimed (no dangling field). r is returned → escapes
	// too. Value: 5 + 8 = 13. (Both structs leak — safe.)
	{"escape-struct-field-value",
		`struct Inner { a: i32 } struct Outer { v: i32, w: i32 } function main(): i32 { var q: Inner = Inner { a: 5 }; var r: Outer = Outer { v: q.a, w: 8 }; return r.v + r.w; }`,
		13, 0},
}

// TestSelfHostStructRCIRX86_64 compiles each case through the self-hosted x86-64
// driver (asm_run, IR default-on), asserts the exit code, and (per freeAssert)
// asserts the struct-free emission contract.
func TestSelfHostStructRCIRX86_64(t *testing.T) {
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

	for _, tc := range structRCIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			// No case uses an array, so every `call __fn___fern_arr_dec` is a
			// struct-box release; the bare `__fn___fern_arr_dec:` label (the
			// runtime helper definition) is always present and not counted.
			frees := bytes.Count(asm, []byte("call __fn___fern_arr_dec"))
			switch {
			case tc.freeAssert > 0 && frees == 0:
				t.Errorf("%s: expected a struct free (call __fn___fern_arr_dec), found none — regressed to leak-only", tc.name)
			case tc.freeAssert < 0 && frees != 0:
				t.Errorf("%s: expected NO struct free (escaping struct), found %d — escape gate regressed (double-free risk)", tc.name, frees)
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
