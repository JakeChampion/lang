package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// constCases exercise top-level `const NAME: T = EXPR;` declarations,
// which the self-host parser previously stalled on (exit-137). They
// desugar to a zero-arg function; a bare reference lowers to a call.
// Values cross-checked vs the Go backend.
var constCases = []struct {
	name string
	src  string
	exit int
}{
	{"simple", "const X: i32 = 42; function main(): i32 { return X; }", 42},
	{"two-consts", "const A: i32 = 10; const B: i32 = 32; function main(): i32 { return A + B; }", 42},
	{"pub-const-loop", "pub const N: i32 = 7; function main(): i32 { var s: i32 = 0; var i: i32 = 0; while (i < N) { s = s + i; i = i + 1; } return s; }", 21},
	{"const-expr", "const BASE: i32 = 1000; function main(): i32 { return (BASE / 100) + 32; }", 42},
}

// TestSelfHostConstX86_64 compiles const programs with the self-hosted
// compiler and checks exit codes.
func TestSelfHostConstX86_64(t *testing.T) {
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

	for _, tc := range constCases {
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

// TestSelfHostConstArm64 — CI-gated arm64 counterpart.
func TestSelfHostConstArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")
	for _, tc := range constCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64")
			progBin := buildBin(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
