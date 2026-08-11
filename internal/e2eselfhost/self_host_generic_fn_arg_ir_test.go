package e2eselfhost

import (
	"os/exec"
	"testing"
)

// genericFnArgIRCases pin that a fn value passed to an ERASED-GENERIC parameter
// is lifted, and that what comes back out is dispatched as the env box it is —
// #6466.
//
// An unbounded type param is erased rather than monomorphised, so `id[T](x: T)`
// keeps the spelling `T` and the lift's fn-typed gate (which tests for the
// literal "fn") never matched it. The lambda reached lowering unlifted and asked
// for a `<fn>$clo` nothing had built.
//
// Boxing at the call site alone is NOT the fix, and the issue records why: the
// erased callee is a passthrough that neither takes nor understands an env, so
// the box lands in the binding raw and `v(1)` bare-calls a box pointer. The
// non-capturing row below is the one that catches that — it answers correctly
// today, and a call-site-only widening turns it into a segfault. Every row
// asserts the ANSWER against the interpreter's for that reason.
var genericFnArgIRCases = []struct {
	name string
	src  string
	exit int
}{
	// The issue's repro: a CAPTURING lambda through an erased generic.
	{"capturing-through-generic", `function id[T](x: T): T { return x; }
function gen(p: i32): i32 {
    var v: (i32) => i32 = id(((a: i32) => (a + p)));
    return v(1i32);
}
function main(): i32 { return gen(6i32) & 255i32; }`, 7},
	// The regression guard: NON-capturing through the same generic. This
	// compiled and answered 2 before, and must keep doing so.
	{"non-capturing-through-generic", `function id[T](x: T): T { return x; }
function gen(p: i32): i32 {
    var v: (i32) => i32 = id(((a: i32) => (a + 1i32)));
    return v(1i32);
}
function main(): i32 { return gen(6i32) & 255i32; }`, 2},
	// Controls from the issue's isolation table — each already worked, and each
	// is a neighbouring position the widened gate must not disturb.
	{"bound-directly-control", `function gen(p: i32): i32 {
    var v: (i32) => i32 = ((a: i32) => (a + p));
    return v(1i32);
}
function main(): i32 { return gen(6i32) & 255i32; }`, 7},
	{"fn-typed-param-control", `function apply(f: (i32) => i32, x: i32): i32 { return f(x); }
function gen(p: i32): i32 { return apply(((a: i32) => (a + p)), 1i32); }
function main(): i32 { return gen(6i32) & 255i32; }`, 7},
	{"fn-typed-param-returning-it-control", `function idf(f: (i32) => i32): (i32) => i32 { return f; }
function gen(p: i32): i32 {
    var v: (i32) => i32 = idf(((a: i32) => (a + p)));
    return v(1i32);
}
function main(): i32 { return gen(6i32) & 255i32; }`, 7},
}

// TestSelfHostGenericFnArgIRX86_64 drives the production x86-64 IR path.
func TestSelfHostGenericFnArgIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range genericFnArgIRCases {
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

// TestSelfHostGenericFnArgIRArm64 — the arm64 counterpart. Closure dispatch is
// where the two register backends have diverged before (#5001 / #5007 / #5009 /
// #5026 were all found one backend at a time), so running it is what proves the
// shared lift and the shared binding rule agree here.
func TestSelfHostGenericFnArgIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("arm64 generic-fn-arg gate needs a native x86 host to run the driver")
	}
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range genericFnArgIRCases {
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
