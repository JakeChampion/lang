package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostClosureCaptureBorrowIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	run := func(t *testing.T, prog, name string, want int) {
		t.Helper()
		asm := runCapture(t, gcc, runner, driverBin, []byte(prog))
		if len(asm) == 0 {
			t.Fatalf("%s: self-host compiler emitted 0 bytes", name)
		}
		bin := buildBin(t, gcc, dir, name, string(asm))
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(bin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], bin)...)
		}
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != want {
			t.Errorf("%s exited %d, want %d (nonzero underflow = a borrowed capture extract was released)", name, code, want)
		}
	}

	run(t, `function call2(f: () => i32): i32 { return f() + f(); }
function go(k: i32): i32 {
    let xs: i32[] = [k, k + 1];
    let c = () => xs[0] + xs[1];
    return call2(c) / 2;
}
function main(): i32 {
    let q: i32 = go(3);
    if (q != 7) { return 90 + q; }
    return __rc_underflow_count();
}`, "escaping-capture-borrow-detector-zero", 0)

	run(t, `function go(k: i32): i32 {
    let xs: i32[] = [k, k + 1];
    let c = () => xs[0] + xs[1];
    let d = c;
    let a: i32 = c();
    let b: i32 = d();
    if (a != b) { return 98; }
    return a;
}
function main(): i32 {
    let q: i32 = go(3);
    if (q != 7) { return 90 + q; }
    return __rc_underflow_count();
}`, "alias-capture-borrow-detector-zero", 0)

	run(t, `function go(k: i32): i32 {
    let xs: i32[] = [k, k + 1];
    let c = () => xs[0] + xs[1];
    let d = c;
    let a: i32 = c();
    let b: i32 = d();
    if (a != b) { return 98; }
    return a;
}
function main(): i32 {
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < 3000) { acc = (acc + go(i)) % 251; i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    i = 0;
    while (i < 3000) { acc = (acc + go(i)) % 251; i = i + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 97; }
    return 0;
}`, "alias-capture-churn-flat-detector-zero", 0)
}

func TestSelfHostClosureCaptureBorrowIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	prog := `function call2(f: () => i32): i32 { return f() + f(); }
function go(k: i32): i32 {
    let xs: i32[] = [k, k + 1];
    let c = () => xs[0] + xs[1];
    return call2(c) / 2;
}
function main(): i32 {
    let q: i32 = go(3);
    if (q != 7) { return 90 + q; }
    return __rc_underflow_count();
}`
	asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(prog), "-target", "arm64-linux")
	if len(asm) == 0 {
		t.Fatalf("self-host arm64 compiler emitted 0 bytes")
	}
	bin := buildBinArm64(t, arm64gcc, dir, "escaping-capture-borrow-arm64", string(asm))
	cmd := runArm64Bin(qemu, bin)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("escaping-capture-borrow-arm64 exited %d, want 0 (nonzero underflow = borrowed extract released)", code)
	}
}
