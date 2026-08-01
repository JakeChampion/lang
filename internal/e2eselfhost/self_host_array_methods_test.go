package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// arrayMethodCases cover two self-host fixes: (1) an untyped array
// literal `var a = [1,2,3]` infers as array_i32 (so `.sum()` etc.
// dispatch correctly, not as a generic array); (2) `.min()` / `.max()`
// return Option[i32] (Some on non-empty, None on empty) — matching the
// reference semantics — rather than a raw i32. Exit codes cross-checked
// vs the Go backend.
var arrayMethodCases = []struct {
	name string
	src  string
	exit int
}{
	{"untyped-sum", "function main(): i32 { var a = [5, 3, 8, 1]; return a.sum(); }", 17},
	{"untyped-product", "function main(): i32 { var a = [2, 3, 4]; return a.product(); }", 24},
	{"min-some", "function main(): i32 { var a = [5, 3, 8, 1]; match (a.min()) { Some(m) => { return m; }, None => { return 0; } } }", 1},
	{"max-some", "function main(): i32 { var a = [5, 3, 8, 1]; match (a.max()) { Some(m) => { return m; }, None => { return 0; } } }", 8},
	{"max-empty-none", "function main(): i32 { var a: i32[] = []; match (a.max()) { Some(_) => { return 1; }, None => { return 42; } } }", 42},
	{"sum-and-max", "function main(): i32 { var a = [5, 3, 8, 1]; var t = a.sum(); match (a.max()) { Some(m) => { return t + m; }, None => { return 255; } } }", 25},
}

// TestSelfHostArrayMethodsX86_64 — untyped array inference + Option-
// returning min/max with the self-hosted x86-64 compiler.
func TestSelfHostArrayMethodsX86_64(t *testing.T) {
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

	for _, tc := range arrayMethodCases {
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

// TestSelfHostArrayMethodsArm64 — CI-gated arm64 counterpart.
func TestSelfHostArrayMethodsArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range arrayMethodCases {
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
