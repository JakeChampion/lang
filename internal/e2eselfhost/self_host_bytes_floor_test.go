package e2eselfhost

import (
	"os/exec"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// The runtime's bytes floor (#9853): `__str_bytes` and `__arr_set_len` lower
// onto the raw floor the self-host's own runtime bodies use.
func TestSelfHostBytesFloorX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driver := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")
	asm := runCaptureStrictIR(t, gcc, runner, driver, []byte(e2eharness.BytesFloorProbe()))
	bin := buildBin(t, gcc, dir, "floor", string(asm))
	requireExit42(t, runX86_64Bin(runner, bin))
}

func TestSelfHostBytesFloorArm64(t *testing.T) {
	armgcc, qemu := arm64Tooling(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driver := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")
	asm := runCaptureStrictIR(t, gcc, runner, driver, []byte(e2eharness.BytesFloorProbe()), "-target", "arm64-linux")
	bin := buildBinArm64(t, armgcc, dir, "floor", string(asm))
	requireExit42(t, runArm64Bin(qemu, bin))
}

func requireExit42(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	out, err := cmd.CombinedOutput()
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 42 {
		t.Fatalf("probe: %v, want exit 42; first failing step: %s", err, out)
	}
}
