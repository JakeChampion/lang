package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostArm64IntRuntime byte-checks the integer / load-store / system
// instruction batch added to arm64_native.fern (the surface the self-host
// asm_arm64 darwin runtime uses beyond the earlier slices): orr, subs
// (reg/imm), udiv/sdiv/msub, rev16, ldrb/strb/ldrh/strh/ldrsw, and mrs of
// the clock system registers. Expected bytes pinned vs llvm-mc; run through
// the self-host wasm pipeline. Exit 0 = all pass, else the failing check id.
func TestSelfHostArm64IntRuntime(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host arm64 int-runtime e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")

	source := arm64NativeSrc(t) + "\n" + arm64IntRuntimeSelfTestMain

	wat := runCapture(t, gcc, runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the arm64 int-runtime self-test")
	}
	watPath := filepath.Join(dir, "arm64_intrt_selftest.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", watPath)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("arm64 int-runtime self-test failed at check %d\n--- WAT ---\n%s", code, wat)
	}
}

const arm64IntRuntimeSelfTestMain = `
function main(): i32 {
    // orr x0, x1, x2 -> 0xAA020020 -> 20 00 02 AA
    let a: Arm64Asm = arm64_gas_assemble("orr x0, x1, x2");
    if (a.text[0] != 32 || a.text[1] != 0 || a.text[2] != 2 || a.text[3] != 170) { return 1; }
    // subs x0, x1, x2 -> 0xEB020020 -> 20 00 02 EB
    let b: Arm64Asm = arm64_gas_assemble("subs x0, x1, x2");
    if (b.text[0] != 32 || b.text[1] != 0 || b.text[2] != 2 || b.text[3] != 235) { return 2; }
    // subs x0, x1, #5 -> 0xF1001420 -> 20 14 00 F1
    let c: Arm64Asm = arm64_gas_assemble("subs x0, x1, #5");
    if (c.text[0] != 32 || c.text[1] != 20 || c.text[2] != 0 || c.text[3] != 241) { return 3; }
    // udiv x0, x1, x2 -> 0x9AC20820 -> 20 08 C2 9A
    let d: Arm64Asm = arm64_gas_assemble("udiv x0, x1, x2");
    if (d.text[0] != 32 || d.text[1] != 8 || d.text[2] != 194 || d.text[3] != 154) { return 4; }
    // sdiv x0, x1, x2 -> 0x9AC20C20 -> 20 0C C2 9A
    let e: Arm64Asm = arm64_gas_assemble("sdiv x0, x1, x2");
    if (e.text[0] != 32 || e.text[1] != 12 || e.text[2] != 194 || e.text[3] != 154) { return 5; }
    // msub x0, x1, x2, x3 -> 0x9B028C20 -> 20 8C 02 9B
    let f: Arm64Asm = arm64_gas_assemble("msub x0, x1, x2, x3");
    if (f.text[0] != 32 || f.text[1] != 140 || f.text[2] != 2 || f.text[3] != 155) { return 6; }
    // rev16 x0, x1 -> 0xDAC00420 -> 20 04 C0 DA
    let g: Arm64Asm = arm64_gas_assemble("rev16 x0, x1");
    if (g.text[0] != 32 || g.text[1] != 4 || g.text[2] != 192 || g.text[3] != 218) { return 7; }
    // ldrb w0, [x1, #5] -> 0x39401420 -> 20 14 40 39
    let h: Arm64Asm = arm64_gas_assemble("ldrb w0, [x1, #5]");
    if (h.text[0] != 32 || h.text[1] != 20 || h.text[2] != 64 || h.text[3] != 57) { return 8; }
    // strb w0, [x1, #5] -> 0x39001420 -> 20 14 00 39
    let i: Arm64Asm = arm64_gas_assemble("strb w0, [x1, #5]");
    if (i.text[0] != 32 || i.text[1] != 20 || i.text[2] != 0 || i.text[3] != 57) { return 9; }
    // ldrh w0, [x1, #6] -> 0x79400C20 -> 20 0C 40 79
    let j: Arm64Asm = arm64_gas_assemble("ldrh w0, [x1, #6]");
    if (j.text[0] != 32 || j.text[1] != 12 || j.text[2] != 64 || j.text[3] != 121) { return 10; }
    // strh w0, [x1, #6] -> 0x79000C20 -> 20 0C 00 79
    let k: Arm64Asm = arm64_gas_assemble("strh w0, [x1, #6]");
    if (k.text[0] != 32 || k.text[1] != 12 || k.text[2] != 0 || k.text[3] != 121) { return 11; }
    // ldrsw x0, [x1, #8] -> 0xB9800820 -> 20 08 80 B9
    let l: Arm64Asm = arm64_gas_assemble("ldrsw x0, [x1, #8]");
    if (l.text[0] != 32 || l.text[1] != 8 || l.text[2] != 128 || l.text[3] != 185) { return 12; }
    // mrs x0, cntvct_el0 -> 0xD53BE040 -> 40 E0 3B D5
    let m: Arm64GasProg = arm64_gas_program("mrs x0, cntvct_el0\n");
    let ma: Arm64Asm = m.asm;
    if (ma.text[0] != 64 || ma.text[1] != 224 || ma.text[2] != 59 || ma.text[3] != 213) { return 13; }
    if (m.unknown.len() != 0) { return 14; }
    // mrs x0, cntfrq_el0 -> 0xD53BE000 -> 00 E0 3B D5
    let n: Arm64GasProg = arm64_gas_program("mrs x0, cntfrq_el0\n");
    let na: Arm64Asm = n.asm;
    if (na.text[0] != 0 || na.text[1] != 224 || na.text[2] != 59 || na.text[3] != 213) { return 15; }
    // an unknown sysreg is recorded, not mis-encoded.
    let o: Arm64GasProg = arm64_gas_program("mrs x0, ttbr0_el1\n");
    if (o.unknown.len() != 1) { return 16; }
    return 0;
}
`
