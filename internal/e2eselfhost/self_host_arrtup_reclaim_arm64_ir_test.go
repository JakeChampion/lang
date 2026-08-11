package e2eselfhost

import (
	"testing"
)

// TestSelfHostArrTupReclaimIRArm64 is the arm64 port of
// TestSelfHostArrTupReclaimIRX86_64: the ARRTUP class (admission + the counted
// element-walk deep-free + the element-payload escape checker) lives in shared
// irlower.fern and lowers through backend-common IR ops (block / loop / arr_len /
// arr_get / __fern_rc_dec + emit_tuple_type_child_drops), all backend-complete.
// Case table shared with the x86-64 leg.
func TestSelfHostArrTupReclaimIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range arrtupReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (98 = array-of-tuples leaked; 99 = over-release/underflow; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}
