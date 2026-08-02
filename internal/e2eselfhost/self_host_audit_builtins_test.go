package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// auditBuiltinCases isolate one foundational built-in language feature
// each and compile+run them through the SELF-HOSTED compiler, asserting
// the process exit code. This is the self-host arm of the feature audit
// (docs/FEATURE-AUDIT.md §A) — the native arm lives in the
// `audit_core_builtins` fixture (all four native backends). A feature
// that lowers cleanly here proves the self-hosted compiler covers it;
// a failure pinpoints exactly which built-in the IR subset is missing.
//
// Exit codes are the observable: each `main` returns a value in 0..255.
var auditBuiltinCases = []struct {
	name string
	src  string
	exit int
}{
	{"arith-add-mul", `function main(): i32 { return 7 + 3 * 4; }`, 19},
	{"arith-divmod", `function main(): i32 { return 7 / 3 * 10 + 7 % 3; }`, 21},
	{"arith-divneg", `function main(): i32 { return (0 - 7) / 3 + 10; }`, 8}, // -2 + 10 (trunc toward zero)
	{"unary-minus", `function main(): i32 { return -5 + 13; }`, 8},
	{"compare-chain", `function main(): i32 { if (3 < 4 && 4 >= 4 && 3 != 4) { return 5; } return 0; }`, 5},
	{"bitwise-and-or-shl", `function main(): i32 { return (6 & 3) + (6 | 1) + (1 << 4); }`, 25},
	{"bitwise-shr-xor", `function main(): i32 { return (240 >> 4) + (6 ^ 3); }`, 20},
	{"compound-assign", `function main(): i32 { var c: i32 = 10; c += 5; c *= 2; c -= 4; return c; }`, 26},
	{"if-expression", `function main(): i32 { var x: i32 = if (true) { 11 } else { 22 }; return x; }`, 11},
	{"while-loop", `function main(): i32 { var s: i32 = 0; var i: i32 = 1; while (i <= 10) { s = s + i; i = i + 1; } return s; }`, 55},
	{"for-in-array", `function main(): i32 { var a: i32[] = [2, 3, 4]; var s: i32 = 0; for x in a { s = s + x * x; } return s; }`, 29},
	{"range-inclusive", `function main(): i32 { var s: i32 = 0; for k in 0..=5 { s = s + k; } return s; }`, 15},
	{"range-half-open", `function main(): i32 { var s: i32 = 0; for k in 0..4 { s = s + k; } return s; }`, 6},
	// break / continue exercised over a foreach loop (not a C-style for —
	// see the held-out gaps below): break at the 5th element -> 5;
	// continue skipping evens, summing odds -> 1+3+5 = 9.
	{"break", `function main(): i32 { var a: i32[] = [0,1,2,3,4,5,6,7]; var n: i32 = 0; for x in a { if (x == 5) { break; } n = n + 1; } return n; }`, 5},
	{"continue", `function main(): i32 { var a: i32[] = [0,1,2,3,4,5]; var n: i32 = 0; for x in a { if (x % 2 == 0) { continue; } n = n + x; } return n; }`, 9},
	// Bare nested block `{ ... }` — fixed by #2821 (#2831 added StmtBlock
	// to the self-host parser). Re-enabled here as a regression guard.
	{"nested-block", `function main(): i32 { var b: i32 = 1; { var inner: i32 = 40; b = b + inner; } return b; }`, 41},
	// C-style `for (var i = …; …; …)` — fixed by #2820 (#2841: parser
	// desugar to a while-loop with a first-iteration flag so `continue`
	// re-runs the step). Runs on this AST path too (the desugar is at parse
	// time). Re-enabled as a regression guard.
	{"c-style-for", `function main(): i32 { var s: i32 = 0; for (var i: i32 = 1; i <= 10; i = i + 1) { s = s + i; } return s; }`, 55},
}

// Known self-host gaps surfaced by this audit (2026-06-12) — held out of
// the executed table because the self-hosted compiler currently
// MISCOMPILES them (the native compiler handles both on every backend).
// Each is a goal-1 self-host widening, tracked by an issue. Re-add the
// case here once its issue is fixed.
//
//   - `for x in <string>` — the foreach lowering assumed an array layout
//     (len@0, elem*8+8) for the string iterable. Fixed on the IR path by
//     #2822 (#2834: irlower desugars to a byte-index counted loop), guarded
//     by self_host_for_in_string_ir_test.go. This AST-path driver (asm_run)
//     still routes a string foreach through the array path, so the case
//     stays held out here until the AST backend is taught the same.
//       function main(): i32 { var s: i32 = 0; for b in "AB" { s = s + b; } return s; } // want 131, gets 2 on AST

// TestSelfHostAuditBuiltinsX86_64 runs each isolated built-in through the
// self-hosted x86-64 driver and asserts the exit code.
func TestSelfHostAuditBuiltinsX86_64(t *testing.T) {
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

	for _, tc := range auditBuiltinCases {
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
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostAuditBuiltinsArm64 — CI-gated arm64 counterpart.
func TestSelfHostAuditBuiltinsArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range auditBuiltinCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBin(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
