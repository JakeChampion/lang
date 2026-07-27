package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostScriptMainIRX86_64 pins that SCRIPT-shaped programs — top-level
// statements with no `main` — compile through the IR path rather than the legacy
// AST emitter (#3457).
//
// The IR emitters are whole-program emitters whose `_start` does `call
// __fn_main`, so a script had no entry to lower: asm_ir.emit_module_ir_gated's
// `has_main` gate turned it away and asm.emit_module's AST fallback picked it up,
// inlining the statements into `_start` itself. That made script support a reason
// asm.fern could not be deleted. asmcore.synth_script_main now desugars the script
// into `function main(): i32 { … }` before the gate, so the one IR pipeline serves
// both shapes.
//
// Measuring the fallback (replacing it with a hard error and running the suite)
// put 39 of the reachable AST-emit cases in this bucket, all of them here and in
// the cross-validation suite — so this is the guard that keeps them off the AST
// emitter once it is gone.
//
// The assertion is deliberately two-part: the exit code alone would still pass if
// the program silently fell back to AST, so each case also asserts the emitted asm
// carries the IR shape (`call __fn_main`), which the AST no-main path never emits.
func TestSelfHostScriptMainIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm.fern", "asm_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	cases := []struct {
		name     string
		source   string
		expected int
	}{
		{"bare-return", "return 42;", 42},
		{"var-then-return", "var x = 5; x = x + 3; return x;", 8},
		{"two-vars", "var a = 3; var b = 4; return a * b;", 12},
		{"while-loop", "var i = 1; var s = 0; while (i <= 5) { s += i; i += 1; } return s;", 15},
		{"if-else", "if (1 < 2) { return 9; } return 3;", 9},
		// A boolean-valued return from the synthesized `main(): i32`.
		{"boolean-return", "return 7 == 7;", 1},
		// No trailing `return`: synth_script_main appends `return 0;`, matching the
		// fallback exit-0 epilogue the AST emitter wrote after the inlined statements.
		{"no-trailing-return", "var x = 1;", 0},
		// Unary minus: the AST emitter selected `negq`, the IR path lowers `0 - x`.
		// Same answer either way; this pins the answer, not the encoding.
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
