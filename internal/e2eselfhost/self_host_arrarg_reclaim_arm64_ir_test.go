package e2eselfhost

import (
	"testing"
)

// TestSelfHostArrArgReclaimIRArm64 is the arm64 port of
// TestSelfHostArrArgReclaimIRX86_64: the call-arg array-temp stash + post-call
// __fern_rc_dec and the consumed-param borrow-verdict fix both live in shared
// irlower.fern, so the arm64 leg only differs in the release helper body
// (__fn___fern_arr_dec). Case table shared with the x86-64 leg.
func TestSelfHostArrArgReclaimIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range arrArgReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (98 = arg temp leaked; 99 = over-release/underflow; 94-97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}
