package e2ecompiler

import (
	"os/exec"
	"testing"
)

var tupleFnStructFieldCases = []struct {
	name string
	src  string
	exit int
}{
	{"bare", "struct S { p: (i32, () => i32) } function main(): i32 { let n: i32 = 4; let s = S { p: (1, () => n) }; return s.p.1(); }", 4},
	{"arg-elem0", "struct S { p: ((i32) => i32, i32) } function main(): i32 { let n: i32 = 5; let s = S { p: ((x: i32) => x + n, 9) }; return s.p.0(10); }", 15},
	{"two-arg", "struct S { p: (i32, (i32, i32) => i32) } function main(): i32 { let s = S { p: (0, (a: i32, b: i32) => a * b) }; return s.p.1(6, 7); }", 42},
	{"read-then-call", "struct S { p: (i32, () => i32) } function main(): i32 { let n: i32 = 4; let s = S { p: (1, () => n) }; let g = s.p.1; return g(); }", 4},
	{"churn", "struct S { p: (i32, (i32) => i32) } function main(): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < 300) { let k: i32 = i % 7; let s = S { p: (k, (x: i32) => x + k) }; acc = (acc + s.p.1(2) + s.p.0) % 1000; i = i + 1; } return acc % 256; }", 138},
}

func TestSelfHostTupleFnStructFieldX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range tupleFnStructFieldCases {
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

func TestSelfHostTupleFnStructFieldArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("arm64 tuple-fn-struct-field gate needs a native x86 host to run the driver")
	}
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range tupleFnStructFieldCases {
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
