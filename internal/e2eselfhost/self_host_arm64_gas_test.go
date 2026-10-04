package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostArm64Gas exercises the self-hosted AArch64 GAS-text assembler
// (examples/self_host/arm64_gas.fern) — the text->bytes step of the
// arm64-darwin native path, the counterpart of x86_native.fern PART 2. It parses
// assembly-text statements into machine code via the arm64_encode.fern
// encoders + Arm64Asm label machinery.
//
// arm64_gas.fern depends on arm64_encode.fern's names, so this test
// concatenates the two with a self-test main() that assembles snippets and
// asserts the emitted bytes, then runs the whole thing through the
// self-host wasm pipeline. Exit 0 = all checks pass; a failing check
// returns its 1-based id.
func TestSelfHostArm64Gas(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host arm64 gas e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	source := arm64NativeSrc(t) + "\n" + arm64GasSelfTestMain

	wat := runCapture(t, gcc, runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the arm64 gas self-test")
	}
	watPath := filepath.Join(dir, "arm64_gas_selftest.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", watPath)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("arm64 gas self-test failed at check %d\n--- WAT ---\n%s", code, wat)
	}
}

// TestSelfHostArm64DarwinMachOGasRuns is the end-to-end proof of the GAS
// assembler: a Fern program feeds an AArch64 assembly-text program (a
// subroutine call + a backward loop, by label) to arm64_gas_assemble,
// wraps the resulting bytes with macho.fern into an ad-hoc-signed Mach-O,
// and the binary exits 42 (= 6 × 7). This is the first time the
// arm64-darwin path goes from *assembly text* to a runnable signed binary
// with no external as/clang/ld64.
func TestSelfHostArm64DarwinMachOGasRuns(t *testing.T) {
	assertMachORuns(t, machoRun{name: "gas42", main: arm64MachOGasDriverMain, wantExit: 42})
}

// arm64GasSelfTestMain assembles individual GAS-text statements and asserts
// the emitted little-endian bytes. Each `return N` is a distinct
// failing-check id (0 = all pass).
const arm64GasSelfTestMain = `
function main(): i32 {
    // mov x0, #42 -> movz -> 0xD2800540 -> 40 05 80 D2
    let a: Arm64Asm = arm64_gas_assemble("mov x0, #42");
    if (a.text[0] != 64 || a.text[1] != 5 || a.text[2] != 128 || a.text[3] != 210) { return 1; }
    // mov x0, x1 -> movreg -> 0xAA0103E0 -> E0 03 01 AA
    let b: Arm64Asm = arm64_gas_assemble("mov x0, x1");
    if (b.text[0] != 224 || b.text[1] != 3 || b.text[2] != 1 || b.text[3] != 170) { return 2; }
    // add x0, x1, #5 -> 0x91001420 -> 20 14 00 91
    let c: Arm64Asm = arm64_gas_assemble("add x0, x1, #5");
    if (c.text[0] != 32 || c.text[1] != 20 || c.text[2] != 0 || c.text[3] != 145) { return 3; }
    // add x0, x1, x2 -> 0x8B020020 -> 20 00 02 8B
    let d: Arm64Asm = arm64_gas_assemble("add x0, x1, x2");
    if (d.text[0] != 32 || d.text[1] != 0 || d.text[2] != 2 || d.text[3] != 139) { return 4; }
    // cmp x0, x1 -> 0xEB01001F -> 1F 00 01 EB
    let e: Arm64Asm = arm64_gas_assemble("cmp x0, x1");
    if (e.text[0] != 31 || e.text[1] != 0 || e.text[2] != 1 || e.text[3] != 235) { return 5; }
    // ldr x0, [x1, #8] -> 0xF9400420 -> 20 04 40 F9
    let f: Arm64Asm = arm64_gas_assemble("ldr x0, [x1, #8]");
    if (f.text[0] != 32 || f.text[1] != 4 || f.text[2] != 64 || f.text[3] != 249) { return 6; }
    // str x0, [x1, #8] -> 0xF9000420 -> 20 04 00 F9
    let g: Arm64Asm = arm64_gas_assemble("str x0, [x1, #8]");
    if (g.text[0] != 32 || g.text[1] != 4 || g.text[2] != 0 || g.text[3] != 249) { return 7; }
    // svc #0x80 -> 0xD4001001 -> 01 10 00 D4
    let h: Arm64Asm = arm64_gas_assemble("svc #0x80");
    if (h.text[0] != 1 || h.text[1] != 16 || h.text[2] != 0 || h.text[3] != 212) { return 8; }
    // ret -> 0xD65F03C0 -> C0 03 5F D6
    let i: Arm64Asm = arm64_gas_assemble("ret");
    if (i.text[0] != 192 || i.text[1] != 3 || i.text[2] != 95 || i.text[3] != 214) { return 9; }
    // forward b.eq end: branch at 0 to off 4 -> 0x54000020 -> 20 00 00 54
    let j: Arm64Asm = arm64_gas_assemble("b.eq end\nend:\n");
    if (j.text[0] != 32 || j.text[1] != 0 || j.text[2] != 0 || j.text[3] != 84) { return 10; }
    // backward loop: sub at 0, cbnz at 4 targeting 0 (rel -4) -> 0xB5FFFFE1.
    let k: Arm64Asm = arm64_gas_assemble("loop:\nsub x1, x1, #1\ncbnz x1, loop\n");
    if (k.text[4] != 225 || k.text[5] != 255 || k.text[6] != 255 || k.text[7] != 181) { return 11; }
    // comments + blank lines are ignored; labels with trailing code parse.
    let l: Arm64Asm = arm64_gas_assemble("// a comment\n\n  ret // trailing\n");
    if (l.text.len() != 4 || l.text[0] != 192) { return 12; }
    // exponent parsing (#4342): arm64_parse_f64 mirrors x86_gas_parse_f64 —
    // a spliced-text .double operand like 1e3 must scale by its exponent,
    // not stop at the 'e'.
    let pf: f64 = arm64_parse_f64("1e3");
    if (pf < 999.9 || pf > 1000.1) { return 13; }
    let pg: f64 = arm64_parse_f64("1.5e-2");
    if (pg < 0.0149 || pg > 0.0151) { return 14; }
    let ph: f64 = arm64_parse_f64("-2.5e+2");
    if (ph < (0.0 - 250.1) || ph > (0.0 - 249.9)) { return 15; }
    // SH-006: the register decode is STRICT — digit-suffix garbage and
    // out-of-range numbers return -1 instead of a wrong register (x1a
    // decoded as x1, unknown tokens as x0 before).
    if (arm64_gas_reg("x1a") != (0 - 1)) { return 16; }
    if (arm64_gas_reg("x99") != (0 - 1)) { return 17; }
    if (arm64_gas_reg("x31") != (0 - 1)) { return 18; }
    if (arm64_gas_reg("banana") != (0 - 1)) { return 19; }
    if (arm64_gas_reg("x30") != 30 || arm64_gas_reg("w7") != 7 || arm64_gas_reg("d31") != 31 || arm64_gas_reg("sp") != 31) { return 20; }
    // …and the program-level assembler RECORDS a register-shaped operand
    // that fails the decode on p.unknown (top-level and inside a memory
    // operand), so the driver refuses the corrupt output; a clean program
    // records nothing.
    let u1: Arm64GasProg = arm64_gas_program("mov x1a, #1\n");
    if (u1.unknown.len() != 1) { return 21; }
    let u2: Arm64GasProg = arm64_gas_program("ldr x0, [x9q, #8]\n");
    if (u2.unknown.len() != 1) { return 22; }
    let u3: Arm64GasProg = arm64_gas_program("add x0, x1, x2\nldr x0, [sp, #16]\n");
    if (u3.unknown.len() != 0) { return 23; }
    // Shifted-register add/sub: the index-scaling form the backend emits for
    // every array element past [0]. The shift used to be dropped (#6849).
    // add x0, x1, x0, lsl #2 -> 0x8B000820 -> 20 08 00 8B
    let s1: Arm64Asm = arm64_gas_assemble("add x0, x1, x0, lsl #2");
    if (s1.text[0] != 32 || s1.text[1] != 8 || s1.text[2] != 0 || s1.text[3] != 139) { return 24; }
    // sub x3, x4, x5, lsl #3 -> 0xCB050C83 -> 83 0C 05 CB
    let s2: Arm64Asm = arm64_gas_assemble("sub x3, x4, x5, lsl #3");
    if (s2.text[0] != 131 || s2.text[1] != 12 || s2.text[2] != 5 || s2.text[3] != 203) { return 25; }
    // Extended-register add: the 32-bit-index widening form.
    // add x2, x0, w1, uxtw -> 0x8B214002 -> 02 40 21 8B
    let s3: Arm64Asm = arm64_gas_assemble("add x2, x0, w1, uxtw");
    if (s3.text[0] != 2 || s3.text[1] != 64 || s3.text[2] != 33 || s3.text[3] != 139) { return 26; }
    // add x2, x0, w1, sxtw #2 -> 0x8B21C802 -> 02 C8 21 8B
    let s4: Arm64Asm = arm64_gas_assemble("add x2, x0, w1, sxtw #2");
    if (s4.text[0] != 2 || s4.text[1] != 200 || s4.text[2] != 33 || s4.text[3] != 139) { return 27; }
    // A fourth operand that names neither a shift nor an extend is RECORDED
    // rather than dropped, and a shifted one is not.
    let u4: Arm64GasProg = arm64_gas_program("add x0, x1, x2, banana #1\n");
    if (u4.unknown.len() != 1) { return 28; }
    let u5: Arm64GasProg = arm64_gas_program("add x0, x1, x2, lsl #2\nadd x2, x0, w1, uxtw\n");
    if (u5.unknown.len() != 0) { return 29; }
    // Packed NEON — the five encodings the __memchr / __ascii_run kernels need
    // (docs/ATLAS-PLATFORM-PLAN.md §3.3a: the assembler for every target a
    // kernel is emitted on gets checked, and a self-host backend has one of its
    // own). Every expectation is aarch64-linux-gnu-as output, little-endian, and
    // each instruction is checked once with low registers and once with high
    // ones so a dropped register field cannot pass.
    // ld1 {v0.16b}, [x8] -> 0x4C407100 -> 00 71 40 4C
    let n1: Arm64Asm = arm64_gas_assemble("ld1 {v0.16b}, [x8]");
    if (n1.text[0] != 0 || n1.text[1] != 113 || n1.text[2] != 64 || n1.text[3] != 76) { return 30; }
    // ld1 {v3.16b}, [x0] -> 0x4C407003 -> 03 70 40 4C
    let n2: Arm64Asm = arm64_gas_assemble("ld1 {v3.16b}, [x0]");
    if (n2.text[0] != 3 || n2.text[1] != 112 || n2.text[2] != 64 || n2.text[3] != 76) { return 31; }
    // cmeq v0.16b, v0.16b, v1.16b -> 0x6E218C00 -> 00 8C 21 6E
    let n3: Arm64Asm = arm64_gas_assemble("cmeq v0.16b, v0.16b, v1.16b");
    if (n3.text[0] != 0 || n3.text[1] != 140 || n3.text[2] != 33 || n3.text[3] != 110) { return 32; }
    // cmeq v5.16b, v6.16b, v7.16b -> 0x6E278CC5 -> C5 8C 27 6E
    let n4: Arm64Asm = arm64_gas_assemble("cmeq v5.16b, v6.16b, v7.16b");
    if (n4.text[0] != 197 || n4.text[1] != 140 || n4.text[2] != 39 || n4.text[3] != 110) { return 33; }
    // cmlt v0.16b, v0.16b, #0 -> 0x4E20A800 -> 00 A8 20 4E
    let n5: Arm64Asm = arm64_gas_assemble("cmlt v0.16b, v0.16b, #0");
    if (n5.text[0] != 0 || n5.text[1] != 168 || n5.text[2] != 32 || n5.text[3] != 78) { return 34; }
    // cmlt v9.16b, v10.16b, #0 -> 0x4E20A949 -> 49 A9 20 4E
    let n6: Arm64Asm = arm64_gas_assemble("cmlt v9.16b, v10.16b, #0");
    if (n6.text[0] != 73 || n6.text[1] != 169 || n6.text[2] != 32 || n6.text[3] != 78) { return 35; }
    // shrn v0.8b, v0.8h, #4 -> 0x0F0C8400 -> 00 84 0C 0F
    let n7: Arm64Asm = arm64_gas_assemble("shrn v0.8b, v0.8h, #4");
    if (n7.text[0] != 0 || n7.text[1] != 132 || n7.text[2] != 12 || n7.text[3] != 15) { return 36; }
    // shrn v2.8b, v3.8h, #4 -> 0x0F0C8462 -> 62 84 0C 0F
    let n8: Arm64Asm = arm64_gas_assemble("shrn v2.8b, v3.8h, #4");
    if (n8.text[0] != 98 || n8.text[1] != 132 || n8.text[2] != 12 || n8.text[3] != 15) { return 37; }
    // The immediate is 16 MINUS the shift, so the two ends of the legal range
    // pin the direction: #1 -> 0x0F0F8400, #8 -> 0x0F088400.
    let n9: Arm64Asm = arm64_gas_assemble("shrn v0.8b, v0.8h, #1");
    if (n9.text[2] != 15 || n9.text[3] != 15) { return 38; }
    let n10: Arm64Asm = arm64_gas_assemble("shrn v0.8b, v0.8h, #8");
    if (n10.text[2] != 8 || n10.text[3] != 15) { return 39; }
    // dup v1.16b, w1 -> 0x4E010C21 -> 21 0C 01 4E
    let n11: Arm64Asm = arm64_gas_assemble("dup v1.16b, w1");
    if (n11.text[0] != 33 || n11.text[1] != 12 || n11.text[2] != 1 || n11.text[3] != 78) { return 40; }
    // dup v11.16b, w12 -> 0x4E010D8B -> 8B 0D 01 4E
    let n12: Arm64Asm = arm64_gas_assemble("dup v11.16b, w12");
    if (n12.text[0] != 139 || n12.text[1] != 13 || n12.text[2] != 1 || n12.text[3] != 78) { return 41; }
    // A whole kernel body assembles clean — the five above plus the scalar
    // mask arithmetic they feed.
    let nk: Arm64GasProg = arm64_gas_program("dup v1.16b, w1\nld1 {v0.16b}, [x8]\ncmeq v0.16b, v0.16b, v1.16b\nshrn v0.8b, v0.8h, #4\nfmov x11, d0\nrbit x12, x11\nclz x12, x12\n");
    if (nk.unknown.len() != 0) { return 42; }
    // Each shape the encoders CANNOT express is refused, not folded into a
    // field that does not exist. cmlt's #0 is part of the opcode; shrn's shift
    // is only 1..8; ld1 takes a one-register list and a bare [Xn]; an
    // arrangement the encoder does not pin would select a different element
    // size on the same bytes.
    let b1: Arm64GasProg = arm64_gas_program("cmlt v0.16b, v0.16b, #1\n");
    if (b1.unknown.len() != 1) { return 43; }
    let b2: Arm64GasProg = arm64_gas_program("shrn v0.8b, v0.8h, #9\n");
    if (b2.unknown.len() != 1) { return 44; }
    let b3: Arm64GasProg = arm64_gas_program("shrn v0.8b, v0.8h, #0\n");
    if (b3.unknown.len() != 1) { return 45; }
    let b4: Arm64GasProg = arm64_gas_program("shrn v0.8b, v0.16b, #4\n");
    if (b4.unknown.len() != 1) { return 46; }
    // The .8b cmeq used to be refused here: the encoder pinned .16b, so the
    // narrower arrangement had no field to go in. The general Advanced SIMD
    // path (#8000) reads the arrangement instead, so it is now a valid
    // instruction and must ASSEMBLE — to the word GNU as gives it.
    let b5: Arm64GasProg = arm64_gas_program("cmeq v0.8b, v0.8b, v1.8b\n");
    if (b5.unknown.len() != 0) { return 47; }
    // cmeq v0.8b, v0.8b, v1.8b -> 0x2E218C00 -> 00 8C 21 2E
    let b5a: Arm64Asm = arm64_gas_assemble("cmeq v0.8b, v0.8b, v1.8b");
    if (b5a.text[0] != 0 || b5a.text[1] != 140 || b5a.text[2] != 33 || b5a.text[3] != 46) { return 47; }
    let b6: Arm64GasProg = arm64_gas_program("ld1 {v0.16b, v1.16b}, [x8]\n");
    if (b6.unknown.len() != 1) { return 48; }
    let b7: Arm64GasProg = arm64_gas_program("ld1 {v0.16b}, [x8, #16]\n");
    if (b7.unknown.len() != 1) { return 49; }
    let b8: Arm64GasProg = arm64_gas_program("ld1 {v0.16b}, [x8, x9]\n");
    if (b8.unknown.len() != 1) { return 50; }
    let b9: Arm64GasProg = arm64_gas_program("dup v1.16b, x1\n");
    if (b9.unknown.len() != 1) { return 51; }
    // movk carries its operand WIDTH, like movz and movn beside it. AArch64
    // encodes width in the sf bit, not the register name, and arm64_gas_reg maps
    // w4 and x4 to the same number — so a movk that ignored it assembled
    // "movk w4, #0x734F, lsl #16" as the 64-bit MOVK, one bit from what GNU as
    // emits. 0x72AE69E4 (w) and 0xF2AE69E4 (x), little-endian.
    let k1: Arm64Asm = arm64_gas_assemble("movk w4, #29519, lsl #16");
    if (k1.text[0] != 228 || k1.text[1] != 105 || k1.text[2] != 174 || k1.text[3] != 114) { return 52; }
    let k2: Arm64Asm = arm64_gas_assemble("movk x4, #29519, lsl #16");
    if (k2.text[0] != 228 || k2.text[1] != 105 || k2.text[2] != 174 || k2.text[3] != 242) { return 53; }
    // The three shapes __mismatch adds on top of memchr's five (#8791): the
    // vector eor and cmtst that replace the broadcast compare, and the
    // register-offset load its overlapping trailing window addresses with.
    // Same rule as above — a kernel about to be emitted on this target gets
    // its assembler checked, and each is pinned twice so a dropped register
    // field cannot pass.
    // eor v0.16b, v0.16b, v1.16b -> 0x6E211C00 -> 00 1C 21 6E
    let m1: Arm64Asm = arm64_gas_assemble("eor v0.16b, v0.16b, v1.16b");
    if (m1.text[0] != 0 || m1.text[1] != 28 || m1.text[2] != 33 || m1.text[3] != 110) { return 54; }
    // eor v5.16b, v6.16b, v7.16b -> 0x6E271CC5 -> C5 1C 27 6E
    let m2: Arm64Asm = arm64_gas_assemble("eor v5.16b, v6.16b, v7.16b");
    if (m2.text[0] != 197 || m2.text[1] != 28 || m2.text[2] != 39 || m2.text[3] != 110) { return 55; }
    // cmtst v0.16b, v0.16b, v0.16b -> 0x4E208C00 -> 00 8C 20 4E
    let m3: Arm64Asm = arm64_gas_assemble("cmtst v0.16b, v0.16b, v0.16b");
    if (m3.text[0] != 0 || m3.text[1] != 140 || m3.text[2] != 32 || m3.text[3] != 78) { return 56; }
    // cmtst v9.16b, v10.16b, v11.16b -> 0x4E2B8D49 -> 49 8D 2B 4E
    let m4: Arm64Asm = arm64_gas_assemble("cmtst v9.16b, v10.16b, v11.16b");
    if (m4.text[0] != 73 || m4.text[1] != 141 || m4.text[2] != 43 || m4.text[3] != 78) { return 57; }
    // ldr x11, [x8, x14] -> 0xF86E690B -> 0B 69 6E F8. The unscaled register
    // offset, not the lsl #3 form the array loads use: the window offset is a
    // byte count.
    let m5: Arm64Asm = arm64_gas_assemble("ldr x11, [x8, x14]");
    if (m5.text[0] != 11 || m5.text[1] != 105 || m5.text[2] != 110 || m5.text[3] != 248) { return 58; }
    // ldr w15, [x9, x14] -> 0xB86E692F -> 2F 69 6E B8
    let m6: Arm64Asm = arm64_gas_assemble("ldr w15, [x9, x14]");
    if (m6.text[0] != 47 || m6.text[1] != 105 || m6.text[2] != 110 || m6.text[3] != 184) { return 59; }
    // The whole __mismatch body assembles clean — the vector gather, both
    // overlapping windows and the scalar remainder.
    let mk: Arm64GasProg = arm64_gas_program("ld1 {v0.16b}, [x8]\nld1 {v1.16b}, [x9]\neor v0.16b, v0.16b, v1.16b\ncmtst v0.16b, v0.16b, v0.16b\nshrn v0.8b, v0.8h, #4\nfmov x11, d0\nrbit x11, x11\nclz x11, x11\nlsr x11, x11, #2\ncsel x1, x5, x1, gt\nldr x11, [x8, x14]\nldr w15, [x9, x14]\neor x11, x11, x14\neor w11, w11, w15\nldrb w11, [x8]\n");
    if (mk.unknown.len() != 0) { return 60; }
    return 0;
}
`

// arm64MachOGasDriverMain assembles a full AArch64 program from text — a
// subroutine call and a backward loop, by label — computing 6 × 7 = 42.
const arm64MachOGasDriverMain = "\n" +
	"function main(): i32 {\n" +
	"    let asm: string = \"\";\n" +
	"    asm = asm + \"_main:\\n\";\n" +
	"    asm = asm + \"    bl compute\\n\";\n" +
	"    asm = asm + \"    mov x16, #1\\n\";\n" +
	"    asm = asm + \"    svc #0x80\\n\";\n" +
	"    asm = asm + \"compute:\\n\";\n" +
	"    asm = asm + \"    mov x0, #0\\n\";\n" +
	"    asm = asm + \"    mov x1, #7\\n\";\n" +
	"    asm = asm + \"loop:\\n\";\n" +
	"    asm = asm + \"    add x0, x0, #6\\n\";\n" +
	"    asm = asm + \"    sub x1, x1, #1\\n\";\n" +
	"    asm = asm + \"    cbnz x1, loop\\n\";\n" +
	"    asm = asm + \"    ret\\n\";\n" +
	"    let a: Arm64Asm = arm64_gas_assemble(asm);\n" +
	"    let none: i32[] = [];\n" +
	"    let bin: i32[] = macho_executable(a.text, none, none, \"fern\", macho_entry_off(a), 0, none);\n" +
	"    write(string_from_bytes_unchecked(to_u8(bin)));\n" +
	"    return 0;\n" +
	"}\n"
