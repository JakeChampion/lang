package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostArm64LoadStore byte-checks the AArch64 load/store encoders
// added in slice 3e of arm64_encode.fern (ldr/str, 64-bit unsigned scaled
// immediate) against the ground-truth little-endian encodings, run through
// the self-host wasm pipeline. Exit 0 = all checks pass; a failing check
// returns its 1-based id.
func TestSelfHostArm64LoadStore(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host arm64 load/store e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	source := arm64NativeSrc(t) + "\n" + arm64LoadStoreSelfTestMain

	wat := runCapture(t, gcc, runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the arm64 load/store self-test")
	}
	watPath := filepath.Join(dir, "arm64_loadstore_selftest.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", watPath)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("arm64 load/store self-test failed at check %d\n--- WAT ---\n%s", code, wat)
	}
}

// TestSelfHostArm64DarwinMachOFrameRuns is the end-to-end proof of the
// load/store encoders: a Fern program assembles a stack-frame round-trip —
// sub sp; movz x0,#42; str x0,[sp,#8]; clobber x0; ldr x0,[sp,#8]; add sp
// — then exits with x0. A wrong scaled offset or store/load opcode would
// not reload 42. macho.fern wraps it into an ad-hoc-signed Mach-O — no
// clang/ld64/codesign.
func TestSelfHostArm64DarwinMachOFrameRuns(t *testing.T) {
	assertMachORuns(t, machoRun{name: "frame42", main: arm64MachOFrameDriverMain, wantExit: 42})
}

// arm64LoadStoreSelfTestMain asserts the ldr/str encoders against their
// ground-truth little-endian bytes. Each `return N` is a distinct
// failing-check id (0 = all pass).
const arm64LoadStoreSelfTestMain = `
function main(): i32 {
    // str x0, [sp, #8] -> 0xF90007E0 -> E0 07 00 F9
    var a: i32[] = arm64_str([], arm64_x0(), arm64_sp(), 8, false);
    if (a[0] != 224 || a[1] != 7 || a[2] != 0 || a[3] != 249) { return 1; }
    // ldr x0, [sp, #8] -> 0xF94007E0 -> E0 07 40 F9
    var b: i32[] = arm64_ldr([], arm64_x0(), arm64_sp(), 8, false);
    if (b[0] != 224 || b[1] != 7 || b[2] != 64 || b[3] != 249) { return 2; }
    // str x1, [x2, #0] -> 0xF9000041 -> 41 00 00 F9
    var c: i32[] = arm64_str([], arm64_x1(), arm64_x2(), 0, false);
    if (c[0] != 65 || c[1] != 0 || c[2] != 0 || c[3] != 249) { return 3; }
    // ldr x1, [x2, #16] -> 0xF9400841 -> 41 08 40 F9
    var d: i32[] = arm64_ldr([], arm64_x1(), arm64_x2(), 16, false);
    if (d[0] != 65 || d[1] != 8 || d[2] != 64 || d[3] != 249) { return 4; }
    return 0;
}
`

// arm64MachOFrameDriverMain assembles a stack-frame spill/reload that
// exits with the reloaded value (42): allocate 16 bytes, store 42 at
// [sp,#8], clobber the register, reload it, free the frame.
const arm64MachOFrameDriverMain = `
function main(): i32 {
    var code: i32[] = [];
    code = arm64_subimm(code, arm64_sp(), arm64_sp(), 16, false); // sub sp, sp, #16
    code = arm64_movz(code, arm64_x0(), 42, 0, false);            // x0 = 42
    code = arm64_str(code, arm64_x0(), arm64_sp(), 8, false);     // str x0, [sp, #8]
    code = arm64_movz(code, arm64_x0(), 0, 0, false);             // clobber x0
    code = arm64_ldr(code, arm64_x0(), arm64_sp(), 8, false);     // ldr x0, [sp, #8]
    code = arm64_addimm(code, arm64_sp(), arm64_sp(), 16, false); // add sp, sp, #16
    code = arm64_movz(code, arm64_x16(), 1, 0, false);            // SYS_exit (Darwin)
    code = arm64_svc(code, 128);                            // svc #0x80
    var none: i32[] = [];
    var bin: i32[] = macho_executable(code, none, "fern", 0, 0, none);
    write(string_from_bytes_unchecked(bin));
    return 0;
}
`
