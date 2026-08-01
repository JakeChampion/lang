package e2eselfhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostEnumCapturingPayloadIRArm64 is the arm64 half of slice 5b
// (docs/ASYNC-SELFHOST-IR.md). The fix is in the shared, target-agnostic
// irlower.fern (env-box user-enum fn payloads in the lift; mark the match bind a
// closure local before the enum/struct branch), so arm64 gets it via the
// existing closure call_indirect machinery. The capturing-payload enum routes
// the IR path and runs to the interp oracle (42) under qemu.
func TestSelfHostEnumCapturingPayloadIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	src := []byte(capturingEnumProgram + "\n")
	want := interpExit(t, interpBin, string(src)) // 42

	asm := runCapture(t, x86gcc, x86runner, driverBin, src, "-target", "arm64")
	if len(asm) == 0 {
		t.Fatal("self-host arm64 compiler emitted 0 bytes")
	}
	// The continuation dispatches via a closure call_indirect (blr).
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
