package e2eselfhost

import (
	"testing"
)

// TestSelfHostTupDiscReclaimIRArm64 is the arm64 port of
// TestSelfHostTupDiscReclaimIRX86_64: the discarded-tuple deep-drop (literal +
// TUPRET call arms) lives in shared irlower.fern and lowers through
// op_tuple_get + __fern_rc_dec, both already backend-complete. Case table
// shared with the x86-64 leg.
func TestSelfHostTupDiscReclaimIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tupDiscReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (98 = tuple temp leaked; 99 = over-release/underflow; 96-97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}
