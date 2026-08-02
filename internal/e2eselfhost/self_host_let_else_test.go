package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// letElseCases cover `let PAT = EXPR else { divergent };`, which the
// parser desugars by folding the rest of the enclosing block into the
// success arm of a statement-match:
//
//	match (EXPR) { PAT => { <rest of block> }, _ => { divergent } }
//
// Covers the matched arm, the else (diverging) arm, a built-in Option
// scrutinee both ways, and a multi-statement success body that uses the
// binding. Exit codes cross-checked vs the Go backend.
var letElseCases = []struct {
	name string
	src  string
	exit int
}{
	{"matched", "enum Shape { Circle(i32), Empty } function main(): i32 { var s: Shape = Circle(42); let Circle(r) = s else { return 0; } return r; }", 42},
	{"else-path", "enum Shape { Circle(i32), Empty } function main(): i32 { var s: Shape = Empty; let Circle(r) = s else { return 7; } return r; }", 7},
	{"opt-some", "function main(): i32 { var m: Map[string,i32] = map_new(4); m = m.insert(\"k\", 42); let Some(v) = m.get(\"k\") else { return 1; } return v; }", 42},
	{"opt-none", "function main(): i32 { var m: Map[string,i32] = map_new(4); m = m.insert(\"k\", 42); let Some(v) = m.get(\"absent\") else { return 9; } return v; }", 9},
	{"rest-multi", "function main(): i32 { var m: Map[string,i32] = map_new(4); m = m.insert(\"k\", 40); let Some(v) = m.get(\"k\") else { return 1; } var w: i32 = v + 2; return w; }", 42},
}

// TestSelfHostLetElseX86_64 — `let else` desugar with the self-hosted
// x86-64 compiler.
func TestSelfHostLetElseX86_64(t *testing.T) {
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

	for _, tc := range letElseCases {
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

// TestSelfHostLetElseArm64 — CI-gated arm64 counterpart.
func TestSelfHostLetElseArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range letElseCases {
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
