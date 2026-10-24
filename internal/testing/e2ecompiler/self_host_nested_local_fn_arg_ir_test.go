package e2ecompiler

import (
	"os/exec"
	"testing"
)

var nestedLocalFnArgIRCases = []struct {
	name string
	src  string
	exit int
}{
	{"fn-name-to-nested-fn", `function dbl(x: i32): i32 { return x * 2i32; }
function main(): i32 {
    function helper(f: (i32) => i32): i32 { return f(3i32); }
    return helper(dbl) & 63i32;
}`, 6},
	{"lambda-to-nested-fn", `function main(): i32 {
    function helper(f: (i32) => i32): i32 { return f(3i32); }
    let v: i32 = helper(((x: i32) => 481i32));
    return v & 63i32;
}`, 33},
	{"via-local-control", `function main(): i32 {
    function helper(f: (i32) => i32): i32 { return f(3i32); }
    let g: (i32) => i32 = ((x: i32) => 481i32);
    return helper(g) & 63i32;
}`, 33},
	{"top-level-callee-control", `function helper(f: (i32) => i32): i32 { return f(3i32); }
function main(): i32 {
    let v: i32 = helper(((x: i32) => 481i32));
    return v & 63i32;
}`, 33},
}

func TestSelfHostNestedLocalFnArgIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range nestedLocalFnArgIRCases {
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
				t.Errorf("%s exited %d, want %d (139 = SIGSEGV, the #6341 symptom)", tc.name, code, tc.exit)
			}
		})
	}
}

func TestSelfHostNestedLocalFnArgIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("arm64 nested-local-fn-arg gate needs a native x86 host to run the driver")
	}
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range nestedLocalFnArgIRCases {
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
