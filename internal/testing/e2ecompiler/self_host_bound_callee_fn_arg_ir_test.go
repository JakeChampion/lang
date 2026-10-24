package e2ecompiler

import (
	"os/exec"
	"testing"
)

var boundCalleeFnArgIRCases = []struct {
	name string
	src  string
	exit int
}{
	{"param-callee-fn-name-arg", `function taker(f: (string) => i32): i32 { return f("hi"); }
function len_of(s: string): i32 { return s.len() + 19; }
function call_it(t: (((string) => i32)) => i32): i32 { return t(len_of); }
function main(): i32 { return call_it(taker); }`, 21},
	{"local-callee-fn-name-arg", `function taker(f: (string) => i32): i32 { return f("hi"); }
function len_of(s: string): i32 { return s.len() + 19; }
function g(): i32 { let t = taker; return t(len_of); }
function main(): i32 { return g(); }`, 21},
	{"local-callee-lambda-arg", `function taker(f: (string) => i32): i32 { return f("hi"); }
function g(): i32 { let t = taker; return t((x: string) => x.len() + 20); }
function main(): i32 { return g(); }`, 22},
	{"annotated-local-callee", `function taker(f: (string) => i32): i32 { return f("hi"); }
function g(): i32 {
    let t: (((string) => i32)) => i32 = taker;
    return t((x: string) => x.len() + 21);
}
function main(): i32 { return g(); }`, 23},
	{"aliased-local-callee", `function taker(f: (string) => i32): i32 { return f("hi"); }
function len_of(s: string): i32 { return s.len() + 22; }
function g(): i32 { let t = taker; let u = t; return u(len_of); }
function main(): i32 { return g(); }`, 24},
	{"use-through-local", `function taker(f: (string) => i32): i32 { return f("hi"); }
function g(): i32 {
    let t = taker;
    use x <- t();
    if (x == "hi") { return 25; }
    return 0;
}
function main(): i32 { return g(); }`, 25},
	{"arg-via-local-control", `function taker(f: (string) => i32): i32 { return f("hi"); }
function len_of(s: string): i32 { return s.len() + 24; }
function g(): i32 { let t = taker; let l = len_of; return t(l); }
function main(): i32 { return g(); }`, 26},
}

func TestSelfHostBoundCalleeFnArgIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range boundCalleeFnArgIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
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

func TestSelfHostBoundCalleeFnArgIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("arm64 bound-callee-fn-arg gate needs a native x86 host to run the driver")
	}
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range boundCalleeFnArgIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
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
