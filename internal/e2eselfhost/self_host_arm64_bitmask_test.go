package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostArm64Bitmask byte-checks the AArch64 logical (bitmask)
// immediate encoder in arm64_encode.fern (arm64_encode_bitmask, used by
// `and Xd, Xn, #imm`) against llvm-mc-pinned encodings across a spread of
// masks — the alloc alignment mask, byte/nibble masks, single bits, and a
// negative (high-ones) mask — plus the not-encodable cases. Run through the
// self-host wasm pipeline; exit 0 = all pass, else the failing check id.
func TestSelfHostArm64Bitmask(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host arm64 bitmask e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	source := arm64NativeSrc(t) + "\n" + arm64BitmaskSelfTestMain

	wat := runCapture(t, gcc, runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the arm64 bitmask self-test")
	}
	watPath := filepath.Join(dir, "arm64_bitmask_selftest.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", watPath)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("arm64 bitmask self-test failed at check %d\n--- WAT ---\n%s", code, wat)
	}
}

// arm64BitmaskSelfTestMain encodes `and x0, x0, #imm` for several masks and
// asserts the bytes against llvm-mc. Each `return N` is a distinct
// failing-check id (0 = all pass).
const arm64BitmaskSelfTestMain = `
function main(): i32 {
    // and x0, x0, #-16 -> 0x927CEC00 -> 00 EC 7C 92
    var a: i32[] = arm64_and_imm([], arm64_x0(), arm64_x0(), 0 - 16);
    if (a[0] != 0 || a[1] != 236 || a[2] != 124 || a[3] != 146) { return 1; }
    // and x0, x0, #-256 -> 0x9278DC00 -> 00 DC 78 92
    var b: i32[] = arm64_and_imm([], arm64_x0(), arm64_x0(), 0 - 256);
    if (b[0] != 0 || b[1] != 220 || b[2] != 120 || b[3] != 146) { return 2; }
    // and x0, x0, #7 -> 0x92400800 -> 00 08 40 92
    var c: i32[] = arm64_and_imm([], arm64_x0(), arm64_x0(), 7);
    if (c[0] != 0 || c[1] != 8 || c[2] != 64 || c[3] != 146) { return 3; }
    // and x0, x0, #0xff -> 0x92401C00 -> 00 1C 40 92
    var d: i32[] = arm64_and_imm([], arm64_x0(), arm64_x0(), 255);
    if (d[0] != 0 || d[1] != 28 || d[2] != 64 || d[3] != 146) { return 4; }
    // and x0, x0, #1 -> 0x92400000 -> 00 00 40 92
    var e: i32[] = arm64_and_imm([], arm64_x0(), arm64_x0(), 1);
    if (e[0] != 0 || e[1] != 0 || e[2] != 64 || e[3] != 146) { return 5; }
    // and x3, x5, #0xf -> 0x92400CA3 -> A3 0C 40 92
    var f: i32[] = arm64_and_imm([], 3, 5, 15);
    if (f[0] != 163 || f[1] != 12 || f[2] != 64 || f[3] != 146) { return 6; }
    // legality: 0 and -1 (all ones) are not encodable; -16 and 0xff are.
    if (arm64_and_imm_ok(0)) { return 7; }
    if (arm64_and_imm_ok(0 - 1)) { return 8; }
    if (!arm64_and_imm_ok(0 - 16)) { return 9; }
    if (!arm64_and_imm_ok(255)) { return 10; }
    // a non-bitmask value (e.g. 5 = 0b101) is not encodable.
    if (arm64_and_imm_ok(5)) { return 11; }
    return 0;
}
`
