package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostELF(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host elf e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")

	source := assemblerSource(t, "elf.fern") + "\n" + elfSelfTestMain

	wat := runCapture(t, gcc, runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the elf self-test")
	}
	watPath := filepath.Join(dir, "elf_selftest.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", watPath)
	out, runErr := cmd.CombinedOutput()
	if cmd.ProcessState == nil {
		t.Fatalf("elf self-test did not start: %v\n%s", runErr, out)
	}
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("elf self-test exited %d: %v\n%s\n--- WAT ---\n%s", code, runErr, out, wat)
	}
}

const elfSelfTestMain = `
function test_u32(bytes: u8[], at: i32): i32 {
    return bytes[at] as i32 | bytes[at + 1] as i32 << 8 | bytes[at + 2] as i32 << 16 | bytes[at + 3] as i32 << 24;
}

function main(): i32 {
    // arm64 R+X executable from one instruction (movz x0,#0: 00 00 80 d2).
    let text: i32[] = [0, 0, 128, 210];
    let bin: i32[] = elf_static_executable(text);

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

    // x86-64 R+W+X data variant: 5-byte text (padded to 16) + 2-byte data.
    let t2: u8[] = [1, 2, 3, 4, 5];
    let d2: i32[] = [9, 9];
    let b2: i32[] = elf_static_executable_data_x86(t2, d2);
    // body = pad(5 -> 16) + 2 = 18; total = 64 + 56 + 18 = 138.
    if (b2.len() != 64 + 56 + 18) { return 21; }
    // e_machine = EM_X86_64 (62) @18.
    if (b2[18] != 62 || b2[19] != 0) { return 22; }
    // p_flags = 7 (R|W|X) @68.
    if (b2[68] != 7) { return 23; }
    // text @120..124, pad @125..135 zero, data @136..137.
    if (b2[120] != 1 || b2[124] != 5) { return 24; }
    if (b2[125] != 0 || b2[130] != 0 || b2[135] != 0) { return 25; }
    if (b2[136] != 9 || b2[137] != 9) { return 26; }
    // p_filesz = 138 @96.
    if (b2[96] != 138 || b2[97] != 0) { return 27; }

    // ---- W^X two-segment layout ----
    // Measured packed layout: six headers, text at 400, data at 416.
    // Empty constant and unwind regions keep their reserved header slots.
    let t3: u8[] = [0, 0, 128, 210];
    let d3: i32[] = [7, 7, 7, 7, 7, 7, 7, 7];
    let b3: u8[] = elf_static_executable_data_wx(t3, d3);
    if (b3.len() != 424) { return 28; }
    // e_phnum = 6 @56; e_entry = 0x400190 @24.
    if (b3[56] != 6 || b3[57] != 0) { return 29; }
    if (b3[24] != 144 || b3[25] != 1 || b3[26] != 64 || b3[27] != 0) { return 30; }
    // phdr0 @64: p_type = PT_LOAD (1), p_flags = 5 (R|X) @68.
    if (b3[64] != 1 || b3[68] != 5) { return 31; }
    // phdr0 p_offset = 0 @72; p_filesz = 404 @96.
    if (b3[72] != 0 || b3[96] != 148 || b3[97] != 1) { return 32; }
    // phdr1 @120: p_type = PT_LOAD (1), p_flags = 6 (R|W) @124.
    if (b3[120] != 1 || b3[124] != 6) { return 33; }
    // phdr1 p_offset = 416 @128; p_vaddr = 0x4101a0 @136.
    if (b3[128] != 160 || b3[129] != 1 || b3[130] != 0 || b3[131] != 0) { return 34; }
    if (b3[136] != 160 || b3[137] != 1 || b3[138] != 65 || b3[139] != 0) { return 35; }
    // phdr1 p_filesz = 8 @152.
    if (b3[152] != 8 || b3[153] != 0) { return 36; }
    // phdr2 @176 is PT_NULL: no separate constant load in this image.
    if (b3[176] != 0 || b3[177] != 0 || b3[178] != 0 || b3[179] != 0 || b3[288] != 0 || b3[289] != 0 || b3[290] != 0 || b3[291] != 0) { return 53; }
    if (b3[232] != 82 || b3[233] != 229 || b3[234] != 116 || b3[235] != 100 || b3[236] != 4) { return 60; }
    if (b3[240] != 160 || b3[241] != 1 || b3[264] != 0 || b3[272] != 0 || b3[280] != 1) { return 62; }
    if (b3[344] != 81 || b3[345] != 229 || b3[346] != 116 || b3[347] != 100 || b3[348] != 6) { return 63; }
    // Text at 400, zero alignment padding, then eight data bytes at 416.
    if (b3[400] != 0 || b3[401] != 0 || b3[402] != 128 || b3[403] != 210) { return 37; }
    if (b3[404] != 0 || b3[410] != 0 || b3[415] != 0) { return 38; }
    if (b3[416] != 7 || b3[423] != 7) { return 39; }
    // The same image carrying unwind data: .eh_frame_hdr (12 bytes) lands
    // 4-aligned right after .text, .eh_frame (16 bytes) 8-aligned after it,
    // and phdr4 names PT_GNU_EH_FRAME over the header.
    let h3: i32[] = [1, 27, 3, 59, 0, 0, 0, 0, 0, 0, 0, 0];
    let e3: i32[] = [12, 0, 0, 0, 0, 0, 0, 0, 1, 122, 82, 0, 1, 120, 30, 1];
    let b8: u8[] = elf_image_wx_unwind(t3, h3, e3, d3, elf_em_aarch64(), 0, 0, false, 0);
    // p_type @288 = 0x6474e550, p_flags @292 = 4.
    if (b8[288] != 80 || b8[289] != 229 || b8[290] != 116 || b8[291] != 100) { return 54; }
    if (b8[292] != 4) { return 55; }
    // p_offset @296 = 404; p_filesz @320 = 12.
    if (b8[296] != 148 || b8[297] != 1 || b8[320] != 12 || b8[321] != 0) { return 56; }
    // Header bytes at 404, frame bytes at 416, data at 432.
    if (b8[404] != 1 || b8[405] != 27 || b8[416] != 12 || b8[424] != 1 || b8[425] != 122) { return 57; }
    // phdr0 p_filesz @96 = 432, past the frame bytes.
    if (b8[96] != 176 || b8[97] != 1 || b8[240] != b8[128] || b8[241] != b8[129]) { return 58; }
    if (b8[432] != 7 || b8.len() != 440) { return 59; }
    // x86-64 W^X with a 16-byte .bss past the data: p_memsz = p_filesz + bss.
    let b4: u8[] = elf_static_executable_bss_x86_wx_at(t3, d3, 16, 0);
    // e_machine = EM_X86_64 (62) @18.
    if (b4[18] != 62 || b4[19] != 0) { return 40; }
    // phdr1 p_filesz = 8 @152, p_memsz = 24 (8 + 16) @160.
    if (b4[152] != 8 || b4[160] != 24 || b4[161] != 0) { return 41; }

    // Large .bss (> 2^31) must zero-extend, not sign-extend, into p_memsz.
    // The self-host arm64 heap is 2.5 GiB (0xA0000000), which overflows i32
    // into a negative bit pattern; a plain (bss as i64) would sign-extend it
    // to a garbage p_memsz the kernel cannot map. bss = 0 - 1610612736 is the
    // i32 whose bit pattern is 0xA0000000 (== 2_684_354_560 unsigned).
    let bigbss: i32 = 0 - 1610612736;
    // W^X x86 data segment: p_memsz @160 = data.len()(8) + 0xA0000000 =
    // 0xA0000008 (LE 8,0,0,0xA0=160,0,0,0,0) — high word stays 0.
    let b5: u8[] = elf_static_executable_bss_x86_wx_at(t3, d3, bigbss, 0);
    if (b5[160] != 8 || b5[161] != 0 || b5[162] != 0 || b5[163] != 160) { return 42; }
    if (b5[164] != 0 || b5[165] != 0 || b5[166] != 0 || b5[167] != 0) { return 43; }
    // Same guard on the single-segment contiguous path (production -target
    // arm64 used elf_image_entry_bss): p_memsz @104. body = pad8(4)=8 + 8 data
    // = 16; filesz = 64+56+16 = 136; memsz = 136 + 0xA0000000 = 0xA0000088
    // (LE 0x88=136,0,0,0xA0,0,0,0,0).
    let body6: i32[] = elf_pad_to(elf_cat_u8([], t3), 8);
    body6 = util__append_all(body6, d3);
    let b6: i32[] = elf_image_entry_bss(body6, 7, elf_em_aarch64(), 0, bigbss);
    if (b6[104] != 136 || b6[105] != 0 || b6[106] != 0 || b6[107] != 160) { return 44; }
    if (b6[108] != 0 || b6[109] != 0 || b6[110] != 0 || b6[111] != 0) { return 45; }

    // PIE (ET_DYN, base 0): elf_image_pie shares the W^X two-segment layout
    // but with e_type = ET_DYN (3) and a load base of 0. 4-byte text + 8-byte
    // data, no bss. Measured text offset 352 and data offset 368.
    let b7: u8[] = elf_image_pie(t3, d3, elf_em_aarch64(), 0, 0, 0, false);
    if (b7.len() != 376) { return 46; }
    // e_type = ET_DYN (3) @16.
    if (b7[16] != 3 || b7[17] != 0) { return 47; }
    // e_entry = 352 @24.
    if (b7[24] != 96 || b7[25] != 1 || b7[26] != 0 || b7[27] != 0) { return 48; }
    // phdr0 R+X p_vaddr = 0 @80 (base 0).
    if (b7[80] != 0 || b7[81] != 0 || b7[82] != 0 || b7[83] != 0) { return 49; }
    // phdr1 R+W @120: p_flags = 6 @124, p_vaddr = 65904 @136.
    if (b7[120] != 1 || b7[124] != 6) { return 50; }
    if (b7[136] != 112 || b7[137] != 1 || b7[138] != 1 || b7[139] != 0) { return 51; }
    if (b7[56] != 5 || b7[176] != 0 || b7[232] != 82 || b7[236] != 4 || b7[288] != 81 || b7[292] != 6) { return 61; }
    // Data blob at file offset 368.
    if (b7[368] != 7 || b7[375] != 7) { return 52; }
    // The compact image omits the virtual gap between constants and globals.
    let gap = elf_executable_relro_gap(d3.len(), elf_page_align());
    let protected = d3.len() + gap;
    let d9 = util__append_all(elf_pad_to(d3, protected), d3);
    let b9: u8[] = elf_image_pie(t3, d9, elf_em_aarch64(), 0, 0, protected, false);
    if (b9[240] != b7[128] || b9[241] != b7[129] || b9[248] != b7[136] || b9[249] != b7[137] || b9[250] != b7[138] || b9[251] != b7[139]) { return 64; }
    let constant_addr = test_u32(b9, 136);
    let mapped = protected - (constant_addr + protected) % 4096;
    // PT_GNU_RELRO keeps its file size to the constants the file holds.
    if (test_u32(b9, 264) != d3.len() || test_u32(b9, 272) != mapped || b9[280] != 1) { return 65; }
    if (gap != 65536 || b9.len() != 384 || b9[120] != 1 || b9[124] != 6 || test_u32(b9, 152) != 8 || test_u32(b9, 160) != mapped) { return 66; }
    if (b9[176] != 1 || b9[180] != 6 || test_u32(b9, 184) != test_u32(b9, 128) + 8 || test_u32(b9, 192) != constant_addr + protected) { return 67; }
    if (test_u32(b9, 208) != 8 || test_u32(b9, 216) != 8 || b9[368] != 7 || b9[375] != 7 || b9[376] != 7 || b9[383] != 7) { return 68; }
    return 0;
}
`
