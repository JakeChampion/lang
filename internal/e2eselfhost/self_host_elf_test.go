package e2eselfhost

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
	copySelfHostDriver(t, dir, "wasm_run.fern")
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

    // ---- W^X two-segment layout ----
    // arm64 W^X: 4-byte text + 8-byte data. headers = 64 + 2*56 = 176;
    // text_end = 180; data_off = page_up(180) = 65536; total = 65544.
    var t3: i32[] = [0, 0, 128, 210];
    var d3: i32[] = [7, 7, 7, 7, 7, 7, 7, 7];
    var b3: i32[] = elf_static_executable_data_wx(t3, d3);
    if (b3.len() != 65536 + 8) { return 28; }
    // e_phnum = 2 @56.
    if (b3[56] != 2 || b3[57] != 0) { return 29; }
    // e_entry = 0x4000B0 (base + 176) @24 (LE 176,0,64,0).
    if (b3[24] != 176 || b3[25] != 0 || b3[26] != 64 || b3[27] != 0) { return 30; }
    // phdr0 @64: p_type = PT_LOAD (1), p_flags = 5 (R|X) @68.
    if (b3[64] != 1 || b3[68] != 5) { return 31; }
    // phdr0 p_offset = 0 @72; p_filesz = 180 (headers + text) @96.
    if (b3[72] != 0 || b3[96] != 180 || b3[97] != 0) { return 32; }
    // phdr1 @120: p_type = PT_LOAD (1), p_flags = 6 (R|W) @124.
    if (b3[120] != 1 || b3[124] != 6) { return 33; }
    // phdr1 p_offset = 65536 @128 (LE 0,0,1,0).
    if (b3[128] != 0 || b3[129] != 0 || b3[130] != 1 || b3[131] != 0) { return 34; }
    // phdr1 p_vaddr = 0x410000 @136 (LE 0,0,65,0).
    if (b3[136] != 0 || b3[137] != 0 || b3[138] != 65 || b3[139] != 0) { return 35; }
    // phdr1 p_filesz = 8 @152.
    if (b3[152] != 8 || b3[153] != 0) { return 36; }
    // .text @176..179; page padding zero; data blob @65536.
    if (b3[176] != 0 || b3[177] != 0 || b3[178] != 128 || b3[179] != 210) { return 37; }
    if (b3[180] != 0 || b3[1000] != 0 || b3[65535] != 0) { return 38; }
    if (b3[65536] != 7 || b3[65543] != 7) { return 39; }
    // x86-64 W^X with a 16-byte .bss past the data: p_memsz = p_filesz + bss.
    var b4: i32[] = elf_static_executable_bss_x86_wx_at(t3, d3, 16, 0);
    // e_machine = EM_X86_64 (62) @18.
    if (b4[18] != 62 || b4[19] != 0) { return 40; }
    // phdr1 p_filesz = 8 @152, p_memsz = 24 (8 + 16) @160.
    if (b4[152] != 8 || b4[160] != 24 || b4[161] != 0) { return 41; }

    // Large .bss (> 2^31) must zero-extend, not sign-extend, into p_memsz.
    // The self-host arm64 heap is 2.5 GiB (0xA0000000), which overflows i32
    // into a negative bit pattern; a plain (bss as i64) would sign-extend it
    // to a garbage p_memsz the kernel cannot map. bss = 0 - 1610612736 is the
    // i32 whose bit pattern is 0xA0000000 (== 2_684_354_560 unsigned).
    var bigbss: i32 = 0 - 1610612736;
    // W^X x86 data segment: p_memsz @160 = data.len()(8) + 0xA0000000 =
    // 0xA0000008 (LE 8,0,0,0xA0=160,0,0,0,0) — high word stays 0.
    var b5: i32[] = elf_static_executable_bss_x86_wx_at(t3, d3, bigbss, 0);
    if (b5[160] != 8 || b5[161] != 0 || b5[162] != 0 || b5[163] != 160) { return 42; }
    if (b5[164] != 0 || b5[165] != 0 || b5[166] != 0 || b5[167] != 0) { return 43; }
    // Same guard on the single-segment contiguous path (production -target
    // arm64 used elf_image_entry_bss): p_memsz @104. body = pad8(4)=8 + 8 data
    // = 16; filesz = 64+56+16 = 136; memsz = 136 + 0xA0000000 = 0xA0000088
    // (LE 0x88=136,0,0,0xA0,0,0,0,0).
    var body6: i32[] = elf_pad_to_8(elf_cat([], t3));
    body6 = elf_cat(body6, d3);
    var b6: i32[] = elf_image_entry_bss(body6, 7, elf_em_aarch64(), 0, bigbss);
    if (b6[104] != 136 || b6[105] != 0 || b6[106] != 0 || b6[107] != 160) { return 44; }
    if (b6[108] != 0 || b6[109] != 0 || b6[110] != 0 || b6[111] != 0) { return 45; }

    // PIE (ET_DYN, base 0): elf_image_pie shares the W^X two-segment layout
    // but with e_type = ET_DYN (3) and a load base of 0. 4-byte text + 8-byte
    // data, no bss. headers = 176; data_off = page_up(180) = 65536.
    var b7: i32[] = elf_image_pie(t3, d3, elf_em_aarch64(), 0, 0);
    if (b7.len() != 65536 + 8) { return 46; }
    // e_type = ET_DYN (3) @16.
    if (b7[16] != 3 || b7[17] != 0) { return 47; }
    // e_entry = 176 (base 0, past ehdr + 2 phdrs) @24 (LE 176,0,0,0).
    if (b7[24] != 176 || b7[25] != 0 || b7[26] != 0 || b7[27] != 0) { return 48; }
    // phdr0 R+X p_vaddr = 0 @80 (base 0).
    if (b7[80] != 0 || b7[81] != 0 || b7[82] != 0 || b7[83] != 0) { return 49; }
    // phdr1 R+W @120: p_flags = 6 @124, p_vaddr = 65536 @136 (LE 0,0,1,0).
    if (b7[120] != 1 || b7[124] != 6) { return 50; }
    if (b7[136] != 0 || b7[137] != 0 || b7[138] != 1 || b7[139] != 0) { return 51; }
    // data blob at file offset 65536.
    if (b7[65536] != 7 || b7[65543] != 7) { return 52; }
    return 0;
}
`
