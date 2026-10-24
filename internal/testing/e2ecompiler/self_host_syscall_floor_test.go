package e2ecompiler

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostSyscallFloorX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driver := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")
	src := e2eharness.SyscallFloorProbe(t, dir, "x86-64-linux")
	asm := runCaptureStrictIR(t, gcc, runner, driver, []byte(src))
	bin := buildBin(t, gcc, dir, "floor", string(asm))
	if out, err := runX86_64Bin(runner, bin).CombinedOutput(); err != nil {
		t.Fatalf("probe step failed: %v\n%s", err, out)
	}
}

func TestSelfHostSyscallFloorArm64(t *testing.T) {
	armgcc, qemu := arm64Tooling(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driver := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")
	src := e2eharness.SyscallFloorProbe(t, dir, "arm64-linux")
	asm := runCaptureStrictIR(t, gcc, runner, driver, []byte(src), "-target", "arm64-linux")
	bin := buildBinArm64(t, armgcc, dir, "floor", string(asm))
	if out, err := runArm64Bin(qemu, bin).CombinedOutput(); err != nil {
		t.Fatalf("probe step failed: %v\n%s", err, out)
	}
}
