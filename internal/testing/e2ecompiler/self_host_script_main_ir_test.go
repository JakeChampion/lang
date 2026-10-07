package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostScriptMainIRX86_64 pins that SCRIPT-shaped programs — top-level
// statements with no `main` — compile through the IR path (#3457).
//
// The IR emitters are whole-program emitters whose `_start` does `call
// __fn_main`, so asmcore.synth_script_main desugars the script into
// `function main(): i32 { … }` before the gate, and the one IR pipeline serves
// both shapes.
//
// The assertion is two-part: each case checks its exit code and that the
// emitted asm carries the IR shape (`call __fn_main`).
func TestSelfHostScriptMainIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	cases := []struct {
		name     string
		source   string
		expected int
	}{
		{"bare-return", "return 42;", 42},
		{"var-then-return", "let x = 5; x = x + 3; return x;", 8},
		{"two-vars", "let a = 3; let b = 4; return a * b;", 12},
		{"while-loop", "let i = 1; let s = 0; while (i <= 5) { s += i; i += 1; } return s;", 15},
		{"if-else", "if (1 < 2) { return 9; } return 3;", 9},
		// No trailing `return`: synth_script_main appends `return 0;`.
		{"no-trailing-return", "let x = 1;", 0},
		// Subtraction from zero; this pins the answer, not the encoding.
		{"unary-negation", "return 0 - 5 + 10;", 5},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], driverBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.source))
			emittedAsm, err := cmd.Output()
			if err != nil {
				t.Fatalf("driver run: %v\n--- source ---\n%s", err, tc.source)
			}
			if !strings.Contains(string(emittedAsm), "call __fn_main") {
				t.Fatalf("script did not route through the IR path (no `call __fn_main` — the AST "+
					"no-main path inlines the statements into _start instead)\n--- source ---\n%s\n--- asm ---\n%s",
					tc.source, emittedAsm)
			}
			caseDir := t.TempDir()
			innerAsm := filepath.Join(caseDir, "inner.s")
			innerBin := filepath.Join(caseDir, "inner")
			if err := os.WriteFile(innerAsm, emittedAsm, 0o644); err != nil {
				t.Fatalf("write inner asm: %v", err)
			}
			if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", innerAsm, "-o", innerBin).CombinedOutput(); err != nil {
				t.Fatalf("inner gcc: %v\n%s\n--- asm ---\n%s", err, out, emittedAsm)
			}
			var inner *exec.Cmd
			if len(runner) == 0 {
				inner = exec.Command(innerBin)
			} else {
				inner = exec.Command(runner[0], append(runner[1:], innerBin)...)
			}
			_ = inner.Run()
			if code := inner.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("inner exit code = %d, want %d\n--- source ---\n%s", code, tc.expected, tc.source)
			}
		})
	}
}
