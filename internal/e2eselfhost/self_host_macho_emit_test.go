package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostMachO exercises the self-hosted static, ad-hoc-signed arm64
// Mach-O executable writer (examples/self_host/macho.fern) — the Darwin
// counterpart of TestSelfHostELF and the container half of the native
// binary backend (the part that aims to remove the external clang/ld64
// link step, mirroring the Go reference internal/native/macho/*.go).
//
// macho.fern is intentionally import-free, so this test reads it from disk
// and concatenates it with a self-test main() that (a) builds Mach-O
// images and asserts the fixed-layout header + load-command bytes (magic,
// cputype, filetype, ncmds, sizeofcmds, MH_PIE, the __PAGEZERO/__TEXT/__DATA
// segments, the dyld command set + LC_MAIN entryoff, and the
// embedded-signature / CodeDirectory magics), and (b) checks the
// self-contained SHA-256 against
// the FIPS 180-4 "abc" and "" test vectors (the page-hash primitive the
// ad-hoc signature relies on). It then runs the combined program through
// the existing self-host wasm pipeline (wasm_run -> WAT -> wasmtime). The
// program returns 0 when every check passes, or the (1-based) number of
// the failing check — surfaced in the error message so a regression points
// straight at the bad field.
func TestSelfHostMachO(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host macho e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	source := arm64NativeSrc(t) + "\n" + machoSelfTestMain

	wat := runCapture(t, gcc, runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the macho self-test")
	}
	watPath := filepath.Join(dir, "macho_selftest.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", watPath)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("macho self-test failed at check %d\n--- WAT ---\n%s", code, wat)
	}
}

// machoSelfTestMain asserts the fixed-layout fields of the produced arm64
// Mach-O — the same shape the Go reference's macho_test.go validates —
// plus the SHA-256 test vectors that back the ad-hoc code signature. Each
// `return N` is a distinct failing-check id (0 = all pass).
//
// For the no-data case: load_cmds_len = 648, so text_off = 32 + 648 = 680 and
// sizeofcmds = 600 (the 48-byte difference is the layout slack the native
// writer also carries). text_vmsize rounds 684 up to one 16 KiB page = 16384
// = code_limit; ident "fern" (len 4) gives sig_len = 20 + (88+5) + 4*32 = 241,
// so total = 16625.
//
// Load-command offsets, no-data image:
//
//	32 __PAGEZERO(72)  104 __TEXT(72+80)  256 __LINKEDIT(72)
//	328 LC_DYLD_INFO_ONLY(48)  376 LC_SYMTAB(24)  400 LC_DYSYMTAB(80)
//	480 LC_BUILD_VERSION(24)  504 LC_LOAD_DYLINKER(32)
//	536 LC_LOAD_DYLIB(56)  592 LC_MAIN(24)  616 LC_CODE_SIGNATURE(16)
const machoSelfTestMain = `
function main(): i32 {
    var text: i32[] = [1, 2, 3, 4];
    var none: i32[] = [];
    var bin: i32[] = macho_executable(text, none, "fern", 0, 0);

    // total = code_limit (16384) + sig_len (241).
    if (bin.len() != 16625) { return 1; }
    // MH_MAGIC_64 = 0xfeedfacf, little-endian.
    if (bin[0] != 207 || bin[1] != 250 || bin[2] != 237 || bin[3] != 254) { return 2; }
    // CPU_TYPE_ARM64 = 0x0100000c, little-endian.
    if (bin[4] != 12 || bin[5] != 0 || bin[6] != 0 || bin[7] != 1) { return 3; }
    // filetype = MH_EXECUTE (2) @12.
    if (bin[12] != 2 || bin[13] != 0) { return 4; }
    // ncmds = 11 @16 (no __DATA).
    if (bin[16] != 11 || bin[17] != 0) { return 5; }
    // sizeofcmds = 600 (0x258) @20.
    if (bin[20] != 88 || bin[21] != 2) { return 6; }
    // flags @24 = MH_NOUNDEFS|MH_DYLDLINK|MH_TWOLEVEL|MH_PIE = 0x00200085.
    // MH_PIE (bit 21, the 0x20 in byte 26) is the one the kernel checks
    // before dyld runs; without it the image is SIGKILLed at exec.
    if (bin[24] != 133 || bin[25] != 0 || bin[26] != 32 || bin[27] != 0) { return 7; }
    // first load command @32 = LC_SEGMENT_64 (25), cmdsize 72 @36.
    if (bin[32] != 25 || bin[36] != 72) { return 8; }
    // __PAGEZERO name @40.
    if (bin[40] != 95 || bin[41] != 95 || bin[42] != 80) { return 9; }
    // __TEXT segment @104 (32+72): LC_SEGMENT_64, name "__TEXT" @112.
    if (bin[104] != 25) { return 10; }
    if (bin[112] != 95 || bin[113] != 95 || bin[114] != 84) { return 11; }
    // __LINKEDIT segment @256 (32+72+72+80): name "__LINKEDIT" @264.
    if (bin[256] != 25) { return 12; }
    if (bin[264] != 95 || bin[265] != 95 || bin[266] != 76) { return 13; }

    // ---- the dyld command set (#6042) ----
    // LC_DYLD_INFO_ONLY @328 = 0x80000022, cmdsize 48; rebase stream empty.
    if (bin[328] != 34 || bin[329] != 0 || bin[330] != 0 || bin[331] != 128) { return 14; }
    if (bin[332] != 48) { return 15; }
    if (bin[340] != 0 || bin[341] != 0 || bin[342] != 0 || bin[343] != 0) { return 16; }
    // LC_SYMTAB @376 = 2, cmdsize 24, all four counts zero.
    if (bin[376] != 2 || bin[380] != 24) { return 17; }
    // LC_DYSYMTAB @400 = 11, cmdsize 80. Required on a two-level image, and
    // dyld aborts on it without the LC_SYMTAB above.
    if (bin[400] != 11 || bin[404] != 80) { return 18; }
    // LC_BUILD_VERSION @480 = 50 (0x32), cmdsize 24, PLATFORM_MACOS = 1 @488,
    // minos 11.0.0 = 0x000b0000 @492.
    if (bin[480] != 50 || bin[484] != 24) { return 19; }
    if (bin[488] != 1 || bin[489] != 0) { return 20; }
    if (bin[492] != 0 || bin[493] != 0 || bin[494] != 11 || bin[495] != 0) { return 21; }
    // LC_LOAD_DYLINKER @504 = 14, cmdsize 32, name offset 12, "/usr/lib/dyld".
    if (bin[504] != 14 || bin[508] != 32 || bin[512] != 12) { return 22; }
    if (bin[516] != 47 || bin[517] != 117 || bin[518] != 115 || bin[519] != 114) { return 23; }
    // LC_LOAD_DYLIB @536 = 12, cmdsize 56, name offset 24 (past the timestamp
    // + the two version words), "/usr/lib/libSystem.B.dylib".
    if (bin[536] != 12 || bin[540] != 56 || bin[544] != 24) { return 24; }
    if (bin[560] != 47 || bin[561] != 117 || bin[562] != 115 || bin[563] != 114) { return 25; }
    // LC_MAIN @592 = 0x80000028, cmdsize 24. entryoff @600 is a FILE offset
    // (dyld adds the slid load address), = text_off + entry_off = 680 (0x2a8).
    if (bin[592] != 40 || bin[593] != 0 || bin[594] != 0 || bin[595] != 128) { return 26; }
    if (bin[596] != 24) { return 27; }
    if (bin[600] != 168 || bin[601] != 2 || bin[602] != 0 || bin[603] != 0) { return 28; }
    // LC_CODE_SIGNATURE @616 = 29 (0x1d), cmdsize 16.
    if (bin[616] != 29 || bin[620] != 16) { return 29; }

    // No LC_UNIXTHREAD anywhere: a static, dyld-free thread-state entry is
    // exactly what made every emitted binary unlaunchable on Apple Silicon.
    var off: i32 = 32;
    var ci: i32 = 0;
    while (ci < 11) {
        if (bin[off] == 5 && bin[off + 1] == 0 && bin[off + 2] == 0 && bin[off + 3] == 0) { return 30; }
        off = off + bin[off + 4] + bin[off + 5] * 256;
        ci = ci + 1;
    }
    // The walk must land exactly on the end of the commands (32 + 600).
    if (off != 632) { return 31; }

    // .text bytes laid down at text_off = 680.
    if (bin[680] != 1 || bin[681] != 2 || bin[682] != 3 || bin[683] != 4) { return 32; }
    // code signature at code_limit = 16384: CSMAGIC_EMBEDDED_SIGNATURE
    // (0xfade0cc0) big-endian.
    if (bin[16384] != 250 || bin[16385] != 222 || bin[16386] != 12 || bin[16387] != 192) { return 33; }
    // CodeDirectory @16404 (+20): CSMAGIC_CODEDIRECTORY (0xfade0c02) BE.
    if (bin[16404] != 250 || bin[16405] != 222 || bin[16406] != 12 || bin[16407] != 2) { return 34; }

    // ---- SHA-256 test vectors (FIPS 180-4) ----
    // sha256("abc") = ba7816bf 8f01cfea ... f20015ad.
    var abc: i32[] = [97, 98, 99];
    var ha: i32[] = sha256_bytes(abc, 0, 3);
    if (ha.len() != 32) { return 35; }
    if (ha[0] != 186 || ha[1] != 120 || ha[2] != 22 || ha[3] != 191) { return 36; }
    if (ha[31] != 173) { return 37; }
    // sha256("") = e3b0c442 98fc1c14 ... 7852b855.
    var empty: i32[] = [];
    var he: i32[] = sha256_bytes(empty, 0, 0);
    if (he[0] != 227 || he[1] != 176 || he[2] != 196 || he[3] != 66) { return 38; }
    if (he[31] != 85) { return 39; }

    // ---- data variant: text (5 bytes) + data (2 bytes) ----
    // __DATA adds a 152-byte segment+section, so text_off = 32 + 800 = 832.
    var t2: i32[] = [1, 2, 3, 4, 5];
    var d2: i32[] = [9, 9];
    var b2: i32[] = macho_executable(t2, d2, "fern", 0, 0);
    // ncmds = 12 @16 (now with __DATA).
    if (b2[16] != 12) { return 40; }
    // sizeofcmds = 752 (0x2f0) @20.
    if (b2[20] != 240 || b2[21] != 2) { return 41; }
    // __DATA segment @256 (32+72+72+80): LC_SEGMENT_64, name "__DATA" @264.
    if (b2[256] != 25) { return 42; }
    if (b2[264] != 95 || b2[265] != 95 || b2[266] != 68) { return 43; }
    // With no bss the __DATA vmsize (@288) equals its filesize (@304): both
    // one 16 KiB page.
    if (b2[289] != 64 || b2[305] != 64) { return 44; }
    // data bytes laid down at text_vmsize = 16384.
    if (b2[16384] != 9 || b2[16385] != 9) { return 45; }
    // total = code_limit (32768) + sig_len (369).
    if (b2.len() != 33137) { return 46; }
    // LC_MAIN entryoff @752 = text_off = 832 (0x340).
    if (b2[752] != 64 || b2[753] != 3 || b2[754] != 0 || b2[755] != 0) { return 47; }

    // ---- entry_off + bss (#6042) ----
    // entry_off shifts LC_MAIN only: the emitter writes _main after the
    // user's functions, so the entry is not the first code byte.
    // bss extends __DATA's vmsize past its filesize (the kernel zero-fills
    // the difference) so a .bss symbol at data_vaddr + data.len() + off is
    // inside the mapped segment. 2 + 20000 rounds to two pages = 32768,
    // against one page (16384) of file bytes.
    var b3: i32[] = macho_executable(t2, d2, "fern", 8, 20000);
    if (b3[752] != 72 || b3[753] != 3 || b3[754] != 0 || b3[755] != 0) { return 48; }
    if (b3[289] != 128 || b3[288] != 0) { return 49; }
    if (b3[305] != 64 || b3[304] != 0) { return 50; }
    // The file layout (hence the signature) is unchanged by bss.
    if (b3.len() != 33137) { return 51; }
    return 0;
}
`
