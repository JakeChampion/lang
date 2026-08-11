package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// tupleTypeCases exercise tuple TYPE annotations — `(T1, T2)` as a
// return/var type and nested inside a generic (`Option[(i32, i32)]`).
// The self-host parser must not stall (OOM) on the leading `(` of a tuple
// type; these confirm it parses and the i32-tuple values work.
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
	// Tuple literal with an i32[] element (leak mode): the array pointer rides
	// one tuple slot and `t.N` recovers it via tuple_get — `(t.0)[i]`, a bound
	// `var a = t.0; a[i]`, and `a.len()` all work without extra element-tag
	// plumbing (array ops only need the pointer the tuple_get produces).
	{"tuple-arr-elem", "function main(): i32 { var t = ([10, 20, 30], 9); var a = t.0; return a[0] + a[2] + t.1; }", 49},
	{"tuple-arr-len", "function main(): i32 { var t = ([10, 20, 30], 9); var a = t.0; return a.len() + t.1; }", 12},
	{"tuple-arr-direct-index", "function main(): i32 { var t = ([10, 20, 30], 9); return (t.0)[1] + t.1; }", 29},
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
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tupleTypeCases {
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
