package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const allocTrapSrc = "function main(): i32 { let i: i32 = 0; let last: usize = 0; while (i < 100000) { last = __alloc(2000000000); i = i + 1; } if (last == 0) { return 99; } return i; }"

func TestSelfHostAllocTrapX86_64(t *testing.T) {
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

	asm := runCapture(t, gcc, runner, driverBin, []byte(allocTrapSrc))
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes")
	}
	progBin := buildBin(t, gcc, dir, "alloc_trap", string(asm))
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 125 {
		t.Errorf("heap-overflow program exited %d, want 125 (clean arena trap; 137 "+
			"would mean the HOST OOM-killed it, a different failure)", code)
	}
}

func TestSelfHostAllocTrapArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(allocTrapSrc), "-target", "arm64-linux")
	if len(asm) == 0 {
		t.Fatal("self-host arm64 compiler emitted 0 bytes")
	}
	progBin := buildBin(t, arm64gcc, dir, "alloc_trap", string(asm))
	cmd := runArm64Bin(qemu, progBin)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 125 {
		t.Errorf("heap-overflow program exited %d, want 125 (clean arena trap; 137 "+
			"would mean the HOST OOM-killed it, a different failure)", code)
	}
}
