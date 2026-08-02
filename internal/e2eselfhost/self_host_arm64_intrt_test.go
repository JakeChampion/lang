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
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

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
    var a: Arm64Asm = arm64_gas_assemble("orr x0, x1, x2");
    if (a.code[0] != 32 || a.code[1] != 0 || a.code[2] != 2 || a.code[3] != 170) { return 1; }
    // subs x0, x1, x2 -> 0xEB020020 -> 20 00 02 EB
    var b: Arm64Asm = arm64_gas_assemble("subs x0, x1, x2");
    if (b.code[0] != 32 || b.code[1] != 0 || b.code[2] != 2 || b.code[3] != 235) { return 2; }
    // subs x0, x1, #5 -> 0xF1001420 -> 20 14 00 F1
    var c: Arm64Asm = arm64_gas_assemble("subs x0, x1, #5");
    if (c.code[0] != 32 || c.code[1] != 20 || c.code[2] != 0 || c.code[3] != 241) { return 3; }
    // udiv x0, x1, x2 -> 0x9AC20820 -> 20 08 C2 9A
    var d: Arm64Asm = arm64_gas_assemble("udiv x0, x1, x2");
    if (d.code[0] != 32 || d.code[1] != 8 || d.code[2] != 194 || d.code[3] != 154) { return 4; }
    // sdiv x0, x1, x2 -> 0x9AC20C20 -> 20 0C C2 9A
    var e: Arm64Asm = arm64_gas_assemble("sdiv x0, x1, x2");
    if (e.code[0] != 32 || e.code[1] != 12 || e.code[2] != 194 || e.code[3] != 154) { return 5; }
    // msub x0, x1, x2, x3 -> 0x9B028C20 -> 20 8C 02 9B
    var f: Arm64Asm = arm64_gas_assemble("msub x0, x1, x2, x3");
    if (f.code[0] != 32 || f.code[1] != 140 || f.code[2] != 2 || f.code[3] != 155) { return 6; }
    // rev16 x0, x1 -> 0xDAC00420 -> 20 04 C0 DA
    var g: Arm64Asm = arm64_gas_assemble("rev16 x0, x1");
    if (g.code[0] != 32 || g.code[1] != 4 || g.code[2] != 192 || g.code[3] != 218) { return 7; }
    // ldrb w0, [x1, #5] -> 0x39401420 -> 20 14 40 39
    var h: Arm64Asm = arm64_gas_assemble("ldrb w0, [x1, #5]");
    if (h.code[0] != 32 || h.code[1] != 20 || h.code[2] != 64 || h.code[3] != 57) { return 8; }
    // strb w0, [x1, #5] -> 0x39001420 -> 20 14 00 39
    var i: Arm64Asm = arm64_gas_assemble("strb w0, [x1, #5]");
    if (i.code[0] != 32 || i.code[1] != 20 || i.code[2] != 0 || i.code[3] != 57) { return 9; }
    // ldrh w0, [x1, #6] -> 0x79400C20 -> 20 0C 40 79
    var j: Arm64Asm = arm64_gas_assemble("ldrh w0, [x1, #6]");
    if (j.code[0] != 32 || j.code[1] != 12 || j.code[2] != 64 || j.code[3] != 121) { return 10; }
    // strh w0, [x1, #6] -> 0x79000C20 -> 20 0C 00 79
    var k: Arm64Asm = arm64_gas_assemble("strh w0, [x1, #6]");
    if (k.code[0] != 32 || k.code[1] != 12 || k.code[2] != 0 || k.code[3] != 121) { return 11; }
    // ldrsw x0, [x1, #8] -> 0xB9800820 -> 20 08 80 B9
    var l: Arm64Asm = arm64_gas_assemble("ldrsw x0, [x1, #8]");
    if (l.code[0] != 32 || l.code[1] != 8 || l.code[2] != 128 || l.code[3] != 185) { return 12; }
    // mrs x0, cntvct_el0 -> 0xD53BE040 -> 40 E0 3B D5
    var m: Arm64GasProg = arm64_gas_program("mrs x0, cntvct_el0\n");
    var ma: Arm64Asm = m.asm;
    if (ma.code[0] != 64 || ma.code[1] != 224 || ma.code[2] != 59 || ma.code[3] != 213) { return 13; }
    if (m.unknown.len() != 0) { return 14; }
    // mrs x0, cntfrq_el0 -> 0xD53BE000 -> 00 E0 3B D5
    var n: Arm64GasProg = arm64_gas_program("mrs x0, cntfrq_el0\n");
    var na: Arm64Asm = n.asm;
    if (na.code[0] != 0 || na.code[1] != 224 || na.code[2] != 59 || na.code[3] != 213) { return 15; }
    // an unknown sysreg is recorded, not mis-encoded.
    var o: Arm64GasProg = arm64_gas_program("mrs x0, ttbr0_el1\n");
    if (o.unknown.len() != 1) { return 16; }
    return 0;
}
`
