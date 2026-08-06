package e2eselfhost

import (
	"os/exec"
	"testing"
)

// nestedLocalFnArgIRCases pin a function value passed as an ARGUMENT to a
// NESTED local function — #6341.
//
// This was a silent miscompile, not a bail. The program compiled clean, with
// nothing printed even under FERN_STRICT_IR=1, and the binary segfaulted.
//
// The lift decides whether a fn-value argument must be env-boxed by asking
// lift_callee_param_is_fn, which looks the callee up among MODULE functions. A
// nested local function is not there: hoist_local_funcs_module only rewrites a
// body containing a SELF-RECURSIVE local, so a plain nested function stays a
// statement (a `var helper = <lambda>` binding) and never reaches `mfuncs`. The
// lift therefore judged "callee param is not fn-typed", passed the value RAW,
// and the callee — which dispatches env-first, reading slot 0 of an assumed
// [funcval, caps…] box — dereferenced a bare code address.
//
// irlower's own comment in lift_inline_closures_stmts describes the same
// failure for the sibling case it already handled: "an UNBOXED reassigned value
// (a bare lambda / fn pointer) in that slot would env-first-dispatch a non-box
// and crash."
//
// Each case was checked to SEGFAULT (exit 139) on the parent commit and to
// answer correctly with the fix. The two controls are the shapes that already
// worked and must keep working — they are what isolate "nested callee" as the
// variable rather than "fn-value argument".
var nestedLocalFnArgIRCases = []struct {
	name string
	src  string
	exit int
}{
	// A bare module fn-name as the argument. The minimal repro — no lambda at
	// all, which is what rules out the lambda machinery as the cause.
	{"fn-name-to-nested-fn", `function dbl(x: i32): i32 { return x * 2i32; }
function main(): i32 {
    function helper(f: (i32) => i32): i32 { return f(3i32); }
    return helper(dbl) & 63i32;
}`, 6},
	// An inline lambda as the argument — the shape fernsmith generates.
	{"lambda-to-nested-fn", `function main(): i32 {
    function helper(f: (i32) => i32): i32 { return f(3i32); }
    var v: i32 = helper(((x: i32) => 481i32));
    return v & 63i32;
}`, 33},
	// Control: the same call with the fn value bound to a LOCAL first. That
	// path already boxed the value, so it answered correctly before the fix.
	{"via-local-control", `function main(): i32 {
    function helper(f: (i32) => i32): i32 { return f(3i32); }
    var g: (i32) => i32 = ((x: i32) => 481i32);
    return helper(g) & 63i32;
}`, 33},
	// Control: the same call with the helper at TOP LEVEL, where
	// lift_callee_param_is_fn could always see it. This is the row that says
	// the bug was the callee's nesting, not the argument.
	{"top-level-callee-control", `function helper(f: (i32) => i32): i32 { return f(3i32); }
function main(): i32 {
    var v: i32 = helper(((x: i32) => 481i32));
    return v & 63i32;
}`, 33},
}

// TestSelfHostNestedLocalFnArgIRX86_64 runs each case through the production
// x86-64 IR driver and asserts the ANSWER. A compile-only assertion would have
// passed throughout the bug's lifetime — the module was always IR-eligible.
func TestSelfHostNestedLocalFnArgIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range nestedLocalFnArgIRCases {
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
				t.Errorf("%s exited %d, want %d (139 = SIGSEGV, the #6341 symptom)", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostNestedLocalFnArgIRArm64 — the arm64 counterpart. The fix is in the
// shared lift, so arm64 picks it up unchanged; running it is what proves that
// rather than assuming it.
func TestSelfHostNestedLocalFnArgIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("arm64 nested-local-fn-arg gate needs a native x86 host to run the driver")
	}
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range nestedLocalFnArgIRCases {
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
