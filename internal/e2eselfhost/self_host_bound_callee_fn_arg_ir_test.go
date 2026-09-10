package e2eselfhost

import (
	"os/exec"
	"testing"
)

// boundCalleeFnArgIRCases pin a function value passed as an ARGUMENT to a call
// whose callee is a BINDING of the enclosing function — a fn-typed parameter, a
// local bound from a module function, an annotated fn-typed local — rather than
// a module function named at the call site (#9009).
//
// The lift decides whether such an argument must be env-boxed by asking whether
// the callee's parameter is fn-typed. It answered that from the module function
// table (lift_callee_param_is_fn) and from nested local functions (#6341), so a
// callee that was any other binding read as "not fn-typed" and the argument was
// passed RAW. The callee dispatches env-first, reading slot 0 of what it takes
// for a [funcval, caps…] box, so it dereferenced a bare code address and the
// binary segfaulted with no diagnostic. Binding the argument to a local first
// dodged it, because that path boxes.
//
// A shadowing binding (`var withRes = taker;` under a module `withRes`) used to
// hit the module function's signature by NAME and box by accident; once
// bindings carry their own symbols (#8982) that accident stopped, which is how
// `use x <- withRes();` in internal/e2e's shadowed-use case came to segfault
// (#8983). The rows here are the shapes with no same-named module function,
// which segfaulted before #8982 as well.
var boundCalleeFnArgIRCases = []struct {
	name string
	src  string
	exit int
}{
	// The callee is a fn-typed PARAMETER; the argument a bare module fn-name.
	{"param-callee-fn-name-arg", `function taker(f: (string) => i32): i32 { return f("hi"); }
function len_of(s: string): i32 { return s.len() + 19; }
function call_it(t: (((string) => i32)) => i32): i32 { return t(len_of); }
function main(): i32 { return call_it(taker); }`, 21},
	// The callee is a local bound from a module function.
	{"local-callee-fn-name-arg", `function taker(f: (string) => i32): i32 { return f("hi"); }
function len_of(s: string): i32 { return s.len() + 19; }
function g(): i32 { var t = taker; return t(len_of); }
function main(): i32 { return g(); }`, 21},
	// The same callee with an inline lambda argument.
	{"local-callee-lambda-arg", `function taker(f: (string) => i32): i32 { return f("hi"); }
function g(): i32 { var t = taker; return t((x: string) => x.len() + 20); }
function main(): i32 { return g(); }`, 22},
	// An annotated fn-typed local as the callee.
	{"annotated-local-callee", `function taker(f: (string) => i32): i32 { return f("hi"); }
function g(): i32 {
    var t: (((string) => i32)) => i32 = taker;
    return t((x: string) => x.len() + 21);
}
function main(): i32 { return g(); }`, 23},
	// A local aliased from another such local.
	{"aliased-local-callee", `function taker(f: (string) => i32): i32 { return f("hi"); }
function len_of(s: string): i32 { return s.len() + 22; }
function g(): i32 { var t = taker; var u = t; return u(len_of); }
function main(): i32 { return g(); }`, 24},
	// A `use` clause whose source call goes through a local: the desugared
	// callback lambda is the argument.
	{"use-through-local", `function taker(f: (string) => i32): i32 { return f("hi"); }
function g(): i32 {
    var t = taker;
    use x <- t();
    if (x == "hi") { return 25; }
    return 0;
}
function main(): i32 { return g(); }`, 25},
	// Control: the argument bound to a local first, the path that already boxed.
	{"arg-via-local-control", `function taker(f: (string) => i32): i32 { return f("hi"); }
function len_of(s: string): i32 { return s.len() + 24; }
function g(): i32 { var t = taker; var l = len_of; return t(l); }
function main(): i32 { return g(); }`, 26},
}

// TestSelfHostBoundCalleeFnArgIRX86_64 runs each case through the production
// x86-64 IR driver and asserts the ANSWER: the module was always IR-eligible,
// so only the exit code separates the fix from the segfault.
func TestSelfHostBoundCalleeFnArgIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range boundCalleeFnArgIRCases {
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
				t.Errorf("%s exited %d, want %d (139 = SIGSEGV)", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostBoundCalleeFnArgIRArm64 is the arm64 counterpart: the fix is in
// the shared lift, and running it here is what proves arm64 picks it up.
func TestSelfHostBoundCalleeFnArgIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("arm64 bound-callee-fn-arg gate needs a native x86 host to run the driver")
	}
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range boundCalleeFnArgIRCases {
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
