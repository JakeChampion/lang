package e2eselfhost

import (
	"testing"
)

// TestSelfHostArrArrDiscReclaimIRArm64 is the arm64 port of
// TestSelfHostArrArrDiscReclaimIRX86_64: the discardable_scalar_arrarr_lit
// admission + __fern_arrarr_free routing live in shared irlower.fern; the arm64
// leg differs only in the release-helper body (__fn___fern_arrarr_free, need-
// seeded from the op-scan). Case table shared with the x86-64 leg.
func TestSelfHostArrArrDiscReclaimIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range arrArrDiscReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (98 = arrarr temp leaked; 99 = over-release/underflow; 96-97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}
