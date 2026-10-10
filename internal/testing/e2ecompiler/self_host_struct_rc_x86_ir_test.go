package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// structRCIRCases exercise borrow-aware struct reclamation: a fresh struct local
// used only by field reads, method-receiver borrows, and call args to callees
// that only read it is freed at scope exit / loop-rebind via a shallow rc-dec
// of its box (`call __fn___fern_arr_dec`, the generic size-classed box
// release), while a struct that ESCAPES — returned, or stored into a container
// / struct-literal field — is not freed, so its box never dangles. These cases
// pin that:
//   - exit codes pin VALUE correctness (a double-free / use-after-free corrupts);
//   - freeAssert pins the EMISSION contract: +1 requires a struct free,
//     -1 requires none for a static struct. The runtime-input case checks only
//     probe's body, excluding argv allocation and cleanup in main.
var structRCIRCases = []struct {
	name       string
	src        string
	expected   int
	freeAssert int    // +1: must free a struct; -1: must NOT free any struct; 0: don't check
	body       string // empty: whole assembly; otherwise isolate this function
}{
	// Loop-rebind: a fresh struct built each iteration, only field-read, is freed
	// each iteration (the prior box released on rebind) and at exit. sum over
	// i in 0..4 of (i + (i+1)) = 1+3+5+7+9 = 25.
	{"loop-rebind-reclaimed",
		`struct P { x: i32, y: i32 } function main(): i32 { let sum: i32 = 0; let i: i32 = 0; while (i < 5) { let p: P = P { x: i, y: i + 1 }; sum = sum + p.x + p.y; i = i + 1; } return sum; }`,
		25, 1, ""},
	// A constant struct is static data and needs no release.
	{"single-reclaimed",
		`struct P { x: i32, y: i32 } function main(): i32 { let p: P = P { x: 30, y: 12 }; return p.x + p.y; }`,
		42, -1, "main"},
	// The same field reads with runtime data allocate and release the box.
	// args includes the program name, so n=30 preserves the original values.
	{"single-runtime-reclaimed",
		`struct P { x: i32, y: i32 } @noinline function probe(n: i32): i32 { let p: P = P { x: n, y: 12 }; return p.x + p.y; } function main(): i32 { return probe(args().len() + 29); }`,
		42, 1, "probe"},
	// Borrow via call argument: p is passed to sumit, which only FIELD-READS it
	// (`return p.x + p.y`) and so has a BORROWABLE param — passing p there is a
	// borrow, not an escape. p is sole-owner, dead after the call, and the callee
	// does not retain it, so it is freed once at loop-rebind/scope-exit (#3456).
	// The value stays correct (no double-free: sumit never frees its borrowed
	// param), which pins soundness: total = (0+10)+(1+10)+(2+10) = 33, and a
	// struct free is REQUIRED (regressing to leak-only would drop it).
	{"borrow-call-arg-reclaimed",
		`struct P { x: i32, y: i32 } function sumit(p: P): i32 { return p.x + p.y; } function main(): i32 { let total: i32 = 0; let i: i32 = 0; while (i < 3) { let p: P = P { x: i, y: 10 }; total = total + sumit(p); i = i + 1; } return total; }`,
		33, 1, ""},
	// Escape via return: make() returns its fresh local, so it is NOT freed in
	// make (the caller's reference survives); main's q is call-bound (not a fresh
	// literal) so also not reclaimed. A premature free in make would corrupt q.
	// 7 + 9 = 16.
	{"escape-return-survives",
		`struct P { x: i32, y: i32 } function make(): P { let p: P = P { x: 7, y: 9 }; return p; } function main(): i32 { let q: P = make(); return q.x + q.y; }`,
		16, 0, ""},
	// Escape via struct-literal field value: q is built into r (a field value), so
	// q escapes and is not reclaimed (no dangling field). r is returned → escapes
	// too. Value: 5 + 8 = 13. (Both structs leak — safe.)
	{"escape-struct-field-value",
		`struct Inner { a: i32 } struct Outer { v: i32, w: i32 } function main(): i32 { let q: Inner = Inner { a: 5 }; let r: Outer = Outer { v: q.a, w: 8 }; return r.v + r.w; }`,
		13, 0, ""},
}

// TestSelfHostStructRCIRX86_64 compiles each case through the self-hosted x86-64
// driver (asm_run, IR default-on), asserts the exit code, and (per freeAssert)
// asserts the struct-free emission contract.
func TestSelfHostStructRCIRX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	for _, tc := range structRCIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			checked := asm
			if tc.body != "" {
				checked = []byte(selfHostFnBody(t, asm, tc.body))
				if bytes.Contains(checked, []byte("call __fern_arr_box")) != (tc.freeAssert > 0) {
					t.Errorf("%s: allocation disagrees with static/runtime struct:\n%s", tc.name, checked)
				}
				if tc.freeAssert < 0 && !bytes.Contains(checked, []byte(".K0(%rip)")) {
					t.Errorf("%s: missing static struct load:\n%s", tc.name, checked)
				}
			}
			// Count calls, not the runtime helper definition.
			frees := bytes.Count(checked, []byte("call __fn___fern_arr_dec"))
			switch {
			case tc.freeAssert > 0 && frees == 0:
				t.Errorf("%s: expected a struct free (call __fn___fern_arr_dec), found none — regressed to leak-only", tc.name)
			case tc.freeAssert < 0 && frees != 0:
				t.Errorf("%s: expected no static struct free, found %d", tc.name, frees)
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
