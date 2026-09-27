package e2eselfhost

import (
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// The raw floor the Go compiler exposes as `__syscall3` … `__syscall6` and
// `__store_u8` is the same floor the self-host has always been written on,
// so the one probe runs through both compilers (#9853, #4451). The self-host
// leg of `internal/e2e/native_syscall_floor_test.go`.
func TestSelfHostSyscallFloorX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driver := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")
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
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driver := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")
	src := e2eharness.SyscallFloorProbe(t, dir, "arm64-linux")
	asm := runCaptureStrictIR(t, gcc, runner, driver, []byte(src), "-target", "arm64-linux")
	bin := buildBinArm64(t, armgcc, dir, "floor", string(asm))
	if out, err := runArm64Bin(qemu, bin).CombinedOutput(); err != nil {
		t.Fatalf("probe step failed: %v\n%s", err, out)
	}
}
