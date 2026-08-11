package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostFloatX86_64 exercises f64 support in the self-hosted
// x86-64 emitter: float literals (GAS `.double` in .rodata), arithmetic
// (addsd/subsd/mulsd/divsd via xmm regs), comparisons (ucomisd), unary
// negation (sign-bit flip), and int↔float casts (cvtsi2sd / cvttsd2si).
// Each program returns an observable i32 (a comparison bool or a
// float→i32 cast); values were cross-checked against the Go backend.
func TestSelfHostFloatX86_64(t *testing.T) {
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

	cases := []struct {
		name string
		src  string
		exit int
	}{
		{"add-cast", "function main(): i32 { var x: f64 = 3.5 + 2.5; return x as i32; }", 6},
		{"trunc", "function main(): i32 { var x: f64 = 7.9; return x as i32; }", 7},
		{"int-to-float-mul", "function main(): i32 { var n: i32 = 5; var x: f64 = n as f64; return (x * 2.0) as i32; }", 10},
		{"div", "function main(): i32 { var x: f64 = 10.0; var y: f64 = 3.0; return (x / y) as i32; }", 3},
		{"cmp-gt", "function main(): i32 { if (1.5 + 2.5 > 3.9) { return 1; } return 0; }", 1},
		{"cmp-eq", "function main(): i32 { if (2.0 * 3.0 == 6.0) { return 1; } return 0; }", 1},
		{"cmp-lt-false", "function main(): i32 { if (5.0 < 1.0) { return 1; } return 0; }", 0},
		{"neg", "function main(): i32 { var x: f64 = 0.0 - 2.5; if (x < 0.0) { return 1; } return 0; }", 1},
		{"square", "function main(): i32 { return (3.0 * 3.0) as i32; }", 9},
		{"cast-in-expr", "function main(): i32 { return (2.5 as i32) + 40; }", 42},
	}
	for _, tc := range cases {
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

// TestSelfHostFloatArm64 is the ARM64 counterpart (CI-gated, qemu):
// the self-hosted aarch64 emitter's f64 support — float literals
// (.double), fadd/fsub/fmul/fdiv, fcmp + cset, fneg, and scvtf/fcvtzs
// casts. Same cases as the x86 test, run under qemu-aarch64.
func TestSelfHostFloatArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	cases := []struct {
		name string
		src  string
		exit int
	}{
		{"add-cast", "function main(): i32 { var x: f64 = 3.5 + 2.5; return x as i32; }", 6},
		{"trunc", "function main(): i32 { var x: f64 = 7.9; return x as i32; }", 7},
		{"int-to-float-mul", "function main(): i32 { var n: i32 = 5; var x: f64 = n as f64; return (x * 2.0) as i32; }", 10},
		{"div", "function main(): i32 { var x: f64 = 10.0; var y: f64 = 3.0; return (x / y) as i32; }", 3},
		{"cmp-gt", "function main(): i32 { if (1.5 + 2.5 > 3.9) { return 1; } return 0; }", 1},
		{"cmp-eq", "function main(): i32 { if (2.0 * 3.0 == 6.0) { return 1; } return 0; }", 1},
		{"cmp-lt-false", "function main(): i32 { if (5.0 < 1.0) { return 1; } return 0; }", 0},
		{"neg", "function main(): i32 { var x: f64 = 0.0 - 2.5; if (x < 0.0) { return 1; } return 0; }", 1},
		{"square", "function main(): i32 { return (3.0 * 3.0) as i32; }", 9},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
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
