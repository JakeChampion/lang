package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostArm64BitOpsGas byte-checks arm64_gas/arm64_encode's bit-
// manipulation surface: neg, ubfx, tbz/tbnz (with an imm14 label fixup),
// the extended condition codes (cc/cs/hi/ls/...), and the bit-counting trio
// rbit / cnt / addv that the popcount and ctz lowerings emit, against the
// llvm-mc-pinned encodings, through the self-host wasm pipeline. Exit 0 =
// all pass, else the failing check id.
func TestSelfHostArm64BitOpsGas(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host arm64 bitops gas e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")

	source := arm64NativeSrc(t) + "\n" + arm64BitOpsGasSelfTestMain

	wat := runCapture(t, gcc, runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the arm64 bitops gas self-test")
	}
	watPath := filepath.Join(dir, "arm64_bitops_gas_selftest.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", watPath)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("arm64 bitops gas self-test failed at check %d\n--- WAT ---\n%s", code, wat)
	}
}

// TestSelfHostArm64DarwinMachOBitOpsRuns exercises the bit ops end-to-end:
// a Fern program assembles `ubfx`/`neg`/`tbz` into a value computation
// (extract -> negate twice -> add -> test-bit branch) that exits 42, wraps
// it with macho.fern, and the signed Mach-O runs: no external tool.
func TestSelfHostArm64DarwinMachOBitOpsRuns(t *testing.T) {
	assertMachORuns(t, machoRun{name: "bitops42", main: arm64MachOBitOpsDriverMain, wantExit: 42})
}

// arm64BitOpsGasSelfTestMain asserts the new encoders against their
// llvm-mc-pinned bytes. Each `return N` is a distinct failing-check id.
const arm64BitOpsGasSelfTestMain = `
function badvec(mnem: string, text: string): string {
    let ops: string[] = arm64_gas_operands(text);
    return arm64_gas_bad_vec_token(mnem, ops, arm64_gas_line_mem(ops));
}
function main(): i32 {
    // neg x0, x0 (sub x0, xzr, x0) -> 0xCB0003E0 -> E0 03 00 CB
    let a: Arm64Asm = arm64_gas_assemble("neg x0, x0");
    if (a.text[0] != 224 || a.text[1] != 3 || a.text[2] != 0 || a.text[3] != 203) { return 1; }
    // ubfx x1, x0, #1, #5 -> 0xD3411401 -> 01 14 41 D3
    let b: Arm64Asm = arm64_gas_assemble("ubfx x1, x0, #1, #5");
    if (b.text[0] != 1 || b.text[1] != 20 || b.text[2] != 65 || b.text[3] != 211) { return 2; }
    // tbnz x0, #1, skip (rel +4) -> 0x37080020 -> 20 00 08 37
    let c: Arm64Asm = arm64_gas_assemble("tbnz x0, #1, skip\nskip:\n");
    if (c.text[0] != 32 || c.text[1] != 0 || c.text[2] != 8 || c.text[3] != 55) { return 3; }
    // tbz x0, #0, skip (rel +4) -> 0x36000020 -> 20 00 00 36
    let d: Arm64Asm = arm64_gas_assemble("tbz x0, #0, skip\nskip:\n");
    if (d.text[0] != 32 || d.text[1] != 0 || d.text[2] != 0 || d.text[3] != 54) { return 4; }
    // b.cc end (cond 3, rel +4) -> 0x54000023 -> 23 00 00 54
    let e: Arm64Asm = arm64_gas_assemble("b.cc end\nend:\n");
    if (e.text[0] != 35 || e.text[1] != 0 || e.text[2] != 0 || e.text[3] != 84) { return 5; }
    // b.hi end (cond 8, rel +4) -> 0x54000028 -> 28 00 00 54
    let f: Arm64Asm = arm64_gas_assemble("b.hi end\nend:\n");
    if (f.text[0] != 40 || f.text[1] != 0 || f.text[2] != 0 || f.text[3] != 84) { return 6; }
    // condition-code values.
    if (arm64_gas_cond("cc") != 3 || arm64_gas_cond("hs") != 2 || arm64_gas_cond("ls") != 9) { return 7; }
    // rbit x0, x0 -> 0xDAC00000 -> 00 00 C0 DA. The ctz idiom's first half.
    let g: Arm64Asm = arm64_gas_assemble("rbit x0, x0");
    if (g.text[0] != 0 || g.text[1] != 0 || g.text[2] != 192 || g.text[3] != 218) { return 8; }
    // rbit w0, w0 -> 0x5AC00000 -> 00 00 C0 5A. The sf clear: without it a
    // 32-bit ctz reverses over 64 bits and counts the empty upper half.
    let h: Arm64Asm = arm64_gas_assemble("rbit w0, w0");
    if (h.text[0] != 0 || h.text[1] != 0 || h.text[2] != 192 || h.text[3] != 90) { return 9; }
    // cnt v0.8b, v0.8b -> 0x0E205800 -> 00 58 20 0E
    let i0: Arm64Asm = arm64_gas_assemble("cnt v0.8b, v0.8b");
    if (i0.text[0] != 0 || i0.text[1] != 88 || i0.text[2] != 32 || i0.text[3] != 14) { return 10; }
    // addv b0, v0.8b -> 0x0E31B800 -> 00 B8 31 0E
    let j0: Arm64Asm = arm64_gas_assemble("addv b0, v0.8b");
    if (j0.text[0] != 0 || j0.text[1] != 184 || j0.text[2] != 49 || j0.text[3] != 14) { return 11; }
    // The Q bit, on both: cnt v1.16b, v2.16b -> 0x4E205841 -> 41 58 20 4E
    let k0: Arm64Asm = arm64_gas_assemble("cnt v1.16b, v2.16b");
    if (k0.text[0] != 65 || k0.text[1] != 88 || k0.text[2] != 32 || k0.text[3] != 78) { return 12; }
    // addv b3, v5.16b -> 0x4E31B8A3 -> A3 B8 31 4E
    let l0: Arm64Asm = arm64_gas_assemble("addv b3, v5.16b");
    if (l0.text[0] != 163 || l0.text[1] != 184 || l0.text[2] != 49 || l0.text[3] != 78) { return 13; }
    // addv h0, v0.8h -> 0x4E71B800 -> 00 B8 71 4E. The across-lanes class: the
    // destination is the scalar class the source arrangement names.
    let m0: Arm64Asm = arm64_gas_assemble("addv h0, v0.8h");
    if (m0.text[0] != 0 || m0.text[1] != 184 || m0.text[2] != 113 || m0.text[3] != 78) { return 14; }
    // A shape the encoding has no form for must be REFUSED, not assembled as
    // a different instruction on the same bytes. In order: cnt is byte-lane
    // only; a pair must share one arrangement; an across-lanes destination is
    // the scalar class the arrangement names, since nothing in the encoding
    // says how wide the result is; .2s has no across-lanes form; a vector
    // destination where a scalar is required; a missing operand.
    if (badvec("cnt", "v0.4h, v0.4h").len() == 0) { return 15; }
    if (badvec("cnt", "v0.8b, v1.16b").len() == 0) { return 16; }
    if (badvec("addv", "b0, v0.8h").len() == 0) { return 17; }
    if (badvec("addv", "s0, v0.2s").len() == 0) { return 18; }
    if (badvec("addv", "v0.8b, v0.8b").len() == 0) { return 19; }
    if (badvec("cnt", "v0.8b").len() == 0) { return 20; }
    // ...and a well-formed pair must NOT be refused.
    if (badvec("cnt", "v0.8b, v0.8b").len() != 0) { return 21; }
    if (badvec("addv", "b0, v0.8b").len() != 0) { return 22; }
    return 0;
}
`

// arm64MachOBitOpsDriverMain computes 42 via ubfx + neg + a tbz branch:
// ubfx extracts (42>>1)&31 = 21, negate twice = 21, +21 = 42, then tbz on
// bit 0 (42 is even) branches over the poison `mov x0, #99`.
const arm64MachOBitOpsDriverMain = "\n" +
	"function main(): i32 {\n" +
	"    let asm: string = \"\";\n" +
	"    asm = asm + \"_main:\\n\";\n" +
	"    asm = asm + \"    mov x0, #42\\n\";\n" +
	"    asm = asm + \"    ubfx x1, x0, #1, #5\\n\";\n" +
	"    asm = asm + \"    neg x1, x1\\n\";\n" +
	"    asm = asm + \"    neg x1, x1\\n\";\n" +
	"    asm = asm + \"    add x0, x1, #21\\n\";\n" +
	"    asm = asm + \"    tbz x0, #0, even\\n\";\n" +
	"    asm = asm + \"    mov x0, #99\\n\";\n" +
	"    asm = asm + \"even:\\n\";\n" +
	"    asm = asm + \"    mov x16, #1\\n\";\n" +
	"    asm = asm + \"    svc #0x80\\n\";\n" +
	"    let a: Arm64Asm = arm64_gas_assemble(asm);\n" +
	"    let none: i32[] = [];\n" +
	"    let bin: i32[] = macho_executable(a.text, none, none, \"fern\", macho_entry_off(a), 0, none);\n" +
	"    write(string_from_bytes_unchecked(to_u8(bin)));\n" +
	"    return 0;\n" +
	"}\n"
