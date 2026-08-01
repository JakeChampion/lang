package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Issue #2649 — the raw-memory intrinsic floor (Tier-2; docs/RUNTIME-INTRINSICS.md).
//
// rawIntrinsicsProg exercises every intrinsic end-to-end with no runtime helper
// consumer yet (chr et al. migrate on top later): __raw_alloc a buffer, fill it
// with __raw_store8, box it with __raw_string and read a byte back via s[i];
// separately round-trip a word slot through __raw_store_ptr / __raw_load_ptr and
// a byte through __raw_store8 / __raw_load8. The arithmetic collapses the
// round-trips to 0 so the exit code is exactly s.len() + s[0] = 3 + 72 = 75 —
// any miscompiled load/store/box shifts it off 75.
const rawIntrinsicsProg = `function build_str(): string {
    var p: i32 = __raw_alloc(3);
    __raw_store8(p, 0, 72);
    __raw_store8(p, 1, 105);
    __raw_store8(p, 2, 33);
    return __raw_string(p, 3);
}
function main(): i32 {
    var s: string = build_str();
    var p2: i32 = __raw_alloc(16);
    __raw_store_ptr(p2, 0, 1234);
    __raw_store8(p2, 8, 200);
    var w: i32 = __raw_load_ptr(p2, 0);
    var b: i32 = __raw_load8(p2, 8);
    return s.len() + (s[0] as i32) + (w - 1234) + (b - 200);
}`

// TestSelfHostRawIntrinsicsX86_64 runs the intrinsic probe through both the AST
// (asm_run) and IR (asm_ir_run) x86-64 self-host drivers and checks exit 75,
// proving the floor lowers correctly on both code paths. arm64 is left to CI.
func TestSelfHostRawIntrinsicsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	for _, drv := range []string{"asm_run.fern", "asm_arm64_ir.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", drv))
		if err != nil {
			t.Fatalf("read %s: %v", drv, err)
		}
		if err := os.WriteFile(filepath.Join(dir, drv), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", drv, err)
		}
	}

	cases := []struct {
		name   string
		driver string
	}{
		{"ast", "asm_run.fern"},
		{"ir", "asm_ir_run.fern"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			driverBin := buildSelfHostBin(t, gcc, dir, tc.driver, "rawintr_"+tc.name)
			asm := runCapture(t, gcc, runner, driverBin, []byte(rawIntrinsicsProg))
			progBin := buildBin(t, gcc, dir, "rawintr_prog_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 75 {
				t.Errorf("%s path: raw-intrinsic probe exited %d, want 75 (s.len()=3 + s[0]=72)", tc.name, code)
			}
		})
	}
}
