package e2eselfhost

import (
	"strings"
	"testing"
)

// TestSelfHostEnumCapturingPayloadIRArm64 is the arm64 half of slice 5b
// (docs/ASYNC-SELFHOST-IR.md). The fix is in the shared, target-agnostic
// lowering (env-box user-enum fn payloads in the lift; mark the match bind a
// closure local before the enum/struct branch), so arm64 gets it via the
// existing closure call_indirect machinery. The capturing-payload enum routes
// the IR path and runs to the interp oracle (42) under qemu.
func TestSelfHostEnumCapturingPayloadIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range []struct {
		name     string
		source   string
		indirect bool
	}{
		{"optimized", capturingEnumProgram, false},
		// Keep the producer opaque so the continuation must dispatch indirectly.
		{"indirect", strings.Replace(capturingEnumProgram, "function make(", "@noinline\nfunction make(", 1), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.source + "\n")
			want := interpExit(t, interpBin, string(src)) // 42
			asm := runCapture(t, x86gcc, x86runner, driverBin, src, "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			if tc.indirect && !strings.Contains(string(asm), "blr ") {
				t.Error("arm64 asm has no blr: the captured continuation did not dispatch indirectly")
			}
			bin := buildBinArm64(t, arm64gcc, t.TempDir(), "enum_capturing_payload_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("capturing-payload enum exited %d, want %d (interp oracle)", code, want)
			}
		})
	}
}
