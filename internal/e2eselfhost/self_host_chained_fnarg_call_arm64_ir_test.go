package e2eselfhost

import (
	"testing"
)

// TestSelfHostChainedFnArgCallIRArm64 is the arm64 port of
// TestSelfHostChainedFnArgCallIRX86_64 (#4767): the chained fn-arg-call
// miscompile lived in the shared lift pass (irlower.fern's
// lift_inline_closures_expr), so the arm64 IR path crashed on the same
// shapes. The case table is shared with the x86-64 leg.
func TestSelfHostChainedFnArgCallIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range chainedFnArgCallCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s self-host arm64 = %d, want %d (-1 = signal crash, the #4767 shape)", tc.name, code, tc.want)
			}
		})
	}
}
