package e2eselfhost

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// The string builder past 64 MiB through the SELF-HOST-emitted runtime. Both
// self-host register emitters reserved a fixed 64 MiB .bss buffer and trapped
// an append past it with exit 125, which is what #7267 read as arena
// exhaustion when the compiler compiled itself; the buffer is a heap block
// that doubles now. Each leg builds the probe with the self-host compiler and
// runs it, so the grow copy, the append after a grow and the take of a buffer
// that grew all execute in the emitted runtime.

func TestSelfHostStrbufGrowsPastOldCeilingX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	asm := string(runCapture(t, gcc, runner, driverBin, []byte(e2eharness.StrbufCeilingProbe)))
	if !strings.Contains(asm, "call __fern_strbuf_grow") {
		t.Fatal("the emitted __fern_strbuf_append never calls __fern_strbuf_grow; the buffer is still fixed")
	}
	bin := buildBin(t, gcc, dir, "strbuf_ceiling", asm)
	cmd := runX86_64Bin(runner, bin)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("exit %d, want 0 (%s)", code, e2eharness.StrbufCeilingProbeCodes)
	}
}

func TestSelfHostStrbufGrowsPastOldCeilingArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")
	asm := string(runCapture(t, x86gcc, x86runner, driverBin, []byte(e2eharness.StrbufCeilingProbe), "-target", "arm64-linux", "-ir"))
	if !strings.Contains(asm, "bl __fern_strbuf_grow") {
		t.Fatal("the emitted __fern_strbuf_append never calls __fern_strbuf_grow; the buffer is still fixed")
	}
	bin := buildBinArm64(t, arm64gcc, dir, "strbuf_ceiling", asm)
	cmd := runArm64Bin(qemu, bin)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("exit %d, want 0 (%s)", code, e2eharness.StrbufCeilingProbeCodes)
	}
}
