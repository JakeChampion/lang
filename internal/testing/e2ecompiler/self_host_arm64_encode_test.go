package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func arm64NativeSrc(t *testing.T) string {
	b, err := os.ReadFile("../../../compiler/arm64_native.fern")
	if err != nil {
		t.Fatalf("read arm64_native.fern: %v", err)
	}
	return string(b) + toU8Src
}

const toU8Src = `
function to_u8(b: i32[]): u8[] { let o: u8[] = []; let i: i32 = 0; while (i < b.len()) { o = o.append(b[i] as u8); i = i + 1; } return o; }
`

func TestSelfHostArm64Encode(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host arm64_encode e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")

	source := arm64NativeSrc(t) + "\n" + arm64EncodeSelfTestMain

	wat := runCapture(t, gcc, runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the arm64_encode self-test")
	}
	watPath := filepath.Join(dir, "arm64_encode_selftest.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", watPath)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("arm64_encode self-test failed at check %d\n--- WAT ---\n%s", code, wat)
	}
}

func TestSelfHostArm64DarwinMachOExitRuns(t *testing.T) {
	assertMachORuns(t, machoRun{name: "exit42", main: arm64MachOExitDriverMain, wantExit: 42})
}

const arm64EncodeSelfTestMain = `
function main(): i32 {
    // movz x0, #42 -> 0xD2800540 -> 40 05 80 D2
    let a: i32[] = arm64_movz([], arm64_x0(), 42, 0, false);
    if (a.len() != 4 || a[0] != 64 || a[1] != 5 || a[2] != 128 || a[3] != 210) { return 1; }
    // movz x16, #1 -> 0xD2800030 -> 30 00 80 D2
    let b: i32[] = arm64_movz([], arm64_x16(), 1, 0, false);
    if (b.len() != 4 || b[0] != 48 || b[1] != 0 || b[2] != 128 || b[3] != 210) { return 2; }
    // movk x0, #0x10 -> 0xF2800200 -> 00 02 80 F2
    let c: i32[] = arm64_movk([], arm64_x0(), 16, 0, false);
    if (c[0] != 0 || c[1] != 2 || c[2] != 128 || c[3] != 242) { return 3; }
    // movk w0, #0x10 -> 0x72800200 -> 00 02 80 72. The w flag clears sf, as
    // it does for movz and movn above; without it the 32-bit form assembled as
    // the 64-bit one.
    let cw: i32[] = arm64_movk([], arm64_x0(), 16, 0, true);
    if (cw[0] != 0 || cw[1] != 2 || cw[2] != 128 || cw[3] != 114) { return 33; }
    // movn x0, #0 -> 0x92800000 -> 00 00 80 92
    let d: i32[] = arm64_movn([], arm64_x0(), 0, 0, false);
    if (d[0] != 0 || d[1] != 0 || d[2] != 128 || d[3] != 146) { return 4; }
    // add x0, x1, #5 -> 0x91001420 -> 20 14 00 91
    let e: i32[] = arm64_addimm([], arm64_x0(), arm64_x1(), 5, false);
    if (e[0] != 32 || e[1] != 20 || e[2] != 0 || e[3] != 145) { return 5; }
    // sub x0, x1, #5 -> 0xD1001420 -> 20 14 00 D1
    let f: i32[] = arm64_subimm([], arm64_x0(), arm64_x1(), 5, false);
    if (f[0] != 32 || f[1] != 20 || f[2] != 0 || f[3] != 209) { return 6; }
    // add x0, x1, x2 -> 0x8B020020 -> 20 00 02 8B
    let g: i32[] = arm64_addreg([], arm64_x0(), arm64_x1(), arm64_x2());
    if (g[0] != 32 || g[1] != 0 || g[2] != 2 || g[3] != 139) { return 7; }
    // sub x0, x1, x2 -> 0xCB020020 -> 20 00 02 CB
    let h: i32[] = arm64_subreg([], arm64_x0(), arm64_x1(), arm64_x2());
    if (h[0] != 32 || h[1] != 0 || h[2] != 2 || h[3] != 203) { return 8; }
    // mov x0, x1 (orr x0, xzr, x1) -> 0xAA0103E0 -> E0 03 01 AA
    let i: i32[] = arm64_movreg([], arm64_x0(), arm64_x1(), false);
    if (i[0] != 224 || i[1] != 3 || i[2] != 1 || i[3] != 170) { return 9; }
    // svc #0x80 -> 0xD4001001 -> 01 10 00 D4
    let j: i32[] = arm64_svc([], 128);
    if (j[0] != 1 || j[1] != 16 || j[2] != 0 || j[3] != 212) { return 10; }
    // ret (x30) -> 0xD65F03C0 -> C0 03 5F D6
    let k: i32[] = arm64_ret([], arm64_lr());
    if (k[0] != 192 || k[1] != 3 || k[2] != 95 || k[3] != 214) { return 11; }
    // blr x1 (indirect call) -> 0xD63F0020 -> 20 00 3F D6
    let l: i32[] = arm64_blr([], arm64_x1());
    if (l[0] != 32 || l[1] != 0 || l[2] != 63 || l[3] != 214) { return 12; }
    return 0;
}
`

const arm64MachOExitDriverMain = `
function main(): i32 {
    let code: i32[] = [];
    code = arm64_movz(code, arm64_x0(), 42, 0, false);  // exit status
    code = arm64_movz(code, arm64_x16(), 1, 0, false);  // SYS_exit (Darwin)
    code = arm64_svc(code, 128);                  // svc #0x80
    let none: i32[] = [];
    let bin: i32[] = macho_executable(to_u8(code), none, none, "fern", 0, 0, none);
    write(string_from_bytes_unchecked(to_u8(bin)));
    return 0;
}
`
