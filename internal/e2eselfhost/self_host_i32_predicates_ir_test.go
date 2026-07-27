package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostI32PredicatesIRX86_64 pins that the zero-arg i32 builtin predicates
// lower on the IR path rather than bailing the module to the legacy AST emitter
// (#3457).
//
// asm.fern intercepted these inline (its ty_is_i32 block) and irlower had no case
// for them, so ANY program calling one sent its whole module to the AST emitter.
// Measuring the fallback put 105 of the reachable AST-emit cases in this
// "ineligible-fn" bucket — the largest category keeping asm.fern alive — and these
// five predicates are the slice that needs no new opcode and no runtime helper.
//
// Two-part assertion on purpose: the exit code alone would still pass if the
// program silently fell back to AST, so each case also asserts the emitted asm
// carries the IR path's function-scoped `.Lir_main_` labels, which the AST
// emitter (global `.L0`/`.L1` numbering) never produces.
//
// The negative cases are the ones with teeth: parity lowers as `n & 1`, not
// `n % 2`, because two's-complement AND yields 1 for a negative odd n where rem_s
// yields -1 — an `== 1` test over rem_s would call -3 even.
func TestSelfHostI32PredicatesIRX86_64(t *testing.T) {
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

	pred := func(init, call string) string {
		return "function main(): i32 { var n: i32 = " + init + "; if (n." + call + ") { return 1; } return 0; }"
	}
	cases := []struct {
		name     string
		source   string
		expected int
	}{
		{"is-zero-true", pred("0", "is_zero()"), 1},
		{"is-zero-false", pred("5", "is_zero()"), 0},
		{"is-positive-true", pred("5", "is_positive()"), 1},
		{"is-positive-zero", pred("0", "is_positive()"), 0},
		{"is-positive-negative", pred("0 - 5", "is_positive()"), 0},
		{"is-negative-true", pred("0 - 5", "is_negative()"), 1},
		{"is-negative-zero", pred("0", "is_negative()"), 0},
		{"is-even-true", pred("4", "is_even()"), 1},
		{"is-even-false", pred("7", "is_even()"), 0},
		{"is-odd-true", pred("9", "is_odd()"), 1},
		{"is-odd-false", pred("8", "is_odd()"), 0},
		// Two's-complement parity: `-3 & 1 == 1` (odd), `-4 & 1 == 0` (even).
		{"is-odd-negative", pred("0 - 3", "is_odd()"), 1},
		{"is-even-negative", pred("0 - 4", "is_even()"), 1},
		{"is-even-negative-odd", pred("0 - 3", "is_even()"), 0},
		// A side-effect-free but non-trivial receiver: the lowering evaluates the
		// receiver exactly once, so a compound expression must still work.
		{"expr-receiver", "function main(): i32 { var a: i32 = 3; var b: i32 = 4; if ((a + b).is_odd()) { return 1; } return 0; }", 1},
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
			if !strings.Contains(string(emittedAsm), ".Lir_main_") {
				t.Fatalf("predicate did not route through the IR path (no `.Lir_main_` label — the AST "+
					"emitter numbers labels globally as .L0/.L1)\n--- source ---\n%s", tc.source)
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
