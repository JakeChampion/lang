package e2ecompiler

import (
	"os/exec"
	"testing"
)

var returnClosureContainerIRCases = []struct {
	name string
	src  string
	exit int
}{
	{"field-elem", "struct Reg { hs: (() => i32)[] } function pick(r: Reg): () => i32 { return r.hs[0]; } function main(): i32 { let n: i32 = 8; let r = Reg { hs: [() => n] }; let g = pick(r); return g(); }", 8},
	{"whole-arr", "struct Reg { hs: (() => i32)[] } function pick(r: Reg): (() => i32)[] { return r.hs; } function main(): i32 { let n: i32 = 8; let hs = pick(Reg { hs: [() => n] }); return hs[0](); }", 8},
	{"registry", "struct KV { k: i32, f: () => i32 } function lookup(kvs: KV[], key: i32): () => i32 { let i: i32 = 0; while (i < kvs.len()) { if (kvs[i].k == key) { return kvs[i].f; } i = i + 1; } return () => 0; } function main(): i32 { let n: i32 = 8; let kvs: KV[] = [KV { k: 1, f: () => n }, KV { k: 2, f: () => n * 2 }]; let g = lookup(kvs, 2); return g(); }", 16},
	{"param-fnarr", "function get(fs: (() => i32)[], i: i32): () => i32 { return fs[i]; } function main(): i32 { let n: i32 = 5; let g = get([() => n, () => n + 2], 1); return g(); }", 7},
	{"scalar-field", "struct S { f: () => i32 } function pick(s: S): () => i32 { return s.f; } function main(): i32 { let n: i32 = 9; let g = pick(S { f: () => n }); return g(); }", 9},
	{"field-elem-arg", "struct Reg { hs: ((i32) => i32)[] } function pick(r: Reg): (i32) => i32 { return r.hs[0]; } function main(): i32 { let n: i32 = 5; let r = Reg { hs: [(x: i32) => x + n] }; let g = pick(r); return g(10); }", 15},
}

func TestSelfHostReturnClosureContainerIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range returnClosureContainerIRCases {
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

func TestSelfHostReturnClosureContainerIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("arm64 return-closure-container gate needs a native x86 host to run the driver")
	}
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range returnClosureContainerIRCases {
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
