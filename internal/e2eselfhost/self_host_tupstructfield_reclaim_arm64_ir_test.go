package e2eselfhost

import (
	"testing"
)

// TestSelfHostTupStructFieldReclaimIRArm64 is the arm64 port of
// TestSelfHostTupStructFieldReclaimIRX86_64: the field-read lowering
// (expr_tuple_elem_tag's ExprIndex arm) and the struct-aware ARRTUP / OPTTUP escape
// checkers live in shared irlower.fern and lower through op_tuple_get / op_struct_get /
// __struct_drop_<P> / __fern_rc_dec, all backend-complete. Case table shared with the
// x86-64 leg.
func TestSelfHostTupStructFieldReclaimIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tupStructFieldReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (98 = leaked; 99 = over-release/underflow; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}
