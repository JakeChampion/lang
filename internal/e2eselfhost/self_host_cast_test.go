package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
)

// castCases are valid-Fern programs exercising `expr as Type` integer
// casts (the self-host emitter masks unsigned / sign-extends signed to
// the target width). Each returns an i32 exit code; the expected value
// was cross-checked against the Go backend.
var castCases = []struct {
	name string
	src  string
	exit int
}{
	{"u8-truncate", "function main(): i32 { var x: i32 = 300; var y: u8 = x as u8; return y as i32; }", 44},
	{"u8-in-range", "function main(): i32 { return (15 as u8) as i32; }", 15},
	{"u32-passthrough", "function main(): i32 { var x: i32 = 42; return (x as u32) as i32; }", 42},
	{"cast-with-bitwise", "function main(): i32 { var b: i32 = 171; return ((b >> 4) & 15) as u8 as i32; }", 10},
	// Bitwise / shift operators (previously absent from the parser's
	// precedence table — silently dropped, and a parser runaway inside
	// parens).
	{"bit-and", "function main(): i32 { return 5 & 3; }", 1},
	{"bit-or", "function main(): i32 { return 5 | 2; }", 7},
	{"bit-xor", "function main(): i32 { return 6 ^ 3; }", 5},
	{"shift-left", "function main(): i32 { return 1 << 4; }", 16},
	{"shift-right", "function main(): i32 { return 200 >> 2; }", 50},
	{"bit-precedence", "function main(): i32 { return (8 & 12) | (1 << 1); }", 10},
}

// TestSelfHostCastX86_64 builds the asm_run self-host compiler with the
// Go backend, then compiles each cast program with it; the emitted
// binary's exit code must match the expected (Go-cross-checked) value.
func TestSelfHostCastX86_64(t *testing.T) {
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

	for _, tc := range castCases {
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

// TestSelfHostCastArm64 mirrors TestSelfHostCastX86_64 for the ARM64
// emitter (CI-gated under qemu-aarch64): the asm_ir_run (-target arm64) driver
// (x86 host binary) compiles each cast / bitwise program to aarch64
// asm, run under qemu, exit code must match.
func TestSelfHostCastArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	prog, _, err := modload.Load(filepath.Join(dir, "asm_ir_run.fern"))
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	asm, err := x86_64.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	driverBin := buildBin(t, x86gcc, dir, "driver", asm)

	for _, tc := range castCases {
		t.Run(tc.name, func(t *testing.T) {
			progAsm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64")
			if len(progAsm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBin(t, arm64gcc, dir, tc.name, string(progAsm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
