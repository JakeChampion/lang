package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostArm64LoadStore(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host arm64 load/store e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")

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

func TestSelfHostArm64DarwinMachOFrameRuns(t *testing.T) {
	assertMachORuns(t, machoRun{name: "frame42", main: arm64MachOFrameDriverMain, wantExit: 42})
}

const arm64LoadStoreSelfTestMain = `
function main(): i32 {
    // str x0, [sp, #8] -> 0xF90007E0 -> E0 07 00 F9
    let a: i32[] = arm64_str([], arm64_x0(), arm64_sp(), 8, false);
    if (a[0] != 224 || a[1] != 7 || a[2] != 0 || a[3] != 249) { return 1; }
    // ldr x0, [sp, #8] -> 0xF94007E0 -> E0 07 40 F9
    let b: i32[] = arm64_ldr([], arm64_x0(), arm64_sp(), 8, false);
    if (b[0] != 224 || b[1] != 7 || b[2] != 64 || b[3] != 249) { return 2; }
    // str x1, [x2, #0] -> 0xF9000041 -> 41 00 00 F9
    let c: i32[] = arm64_str([], arm64_x1(), arm64_x2(), 0, false);
    if (c[0] != 65 || c[1] != 0 || c[2] != 0 || c[3] != 249) { return 3; }
    // ldr x1, [x2, #16] -> 0xF9400841 -> 41 08 40 F9
    let d: i32[] = arm64_ldr([], arm64_x1(), arm64_x2(), 16, false);
    if (d[0] != 65 || d[1] != 8 || d[2] != 64 || d[3] != 249) { return 4; }
    return 0;
}
`

const arm64MachOFrameDriverMain = `
function main(): i32 {
    let code: i32[] = [];
    code = arm64_subimm(code, arm64_sp(), arm64_sp(), 16, false); // sub sp, sp, #16
    code = arm64_movz(code, arm64_x0(), 42, 0, false);            // x0 = 42
    code = arm64_str(code, arm64_x0(), arm64_sp(), 8, false);     // str x0, [sp, #8]
    code = arm64_movz(code, arm64_x0(), 0, 0, false);             // clobber x0
    code = arm64_ldr(code, arm64_x0(), arm64_sp(), 8, false);     // ldr x0, [sp, #8]
    code = arm64_addimm(code, arm64_sp(), arm64_sp(), 16, false); // add sp, sp, #16
    code = arm64_movz(code, arm64_x16(), 1, 0, false);            // SYS_exit (Darwin)
    code = arm64_svc(code, 128);                            // svc #0x80
    let none: i32[] = [];
    let bin: i32[] = macho_executable(to_u8(code), none, none, "fern", 0, 0, none);
    write(string_from_bytes_unchecked(to_u8(bin)));
    return 0;
}
`
