package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// floatIntrinsicCases exercise the cheap f64 math intrinsics emitted
// by the self-hosted compiler: __abs_f64 / __sqrt_f64 / __floor_f64 /
// __ceil_f64 / __trunc_f64 / __round_f64. The -nostdlib native
// backends can't link libm, so these lower to single SSE / FP
// instructions (sqrtsd, roundsd, andpd-style sign-mask; fsqrt, frintm,
// frintp, frintz, frinta, fabs). Each program returns an observable
// i32 (the intrinsic result cast to i32, or a tolerance comparison).
// Values cross-checked against Go's math package.
var floatIntrinsicCases = []struct {
	name string
	src  string
	exit int
}{
	{"abs-neg", "function main(): i32 { return __abs_f64(0.0 - 5.5) as i32; }", 5},
	{"abs-pos", "function main(): i32 { return __abs_f64(3.0) as i32; }", 3},
	{"sqrt-exact", "function main(): i32 { return __sqrt_f64(16.0) as i32; }", 4},
	{"sqrt-tol", "function main(): i32 { var r: f64 = __sqrt_f64(2.0); if (r > 1.41 && r < 1.42) { return 7; } return 0; }", 7},
	{"floor", "function main(): i32 { return __floor_f64(7.9) as i32; }", 7},
	{"ceil", "function main(): i32 { return __ceil_f64(7.1) as i32; }", 8},
	{"trunc", "function main(): i32 { return __trunc_f64(7.9) as i32; }", 7},
	{"round-up", "function main(): i32 { return __round_f64(2.5) as i32; }", 3},
	{"round-down", "function main(): i32 { return __round_f64(2.4) as i32; }", 2},
}

// TestSelfHostFloatIntrinsicsX86_64 — the self-hosted x86-64 emitter's
// cheap libm intrinsics (plan item 4, cheap subset).
func TestSelfHostFloatIntrinsicsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	for _, tc := range floatIntrinsicCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostFloatIntrinsicsArm64 — CI-gated arm64 counterpart.
func TestSelfHostFloatIntrinsicsArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "asm_arm64.fern", "asm_arm64_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_arm64_run.fern", "driver")

	for _, tc := range floatIntrinsicCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBin(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
