package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostArm64LitPoolGas byte-checks two instruction-selection rules
// arm64_native originally got wrong (they silently mis-assembled, so the
// darwin tests — which only check exit codes / Mach-O parseability — never
// caught them):
//
//   - `ldr Xd, =N` + `.ltorg`: the PC-relative literal pool. asm_arm64
//     loads every integer immediate this way and flushes the pool after each
//     function. The old code parsed `ldr x0, =42` as `ldr x0, [x0]` (a
//     memory load from a garbage base) and dropped `.ltorg` entirely.
//   - `str/ldr Xt, [Xn, #neg]`: a negative frame offset can't use the scaled
//     unsigned form (it mis-encoded `[x29, #-8]` as `[x29, #0x7ff8]`); it
//     must fall back to the unscaled signed-imm9 stur/ldur, as GAS does.
//
// Expected bytes pinned against llvm-mc. Run through the self-host wasm
// pipeline; exit 0 = all pass, else the 1-based failing check id.
func TestSelfHostArm64LitPoolGas(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host arm64 litpool gas e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "wasm.fern", "wasm_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	source := arm64NativeSrc(t) + "\n" + arm64LitPoolGasSelfTestMain

	wat := runCapture(t, gcc, runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the arm64 litpool gas self-test")
	}
	watPath := filepath.Join(dir, "arm64_litpool_gas_selftest.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", watPath)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("arm64 litpool gas self-test failed at check %d\n--- WAT ---\n%s", code, wat)
	}
}

const arm64LitPoolGasSelfTestMain = `
function main(): i32 {
    // ldr x0, =42 + .ltorg: the ldr becomes an LDR-literal pointing 8 bytes
    // ahead (imm19=2 -> 0x58000040 -> 40 00 00 58); the pool is 8-aligned
    // (one 4-byte pad) then the value 42 as a little-endian quad.
    var a: Arm64Asm = arm64_gas_assemble("ldr x0, =42\n.ltorg\n");
    if (a.code.len() != 16) { return 1; }
    if (a.code[0] != 64 || a.code[1] != 0 || a.code[2] != 0 || a.code[3] != 88) { return 2; }
    if (a.code[4] != 0 || a.code[5] != 0 || a.code[6] != 0 || a.code[7] != 0) { return 3; }
    if (a.code[8] != 42 || a.code[9] != 0 || a.code[10] != 0 || a.code[11] != 0) { return 4; }
    if (a.code[12] != 0 || a.code[13] != 0 || a.code[14] != 0 || a.code[15] != 0) { return 5; }
    // A pending literal flushes even without an explicit .ltorg (end of
    // program): ldr x9, =256 -> the value 256 little-endian at offset 8.
    var b: Arm64Asm = arm64_gas_assemble("ldr x9, =256\n");
    if (b.code.len() != 16) { return 6; }
    if (b.code[0] != 73 || b.code[3] != 88) { return 7; } // x9, LDR-literal (0x58000049)
    if (b.code[8] != 0 || b.code[9] != 1 || b.code[10] != 0 || b.code[11] != 0) { return 8; } // 256 = 0x0100
    // str x0, [x29, #-8] -> stur (unscaled signed imm9) -> 0xF81F83A0 -> A0 83 1F F8
    var c: Arm64Asm = arm64_gas_assemble("str x0, [x29, #-8]");
    if (c.code[0] != 160 || c.code[1] != 131 || c.code[2] != 31 || c.code[3] != 248) { return 9; }
    // ldr x0, [x29, #-8] -> ldur -> 0xF85F83A0 -> A0 83 5F F8
    var d: Arm64Asm = arm64_gas_assemble("ldr x0, [x29, #-8]");
    if (d.code[0] != 160 || d.code[1] != 131 || d.code[2] != 95 || d.code[3] != 248) { return 10; }
    // positive 8-aligned offset still uses the scaled unsigned form:
    // str x0, [x29, #8] -> 0xF90007A0 -> A0 07 00 F9
    var e: Arm64Asm = arm64_gas_assemble("str x0, [x29, #8]");
    if (e.code[0] != 160 || e.code[1] != 7 || e.code[2] != 0 || e.code[3] != 249) { return 11; }
    return 0;
}
`
