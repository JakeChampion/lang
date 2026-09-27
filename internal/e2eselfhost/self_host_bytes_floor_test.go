package e2eselfhost

import (
	"os/exec"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// The self-host leg of `internal/e2e/bytes_floor_test.go` (#9853, #4451):
// `__str_bytes` and `__arr_set_len` lower on the self-host too, onto the raw
// floor its own runtime bodies use. The probe fills its array from a literal
// here, since the self-host keeps a byte array one word per element and its
// data-pointer cast names that storage.
func TestSelfHostBytesFloorX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driver := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")
	asm := runCaptureStrictIR(t, gcc, runner, driver, []byte(e2eharness.BytesFloorProbe(false)))
	bin := buildBin(t, gcc, dir, "floor", string(asm))
	requireExit42(t, runX86_64Bin(runner, bin))
}

func TestSelfHostBytesFloorArm64(t *testing.T) {
	armgcc, qemu := arm64Tooling(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driver := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")
	asm := runCaptureStrictIR(t, gcc, runner, driver, []byte(e2eharness.BytesFloorProbe(false)), "-target", "arm64-linux")
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
