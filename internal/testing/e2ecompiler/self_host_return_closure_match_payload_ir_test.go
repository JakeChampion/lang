package e2ecompiler

import (
	"os/exec"
	"testing"
)

var returnMatchPayloadIRCases = []struct {
	name string
	src  string
	exit int
}{
	{"plain", "enum Box { W(() => i32) } function pick(b: Box): () => i32 { match (b) { W(f) => { return f; } } } function main(): i32 { let n: i32 = 9; let g = pick(Box.W(() => n)); return g(); }", 9},
	{"arg-payload", "enum Box { W((i32) => i32) } function pick(b: Box): (i32) => i32 { match (b) { W(f) => { return f; } } } function main(): i32 { let n: i32 = 5; let g = pick(Box.W((x: i32) => x + n)); return g(10); }", 15},
	{"multi-arm", "enum Box { A(() => i32), B(() => i32) } function pick(b: Box): () => i32 { match (b) { A(f) => { return f; }, B(g) => { return g; } } } function main(): i32 { let n: i32 = 3; let h = pick(Box.B(() => n * 7)); return h(); }", 21},
	{"qualified-pat", "enum Box { W(() => i32) } function pick(b: Box): () => i32 { match (b) { Box.W(f) => { return f; } } } function main(): i32 { let n: i32 = 4; let g = pick(Box.W(() => n)); return g(); }", 4},
	{"nested-if", "enum Box { W(() => i32) } function pick(b: Box, flag: i32): () => i32 { match (b) { W(f) => { if (flag > 0) { return f; } else { return f; } } } } function main(): i32 { let n: i32 = 6; let g = pick(Box.W(() => n), 1); return g(); }", 6},
	{"call-not-return", "enum Box { W(() => i32) } function pick(b: Box): i32 { match (b) { W(f) => { return f(); } } } function main(): i32 { let n: i32 = 11; return pick(Box.W(() => n)); }", 11},
	{"nonfn-payload", "enum Box { W(i32) } function pick(b: Box): i32 { match (b) { W(v) => { return v; } } } function main(): i32 { let g = pick(Box.W(42)); return g; }", 42},
}

func TestSelfHostReturnMatchPayloadIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range returnMatchPayloadIRCases {
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

func TestSelfHostReturnMatchPayloadIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("arm64 return-match-payload gate needs a native x86 host to run the driver")
	}
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range returnMatchPayloadIRCases {
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
