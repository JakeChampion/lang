package e2ecompiler

import (
	"os/exec"
	"testing"
)

var cloArrayFieldBindCases = []struct {
	name string
	src  string
	exit int
}{
	{"bind", "struct Reg { hs: (() => i32)[] } function main(): i32 { let n: i32 = 40; let r = Reg { hs: [() => n] }; let f = r.hs[0]; return f(); }", 40},
	{"alias", "struct Reg { hs: (() => i32)[] } function main(): i32 { let n: i32 = 40; let r = Reg { hs: [() => n] }; let fns = r.hs; return fns[0](); }", 40},
	{"for", "struct Reg { hs: (() => i32)[] } function main(): i32 { let n: i32 = 2; let r = Reg { hs: [() => n, () => n + 1] }; let acc: i32 = 0; for h in r.hs { acc = acc + h(); } return acc; }", 5},
	{"bind-arg", "struct Reg { hs: ((i32) => i32)[] } function main(): i32 { let n: i32 = 5; let r = Reg { hs: [(x: i32) => x + n] }; let f = r.hs[0]; return f(10); }", 15},
	{"alias-for", "struct Reg { hs: (() => i32)[] } function main(): i32 { let n: i32 = 3; let r = Reg { hs: [() => n, () => n * 2] }; let fns = r.hs; let acc: i32 = 0; for h in fns { acc = acc + h(); } return acc; }", 9},
	{"rc-soundness", "struct Reg { hs: (() => i32)[] } function one(k: i32): i32 { let r = Reg { hs: [() => k, () => k + 1] }; let f = r.hs[0]; let acc: i32 = f(); for h in r.hs { acc = acc + h(); } let fns = r.hs; for g in fns { acc = acc + g(); } return acc; } function churn(n: i32): i32 { let i: i32 = 0; let s: i32 = 0; while (i < n) { s = one(i); i = i + 1; } return s; } function main(): i32 { let w: i32 = churn(3000); let b1: i32 = (__heap_bump_bytes() as i32); let x: i32 = churn(3000); let b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if (b2 - b1 >= 4096) { return 98; } if (w != x) { return 97; } return 0; }", 0},
}

func TestSelfHostCloArrayFieldBindIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range cloArrayFieldBindCases {
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

func TestSelfHostCloArrayFieldBindIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("arm64 clo-array-field-bind gate needs a native x86 host to run the driver")
	}
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range cloArrayFieldBindCases {
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
