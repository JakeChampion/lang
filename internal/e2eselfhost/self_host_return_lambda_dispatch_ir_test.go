package e2eselfhost

import (
	"os/exec"
	"testing"
)

// returnLambdaDispatchIRCases pin the inline-lambda-return dispatch cluster
// (#5266): a function that returns an inline `return <lambda>` in non-tail /
// multi-return / match-payload-sibling position used to mis-dispatch the closure
// at the caller. The lift pass (lift_stmt) now hoists every `return <lambda>`
// (capturing or not) to `var $lamret$N = <lambda>; return $lamret$N;`, so the
// StmtVar clo_init rule boxes the closure and the caller's `var g = f()` binds g a
// closure local and dispatches `g()` env-first — uniformly, on the IR path.
//
// Before the fix:
//   - a single NON-capturing return (`return () => 7`) returned a bare fn pointer
//     the caller called env-first -> SIGSEGV;
//   - two branch-divergent CAPTURING returns unified to the wrong box -> wrong value;
//   - a lambda return alongside a match-payload closure return bailed the whole
//     module to the AST emitter, which then mis-lowered the CALLER's enum ctor
//     `E.V(<lambda>)` as an unresolved ident -> SIGSEGV.
//
// The self-host sources contain no bare lambda returns, so the desugar never fires
// during the self-compile (byte-identical fixpoint). Found via differential
// probing; exit codes cross-checked against the interpreter and native Go backend.
var returnLambdaDispatchIRCases = []struct {
	name string
	src  string
	exit int
}{
	// Baseline single capturing tail return (already worked; guards no regression).
	{"single-capturing", "function pick(n: i32): () => i32 { return () => n; } function main(): i32 { var g = pick(6); return g(); }", 6},
	// C — single NON-capturing lambda return (was SIGSEGV).
	{"single-noncapturing", "function pick(): () => i32 { return () => 7; } function main(): i32 { var g = pick(); return g(); }", 7},
	// C — non-capturing with an argument.
	{"noncapturing-arg", "function pick(): (i32) => i32 { return (x: i32) => x + 1; } function main(): i32 { var g = pick(); return g(10); }", 11},
	// B — two DIFFERENT capturing returns, taken branch (was wrong value: 7).
	{"two-capturing-then", "function pick(flag: i32, n: i32): () => i32 { if (flag > 0) { return () => n; } return () => n + 1; } function main(): i32 { var g = pick(1, 6); return g(); }", 6},
	// B — two DIFFERENT capturing returns, fall-through branch.
	{"two-capturing-else", "function pick(flag: i32, n: i32): () => i32 { if (flag > 0) { return () => n; } return () => n + 1; } function main(): i32 { var g = pick(0, 6); return g(); }", 7},
	// Sequential two returns in one block (no if), non-capturing.
	{"seq-two-noncapturing", "function pick(): () => i32 { if (true) { return () => 3; } return () => 9; } function main(): i32 { var g = pick(); return g(); }", 3},
	// D — match-bound payload return + inline lambda sibling (was AST fallback -> SIGSEGV).
	{"match-payload-lambda-sibling", "enum Box { W(() => i32) } function pick(b: Box, flag: i32): () => i32 { match (b) { W(f) => { if (flag > 0) { return f; } return () => 0; } } } function main(): i32 { var n: i32 = 6; var g = pick(Box.W(() => n), 1); return g(); }", 6},
	// D — same, fall-through returns the inline lambda.
	{"match-payload-lambda-fallthrough", "enum Box { W(() => i32) } function pick(b: Box, flag: i32): () => i32 { match (b) { W(f) => { if (flag > 0) { return f; } return () => 42; } } } function main(): i32 { var n: i32 = 6; var g = pick(Box.W(() => n), 0); return g(); }", 42},
}

// TestSelfHostReturnLambdaDispatchIRX86_64 — the x86-64 lift-pass fix, through the
// production driver (asm_ir_run `-ir`).
func TestSelfHostReturnLambdaDispatchIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm.fern", "asm_arm64.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range returnLambdaDispatchIRCases {
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

// TestSelfHostReturnLambdaDispatchIRArm64 — CI-gated arm64 counterpart. The fix is
// in the shared irlower.fern lift pass, so the arm64 IR backend picks it up.
func TestSelfHostReturnLambdaDispatchIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("arm64 return-lambda-dispatch gate needs a native x86 host to run the driver")
	}
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_arm64.fern", "asm.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range returnLambdaDispatchIRCases {
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
