package e2ecompiler

import (
	"os/exec"
	"testing"
)

var returnLambdaDispatchIRCases = []struct {
	name string
	src  string
	exit int
}{
	{"single-capturing", "function pick(n: i32): () => i32 { return () => n; } function main(): i32 { let g = pick(6); return g(); }", 6},
	{"single-noncapturing", "function pick(): () => i32 { return () => 7; } function main(): i32 { let g = pick(); return g(); }", 7},
	{"noncapturing-arg", "function pick(): (i32) => i32 { return (x: i32) => x + 1; } function main(): i32 { let g = pick(); return g(10); }", 11},
	{"two-capturing-then", "function pick(flag: i32, n: i32): () => i32 { if (flag > 0) { return () => n; } return () => n + 1; } function main(): i32 { let g = pick(1, 6); return g(); }", 6},
	{"two-capturing-else", "function pick(flag: i32, n: i32): () => i32 { if (flag > 0) { return () => n; } return () => n + 1; } function main(): i32 { let g = pick(0, 6); return g(); }", 7},
	{"seq-two-noncapturing", "function pick(): () => i32 { if (true) { return () => 3; } return () => 9; } function main(): i32 { let g = pick(); return g(); }", 3},
	{"match-payload-lambda-sibling", "enum Box { W(() => i32) } function pick(b: Box, flag: i32): () => i32 { match (b) { W(f) => { if (flag > 0) { return f; } return () => 0; } } } function main(): i32 { let n: i32 = 6; let g = pick(Box.W(() => n), 1); return g(); }", 6},
	{"match-payload-lambda-fallthrough", "enum Box { W(() => i32) } function pick(b: Box, flag: i32): () => i32 { match (b) { W(f) => { if (flag > 0) { return f; } return () => 42; } } } function main(): i32 { let n: i32 = 6; let g = pick(Box.W(() => n), 0); return g(); }", 42},
}

func TestSelfHostReturnLambdaDispatchIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range returnLambdaDispatchIRCases {
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

func TestSelfHostReturnLambdaDispatchIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("arm64 return-lambda-dispatch gate needs a native x86 host to run the driver")
	}
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range returnLambdaDispatchIRCases {
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
