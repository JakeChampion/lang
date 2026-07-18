package e2eselfhost

import (
	"os/exec"
	"testing"
)

// cloArrayFieldCallCases pin the DIRECT call of a closure-array element loaded
// from a struct field — `reg.hs[i](args)`. The struct-field closure array makes
// the enclosing function IR-ineligible, so it bails to the legacy AST emitter
// (asm.fern / asm_arm64.fern). There, emit_call's callee dispatch matched only
// ExprLambda (IIFE), ExprIdent, and ExprFieldAccess (method); a closure-valued
// ExprIndex callee hit the `_` fallthrough, which emitted `pushq $0`
// (arm64: `mov x0, #0`) — silently compiling the call to "return 0". The
// fallthrough now evaluates the callee to its box ptr and invokes it via the
// closure convention (box ptr in %r10/x9, fn_addr = box[0]), mirroring the
// ExprLambda arm right above it.
//
// (The SEPARATE element-BIND defect — issue #5160 defect #2, `var f =
// reg.hs[i]; f()` / `for h in reg.hs { h() }` — is now fixed on the IR path:
// see self_host_clo_array_field_bind_ir_test.go. That fix (irlower.fern) makes
// the whole struct-field-closure-array shape IR-eligible, so these direct-call
// cases now ALSO lower on the IR path rather than bailing to the AST emitter;
// the AST fallthrough fix above stays as a backstop for shapes still outside
// the IR subset.)
//
// Exit codes cross-checked against the interpreter and the native Go backend.
var cloArrayFieldCallCases = []struct {
	name string
	src  string
	exit int
}{
	// No-capture closure element, direct call.
	{"nocap", "struct Reg { hs: (() => i32)[] } function main(): i32 { var r = Reg { hs: [() => 40] }; return r.hs[0](); }", 40},
	// Capturing closures, two elements, both called directly.
	{"capture-multi", "struct Reg { hs: (() => i32)[] } function main(): i32 { var n: i32 = 2; var r = Reg { hs: [() => n, () => n + 1] }; return r.hs[0]() + r.hs[1](); }", 5},
	// Closure taking one arg (exercises the arg-push + cleanup math).
	{"with-arg", "struct Reg { hs: ((i32) => i32)[] } function main(): i32 { var n: i32 = 5; var r = Reg { hs: [(x: i32) => x + n] }; return r.hs[0](10); }", 15},
	// Two args (pins the (args+1)-slot cleanup).
	{"two-arg", "struct Reg { hs: ((i32, i32) => i32)[] } function main(): i32 { var r = Reg { hs: [(a: i32, b: i32) => a * b] }; return r.hs[0](6, 7); }", 42},
	// Regression: a plain (non-struct) local closure array direct call stays
	// correct — it lowers on the IR path and never touches the AST fallback.
	{"local-array-regress", "function main(): i32 { var n: i32 = 2; var hs: (() => i32)[] = [() => n, () => n + 1]; return hs[0]() + hs[1](); }", 5},
}

// TestSelfHostCloArrayFieldCallIRX86_64 — the x86-64 asm.fern fix, through the
// production driver (asm_ir_run `-ir`; the shape bails to the AST emitter).
func TestSelfHostCloArrayFieldCallIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm.fern", "asm_arm64.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range cloArrayFieldCallCases {
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

// TestSelfHostCloArrayFieldCallIRArm64 — CI-gated arm64 counterpart of the
// asm_arm64.fern fix (same callee-dispatch fallthrough), via the arm64 IR path
// (asm_ir_run `-target arm64 -ir`). Mirrors TestSelfHostTupleFnIRArm64.
func TestSelfHostCloArrayFieldCallIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("arm64 clo-array-field gate needs a native x86 host to run the driver")
	}
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_arm64.fern", "asm.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range cloArrayFieldCallCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64", "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
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
