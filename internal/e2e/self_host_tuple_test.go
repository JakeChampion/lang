package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// tupleTypeCases exercise tuple TYPE annotations — `(T1, T2)` as a
// return/var type and nested inside a generic (`Option[(i32, i32)]`).
// The self-host parser previously stalled (OOM) on the leading `(` of a
// tuple type; these confirm it now parses and the i32-tuple values work.
// (Tuple element TYPES are still coarse — e.g. `.len()` on a string
// tuple field mis-infers — a separate inference limitation.)
var tupleTypeCases = []struct {
	name string
	src  string
	exit int
}{
	{"return-tuple", "function pair(): (i32, i32) { return (3, 4); } function main(): i32 { var p = pair(); return p.0 + p.1; }", 7},
	{"var-tuple-type", "function main(): i32 { var p: (i32, i32) = (5, 6); return p.0 * p.1; }", 30},
	{"option-of-tuple", "function f(): Option[(i32, i32)] { return Some((10, 20)); } function main(): i32 { match (f()) { Some(p) => { return p.0 + p.1; }, None => { return 0; } } return 0; }", 30},
	{"tuple-param", "function add(p: (i32, i32)): i32 { return p.0 + p.1; } function main(): i32 { return add((19, 23)); }", 42},
}

// TestSelfHostTupleTypeX86_64 compiles tuple-type programs with the
// self-hosted compiler and checks the exit codes (cross-checked vs the
// Go backend).
func TestSelfHostTupleTypeX86_64(t *testing.T) {
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

	for _, tc := range tupleTypeCases {
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

// TestSelfHostTupleTypeArm64 is the ARM64 counterpart (CI-gated, qemu).
func TestSelfHostTupleTypeArm64(t *testing.T) {
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

	for _, tc := range tupleTypeCases {
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
