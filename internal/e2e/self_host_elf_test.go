package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostELF exercises the self-hosted static ELF-64 executable
// writer (examples/self_host/elf.fern) — the first slice of the native
// binary backend (the container half that aims to remove the external
// gcc/ld link step, mirroring the Go reference internal/native/elf/elf.go).
//
// elf.fern is intentionally import-free, so this test reads it from disk
// and concatenates it with a self-test main() that builds ELF images and
// asserts the fixed-layout header + program-header bytes (magic,
// class/data, e_type, e_machine, e_entry, the single PT_LOAD, p_flags,
// sizes, and the body placement / 8-byte data alignment), then runs the
// combined program through the existing self-host wasm pipeline
// (wasm_run -> WAT -> wasmtime). The program returns 0 when every check
// passes, or the (1-based) number of the failing check — surfaced in the
// error message so a regression points straight at the bad field.
func TestSelfHostELF(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host elf e2e")
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

	elf, err := os.ReadFile("../../examples/self_host/elf.fern")
	if err != nil {
		t.Fatalf("read elf.fern: %v", err)
	}
	source := string(elf) + "\n" + elfSelfTestMain

	wat := runCapture(t, gcc, runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the elf self-test")
	}
	watPath := filepath.Join(dir, "elf_selftest.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", watPath)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("elf self-test failed at check %d\n--- WAT ---\n%s", code, wat)
	}
}

// elfSelfTestMain asserts the fixed-layout fields of the produced ELF-64
// header + program header — the same fields the Go reference's
// TestStaticExecutableHeader checks, plus the x86-64 data variant
// (e_machine, R+W+X flags, 8-byte data alignment). Each `return N` is a
// distinct failing-check id (0 = all pass). 0x400078 = base(0x400000) +
// 64 + 56 = 4194424; LE bytes 0x78,0,0x40,0 = 120,0,64,0.
const elfSelfTestMain = `
function main(): i32 {
    // arm64 R+X executable from one instruction (movz x0,#0: 00 00 80 d2).
    var text: i32[] = [0, 0, 128, 210];
    var bin: i32[] = elf_static_executable(text);

    if (bin.len() != 64 + 56 + 4) { return 1; }
    // e_ident: magic + class(ELF64) + data(LE) + version + osabi.
    if (bin[0] != 127 || bin[1] != 69 || bin[2] != 76 || bin[3] != 70) { return 2; }
    if (bin[4] != 2 || bin[5] != 1 || bin[6] != 1 || bin[7] != 0) { return 3; }
    // e_type = ET_EXEC (2) @16.
    if (bin[16] != 2 || bin[17] != 0) { return 4; }
    // e_machine = EM_AARCH64 (183) @18.
    if (bin[18] != 183 || bin[19] != 0) { return 5; }
    // e_version = 1 @20.
    if (bin[20] != 1 || bin[21] != 0 || bin[22] != 0 || bin[23] != 0) { return 6; }
    // e_entry = 0x400078 @24 (8 bytes LE).
    if (bin[24] != 120 || bin[25] != 0 || bin[26] != 64 || bin[27] != 0) { return 7; }
    if (bin[28] != 0 || bin[29] != 0 || bin[30] != 0 || bin[31] != 0) { return 8; }
    // e_phoff = 64 @32.
    if (bin[32] != 64 || bin[33] != 0) { return 9; }
    // e_ehsize = 64 @52, e_phentsize = 56 @54, e_phnum = 1 @56.
    if (bin[52] != 64 || bin[53] != 0) { return 10; }
    if (bin[54] != 56 || bin[55] != 0) { return 11; }
    if (bin[56] != 1 || bin[57] != 0) { return 12; }
    // program header @64: p_type = PT_LOAD (1).
    if (bin[64] != 1 || bin[65] != 0 || bin[66] != 0 || bin[67] != 0) { return 13; }
    // p_flags = 5 (R|X) @68.
    if (bin[68] != 5 || bin[69] != 0 || bin[70] != 0 || bin[71] != 0) { return 14; }
    // p_offset = 0 @72.
    if (bin[72] != 0 || bin[79] != 0) { return 15; }
    // p_vaddr = 0x400000 @80 (LE 0,0,64,0).
    if (bin[80] != 0 || bin[81] != 0 || bin[82] != 64 || bin[83] != 0) { return 16; }
    // p_filesz = total = 124 @96.
    if (bin[96] != 124 || bin[97] != 0) { return 17; }
    // p_memsz = 124 @104.
    if (bin[104] != 124 || bin[105] != 0) { return 18; }
    // p_align = 0x10000 (65536) @112 (LE 0,0,1,0).
    if (bin[112] != 0 || bin[113] != 0 || bin[114] != 1 || bin[115] != 0) { return 19; }
    // body @120 = the .text bytes.
    if (bin[120] != 0 || bin[121] != 0 || bin[122] != 128 || bin[123] != 210) { return 20; }

    // x86-64 R+W+X data variant: 5-byte text (padded to 8) + 2-byte data.
    var t2: i32[] = [1, 2, 3, 4, 5];
    var d2: i32[] = [9, 9];
    var b2: i32[] = elf_static_executable_data_x86(t2, d2);
    // body = pad(5 -> 8) + 2 = 10; total = 64 + 56 + 10 = 130.
    if (b2.len() != 64 + 56 + 10) { return 21; }
    // e_machine = EM_X86_64 (62) @18.
    if (b2[18] != 62 || b2[19] != 0) { return 22; }
    // p_flags = 7 (R|W|X) @68.
    if (b2[68] != 7) { return 23; }
    // text @120..124, pad @125..127 zero, data @128..129.
    if (b2[120] != 1 || b2[124] != 5) { return 24; }
    if (b2[125] != 0 || b2[126] != 0 || b2[127] != 0) { return 25; }
    if (b2[128] != 9 || b2[129] != 9) { return 26; }
    // p_filesz = 130 @96.
    if (b2[96] != 130 || b2[97] != 0) { return 27; }
    return 0;
}
`
