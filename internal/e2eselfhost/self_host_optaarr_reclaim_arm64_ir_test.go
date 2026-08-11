package e2eselfhost

import (
	"testing"
)

// TestSelfHostOptAarrReclaimIRArm64 is the arm64 port of
// TestSelfHostOptAarrReclaimIRX86_64: the "OPTAARR:" crediting lives in shared
// irlower.fern and the arm64 __fn___fern_optarrarr_free body mirrors the
// x86-64 one (uniqueness-gated payload dec + rc-guarded box dec + buffer
// free). Case table shared with the x86-64 leg.
func TestSelfHostOptAarrReclaimIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range optAarrReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (98 = structure leaked; 99 = over-release/underflow; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}
