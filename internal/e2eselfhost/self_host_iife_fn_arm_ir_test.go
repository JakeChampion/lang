package e2eselfhost

import (
	"os/exec"
	"testing"
)

// iifeFnArmCases exercise a value-position `if` / `match` whose ARMS yield a
// fn value — `var w: (i32) => i32 = (if (c) { <lambda> } else { <lambda> })`,
// `return (match (e) { A => <lambda>, B => <lambda> })`.
//
// A value-position if/match parses as an IIFE (`ExprCall{callee: ExprLambda{
// params: [], body: [StmtIf|StmtMatch]}}`), and whether its arms are ever
// reached used to depend on something unrelated to the arms: lift_call_arg
// hoists a NO-CAPTURE IIFE to a top-level `__lam_N` FuncDecl, and BECOMING a
// FuncDecl is what gets the body walked by every later pass. A CAPTURING IIFE
// was left in place, lower_iife lowered its arms inline in the enclosing
// function, and no lift pass ever saw them — so an arm lambda reached
// lower_expr bare and asked for a `<cur_fn>$clo` nothing had built. The module
// bailed with "function value <fn>$clo not defined" (#6256).
//
// The fix hoists the capturing IIFE too, taking its captures as ORDINARY
// PARAMETERS — an IIFE is applied immediately, so its captures need no env box.
// The call that replaces it is a plain fn-value-returning call, which is what
// makes the RESULT usable: boxing only the arms left the destination unmarked,
// so `w(3)` bare-dispatched a box and SIGSEGV'd.
//
// nocapture-iife-unchanged is the regression guard for the other side: a
// no-capture IIFE must keep taking the `__lam_N` path exactly as before.
var iifeFnArmCases = []struct {
	name string
	src  string
	exit int
}{
	// A match-EXPRESSION arm whose value is a NESTED match-expression with
	// lambda arms. The outer arm is walked; before the fix nothing descended
	// into the inner IIFE's arms. Reduced from fernsmith seed 322.
	{"nested-match-expr-fn-arm", "enum Status { Active, Inactive } function gen_f0(p1: Status): (i32) => i32 { return (match (p1) { Active => (match (p1) { Active => ((a: i32) => a), Inactive => ((b: i32) => 95) }), Inactive => ((c: i32) => 72) }); } function main(): i32 { var f: (i32) => i32 = gen_f0(Status.Active); return f(7); }", 7},
	// An if-EXPRESSION with lambda arms, bound to a local and then CALLED.
	// The capture (`n`) is what kept the IIFE off the hoist path.
	{"ifexpr-lambda-arms-var-called", "function main(): i32 { var n: i32 = 1; var w: (i32) => i32 = (if (n > 0) { ((a: i32) => 89) } else { ((b: i32) => b) }); return w(3); }", 89},
	// A match-EXPRESSION with lambda arms RETURNED from a function, the result
	// bound and called by the caller.
	{"matchexpr-lambda-arms-returned", "enum Status { Active, Inactive } function gen(p1: Status): (i32) => i32 { return (match (p1) { Active => ((a: i32) => 5), Inactive => ((c: i32) => 72) }); } function main(): i32 { var f: (i32) => i32 = gen(Status.Inactive); return f(7); }", 72},
	// Regression guard: a NO-CAPTURE IIFE (constant condition, no free vars)
	// must keep taking the existing `__lam_N` hoist, untouched by the new path.
	{"nocapture-iife-unchanged", "function main(): i32 { var w: (i32) => i32 = (if (true) { ((a: i32) => 89) } else { ((b: i32) => b) }); return w(3); }", 89},
}

// TestSelfHostIIFEFnArmIRX86_64 — fn-valued value-position if/match arms
// through the production x86-64 IR path (asm_ir_run `-ir`).
func TestSelfHostIIFEFnArmIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range iifeFnArmCases {
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

// TestSelfHostIIFEFnArmIRArm64 — CI-gated arm64 counterpart via the arm64 IR
// path (asm_ir_run `-target arm64 -ir`). Shares the fix in irlower.fern.
func TestSelfHostIIFEFnArmIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range iifeFnArmCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64", "-ir")
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
