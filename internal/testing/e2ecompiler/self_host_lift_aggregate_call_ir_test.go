package e2ecompiler

import (
	"os/exec"
	"testing"
)

var liftAggregateCallIRCases = []struct {
	name string
	src  string
	exit int
}{
	{"call-in-array-literal", `function main(): i32 {
    let v0: (i32) => i32 = ((x: i32) => (x * x));
    let v3: i32[] = [v0(2i32)];
    return v3[0i32] & 63i32;
}`, 4},
	{"call-in-array-call-arg", `function take(a: i32[], b: i32[]): i32[] { return a; }
function main(): i32 {
    let v0: (i32) => i32 = ((x: i32) => (x * x));
    let v3: i32[] = take([474i32], [v0(2i32)]);
    return v3[0i32] & 63i32;
}`, 26},
	{"call-in-tuple-literal", `function main(): i32 {
    let v0: (i32) => i32 = ((x: i32) => (x * x));
    let t: (i32, i32) = (v0(2i32), 1i32);
    return (t.0 + t.1) & 63i32;
}`, 5},
	{"call-in-struct-literal", `struct Box { n: i32, m: i32 }
function main(): i32 {
    let v0: (i32) => i32 = ((x: i32) => (x * x));
    let b: Box = Box { n: v0(3i32), m: 1i32 };
    return (b.n + b.m) & 63i32;
}`, 10},
	{"call-in-struct-literal-with-base", `struct Box { n: i32, m: i32 }
function main(): i32 {
    let v0: (i32) => i32 = ((x: i32) => (x * x));
    let base: Box = Box { n: 1i32, m: 2i32 };
    let b: Box = Box { ...base, n: v0(3i32) };
    return (b.n + b.m) & 63i32;
}`, 11},
	{"hoisted-to-temp-control", `function main(): i32 {
    let v0: (i32) => i32 = ((x: i32) => (x * x));
    let t: i32 = v0(2i32);
    let v3: i32[] = [t];
    return v3[0i32] & 63i32;
}`, 4},
}

func TestSelfHostLiftAggregateCallIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range liftAggregateCallIRCases {
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

func TestSelfHostLiftAggregateCallIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("arm64 aggregate-call lift gate needs a native x86 host to run the driver")
	}
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range liftAggregateCallIRCases {
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
