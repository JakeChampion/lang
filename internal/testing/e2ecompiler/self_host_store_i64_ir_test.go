package e2ecompiler

import (
	"bytes"
	"os/exec"
	"testing"
)

const storeI64Want = 42

const storeI64Src = `function main(): i32 {
    let p: usize = __alloc(16);
    __store_i64(p, 5000000005 as i64);
    if (__load_i64(p) != 5000000005) { return 1; }
    __store_i64(p + 8, (0 - 3) as i64);
    if (__load_i64(p + 8) != (0 - 3)) { return 2; }
    return 42;
}`

func TestSelfHostStoreI64IRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
	}
	cmd.Stdin = bytes.NewReader([]byte(storeI64Src + "\n"))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	progBin := buildBin(t, gcc, dir, "store_i64", string(asm))
	var run *exec.Cmd
	if len(runner) == 0 {
		run = exec.Command(progBin)
	} else {
		run = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = run.Run()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatal("store_i64 IR binary did not exit normally (segfault?)")
	}
	if got := run.ProcessState.ExitCode(); got != storeI64Want {
		t.Errorf("store_i64 IR exit = %d, want %d; a truncating store would give a different value", got, storeI64Want)
	}
}

func TestSelfHostStoreI64IRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	var cmd *exec.Cmd
	if len(x86runner) == 0 {
		cmd = exec.Command(driverBin, "-target", "arm64-linux")
	} else {
		cmd = exec.Command(x86runner[0], append(append(append([]string{}, x86runner[1:]...), driverBin), "-target", "arm64-linux")...)
	}
	cmd.Stdin = bytes.NewReader([]byte(storeI64Src + "\n"))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	if !bytes.Contains(asm, []byte("__store_i64")) && !bytes.Contains(asm, []byte("str x1")) {
		t.Logf("note: store_i64 lowered inline (expected)")
	}
	bin := buildBinArm64(t, arm64gcc, dir, "store_i64_arm64", string(asm))
	run := runArm64Bin(qemu, bin)
	_ = run.Run()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatal("store_i64 arm64 IR binary did not exit normally (segfault?)")
	}
	if got := run.ProcessState.ExitCode(); got != 42 {
		t.Errorf("store_i64 arm64 IR exit = %d, want 42 (full 64-bit round-trip; a truncating store would differ)", got)
	}
}
