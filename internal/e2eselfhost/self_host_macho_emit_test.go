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
// cputype, filetype, ncmds, sizeofcmds, the __PAGEZERO/__TEXT/__DATA
// segments, the LC_UNIXTHREAD entry pc, and the embedded-signature /
// CodeDirectory magics), and (b) checks the self-contained SHA-256 against
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
	for _, name := range []string{"lexer.fern", "parser.fern", "util.fern", "astwalk.fern", "asmcore.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
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
// For the no-data case: text_off = 32 + 600 = 632 (0x278); text_vmsize
// rounds 636 up to one 16 KiB page = 16384 = code_limit; ident "fern"
// (len 4) gives sig_len = 20 + (88+5) + 4*32 = 241, so total = 16625.
const machoSelfTestMain = `
function main(): i32 {
    var text: i32[] = [1, 2, 3, 4];
    var none: i32[] = [];
    var bin: i32[] = macho_static_executable(text, none, "fern");

    // total = code_limit (16384) + sig_len (241).
    if (bin.len() != 16625) { return 1; }
    // MH_MAGIC_64 = 0xfeedfacf, little-endian.
    if (bin[0] != 207 || bin[1] != 250 || bin[2] != 237 || bin[3] != 254) { return 2; }
    // CPU_TYPE_ARM64 = 0x0100000c, little-endian.
    if (bin[4] != 12 || bin[5] != 0 || bin[6] != 0 || bin[7] != 1) { return 3; }
    // filetype = MH_EXECUTE (2) @12.
    if (bin[12] != 2 || bin[13] != 0) { return 4; }
    // ncmds = 5 @16 (no __DATA).
    if (bin[16] != 5 || bin[17] != 0) { return 5; }
    // sizeofcmds = 600 (0x258) @20.
    if (bin[20] != 88 || bin[21] != 2) { return 6; }
    // first load command @32 = LC_SEGMENT_64 (25), cmdsize 72 @36.
    if (bin[32] != 25 || bin[36] != 72) { return 7; }
    // __PAGEZERO name @40.
    if (bin[40] != 95 || bin[41] != 95 || bin[42] != 80) { return 8; }
    // __TEXT segment @104 (32+72): LC_SEGMENT_64, name "__TEXT" @112.
    if (bin[104] != 25) { return 9; }
    if (bin[112] != 95 || bin[113] != 95 || bin[114] != 84) { return 10; }
    // LC_UNIXTHREAD @328 (32+72+72+80+72): cmd 5, flavor 6 @336, count 68 @340.
    if (bin[328] != 5) { return 11; }
    if (bin[336] != 6 || bin[340] != 68) { return 12; }
    // entry pc @600 (= 344 + 256) == text_vaddr = 0x100000000 + 632 = 0x100000278.
    if (bin[600] != 120 || bin[601] != 2 || bin[602] != 0 || bin[603] != 0) { return 13; }
    if (bin[604] != 1 || bin[605] != 0 || bin[606] != 0 || bin[607] != 0) { return 14; }
    // .text bytes laid down at text_off = 632.
    if (bin[632] != 1 || bin[633] != 2 || bin[634] != 3 || bin[635] != 4) { return 15; }
    // code signature at code_limit = 16384: CSMAGIC_EMBEDDED_SIGNATURE
    // (0xfade0cc0) big-endian.
    if (bin[16384] != 250 || bin[16385] != 222 || bin[16386] != 12 || bin[16387] != 192) { return 16; }
    // CodeDirectory @16404 (+20): CSMAGIC_CODEDIRECTORY (0xfade0c02) BE.
    if (bin[16404] != 250 || bin[16405] != 222 || bin[16406] != 12 || bin[16407] != 2) { return 17; }

    // ---- SHA-256 test vectors (FIPS 180-4) ----
    // sha256("abc") = ba7816bf 8f01cfea ... f20015ad.
    var abc: i32[] = [97, 98, 99];
    var ha: i32[] = sha256_bytes(abc, 0, 3);
    if (ha.len() != 32) { return 18; }
    if (ha[0] != 186 || ha[1] != 120 || ha[2] != 22 || ha[3] != 191) { return 19; }
    if (ha[31] != 173) { return 20; }
    // sha256("") = e3b0c442 98fc1c14 ... 7852b855.
    var empty: i32[] = [];
    var he: i32[] = sha256_bytes(empty, 0, 0);
    if (he[0] != 227 || he[1] != 176 || he[2] != 196 || he[3] != 66) { return 21; }
    if (he[31] != 85) { return 22; }

    // ---- data variant: text (5 bytes) + data (2 bytes) ----
    var t2: i32[] = [1, 2, 3, 4, 5];
    var d2: i32[] = [9, 9];
    var b2: i32[] = macho_static_executable(t2, d2, "fern");
    // ncmds = 6 @16 (now with __DATA).
    if (b2[16] != 6) { return 23; }
    // sizeofcmds = 752 (0x2f0) @20.
    if (b2[20] != 240 || b2[21] != 2) { return 24; }
    // __DATA segment @256 (32+72+72+80): LC_SEGMENT_64, name "__DATA" @264.
    if (b2[256] != 25) { return 25; }
    if (b2[264] != 95 || b2[265] != 95 || b2[266] != 68) { return 26; }
    // data bytes laid down at text_vmsize = 16384.
    if (b2[16384] != 9 || b2[16385] != 9) { return 27; }
    // total = code_limit (32768) + sig_len (369).
    if (b2.len() != 33137) { return 28; }
    return 0;
}
`
