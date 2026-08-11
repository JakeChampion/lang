package e2eselfhost

import (
	"testing"
)

// TestSelfHostStrOnlyStructExitDropIRArm64 is the arm64 port of
// TestSelfHostStrOnlyStructExitDropIRX86_64: the widened exit-sweep routing
// lives in shared irlower.fern, and emit_arm64_struct_drop_one's k_str arm
// (rc-aware __fern_str_free) does the field release. Case table shared with
// the x86-64 leg.
func TestSelfHostStrOnlyStructExitDropIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range strOnlyStructExitDropCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (98 = string field leaked; 99 = over-release/underflow; 97 = value corrupted; 1/2 = live value lost)", tc.name, code, tc.want)
			}
		})
	}
}
