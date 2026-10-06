package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostArm64OffsetPairGas byte-checks the instruction forms the
// arm64-darwin backend emits for *large* frames and runtime shifts, which
// the GAS assembler originally did not handle:
//
//   - `stp Xt, Xt2, [Xn, #off]` / `ldp Xt, Xt2, [Xn, #off]` — the
//     signed-offset (no-writeback) pair forms. asm_arm64 emits these to
//     spill/reload many callee-saved registers at fixed offsets after a
//     single `sub/add sp`. The old `ldp` handler assumed the 4-operand
//     post-index form and indexed ops[3] out of range (bounds abort);
//     the old `stp` handler silently mis-encoded the offset form as
//     pre-index (writeback).
//   - `sxtw Xd, Wn` — sign-extend word to doubleword (i32 -> i64).
//   - `lsl/lsr/asr Rd, Rn, Rm` — the variable (register) shift forms
//     (LSLV/LSRV/ASRV), 64- and 32-bit. The old `lsl`/`lsr` handlers only
//     parsed the `#imm` form (mis-encoding the register form); `asr` was
//     wholly unknown.
//   - `eor Rd, Rn, Rm` (register, 64/32-bit) and `eor Xd, Xn, #imm` (the
//     logical-immediate boolean-not idiom) — both were unknown; the
//     immediate goes through the same verified-bitmask path as `and #imm`.
//
// Expected bytes pinned against llvm-mc. Run through the self-host wasm
// pipeline; exit 0 = all pass, else the 1-based failing check id.
func TestSelfHostArm64OffsetPairGas(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host arm64 offset-pair gas e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")

	source := arm64NativeSrc(t) + "\n" + arm64OffsetPairGasSelfTestMain

	wat := runCapture(t, gcc, runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the arm64 offset-pair gas self-test")
	}
	watPath := filepath.Join(dir, "arm64_offsetpair_gas_selftest.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", watPath)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("arm64 offset-pair gas self-test failed at check %d\n--- WAT ---\n%s", code, wat)
	}
}

const arm64OffsetPairGasSelfTestMain = `
function main(): i32 {
    // stp x27, x28, [sp, #80] (signed offset, no writeback) -> 0xA90573FB -> FB 73 05 A9
    let a: Arm64Asm = arm64_gas_assemble("stp x27, x28, [sp, #80]");
    if (a.text[0] != 251 || a.text[1] != 115 || a.text[2] != 5 || a.text[3] != 169) { return 1; }
    // ldp x27, x28, [sp, #80] (signed offset) -> 0xA94573FB -> FB 73 45 A9
    let b: Arm64Asm = arm64_gas_assemble("ldp x27, x28, [sp, #80]");
    if (b.text[0] != 251 || b.text[1] != 115 || b.text[2] != 69 || b.text[3] != 169) { return 2; }
    // ldp x27, x28, [sp] (bare base == offset #0) -> 0xA94073FB -> FB 73 40 A9
    let c: Arm64Asm = arm64_gas_assemble("ldp x27, x28, [sp]");
    if (c.text[0] != 251 || c.text[1] != 115 || c.text[2] != 64 || c.text[3] != 169) { return 3; }
    // pre-index stp still works: stp x29, x30, [sp, #-16]! -> 0xA9BF7BFD -> FD 7B BF A9
    let d: Arm64Asm = arm64_gas_assemble("stp x29, x30, [sp, #-16]!");
    if (d.text[0] != 253 || d.text[1] != 123 || d.text[2] != 191 || d.text[3] != 169) { return 4; }
    // post-index ldp still works: ldp x29, x30, [sp], #16 -> 0xA8C17BFD -> FD 7B C1 A8
    let e: Arm64Asm = arm64_gas_assemble("ldp x29, x30, [sp], #16");
    if (e.text[0] != 253 || e.text[1] != 123 || e.text[2] != 193 || e.text[3] != 168) { return 5; }
    // sxtw x0, w0 -> 0x93407C00 -> 00 7C 40 93
    let f: Arm64Asm = arm64_gas_assemble("sxtw x0, w0");
    if (f.text[0] != 0 || f.text[1] != 124 || f.text[2] != 64 || f.text[3] != 147) { return 6; }
    // sxtw x9, w9 -> 0x93407D29 -> 29 7D 40 93
    let g: Arm64Asm = arm64_gas_assemble("sxtw x9, w9");
    if (g.text[0] != 41 || g.text[1] != 125 || g.text[2] != 64 || g.text[3] != 147) { return 7; }
    // lsl x0, x0, x1 (LSLV) -> 0x9AC12000 -> 00 20 C1 9A
    let h: Arm64Asm = arm64_gas_assemble("lsl x0, x0, x1");
    if (h.text[0] != 0 || h.text[1] != 32 || h.text[2] != 193 || h.text[3] != 154) { return 8; }
    // lsr x0, x0, x1 (LSRV) -> 0x9AC12400 -> 00 24 C1 9A
    let i: Arm64Asm = arm64_gas_assemble("lsr x0, x0, x1");
    if (i.text[0] != 0 || i.text[1] != 36 || i.text[2] != 193 || i.text[3] != 154) { return 9; }
    // asr x0, x0, x1 (ASRV) -> 0x9AC12800 -> 00 28 C1 9A
    let j: Arm64Asm = arm64_gas_assemble("asr x0, x0, x1");
    if (j.text[0] != 0 || j.text[1] != 40 || j.text[2] != 193 || j.text[3] != 154) { return 10; }
    // lsl w9, w9, w10 (LSLV 32-bit) -> 0x1ACA2129 -> 29 21 CA 1A
    let k: Arm64Asm = arm64_gas_assemble("lsl w9, w9, w10");
    if (k.text[0] != 41 || k.text[1] != 33 || k.text[2] != 202 || k.text[3] != 26) { return 11; }
    // asr w9, w9, w10 (ASRV 32-bit) -> 0x1ACA2929 -> 29 29 CA 1A
    let l: Arm64Asm = arm64_gas_assemble("asr w9, w9, w10");
    if (l.text[0] != 41 || l.text[1] != 41 || l.text[2] != 202 || l.text[3] != 26) { return 12; }
    // lsl immediate form still works: lsl x0, x0, #3 -> 0xD37DF000 -> 00 F0 7D D3
    let m: Arm64Asm = arm64_gas_assemble("lsl x0, x0, #3");
    if (m.text[0] != 0 || m.text[1] != 240 || m.text[2] != 125 || m.text[3] != 211) { return 13; }
    // eor x0, x0, x1 (register, 64-bit) -> 0xCA010000 -> 00 00 01 CA
    let n: Arm64Asm = arm64_gas_assemble("eor x0, x0, x1");
    if (n.text[0] != 0 || n.text[1] != 0 || n.text[2] != 1 || n.text[3] != 202) { return 14; }
    // eor w9, w9, w10 (register, 32-bit) -> 0x4A0A0129 -> 29 01 0A 4A
    let o: Arm64Asm = arm64_gas_assemble("eor w9, w9, w10");
    if (o.text[0] != 41 || o.text[1] != 1 || o.text[2] != 10 || o.text[3] != 74) { return 15; }
    // eor x0, x1, #1 (logical immediate, boolean-not idiom) -> 0xD2400020 -> 20 00 40 D2
    let q: Arm64Asm = arm64_gas_assemble("eor x0, x1, #1");
    if (q.text[0] != 32 || q.text[1] != 0 || q.text[2] != 64 || q.text[3] != 210) { return 16; }
    // clz x0, x1 (freelist size-class log2, #4801) -> 0xDAC01020 -> 20 10 C0 DA
    let r: Arm64Asm = arm64_gas_assemble("clz x0, x1");
    if (r.text[0] != 32 || r.text[1] != 16 || r.text[2] != 192 || r.text[3] != 218) { return 17; }
    // clz x3, x2 -> 0xDAC01043 -> 43 10 C0 DA
    let r2: Arm64Asm = arm64_gas_assemble("clz x3, x2");
    if (r2.text[0] != 67 || r2.text[1] != 16 || r2.text[2] != 192 || r2.text[3] != 218) { return 18; }
    return 0;
}
`
