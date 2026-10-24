package e2ecompiler

import (
	"os/exec"
	"testing"
)

var returnClosureNestedIRCases = []struct {
	name string
	src  string
	exit int
}{
	{"plain", "function pick(n: i32): () => (() => i32) { return () => () => n; } function main(): i32 { let g = pick(7); let h = g(); return h(); }", 7},
	{"inner-arith", "function pick(n: i32): () => (() => i32) { return () => () => n + 1; } function main(): i32 { let g = pick(10); let h = g(); return h(); }", 11},
	{"inner-arg", "function pick(n: i32): () => ((i32) => i32) { return () => (x: i32) => x + n; } function main(): i32 { let g = pick(5); let h = g(); return h(10); }", 15},
	{"chain-local", "function pick(n: i32): () => (() => i32) { return () => () => n; } function main(): i32 { let g = pick(9); return g()(); }", 9},
	{"chain-full", "function pick(n: i32): () => (() => i32) { return () => () => n; } function main(): i32 { return pick(9)()(); }", 9},
	{"chain-arg", "function pick(n: i32): () => ((i32) => i32) { return () => (x: i32) => x + n; } function main(): i32 { return pick(5)()(10); }", 15},
}

func TestSelfHostReturnClosureNestedIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range returnClosureNestedIRCases {
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

func TestSelfHostReturnClosureNestedIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("arm64 return-closure-nested gate needs a native x86 host to run the driver")
	}
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range returnClosureNestedIRCases {
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
