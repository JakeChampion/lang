package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// ifLetCases cover `if let PAT = EXPR { then } else { else }`, which the
// parser desugars to `match (EXPR) { PAT => { then }, _ => { else } }`.
// Covers the Some arm, the else arm (None), a no-else fall-through, and
// a user enum variant. Exit codes cross-checked vs the Go backend.
var ifLetCases = []struct {
	name string
	src  string
	exit int
}{
	{"some", "function main(): i32 { var m: Map[string,i32] = map_new(4); m = m.insert(\"k\", 42); if let Some(v) = m.get(\"k\") { return v; } else { return 1; } }", 42},
	{"none-else", "function main(): i32 { var m: Map[string,i32] = map_new(4); m = m.insert(\"k\", 42); if let Some(v) = m.get(\"absent\") { return v; } else { return 7; } }", 7},
	{"no-else-fallthrough", "function main(): i32 { var m: Map[string,i32] = map_new(4); m = m.insert(\"k\", 5); if let Some(v) = m.get(\"absent\") { return v; } return 9; }", 9},
	{"user-variant", "enum Shape { Circle(i32), Empty } function main(): i32 { var s: Shape = Circle(42); if let Circle(r) = s { return r; } else { return 0; } }", 42},
}

// TestSelfHostIfLetX86_64 — `if let` desugar with the self-hosted
// x86-64 compiler.
func TestSelfHostIfLetX86_64(t *testing.T) {
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

	for _, tc := range ifLetCases {
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

// TestSelfHostIfLetArm64 — CI-gated arm64 counterpart.
func TestSelfHostIfLetArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range ifLetCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64")
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
