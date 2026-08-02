package e2eselfhost

import (
	"testing"
)

// TestSelfHostOptArrArrReclaimIRArm64 is the arm64 port of
// TestSelfHostOptArrArrReclaimIRX86_64: the OPTARRARR class (admission + inline
// tag-check/__fern_arrarr_free/box-free + the arr-of-arr escape checker) lives in shared
// irlower.fern and lowers through op_opt_tag / op_opt_payload / __fern_arrarr_free /
// __fern_rc_dec, all backend-complete. Case table shared with the x86-64 leg.
func TestSelfHostOptArrArrReclaimIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range optArrArrReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (98 = option leaked; 99 = over-release/underflow; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}
