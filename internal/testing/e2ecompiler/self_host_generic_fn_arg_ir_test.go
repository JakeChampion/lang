package e2ecompiler

import (
	"os/exec"
	"testing"
)

var genericFnArgIRCases = []struct {
	name string
	src  string
	exit int
}{
	{"capturing-through-generic", `function id[T](x: T): T { return x; }
function gen(p: i32): i32 {
    let v: (i32) => i32 = id(((a: i32) => (a + p)));
    return v(1i32);
}
function main(): i32 { return gen(6i32) & 255i32; }`, 7},
	{"non-capturing-through-generic", `function id[T](x: T): T { return x; }
function gen(p: i32): i32 {
    let v: (i32) => i32 = id(((a: i32) => (a + 1i32)));
    return v(1i32);
}
function main(): i32 { return gen(6i32) & 255i32; }`, 2},
	{"capturing-through-nested-generic", `function id[T](x: T): T { return x; }
function gen(p: i32): i32 {
    let v: (i32) => i32 = id(id(((a: i32) => (a + p))));
    return v(1i32);
}
function main(): i32 { return gen(6i32) & 255i32; }`, 7},
	{"non-capturing-through-nested-generic", `function id[T](x: T): T { return x; }
function gen(p: i32): i32 {
    let v: (i32) => i32 = id(id(((a: i32) => (a + 1i32))));
    return v(1i32);
}
function main(): i32 { return gen(6i32) & 255i32; }`, 2},
	{"through-generic-of-pick", `function id[T](x: T): T { return x; }
function pick[T](cond: boolean, a: T, b: T): T { return if (cond) { a } else { b }; }
function gen(p: i32): i32 {
    let v: (i32) => i32 = id(pick(false, ((a: i32) => 40i32), ((b: i32) => (b + p))));
    return v(1i32);
}
function main(): i32 { return gen(6i32) & 255i32; }`, 7},
	{"bound-directly-control", `function gen(p: i32): i32 {
    let v: (i32) => i32 = ((a: i32) => (a + p));
    return v(1i32);
}
function main(): i32 { return gen(6i32) & 255i32; }`, 7},
	{"fn-typed-param-control", `function apply(f: (i32) => i32, x: i32): i32 { return f(x); }
function gen(p: i32): i32 { return apply(((a: i32) => (a + p)), 1i32); }
function main(): i32 { return gen(6i32) & 255i32; }`, 7},
	{"fn-typed-param-returning-it-control", `function idf(f: (i32) => i32): (i32) => i32 { return f; }
function gen(p: i32): i32 {
    let v: (i32) => i32 = idf(((a: i32) => (a + p)));
    return v(1i32);
}
function main(): i32 { return gen(6i32) & 255i32; }`, 7},
}

func TestSelfHostGenericFnArgIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range genericFnArgIRCases {
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
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

func TestSelfHostGenericFnArgIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("arm64 generic-fn-arg gate needs a native x86 host to run the driver")
	}
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range genericFnArgIRCases {
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
