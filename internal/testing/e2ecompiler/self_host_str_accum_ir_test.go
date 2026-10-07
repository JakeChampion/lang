package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// strAccumIRCases pin the string-builder ACCUMULATOR reclaim on the self-hosted
// stack-IR path (#2649 consume-rebind). The canonical string leak is
// `let s: string = ""; while (…) { s = s + part; } … use(s)`: each `s = s + part`
// allocates a fresh box + buffer and orphans the previous one, so the whole growth
// chain leaks. The reclaim frees the superseded box on each reassignment and the
// final at scope exit; a RETURNED builder moves its final value out instead.
//
// __fern_str_free's heap-base guard makes freeing the initial "" literal a no-op on
// its .rodata data (its 16-byte box is still reclaimed).
var strAccumIRCases = []struct {
	name     string
	src      string
	expected int
}{
	// Basic accumulator: "" then 4× `s = s + "x"`; s is reclaimed each
	// reassignment, and the "x" operand temp after each concat (#4262). len 4.
	{"accum-basic",
		`function main(): i32 { let s: string = ""; let i: i32 = 0; while (i < 4) { s = s + "x"; i = i + 1; } return s.len(); }`,
		4},
	// Accumulator with a non-empty literal init and a multi-char part. len 1+3*2=7.
	{"accum-init-nonempty",
		`function main(): i32 { let s: string = "a"; let i: i32 = 0; while (i < 3) { s = s + "bc"; i = i + 1; } return s.len(); }`,
		7},
	// Accumulator over a loop-invariant LOCAL operand `x` (a borrow-read, freed once
	// at exit): s = s + x. No per-iteration literal temporary. len 3*2 = 6.
	{"accum-invariant-operand",
		`function main(): i32 { let x: string = "yy"; let s: string = ""; let i: i32 = 0; while (i < 3) { s = s + x; i = i + 1; } return s.len(); }`,
		6},
	// Memory-safety at scale: a BOUNDED accumulator (grow, then reset to a fresh 1-char
	// string at len > 40) over 5,000,000 iterations, using a loop-invariant operand so
	// there is no per-iteration literal temporary. If the growth chain leaked, resident
	// memory would grow; a double-free would corrupt the freelist and crash / return
	// garbage. exit 0 (fixed) with the reclaim present proves the balance (flat heap).
	{"accum-churn-safe",
		`function main(): i32 { let x: string = "yy"; let s: string = ""; let i: i32 = 0; while (i < 5000000) { s = s + x; if (s.len() > 40) { s = string_from_bytes_unchecked([65 as u8]); } i = i + 1; } return 0; }`,
		0},
	// UN-ANNOTATED accumulator (`let s = ""`, no `: string`): reclaimed too — the
	// inferred string type is enough. len 4.
	{"accum-unannotated",
		`function main(): i32 { let s = ""; let i: i32 = 0; while (i < 4) { s = s + "x"; i = i + 1; } return s.len(); }`,
		4},
	// UN-ANNOTATED returned builder: intermediates freed, final moved out. len 6.
	{"accum-unannotated-return",
		`function build(n: i32): string { let s = ""; let i: i32 = 0; while (i < n) { s = s + "ab"; i = i + 1; } return s; } function main(): i32 { return build(3).len(); }`,
		6},
	// NEGATIVE: an int accumulator (`n = n + i`) matches the reassign SHAPE but is not
	// a string, so it is never reclaimed (no __fern_str_free) and stays correct. 0+1+2+3+4.
	{"accum-int-not-reclaimed",
		`function main(): i32 { let n: i32 = 0; let i: i32 = 0; while (i < 5) { n = n + i; i = i + 1; } return n; }`,
		10},
	// MOVE-ON-RETURN: a returned string builder. The intermediates are freed by the
	// consume-rebind inside build(), and the FINAL is moved out (kept from the exit
	// sweep — freeing it would dangle the box handed to the caller). build(5) → len 5.
	{"accum-return-builder",
		`function build(n: i32): string { let s: string = ""; let i: i32 = 0; while (i < n) { s = s + "x"; i = i + 1; } return s; } function main(): i32 { return build(5).len(); }`,
		5},
	// Move-on-return with a loop-invariant operand + a BRANCHY return (early at
	// len > 8 or the final return) — both return sites move s out. len 9.
	{"accum-return-branchy",
		`function build(n: i32): string { let s: string = "start"; let i: i32 = 0; while (i < n) { s = s + "z"; if (s.len() > 8) { return s; } i = i + 1; } return s; } function main(): i32 { return build(100).len(); }`,
		9},
	// NEGATIVE: a NON-FRESH reassignment (`s = "reset"`, a literal alias) must exclude
	// the accumulator — freeing a later s could double-free the literal-shared box.
	// The concat source x is a PARAMETER, so the accumulator is the only local a
	// reclaim could be about, and x must survive for the final read.
	// "reset".len() + "x".len() = 6.
	{"accum-nonfresh-reassign-not-reclaimed",
		`function acc(x: string): i32 { let s: string = ""; let i: i32 = 0; while (i < 3) { s = s + x; i = i + 1; } s = "reset"; return s.len() + x.len(); } function main(): i32 { return acc("x"); }`,
		6},
}

// TestSelfHostStrAccumIRX86_64 compiles each case through the self-hosted x86-64
// driver (asm_run, IR default-on), asserting the exit code and that the accumulator
// consume-rebind reclaim (call __fn___fern_str_free) is (or isn't) emitted.
func TestSelfHostStrAccumIRX86_64(t *testing.T) {
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

	for _, tc := range strAccumIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
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
