package e2eselfhost

import (
	"testing"
)

// TestSelfHostArrEnumReclaimIRArm64 is the arm64 port of
// TestSelfHostArrEnumReclaimIRX86_64. The ARRENUM class lives entirely in shared
// irlower.fern — the element walk is IR (op_arr_get on the 8-byte pointer element slot
// plus emit_enum_variant_drops' runtime variant dispatch), so it needs no arm64 helper.
// Case table shared with the x86-64 leg.
func TestSelfHostArrEnumReclaimIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range arrEnumReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (98 = enum-array elements leaked; 99 = over-release/underflow; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}
