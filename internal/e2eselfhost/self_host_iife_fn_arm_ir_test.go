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

	// #6323 — the DESTINATION half. The arms above are lambdas, which the
	// #6256 hoist turns into a call whose result a binding already dispatches
	// env-first. When the arms are instead closure LOCALS, no hoist is
	// involved: `var w = (if (c) { v0 } else { v0 })` bound the result to a
	// plain scalar slot because clo_init's match on the init had no case for an
	// IIFE callee, so `w(3)` bare-called the box pointer as code. That compiled
	// clean and SIGSEGV'd — no bail, so no bail-counting gate could see it.
	{"ifexpr-closure-local-arms-called", "function main(): i32 { var v0: (i32) => i32 = ((a: i32) => 89); var n: i32 = 1; var w: (i32) => i32 = (if (n > 0) { v0 } else { v0 }); return w(3); }", 89},
	{"matchexpr-closure-local-arms-called", "enum S { A, B } function main(): i32 { var v0: (i32) => i32 = ((a: i32) => 89); var v1: (i32) => i32 = ((b: i32) => 7); var e: S = S.A; var w: (i32) => i32 = (match (e) { A => v0, B => v1 }); return w(3); }", 89},
	// The counterpart guard, and the reason the marking is gated on every arm
	// being provably a box: BARE fn-name arms are plain fn pointers, so the
	// slot must NOT be marked a closure local — env-first dispatch on a bare
	// pointer is the same crash in the other direction.
	{"ifexpr-bare-fnname-arms-stay-plain", "function inc(x: i32): i32 { return x + 1; } function dbl(x: i32): i32 { return x * 2; } function main(): i32 { var n: i32 = 1; var w: (i32) => i32 = (if (n > 0) { inc } else { dbl }); return w(41); }", 42},

	// #6324 — MIXED arms: one closure-local, one inline lambda. These bailed
	// until the hoist could carry a fn-TYPED capture as a parameter, which needs
	// the signature `cap_type`'s flat "fn" tag throws away (fn_ret /
	// fn_param_types / fn_param_dyn). The capture is copied from the ParamDecl
	// when it is a param and reconstructed from the lambda when it is a local
	// bound from one; anything else still declines.
	//
	// The first of these is #6256's Repro D, which had been open since the issue
	// was filed. All three exercise a different consumption of the result —
	// array element, called local binding, and returned-then-called — because
	// only the last two need the hoisted function to register as
	// closure-returning (form (b') in closure_ret_fns_of), and the first passes
	// without it.
	{"mixed-arms-array-element", "function main(): i32 { var v0: (i32) => i32 = ((a: i32) => 89); var xs: ((i32) => i32)[] = [v0, (if (true) { v0 } else { ((b: i32) => b) }), v0]; return xs[1i32](3i32); }", 89},
	{"mixed-arms-var-called", "function main(): i32 { var v0: (i32) => i32 = ((a: i32) => 89); var n: i32 = 1; var w: (i32) => i32 = (if (n > 0) { v0 } else { ((b: i32) => b) }); return w(3); }", 89},
	{"mixed-arms-returned-then-called", "function gen(n: i32): (i32) => i32 { var v0: (i32) => i32 = ((a: i32) => 89); return (if (n > 0) { v0 } else { ((b: i32) => b) }); } function main(): i32 { var f: (i32) => i32 = gen(1); return f(3); }", 89},
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
