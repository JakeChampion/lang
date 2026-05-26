package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// switchCases cover `switch (E) { case V: …; case A, B: …; default: … }`,
// which the parser desugars to a nested if/else-if chain (each case
// body runs alone — no fall-through; multi-value cases OR their
// comparisons). Covers a multi-value case, the first case, the default,
// and a string scrutinee. Exit codes cross-checked vs the Go backend.
const switchClassify = "function classify(n: i32): i32 { switch (n) { case 0: return 100; case 1, 2, 3: return 42; default: return 0; } return 0 - 1; } "

var switchCases = []struct {
	name string
	src  string
	exit int
}{
	{"multi-value-case", switchClassify + "function main(): i32 { return classify(2); }", 42},
	{"first-case", switchClassify + "function main(): i32 { return classify(0); }", 100},
	{"default", switchClassify + "function main(): i32 { return classify(9); }", 0},
	{"string-scrutinee", "function main(): i32 { var s: string = \"b\"; switch (s) { case \"a\": return 1; case \"b\": return 7; default: return 0; } return 9; }", 7},
}

// TestSelfHostSwitchX86_64 — `switch`/`case` desugar with the
// self-hosted x86-64 compiler.
func TestSelfHostSwitchX86_64(t *testing.T) {
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

	for _, tc := range switchCases {
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

// TestSelfHostSwitchArm64 — CI-gated arm64 counterpart.
func TestSelfHostSwitchArm64(t *testing.T) {
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

	for _, tc := range switchCases {
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
