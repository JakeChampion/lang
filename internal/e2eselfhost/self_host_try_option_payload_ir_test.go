package e2eselfhost

import (
	"os/exec"
	"strings"
	"testing"
)

// tryOptionPayloadIRCases pin the self-host CHECKER's type for the `?` (try)
// operator when the unwrapped payload is itself a generic (Option[T]).
// infer_expr_type's ExprUnary arm typed `try_` (the desugared `?`) as i32 via a
// fallthrough, so `var o: Option[i32] = f()?` where f: Result[Option[i32], i32]
// was rejected E003 ("initializer has type i32") — the self-host compiler
// refused a program the interpreter and native backend both accept. The arm now
// returns the operand's Result/Option payload (result_inner / option_inner). The
// bug only surfaced for an Option (or struct) payload — a string / array /
// scalar payload slipped through the lenient assignable check.
//
// Found via differential probing (native -interp exit vs the self-host-compiled
// binary's).
//
// The exit code alone does not say WHICH emitter produced the binary — the AST
// fallback happens to get these right, so a green run was consistent with the
// module never reaching the IR path. Each case therefore also asserts the
// emitter's per-function label marker (`.Lir_` on x86-64, `.Lira_` on arm64).
// `result-option` / `nested-chain` are the two that really did fall back:
// lower_try's payload whitelist rejected the bracketed `Option[i32]` because
// is_enum_like_name declines any type containing `[`.
var tryOptionPayloadIRCases = []struct {
	name string
	src  string
	exit int
}{
	// The canonical repro: `?` on a Result[Option[i32], i32].
	{"result-option", "function f(n: i32): Result[Option[i32], i32] { return Ok(Some(n)); } function g(n: i32): Result[i32, i32] { var x: Option[i32] = f(n)?; match (x) { Some(v) => { return Ok(v); }, None => { return Ok(0); } } } function main(): i32 { match (g(5)) { Ok(v) => { return v; }, Err(_) => { return 9; } } }", 5},
	// A nested `?` chain that binds the Option payload then re-wraps.
	{"nested-chain", "function inner(n: i32): Result[Option[i32], i32] { if (n > 0) { return Ok(Some(n)); } return Ok(None); } function outer(n: i32): Result[i32, i32] { var o: Option[i32] = inner(n)?; match (o) { Some(v) => { return Ok(v); }, None => { return Ok(0); } } } function main(): i32 { match (outer(9)) { Ok(v) => { return v; }, Err(_) => { return 88; } } }", 9},
	// Regression: scalar payload `?` still types correctly.
	{"result-i32", "function f(n: i32): Result[i32, i32] { return Ok(n); } function g(n: i32): Result[i32, i32] { var x: i32 = f(n)?; return Ok(x); } function main(): i32 { match (g(5)) { Ok(v) => { return v; }, Err(_) => { return 9; } } }", 5},
	// Regression: string payload `?`.
	{"result-string", "function f(n: i32): Result[string, i32] { return Ok(\"hi\"); } function g(n: i32): Result[i32, i32] { var x: string = f(n)?; return Ok(x.len()); } function main(): i32 { match (g(5)) { Ok(v) => { return v; }, Err(_) => { return 9; } } }", 2},
	// `?` on a bare Option[i32] (not wrapped in a Result).
	{"option-payload", "function f(o: Option[i32]): Option[i32] { var x: i32 = o?; return Some(x + 1); } function main(): i32 { match (f(Some(6))) { Some(v) => { return v; }, None => { return 9; } } }", 7},
}

// TestSelfHostTryOptionPayloadIRX86_64 — the x86-64 asmcore checker fix, through
// the production driver (asm_ir_run `-ir`).
func TestSelfHostTryOptionPayloadIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm.fern", "asm_arm64.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tryOptionPayloadIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src), "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			if !strings.Contains(string(asm), ".Lir_") {
				t.Fatalf("%s: fell back to the AST path (no .Lir_ labels)", tc.name)
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

// TestSelfHostTryOptionPayloadIRArm64 — CI-gated arm64 counterpart. The fix is in
// the shared asmcore.fern frontend, so the arm64 IR backend picks it up.
func TestSelfHostTryOptionPayloadIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("arm64 try-option-payload gate needs a native x86 host to run the driver")
	}
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_arm64.fern", "asm.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tryOptionPayloadIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64", "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			if !strings.Contains(string(asm), ".Lira_") {
				t.Fatalf("%s: arm64 asm has no .Lira_ marker — module bailed to the AST path", tc.name)
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
