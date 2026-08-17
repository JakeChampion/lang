package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostArm64Adrp byte-checks the @PAGE/@PAGEOFF addressing encoders
// added in slice 3f of arm64_encode.fern — adrp, the page-delta / page-off
// math, and the adrp/ldr patch splicers — against the ground-truth
// little-endian encodings, run through the self-host wasm pipeline. Exit 0
// = all checks pass; a failing check returns its 1-based id.
func TestSelfHostArm64Adrp(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host arm64 adrp e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	source := arm64NativeSrc(t) + "\n" + arm64AdrpSelfTestMain

	wat := runCapture(t, gcc, runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the arm64 adrp self-test")
	}
	watPath := filepath.Join(dir, "arm64_adrp_selftest.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", watPath)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("arm64 adrp self-test failed at check %d\n--- WAT ---\n%s", code, wat)
	}
}

// TestSelfHostArm64DarwinMachODataRuns is the end-to-end proof of
// @PAGE/@PAGEOFF data addressing — the arm64 counterpart of the x86 rodata
// test, and the first use of macho.fern's macho_data_vaddr (the
// SegmentAddrs parity). A Fern program lays a `.quad 42` in __DATA, loads
// its address with `adrp x1, answer@PAGE` + `ldr x0, [x1, answer@PAGEOFF]`
// (the immediates computed from the fixed segment addresses macho.fern
// uses), and exits with the loaded value. A wrong page delta / page off, or
// a wrong __DATA base, would not exit 42. macho.fern wraps it into an
// ad-hoc-signed Mach-O — no clang/ld64/codesign.
func TestSelfHostArm64DarwinMachODataRuns(t *testing.T) {
	assertMachORuns(t, machoRun{name: "data42", main: arm64MachODataDriverMain, wantExit: 42, wantData: true})
}

// arm64AdrpSelfTestMain asserts the adrp encoder, the page-delta/page-off
// math, and the adrp/ldr patch splicers. The address pair models the
// no-data... here the data layout: adrp at 0x100000310, target __DATA at
// 0x100004000 -> page delta 4, page off 0. Each `return N` is a distinct
// failing-check id (0 = all pass).
const arm64AdrpSelfTestMain = `
function main(): i32 {
    // adrp x1, #4 -> 0x90000021 -> 21 00 00 90
    var a: i32[] = arm64_adrp([], arm64_x1(), 4);
    if (a[0] != 33 || a[1] != 0 || a[2] != 0 || a[3] != 144) { return 1; }
    // adrp x0, #1 -> immlo=1<<29 -> 0xB0000000 -> 00 00 00 B0
    var b: i32[] = arm64_adrp([], arm64_x0(), 1);
    if (b[0] != 0 || b[1] != 0 || b[2] != 0 || b[3] != 176) { return 2; }
    // page delta: target 0x100004000, adrp 0x100000310 -> 4.
    if (arm64_page_delta(0x100004000, 0x100000310) != 4) { return 3; }
    // page off: 0x100004abc & 0xfff -> 0xabc = 2748.
    if (arm64_page_off(0x100004abc) != 2748) { return 4; }
    // page off of a 16 KiB-aligned __DATA base is 0.
    if (arm64_page_off(0x100004000) != 0) { return 5; }
    // patch adrp x1, #0 (0x90000001) with delta 4 -> 0x90000021.
    var c: i32[] = arm64_adrp([], arm64_x1(), 0);
    c = arm64_patch_adrp(c, 0, 4);
    if (c[0] != 33 || c[1] != 0 || c[2] != 0 || c[3] != 144) { return 6; }
    // patch ldr x0, [x1, #0] (0xF9400020) with off 16 -> 0xF9400820.
    var d: i32[] = arm64_ldr([], arm64_x0(), arm64_x1(), 0, false);
    d = arm64_patch_ldr_off(d, 0, 16);
    if (d[0] != 32 || d[1] != 8 || d[2] != 64 || d[3] != 249) { return 7; }
    return 0;
}
`

// arm64MachODataDriverMain assembles `adrp x1, answer@PAGE; ldr x0, [x1,
// answer@PAGEOFF]; exit(x0)` over a `.quad 42` in __DATA. The adrp/ldr
// immediates are computed from macho.fern's fixed segment addresses
// (macho_text_vaddr / macho_data_vaddr), then patched in.
const arm64MachODataDriverMain = `
function main(): i32 {
    var code: i32[] = [];
    code = arm64_adrp(code, arm64_x1(), 0);            // adrp x1, answer@PAGE (placeholder)
    code = arm64_ldr(code, arm64_x0(), arm64_x1(), 0, false); // ldr x0, [x1, answer@PAGEOFF] (placeholder)
    code = arm64_movz(code, arm64_x16(), 1, 0, false);        // SYS_exit (Darwin)
    code = arm64_svc(code, 128);                        // svc #0x80
    var data: i32[] = [42, 0, 0, 0, 0, 0, 0, 0];       // answer: .quad 42

    var tlen: i32 = code.len();
    var dlen: i32 = data.len();
    var text_vaddr: i64 = macho_text_vaddr(tlen, dlen, 0);
    var data_vaddr: i64 = macho_data_vaddr(tlen, dlen, 0);
    var answer: i64 = data_vaddr;       // answer is at __DATA offset 0
    var adrp_at: i64 = text_vaddr;      // adrp is at __TEXT offset 0
    code = arm64_patch_adrp(code, 0, arm64_page_delta(answer, adrp_at));
    code = arm64_patch_ldr_off(code, 4, arm64_page_off(answer));

    var none: i32[] = [];               // no absolute-address data slots to rebase
    var bin: i32[] = macho_executable(code, data, "fern", 0, 0, none);
    write(string_from_bytes_unchecked(bin));
    return 0;
}
`
