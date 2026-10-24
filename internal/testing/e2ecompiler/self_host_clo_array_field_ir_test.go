package e2ecompiler

import (
	"os/exec"
	"testing"
)

var cloArrayFieldCallCases = []struct {
	name string
	src  string
	exit int
}{
	{"clo-local-built", "struct R { hs: (() => i32)[] }\nfunction main(): i32 { let n: i32 = 3; let c: (() => i32)[] = [() => n]; let r = R { hs: c }; return r.hs[0](); }", 3},
	{"clo-rebound-bind", "struct R { hs: (() => i32)[] }\nfunction seven(): i32 { return 7; }\nfunction main(): i32 { let n: i32 = 5; let a: (() => i32)[] = [seven]; a = [() => n, () => n]; let r: R = R { hs: a }; let f = r.hs[1]; return f(); }", 5},
	{"nocap", "struct Reg { hs: (() => i32)[] } function main(): i32 { let r = Reg { hs: [() => 40] }; return r.hs[0](); }", 40},
	{"capture-multi", "struct Reg { hs: (() => i32)[] } function main(): i32 { let n: i32 = 2; let r = Reg { hs: [() => n, () => n + 1] }; return r.hs[0]() + r.hs[1](); }", 5},
	{"with-arg", "struct Reg { hs: ((i32) => i32)[] } function main(): i32 { let n: i32 = 5; let r = Reg { hs: [(x: i32) => x + n] }; return r.hs[0](10); }", 15},
	{"two-arg", "struct Reg { hs: ((i32, i32) => i32)[] } function main(): i32 { let r = Reg { hs: [(a: i32, b: i32) => a * b] }; return r.hs[0](6, 7); }", 42},
	{"local-array-regress", "function main(): i32 { let n: i32 = 2; let hs: (() => i32)[] = [() => n, () => n + 1]; return hs[0]() + hs[1](); }", 5},
}

func TestSelfHostCloArrayFieldCallIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range cloArrayFieldCallCases {
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

func TestSelfHostCloArrayFieldCallIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("arm64 clo-array-field gate needs a native x86 host to run the driver")
	}
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range cloArrayFieldCallCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
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
