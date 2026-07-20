package e2eselfhost

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
	for _, name := range []string{"lexer.fern", "parser.fern", "util.fern", "astwalk.fern", "asmcore.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm.fern", "wasm_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

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
    var a: Arm64Asm = arm64_gas_assemble("stp x27, x28, [sp, #80]");
    if (a.code[0] != 251 || a.code[1] != 115 || a.code[2] != 5 || a.code[3] != 169) { return 1; }
    // ldp x27, x28, [sp, #80] (signed offset) -> 0xA94573FB -> FB 73 45 A9
    var b: Arm64Asm = arm64_gas_assemble("ldp x27, x28, [sp, #80]");
    if (b.code[0] != 251 || b.code[1] != 115 || b.code[2] != 69 || b.code[3] != 169) { return 2; }
    // ldp x27, x28, [sp] (bare base == offset #0) -> 0xA94073FB -> FB 73 40 A9
    var c: Arm64Asm = arm64_gas_assemble("ldp x27, x28, [sp]");
    if (c.code[0] != 251 || c.code[1] != 115 || c.code[2] != 64 || c.code[3] != 169) { return 3; }
    // pre-index stp still works: stp x29, x30, [sp, #-16]! -> 0xA9BF7BFD -> FD 7B BF A9
    var d: Arm64Asm = arm64_gas_assemble("stp x29, x30, [sp, #-16]!");
    if (d.code[0] != 253 || d.code[1] != 123 || d.code[2] != 191 || d.code[3] != 169) { return 4; }
    // post-index ldp still works: ldp x29, x30, [sp], #16 -> 0xA8C17BFD -> FD 7B C1 A8
    var e: Arm64Asm = arm64_gas_assemble("ldp x29, x30, [sp], #16");
    if (e.code[0] != 253 || e.code[1] != 123 || e.code[2] != 193 || e.code[3] != 168) { return 5; }
    // sxtw x0, w0 -> 0x93407C00 -> 00 7C 40 93
    var f: Arm64Asm = arm64_gas_assemble("sxtw x0, w0");
    if (f.code[0] != 0 || f.code[1] != 124 || f.code[2] != 64 || f.code[3] != 147) { return 6; }
    // sxtw x9, w9 -> 0x93407D29 -> 29 7D 40 93
    var g: Arm64Asm = arm64_gas_assemble("sxtw x9, w9");
    if (g.code[0] != 41 || g.code[1] != 125 || g.code[2] != 64 || g.code[3] != 147) { return 7; }
    // lsl x0, x0, x1 (LSLV) -> 0x9AC12000 -> 00 20 C1 9A
    var h: Arm64Asm = arm64_gas_assemble("lsl x0, x0, x1");
    if (h.code[0] != 0 || h.code[1] != 32 || h.code[2] != 193 || h.code[3] != 154) { return 8; }
    // lsr x0, x0, x1 (LSRV) -> 0x9AC12400 -> 00 24 C1 9A
    var i: Arm64Asm = arm64_gas_assemble("lsr x0, x0, x1");
    if (i.code[0] != 0 || i.code[1] != 36 || i.code[2] != 193 || i.code[3] != 154) { return 9; }
    // asr x0, x0, x1 (ASRV) -> 0x9AC12800 -> 00 28 C1 9A
    var j: Arm64Asm = arm64_gas_assemble("asr x0, x0, x1");
    if (j.code[0] != 0 || j.code[1] != 40 || j.code[2] != 193 || j.code[3] != 154) { return 10; }
    // lsl w9, w9, w10 (LSLV 32-bit) -> 0x1ACA2129 -> 29 21 CA 1A
    var k: Arm64Asm = arm64_gas_assemble("lsl w9, w9, w10");
    if (k.code[0] != 41 || k.code[1] != 33 || k.code[2] != 202 || k.code[3] != 26) { return 11; }
    // asr w9, w9, w10 (ASRV 32-bit) -> 0x1ACA2929 -> 29 29 CA 1A
    var l: Arm64Asm = arm64_gas_assemble("asr w9, w9, w10");
    if (l.code[0] != 41 || l.code[1] != 41 || l.code[2] != 202 || l.code[3] != 26) { return 12; }
    // lsl immediate form still works: lsl x0, x0, #3 -> 0xD37DF000 -> 00 F0 7D D3
    var m: Arm64Asm = arm64_gas_assemble("lsl x0, x0, #3");
    if (m.code[0] != 0 || m.code[1] != 240 || m.code[2] != 125 || m.code[3] != 211) { return 13; }
    // eor x0, x0, x1 (register, 64-bit) -> 0xCA010000 -> 00 00 01 CA
    var n: Arm64Asm = arm64_gas_assemble("eor x0, x0, x1");
    if (n.code[0] != 0 || n.code[1] != 0 || n.code[2] != 1 || n.code[3] != 202) { return 14; }
    // eor w9, w9, w10 (register, 32-bit) -> 0x4A0A0129 -> 29 01 0A 4A
    var o: Arm64Asm = arm64_gas_assemble("eor w9, w9, w10");
    if (o.code[0] != 41 || o.code[1] != 1 || o.code[2] != 10 || o.code[3] != 74) { return 15; }
    // eor x0, x1, #1 (logical immediate, boolean-not idiom) -> 0xD2400020 -> 20 00 40 D2
    var q: Arm64Asm = arm64_gas_assemble("eor x0, x1, #1");
    if (q.code[0] != 32 || q.code[1] != 0 || q.code[2] != 64 || q.code[3] != 210) { return 16; }
    // clz x0, x1 (freelist size-class log2, #4801) -> 0xDAC01020 -> 20 10 C0 DA
    // (pinned against internal/native/arm64's CLZ test vectors)
    var r: Arm64Asm = arm64_gas_assemble("clz x0, x1");
    if (r.code[0] != 32 || r.code[1] != 16 || r.code[2] != 192 || r.code[3] != 218) { return 17; }
    // clz x3, x2 -> 0xDAC01043 -> 43 10 C0 DA
    var r2: Arm64Asm = arm64_gas_assemble("clz x3, x2");
    if (r2.code[0] != 67 || r2.code[1] != 16 || r2.code[2] != 192 || r2.code[3] != 218) { return 18; }
    return 0;
}
`
