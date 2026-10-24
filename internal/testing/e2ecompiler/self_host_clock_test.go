package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostClockX86_64(t *testing.T) {
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

	prog := `function main(): i32 {
    if (now_unix_ms() <= 1000000000000) { return 1; }
    let a: i64 = monotonic_ns();
    let b: i64 = monotonic_ns();
    if (b < a) { return 2; }
    return 7;
}`
	asm := runCapture(t, gcc, runner, driverBin, []byte(prog))
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes")
	}
	progBin := buildBin(t, gcc, dir, "clock", string(asm))
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), progBin)...)
	}
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 7 {
		t.Errorf("clock program exited %d, want 7 (1=now_unix_ms too small, 2=monotonic regressed)", code)
	}
}

func TestSelfHostClockArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	clockSrc := `function main(): i32 {
    if (now_unix_ms() <= 1000000000000) { return 1; }
    let a: i64 = monotonic_ns();
    let b: i64 = monotonic_ns();
    if (b < a) { return 2; }
    return 7;
}`
	clockAsm := runCapture(t, x86gcc, x86runner, driverBin, []byte(clockSrc), "-target", "arm64-linux")
	if len(clockAsm) == 0 {
		t.Fatal("self-host arm64 compiler emitted 0 bytes for the clock program")
	}
	clockBin := buildBin(t, arm64gcc, dir, "clock", string(clockAsm))

	cmd := runArm64Bin(qemu, clockBin)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 7 {
		t.Errorf("clock program exited %d, want 7 (1=now_unix_ms too small, 2=monotonic regressed)", code)
	}
}
