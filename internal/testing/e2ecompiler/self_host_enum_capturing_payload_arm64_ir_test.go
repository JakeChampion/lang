package e2ecompiler

import (
	"strings"
	"testing"
)

func TestSelfHostEnumCapturingPayloadIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	src := []byte(capturingEnumProgram + "\n")
	want := interpExit(t, interpBin, string(src))

	asm := runCapture(t, x86gcc, x86runner, driverBin, src, "-target", "arm64-linux")
	if len(asm) == 0 {
		t.Fatal("self-host arm64 compiler emitted 0 bytes")
	}
	if !strings.Contains(string(asm), "blr ") {
		t.Error("arm64 asm has no `blr` — the captured continuation did not dispatch via call_indirect")
	}
	bin := buildBinArm64(t, arm64gcc, dir, "enum_capturing_payload_arm64", string(asm))
	cmd := runArm64Bin(qemu, bin)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != want {
		t.Errorf("capturing-payload enum exited %d, want %d (interp oracle)", code, want)
	}
}
