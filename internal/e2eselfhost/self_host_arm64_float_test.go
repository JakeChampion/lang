package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostArm64Float byte-checks the f64 (scalar double) instruction
// batch added to arm64_native.fern — fadd/fsub/fmul/fdiv, fneg, fcmp,
// frinta, the three fmov forms, fcvtzs, scvtf — against the llvm-mc-pinned
// encodings, via the self-host wasm pipeline.
func TestSelfHostArm64Float(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host arm64 float e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	source := arm64NativeSrc(t) + "\n" + arm64FloatSelfTestMain

	wat := runCapture(t, gcc, runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the arm64 float self-test")
	}
	watPath := filepath.Join(dir, "arm64_float_selftest.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", watPath)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("arm64 float self-test failed at check %d\n--- WAT ---\n%s", code, wat)
	}
}

const arm64FloatSelfTestMain = `
function main(): i32 {
    var a: Arm64Asm = arm64_gas_assemble("fadd d0, d1, d2");   // 0x1E622820
    if (a.code[0] != 32 || a.code[1] != 40 || a.code[2] != 98 || a.code[3] != 30) { return 1; }
    var b: Arm64Asm = arm64_gas_assemble("fsub d0, d1, d2");   // 0x1E623820
    if (b.code[0] != 32 || b.code[1] != 56 || b.code[2] != 98 || b.code[3] != 30) { return 2; }
    var c: Arm64Asm = arm64_gas_assemble("fmul d0, d1, d2");   // 0x1E620820
    if (c.code[0] != 32 || c.code[1] != 8 || c.code[2] != 98 || c.code[3] != 30) { return 3; }
    var d: Arm64Asm = arm64_gas_assemble("fdiv d0, d1, d2");   // 0x1E621820
    if (d.code[0] != 32 || d.code[1] != 24 || d.code[2] != 98 || d.code[3] != 30) { return 4; }
    var e: Arm64Asm = arm64_gas_assemble("fneg d0, d1");       // 0x1E614020
    if (e.code[0] != 32 || e.code[1] != 64 || e.code[2] != 97 || e.code[3] != 30) { return 5; }
    var f: Arm64Asm = arm64_gas_assemble("fcmp d1, d2");       // 0x1E622020
    if (f.code[0] != 32 || f.code[1] != 32 || f.code[2] != 98 || f.code[3] != 30) { return 6; }
    var g: Arm64Asm = arm64_gas_assemble("frinta d3, d2");     // 0x1E664043
    if (g.code[0] != 67 || g.code[1] != 64 || g.code[2] != 102 || g.code[3] != 30) { return 7; }
    var h: Arm64Asm = arm64_gas_assemble("fmov d0, d1");       // 0x1E604020
    if (h.code[0] != 32 || h.code[1] != 64 || h.code[2] != 96 || h.code[3] != 30) { return 8; }
    var i: Arm64Asm = arm64_gas_assemble("fmov d1, x10");      // 0x9E670141
    if (i.code[0] != 65 || i.code[1] != 1 || i.code[2] != 103 || i.code[3] != 158) { return 9; }
    var j: Arm64Asm = arm64_gas_assemble("fmov x10, d0");      // 0x9E66000A
    if (j.code[0] != 10 || j.code[1] != 0 || j.code[2] != 102 || j.code[3] != 158) { return 10; }
    var k: Arm64Asm = arm64_gas_assemble("fcvtzs x10, d3");    // 0x9E78006A
    if (k.code[0] != 106 || k.code[1] != 0 || k.code[2] != 120 || k.code[3] != 158) { return 11; }
    var l: Arm64Asm = arm64_gas_assemble("scvtf d3, x11");     // 0x9E620163
    if (l.code[0] != 99 || l.code[1] != 1 || l.code[2] != 98 || l.code[3] != 158) { return 12; }
    return 0;
}
`
