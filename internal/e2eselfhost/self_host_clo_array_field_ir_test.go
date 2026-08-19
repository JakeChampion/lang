package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// cloArrayFieldCallCases pin the DIRECT call of a closure-array element loaded
// from a struct field — `reg.hs[i](args)`.
//
// These now lower on the IR path (#3457). The gap was narrow and in DISPATCH,
// not eligibility: construction, `.len()`, a named-fn element, and the same
// call through a LOCAL clo-array all lowered already — only the inline call
// bailed, because the element-call dispatch's ExprFieldAccess arm recognised
// only a registered fn-POINTER field, so a field of env BOXES matched neither
// flag and fell through to s.fail(). TestSelfHostCloArrayFieldRoutesIR below
// pins the routing, since these behaviour cases pass either way.
//
// The AST emitter still handles them, and that path stays correct: it bails to
// the legacy emitter
// (asm.fern / asm_arm64.fern). There, emit_call's callee dispatch matched only
// ExprLambda (IIFE), ExprIdent, and ExprFieldAccess (method); a closure-valued
// ExprIndex callee hit the `_` fallthrough, which emitted `pushq $0`
// (arm64: `mov x0, #0`) — silently compiling the call to "return 0". The
// fallthrough now evaluates the callee to its box ptr and invokes it via the
// closure convention (box ptr in %r10/x9, fn_addr = box[0]), mirroring the
// ExprLambda arm right above it.
//
// (A SEPARATE, still-open defect — issue #5160 — is `var f = reg.hs[i]; f()`
// and `for h in reg.hs { h() }`: binding a closure-array element from a struct
// field yields a value the `f()` lowering treats as a raw fn pointer rather
// than a closure box, so it SIGSEGVs. That is the element-BIND path, not the
// direct-call callee dispatch these cases exercise.)
//
// Exit codes cross-checked against the interpreter and the native Go backend.
var cloArrayFieldCallCases = []struct {
	name string
	src  string
	exit int
}{
	// A local-built closure array stored into the field. NEW capability: the
	// closure side of the scan credits a local proven bound to an all-`__mkclo$`
	// array literal, so this is now proven rather than inferred by elimination.
	{"clo-local-built", "struct R { hs: (() => i32)[] }\nfunction main(): i32 { var n: i32 = 3; var c: (() => i32)[] = [() => n]; var r = R { hs: c }; return r.hs[0](); }", 3},
	// The local is REBOUND from a fn-pointer array to a closure array before the
	// store, so the closure proof has to come from the ASSIGNMENT, not the
	// declaration. Read through an element BIND rather than an inline call, so
	// the re-proof is exercised at a second dispatch site.
	{"clo-rebound-bind", "struct R { hs: (() => i32)[] }\nfunction seven(): i32 { return 7; }\nfunction main(): i32 { var n: i32 = 5; var a: (() => i32)[] = [seven]; a = [() => n, () => n]; var r: R = R { hs: a }; var f = r.hs[1]; return f(); }", 5},
	// No-capture closure element, direct call.
	{"nocap", "struct Reg { hs: (() => i32)[] } function main(): i32 { var r = Reg { hs: [() => 40] }; return r.hs[0](); }", 40},
	// Capturing closures, two elements, both called directly.
	{"capture-multi", "struct Reg { hs: (() => i32)[] } function main(): i32 { var n: i32 = 2; var r = Reg { hs: [() => n, () => n + 1] }; return r.hs[0]() + r.hs[1](); }", 5},
	// Closure taking one arg (exercises the arg-push + cleanup math).
	{"with-arg", "struct Reg { hs: ((i32) => i32)[] } function main(): i32 { var n: i32 = 5; var r = Reg { hs: [(x: i32) => x + n] }; return r.hs[0](10); }", 15},
	// Two args (pins the (args+1)-slot cleanup).
	{"two-arg", "struct Reg { hs: ((i32, i32) => i32)[] } function main(): i32 { var r = Reg { hs: [(a: i32, b: i32) => a * b] }; return r.hs[0](6, 7); }", 42},
	// Regression: a plain (non-struct) local closure array direct call stays
	// correct — it lowers on the IR path rather than bailing.
	{"local-array-regress", "function main(): i32 { var n: i32 = 2; var hs: (() => i32)[] = [() => n, () => n + 1]; return hs[0]() + hs[1](); }", 5},
}

// TestSelfHostCloArrayFieldCallIRX86_64 — the x86-64 asm.fern fix, through the
// production driver (asm_ir_run `-ir`).
func TestSelfHostCloArrayFieldCallIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
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
// (asm_ir_run `-target arm64-linux -ir`). Mirrors TestSelfHostTupleFnIRArm64.
func TestSelfHostCloArrayFieldCallIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("arm64 clo-array-field gate needs a native x86 host to run the driver")
	}
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range cloArrayFieldCallCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux", "-ir")
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

// TestSelfHostCloArrayFieldRoutesIR pins that the inline call through a
// struct-field CLOSURE array reaches the IR path. The behaviour test above
// passes either way — the AST emitter compiles these correctly — so without a
// routing assertion a regression back to the fallback is silent (#3457).
func TestSelfHostCloArrayFieldRoutesIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_ir_run.fern")
	if err != nil {
		t.Fatalf("read asm_ir_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_ir_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_ir_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range []struct{ name, src string }{
		{"clo-field-call", "struct R { hs: (() => i32)[] }\nfunction main(): i32 { var n: i32 = 3; var r = R { hs: [() => n] }; return r.hs[0](); }"},
		{"clo-field-arg", "struct R { hs: ((i32) => i32)[] }\nfunction main(): i32 { var n: i32 = 3; var r = R { hs: [(x: i32) => x + n] }; return r.hs[0](4); }"},
		// The proof survives a rebind of the local it comes from: the closure
		// credit is re-established by the ASSIGNMENT. Without that the field is
		// unproven and the module bails (the AST emitter it once fell to returned 0 for
		// this program on wasm).
		{"clo-field-rebound", "struct R { hs: (() => i32)[] }\nfunction seven(): i32 { return 7; }\nfunction main(): i32 { var n: i32 = 5; var a: (() => i32)[] = [seven]; a = [() => n]; var r: R = R { hs: a }; return r.hs[0](); }"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := runCapture(t, gcc, runner, driverBin, []byte(tc.src+"\n"), "-ir-probe")
			if !strings.Contains(string(out), "module: IR") {
				t.Errorf("module is not IR-eligible, want the IR path:\n%s", out)
			}
		})
	}
}
