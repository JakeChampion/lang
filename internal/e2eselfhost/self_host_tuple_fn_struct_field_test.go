package e2eselfhost

import (
	"os/exec"
	"testing"
)

// tupleFnStructFieldCases pin the DIRECT call of a fn-valued TUPLE ELEMENT that
// lives in a struct field — `s.p.N(args)`. The struct-field tuple makes the
// enclosing function IR-ineligible, so it bails to the legacy AST emitter
// (asm.fern / asm_arm64.fern). There, emit_call routed an ExprFieldAccess
// callee straight to emit_method_call; a NUMERIC field ("N", a tuple index) is
// not a method name, so it found no method and returned the -1 sentinel (exit
// 255) — silently miscompiling the call. emit_call now recognises an all-digit
// field as a tuple-element call and invokes it via the closure convention (box
// ptr, fn_addr = box[0]), the same shape as the ExprIndex closure-value arm.
// Reading the element first (`var g = s.p.N; g()`) already worked; this is the
// direct-call sibling (cf. #5160 defect #1 for closure ARRAY elements).
//
// Found via differential probing (interpreter vs self-host-compiled binary).
// Exit codes cross-checked against the interpreter and the native Go backend.
var tupleFnStructFieldCases = []struct {
	name string
	src  string
	exit int
}{
	// Bare: fn is the 2nd tuple element, no-arg call.
	{"bare", "struct S { p: (i32, () => i32) } function main(): i32 { var n: i32 = 4; var s = S { p: (1, () => n) }; return s.p.1(); }", 4},
	// fn is the 1st tuple element and takes an argument.
	{"arg-elem0", "struct S { p: ((i32) => i32, i32) } function main(): i32 { var n: i32 = 5; var s = S { p: ((x: i32) => x + n, 9) }; return s.p.0(10); }", 15},
	// Two-arg fn element (pins the (args+1)-slot cleanup math).
	{"two-arg", "struct S { p: (i32, (i32, i32) => i32) } function main(): i32 { var s = S { p: (0, (a: i32, b: i32) => a * b) }; return s.p.1(6, 7); }", 42},
	// Regression: read the element into a local first, then call (the path
	// that already worked) — must stay correct.
	{"read-then-call", "struct S { p: (i32, () => i32) } function main(): i32 { var n: i32 = 4; var s = S { p: (1, () => n) }; var g = s.p.1; return g(); }", 4},
	// Loop-churn: rebuild the struct + call each iteration, mod 256. Catches a
	// stack-imbalance in the cleanup math the single-shot case can mask.
	{"churn", "struct S { p: (i32, (i32) => i32) } function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 300) { var k: i32 = i % 7; var s = S { p: (k, (x: i32) => x + k) }; acc = (acc + s.p.1(2) + s.p.0) % 1000; i = i + 1; } return acc % 256; }", 138},
}

// TestSelfHostTupleFnStructFieldX86_64 — the x86-64 asm.fern fix, through the
// production driver (asm_ir_run `-ir`; the shape bails to the AST emitter).
func TestSelfHostTupleFnStructFieldX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tupleFnStructFieldCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src), "-ir")
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

// TestSelfHostTupleFnStructFieldArm64 — CI-gated arm64 counterpart of the
// asm_arm64.fern fix (same numeric-callee dispatch). Mirrors
// TestSelfHostTupleFnIRArm64.
func TestSelfHostTupleFnStructFieldArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("arm64 tuple-fn-struct-field gate needs a native x86 host to run the driver")
	}
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tupleFnStructFieldCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64", "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
