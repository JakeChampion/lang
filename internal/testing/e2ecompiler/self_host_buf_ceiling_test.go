package e2ecompiler

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostBufGrowsPastOldCeilingX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")
	asm := string(runCapture(t, gcc, runner, driverBin, []byte(e2eharness.BufCeilingProbe)))
	if !strings.Contains(asm, "call __fern_buf_reserve") {
		t.Fatal("the emitted builder never calls __fern_buf_reserve; the buffer is still fixed")
	}
	bin := buildBin(t, gcc, dir, "buf_ceiling", asm)
	cmd := runX86_64Bin(runner, bin)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("exit %d, want 0 (%s)", code, e2eharness.BufCeilingProbeCodes)
	}
}

func TestSelfHostBufGrowsPastOldCeilingArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")
	asm := string(runCapture(t, x86gcc, x86runner, driverBin, []byte(e2eharness.BufCeilingProbe), "-target", "arm64-linux"))
	if !strings.Contains(asm, "bl __fern_buf_reserve") {
		t.Fatal("the emitted builder never calls __fern_buf_reserve; the buffer is still fixed")
	}
	bin := buildBinArm64(t, arm64gcc, dir, "buf_ceiling", asm)
	cmd := runArm64Bin(qemu, bin)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("exit %d, want 0 (%s)", code, e2eharness.BufCeilingProbeCodes)
	}
}
