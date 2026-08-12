package e2eselfhost

import (
	"os/exec"
	"testing"
)

// returnClosureNestedIRCases pin a DOUBLY-escaping nested closure — a function
// that returns a closure which itself returns a closure (`() => (() => T)`),
// issue #5281. The two-level hoist (`pick$clo` / `pick$clo$clo`) is correct; the
// bug was caller-side: `var g = pick(); var h = g();` bound `h` a plain scalar
// because `pick`'s nested `() => (() => i32)` return type coarsens to "fn",
// losing that CALLING g yields another closure — so `h()` bare-called the inner
// box pointer as code (SIGSEGV). closure_ret_closure_fns_of now marks such a
// factory RETCLO2 (its returned closure's `__mkclo$` funcval is itself
// closure-returning); `var g = pick()` records g a RETCLO local, so `var h = g()`
// binds h a closure local and `h()` dispatches env-first.
//
// Found via differential probing. Exit codes cross-checked against the
// interpreter and the native Go backend.
var returnClosureNestedIRCases = []struct {
	name string
	src  string
	exit int
}{
	// The canonical repro: `return () => () => n`, then g()() at the caller.
	{"plain", "function pick(n: i32): () => (() => i32) { return () => () => n; } function main(): i32 { var g = pick(7); var h = g(); return h(); }", 7},
	// Inner body does arithmetic on the (twice-captured) n.
	{"inner-arith", "function pick(n: i32): () => (() => i32) { return () => () => n + 1; } function main(): i32 { var g = pick(10); var h = g(); return h(); }", 11},
	// Innermost closure takes an argument.
	{"inner-arg", "function pick(n: i32): () => ((i32) => i32) { return () => (x: i32) => x + n; } function main(): i32 { var g = pick(5); var h = g(); return h(10); }", 15},
	// Chained call on a var-bound RETCLO local: `g()()` — the inner g() result
	// is called directly (expression position), no intermediate `var h`.
	{"chain-local", "function pick(n: i32): () => (() => i32) { return () => () => n; } function main(): i32 { var g = pick(9); return g()(); }", 9},
	// Fully chained on the factory call: `pick(9)()()` — no var at all.
	{"chain-full", "function pick(n: i32): () => (() => i32) { return () => () => n; } function main(): i32 { return pick(9)()(); }", 9},
	// Fully chained with an argument-taking innermost closure.
	{"chain-arg", "function pick(n: i32): () => ((i32) => i32) { return () => (x: i32) => x + n; } function main(): i32 { return pick(5)()(10); }", 15},
}

// TestSelfHostReturnClosureNestedIRX86_64 — the x86-64 irlower fix, through the
// production driver (asm_ir_run `-ir`).
func TestSelfHostReturnClosureNestedIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range returnClosureNestedIRCases {
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

// TestSelfHostReturnClosureNestedIRArm64 — CI-gated arm64 counterpart. The fix is
// in the shared irlower.fern, so the arm64 IR backend picks it up.
func TestSelfHostReturnClosureNestedIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("arm64 return-closure-nested gate needs a native x86 host to run the driver")
	}
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range returnClosureNestedIRCases {
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
