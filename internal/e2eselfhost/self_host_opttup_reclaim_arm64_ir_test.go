package e2eselfhost

import (
	"testing"
)

// TestSelfHostOptTupReclaimIRArm64 is the arm64 port of
// TestSelfHostOptTupReclaimIRX86_64: the OPTTUP class (admission + inline
// tag-check/tuple-deep-drop/box-free + the tuple-payload escape checker) lives in
// shared irlower.fern and lowers through op_opt_tag / op_opt_payload /
// op_tuple_get / __fern_rc_dec, all backend-complete. Case table shared with the
// x86-64 leg.
func TestSelfHostOptTupReclaimIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range optTupReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
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
