package e2eselfhost

import (
	"os/exec"
	"testing"
)

// returnClosureElemIRCases pin RETURNING an element of a LOCAL closure array
// across a function boundary — `function pick(): () => i32 { var hs = [...];
// return hs[0]; }` then `var g = pick(); g()`. These lower on the IR path.
//
// closure_ret_fns_of (the pre-pass registering functions whose body returns a
// closure box, so a caller's `var g = pick()` binds g a closure local) scanned
// only ExprLambda / ExprIdent / ExprCall returns — NOT ExprIndex. So a
// `return hs[0]` element return went unregistered: the caller bound g a plain
// scalar and `g()` bare-called the box pointer → SIGSEGV. The scan now also
// recognises a `return <local-closure-array>[i]` element (issue #5202 case A).
//
// The struct-field element return `return r.hs[0]` (#5202 cases B/C) needs
// `structs` threaded into the detector and is deferred; not covered here.
//
// Found via differential probing. Exit codes cross-checked against the
// interpreter and the native Go backend.
var returnClosureElemIRCases = []struct {
	name string
	src  string
	exit int
}{
	// Bind the returned closure, then call it.
	{"bind", "function pick(): () => i32 { var n: i32 = 8; var hs: (() => i32)[] = [() => n]; return hs[0]; } function main(): i32 { var g = pick(); return g(); }", 8},
	// Call the returned closure inline (no intermediate bind).
	{"inline", "function pick(): () => i32 { var n: i32 = 8; var hs: (() => i32)[] = [() => n]; return hs[0]; } function main(): i32 { return pick()(); }", 8},
	// Return a non-zero element index.
	{"elem1", "function pick(): () => i32 { var n: i32 = 8; var hs: (() => i32)[] = [() => n, () => n + 5]; return hs[1]; } function main(): i32 { var g = pick(); return g(); }", 13},
	// Returned closure takes an argument.
	{"arg", "function pick(): (i32) => i32 { var n: i32 = 5; var hs: ((i32) => i32)[] = [(x: i32) => x + n]; return hs[0]; } function main(): i32 { var g = pick(); return g(10); }", 15},
	// Loop-churn: call the returned closure each iteration, mod 256.
	{"churn", "function pick(k: i32): (i32) => i32 { var hs: ((i32) => i32)[] = [(x: i32) => x + k]; return hs[0]; } function main(): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < 300) { var g = pick(i % 7); acc = (acc + g(2)) % 1000; i = i + 1; } return acc % 256; }", 241},
}

// TestSelfHostReturnClosureElemIRX86_64 — the x86-64 irlower fix, through the
// production driver (asm_ir_run `-ir`).
func TestSelfHostReturnClosureElemIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range returnClosureElemIRCases {
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

// TestSelfHostReturnClosureElemIRArm64 — CI-gated arm64 counterpart. The fix is
// in the shared irlower.fern, so the arm64 IR backend picks it up for free.
func TestSelfHostReturnClosureElemIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("arm64 return-closure-elem gate needs a native x86 host to run the driver")
	}
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range returnClosureElemIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux", "-ir")
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
