package e2eselfhost

import (
	"testing"
)

// TestSelfHostStrTupleReclaimIRArm64 is the arm64 port of
// TestSelfHostStrTupleReclaimIRX86_64: the string-element tuple admission
// (tuple_str_elem_fresh) and deep-drop (emit_tuple_child_drops routing through
// the rc-aware __fern_str_free) live in shared irlower.fern and lower through
// op_tuple_get + the backend-complete str_free helper. Case table shared with
// the x86-64 leg.
func TestSelfHostStrTupleReclaimIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range strTupleReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (98 = string-tuple leaked; 99 = over-release/underflow; 97/96 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}
