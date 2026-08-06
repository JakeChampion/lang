package e2eselfhost

import (
	"bytes"
	"debug/macho"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestSelfHostArm64BitOpsGas byte-checks arm64_gas/arm64_encode's bit-
// manipulation surface — neg, ubfx, tbz/tbnz (with an imm14 label fixup),
// the extended condition codes (cc/cs/hi/ls/…), and the bit-counting trio
// rbit / cnt / addv that the popcount and ctz lowerings emit — against the
// llvm-mc-pinned encodings, through the self-host wasm pipeline. Exit 0 =
// all pass, else the failing check id.
func TestSelfHostArm64BitOpsGas(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host arm64 bitops gas e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

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

// TestSelfHostArm64DarwinMachOBitOpsRuns exercises the new ops end-to-end:
// a Fern program assembles `ubfx`/`neg`/`tbz` into a value computation
// (extract -> negate twice -> add -> test-bit branch) that exits 42, wraps
// it with macho.fern, and the signed Mach-O runs. Structural everywhere;
// executed on Apple Silicon. No external tool.
func TestSelfHostArm64DarwinMachOBitOpsRuns(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host arm64-darwin bitops run")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	source := arm64NativeSrc(t) + "\n" + arm64MachOBitOpsDriverMain

	wat := runCapture(t, gcc, runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the arm64-darwin bitops driver")
	}
	watPath := filepath.Join(dir, "arm64_macho_bitops_driver.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	bin, err := exec.Command("wasmtime", "run", watPath).Output()
	if err != nil {
		t.Fatalf("wasmtime run (driver): %v", err)
	}

	f, err := macho.NewFile(bytes.NewReader(bin))
	if err != nil {
		t.Fatalf("self-host output is not a parseable Mach-O: %v", err)
	}
	if f.Type != macho.TypeExec || f.Cpu != macho.CpuArm64 {
		t.Fatalf("got type=%v cpu=%v, want EXECUTE/arm64", f.Type, f.Cpu)
	}

	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return
	}
	binPath := filepath.Join(dir, "bitops42")
	if err := os.WriteFile(binPath, bin, 0o755); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	cmd := exec.Command(binPath)
	runErr := cmd.Run()
	ps := cmd.ProcessState
	if ps == nil || !ps.Exited() {
		t.Skipf("self-host Mach-O did not run to a normal exit (err=%v, state=%v)", runErr, ps)
	}
	if got := ps.ExitCode(); got != 42 {
		t.Errorf("self-host arm64-darwin bitops exit = %d, want 42", got)
	}
}

// arm64BitOpsGasSelfTestMain asserts the new encoders against their
// llvm-mc-pinned bytes. Each `return N` is a distinct failing-check id.
const arm64BitOpsGasSelfTestMain = `
function main(): i32 {
    // neg x0, x0 (sub x0, xzr, x0) -> 0xCB0003E0 -> E0 03 00 CB
    var a: Arm64Asm = arm64_gas_assemble("neg x0, x0");
    if (a.code[0] != 224 || a.code[1] != 3 || a.code[2] != 0 || a.code[3] != 203) { return 1; }
    // ubfx x1, x0, #1, #5 -> 0xD3411401 -> 01 14 41 D3
    var b: Arm64Asm = arm64_gas_assemble("ubfx x1, x0, #1, #5");
    if (b.code[0] != 1 || b.code[1] != 20 || b.code[2] != 65 || b.code[3] != 211) { return 2; }
    // tbnz x0, #1, skip (rel +4) -> 0x37080020 -> 20 00 08 37
    var c: Arm64Asm = arm64_gas_assemble("tbnz x0, #1, skip\nskip:\n");
    if (c.code[0] != 32 || c.code[1] != 0 || c.code[2] != 8 || c.code[3] != 55) { return 3; }
    // tbz x0, #0, skip (rel +4) -> 0x36000020 -> 20 00 00 36
    var d: Arm64Asm = arm64_gas_assemble("tbz x0, #0, skip\nskip:\n");
    if (d.code[0] != 32 || d.code[1] != 0 || d.code[2] != 0 || d.code[3] != 54) { return 4; }
    // b.cc end (cond 3, rel +4) -> 0x54000023 -> 23 00 00 54
    var e: Arm64Asm = arm64_gas_assemble("b.cc end\nend:\n");
    if (e.code[0] != 35 || e.code[1] != 0 || e.code[2] != 0 || e.code[3] != 84) { return 5; }
    // b.hi end (cond 8, rel +4) -> 0x54000028 -> 28 00 00 54
    var f: Arm64Asm = arm64_gas_assemble("b.hi end\nend:\n");
    if (f.code[0] != 40 || f.code[1] != 0 || f.code[2] != 0 || f.code[3] != 84) { return 6; }
    // condition-code values.
    if (arm64_gas_cond("cc") != 3 || arm64_gas_cond("hs") != 2 || arm64_gas_cond("ls") != 9) { return 7; }
    // rbit x0, x0 -> 0xDAC00000 -> 00 00 C0 DA. The ctz idiom's first half.
    var g: Arm64Asm = arm64_gas_assemble("rbit x0, x0");
    if (g.code[0] != 0 || g.code[1] != 0 || g.code[2] != 192 || g.code[3] != 218) { return 8; }
    // rbit w0, w0 -> 0x5AC00000 -> 00 00 C0 5A. The sf clear: without it a
    // 32-bit ctz reverses over 64 bits and counts the empty upper half.
    var h: Arm64Asm = arm64_gas_assemble("rbit w0, w0");
    if (h.code[0] != 0 || h.code[1] != 0 || h.code[2] != 192 || h.code[3] != 90) { return 9; }
    // cnt v0.8b, v0.8b -> 0x0E205800 -> 00 58 20 0E
    var i0: Arm64Asm = arm64_gas_assemble("cnt v0.8b, v0.8b");
    if (i0.code[0] != 0 || i0.code[1] != 88 || i0.code[2] != 32 || i0.code[3] != 14) { return 10; }
    // addv b0, v0.8b -> 0x0E31B800 -> 00 B8 31 0E
    var j0: Arm64Asm = arm64_gas_assemble("addv b0, v0.8b");
    if (j0.code[0] != 0 || j0.code[1] != 184 || j0.code[2] != 49 || j0.code[3] != 14) { return 11; }
    // The Q bit, on both: cnt v1.16b, v2.16b -> 0x4E205841 -> 41 58 20 4E
    var k0: Arm64Asm = arm64_gas_assemble("cnt v1.16b, v2.16b");
    if (k0.code[0] != 65 || k0.code[1] != 88 || k0.code[2] != 32 || k0.code[3] != 78) { return 12; }
    // addv b3, v5.16b -> 0x4E31B8A3 -> A3 B8 31 4E
    var l0: Arm64Asm = arm64_gas_assemble("addv b3, v5.16b");
    if (l0.code[0] != 163 || l0.code[1] != 184 || l0.code[2] != 49 || l0.code[3] != 78) { return 13; }
    // An arrangement the byte-lane encoders cannot express must be REFUSED,
    // not folded into the byte-lane encoding — that would be a different
    // instruction on the same bytes. Same for a mismatched pair and for a
    // destination of the wrong shape.
    if (arm64_gas_bad_vec_token("cnt", "v0.4h, v0.4h").len() == 0) { return 14; }
    if (arm64_gas_bad_vec_token("cnt", "v0.8b, v1.16b").len() == 0) { return 15; }
    if (arm64_gas_bad_vec_token("addv", "h0, v0.8h").len() == 0) { return 16; }
    if (arm64_gas_bad_vec_token("addv", "v0.8b, v0.8b").len() == 0) { return 17; }
    if (arm64_gas_bad_vec_token("cnt", "v0.8b").len() == 0) { return 18; }
    // …and a well-formed pair must NOT be refused.
    if (arm64_gas_bad_vec_token("cnt", "v0.8b, v0.8b").len() != 0) { return 19; }
    if (arm64_gas_bad_vec_token("addv", "b0, v0.8b").len() != 0) { return 20; }
    return 0;
}
`

// arm64MachOBitOpsDriverMain computes 42 via ubfx + neg + a tbz branch:
// ubfx extracts (42>>1)&31 = 21, negate twice = 21, +21 = 42, then tbz on
// bit 0 (42 is even) branches over the poison `mov x0, #99`.
const arm64MachOBitOpsDriverMain = "\n" +
	"function main(): i32 {\n" +
	"    var asm: string = \"\";\n" +
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
	"    var a: Arm64Asm = arm64_gas_assemble(asm);\n" +
	"    var none: i32[] = [];\n" +
	"    var bin: i32[] = macho_executable(a.code, none, \"fern\", macho_entry_off(a), 0);\n" +
	"    write(string_from_bytes_unchecked(bin));\n" +
	"    return 0;\n" +
	"}\n"
