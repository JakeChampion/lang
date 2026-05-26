package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// fieldAssignCases exercise struct field assignment (`obj.field =
// value`), which the self-host parser desugars to __set_field(obj,
// "field", value) and the emitter shape-dispatches to the field's
// slot. Mutation persists through the heap pointer, so a struct passed
// to a function is mutated in place. Exit codes cross-checked vs the Go
// backend (except the compound `+=` case, which the Go *native*
// backend mishandles for fields — the self-host's 42 is correct).
var fieldAssignCases = []struct {
	name string
	src  string
	exit int
}{
	{"bump-through-fn", "struct Box { v: i32 } function bump(b: Box) { b.v = b.v + 1; } function main(): i32 { var b: Box = Box { v: 10 }; bump(b); bump(b); return b.v; }", 12},
	{"two-fields", "struct P { x: i32, y: i32 } function main(): i32 { var p: P = P { x: 1, y: 2 }; p.x = 10; p.y = 30; return p.x + p.y; }", 40},
	{"mutate-in-loop", "struct C { n: i32 } function inc(c: C) { c.n = c.n + 1; } function main(): i32 { var c: C = C { n: 0 }; var i: i32 = 0; while (i < 5) { inc(c); i = i + 1; } return c.n; }", 5},
	{"compound", "struct A { v: i32 } function main(): i32 { var a: A = A { v: 7 }; a.v += 35; return a.v; }", 42},
}

// TestSelfHostFieldAssignX86_64 compiles field-assignment programs with
// the self-hosted x86-64 compiler and checks exit codes.
func TestSelfHostFieldAssignX86_64(t *testing.T) {
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

	for _, tc := range fieldAssignCases {
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

// TestSelfHostFieldAssignArm64 — CI-gated arm64 counterpart.
func TestSelfHostFieldAssignArm64(t *testing.T) {
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

	for _, tc := range fieldAssignCases {
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
