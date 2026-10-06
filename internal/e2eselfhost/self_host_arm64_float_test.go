package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostArm64Float byte-checks the f64 (scalar double) instruction
// batch added to arm64_native.fern — fadd/fsub/fmul/fdiv, fcmp, the three
// fmov forms, fcvtzs, scvtf, and the whole `<op> Dd, Dn` unary family
// (fneg / fabs / fsqrt / frintm / frintp / frintz / frinta) — against the
// llvm-mc-pinned encodings, via the self-host wasm pipeline.
//
// All but fneg and frinta of that unary family were missing, so the
// assembler rejected the asm_arm64 emitter's own output for any program
// using __sqrt_f64 / floor / ceil / trunc / abs: `in-process assembler hit
// an instruction it does not yet support: fsqrt`.
func TestSelfHostArm64Float(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host arm64 float e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")
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
    let a: Arm64Asm = arm64_gas_assemble("fadd d0, d1, d2");   // 0x1E622820
    if (a.text[0] != 32 || a.text[1] != 40 || a.text[2] != 98 || a.text[3] != 30) { return 1; }
    let b: Arm64Asm = arm64_gas_assemble("fsub d0, d1, d2");   // 0x1E623820
    if (b.text[0] != 32 || b.text[1] != 56 || b.text[2] != 98 || b.text[3] != 30) { return 2; }
    let c: Arm64Asm = arm64_gas_assemble("fmul d0, d1, d2");   // 0x1E620820
    if (c.text[0] != 32 || c.text[1] != 8 || c.text[2] != 98 || c.text[3] != 30) { return 3; }
    let d: Arm64Asm = arm64_gas_assemble("fdiv d0, d1, d2");   // 0x1E621820
    if (d.text[0] != 32 || d.text[1] != 24 || d.text[2] != 98 || d.text[3] != 30) { return 4; }
    let e: Arm64Asm = arm64_gas_assemble("fneg d0, d1");       // 0x1E614020
    if (e.text[0] != 32 || e.text[1] != 64 || e.text[2] != 97 || e.text[3] != 30) { return 5; }
    let f: Arm64Asm = arm64_gas_assemble("fcmp d1, d2");       // 0x1E622020
    if (f.text[0] != 32 || f.text[1] != 32 || f.text[2] != 98 || f.text[3] != 30) { return 6; }
    let g: Arm64Asm = arm64_gas_assemble("frinta d3, d2");     // 0x1E664043
    if (g.text[0] != 67 || g.text[1] != 64 || g.text[2] != 102 || g.text[3] != 30) { return 7; }
    let h: Arm64Asm = arm64_gas_assemble("fmov d0, d1");       // 0x1E604020
    if (h.text[0] != 32 || h.text[1] != 64 || h.text[2] != 96 || h.text[3] != 30) { return 8; }
    let i: Arm64Asm = arm64_gas_assemble("fmov d1, x10");      // 0x9E670141
    if (i.text[0] != 65 || i.text[1] != 1 || i.text[2] != 103 || i.text[3] != 158) { return 9; }
    let j: Arm64Asm = arm64_gas_assemble("fmov x10, d0");      // 0x9E66000A
    if (j.text[0] != 10 || j.text[1] != 0 || j.text[2] != 102 || j.text[3] != 158) { return 10; }
    let k: Arm64Asm = arm64_gas_assemble("fcvtzs x10, d3");    // 0x9E78006A
    if (k.text[0] != 106 || k.text[1] != 0 || k.text[2] != 120 || k.text[3] != 158) { return 11; }
    let l: Arm64Asm = arm64_gas_assemble("scvtf d3, x11");     // 0x9E620163
    if (l.text[0] != 99 || l.text[1] != 1 || l.text[2] != 98 || l.text[3] != 158) { return 12; }
    let m: Arm64Asm = arm64_gas_assemble("fabs d0, d1");       // 0x1E60C020
    if (m.text[0] != 32 || m.text[1] != 192 || m.text[2] != 96 || m.text[3] != 30) { return 13; }
    let n: Arm64Asm = arm64_gas_assemble("fsqrt d0, d1");      // 0x1E61C020
    if (n.text[0] != 32 || n.text[1] != 192 || n.text[2] != 97 || n.text[3] != 30) { return 14; }
    let o: Arm64Asm = arm64_gas_assemble("frintm d0, d1");     // 0x1E654020
    if (o.text[0] != 32 || o.text[1] != 64 || o.text[2] != 101 || o.text[3] != 30) { return 15; }
    let q: Arm64Asm = arm64_gas_assemble("frintp d0, d1");     // 0x1E64C020
    if (q.text[0] != 32 || q.text[1] != 192 || q.text[2] != 100 || q.text[3] != 30) { return 16; }
    let r: Arm64Asm = arm64_gas_assemble("frintz d0, d1");     // 0x1E65C020
    if (r.text[0] != 32 || r.text[1] != 192 || r.text[2] != 101 || r.text[3] != 30) { return 17; }
    return 0;
}
`
