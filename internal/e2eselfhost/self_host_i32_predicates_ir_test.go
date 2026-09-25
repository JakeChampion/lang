package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostI32PredicatesIRX86_64 pins that the built-in i32 methods lower on
// the IR path rather than bailing the module (#3457).
//
// asm.fern intercepted these (its ty_is_i32 block) and irlower had no case for
// them, so ANY program calling one bailed its whole module.
// Measuring the fallback put 105 of the reachable AST-emit cases in this
// "ineligible-fn" bucket — the largest category keeping asm.fern alive.
//
// Two shapes are covered:
//   - the zero-arg predicates: pure inline arithmetic over existing ops;
//   - abs / sign: inline too, but they need the receiver value twice, so it is
//     stored to a temp local and re-loaded rather than lowered twice.
//
// Two-part assertion on purpose: the exit code alone would still pass if the
// program took some other route, so each case also asserts the emitted asm
// carries the IR path's function-scoped `.Lssa_main_` labels, which the AST
// emitter (global `.L0`/`.L1` numbering) never produces.
//
// The negative cases are the ones that catch it: parity lowers as `n & 1`, not
// `n % 2`, because two's-complement AND yields 1 for a negative odd n where rem_s
// yields -1 — an `== 1` test over rem_s would call -3 even.
func TestSelfHostI32PredicatesIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_run.fern")
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
		// abs / sign take the temp-slot path: the receiver is stored once and
		// re-loaded, since both need the value twice.
		{"abs-negative", "function main(): i32 { var n: i32 = 0 - 42; return n.abs(); }", 42},
		{"abs-positive", "function main(): i32 { var n: i32 = 7; return n.abs(); }", 7},
		{"abs-zero", "function main(): i32 { var n: i32 = 0; return n.abs(); }", 0},
		{"abs-expr-receiver", "function main(): i32 { var a: i32 = 3; var b: i32 = 0 - 10; return (a + b).abs(); }", 7},
		{"sign-positive", "function main(): i32 { var n: i32 = 42; return n.sign(); }", 1},
		{"sign-zero", "function main(): i32 { var n: i32 = 0; return n.sign(); }", 0},
		// sign(-7) is -1; +2 keeps the exit code in range and pins the sign.
		{"sign-negative", "function main(): i32 { var n: i32 = 0 - 7; return n.sign() + 2; }", 1},
		// is_empty desugars to `len() == 0` and re-lowers, so it inherits the `.len()`
		// arm's receiver dispatch and its fresh-string-receiver reclaim. These cases
		// pin that it covers every receiver .len() does — the AST guard spans string,
		// int arrays and string[], so a string-only port would silently leave the
		// array forms unlowered.
		{"is-empty-string-true", "function main(): i32 { var s: string = \"\"; if (s.is_empty()) { return 1; } return 0; }", 1},
		{"is-empty-string-false", "function main(): i32 { var s: string = \"hi\"; if (s.is_empty()) { return 1; } return 0; }", 0},
		{"is-empty-i32arr-true", "function main(): i32 { var xs: i32[] = []; if (xs.is_empty()) { return 1; } return 0; }", 1},
		{"is-empty-i32arr-false", "function main(): i32 { var xs: i32[] = [1, 2]; if (xs.is_empty()) { return 1; } return 0; }", 0},
		{"is-empty-strarr-true", "function main(): i32 { var ss: string[] = []; if (ss.is_empty()) { return 1; } return 0; }", 1},
		{"is-empty-strarr-false", "function main(): i32 { var ss: string[] = [\"a\"]; if (ss.is_empty()) { return 1; } return 0; }", 0},
		// Fresh string-concat receiver: the temp box the `.len()` arm reclaims (#4365).
		{"is-empty-fresh-concat", "function main(): i32 { var a: string = \"x\"; var b: string = \"y\"; if ((a + b).is_empty()) { return 1; } return 0; }", 0},
		// first_byte / last_byte reuse the string-INDEX op (s[0], s[len-1]) rather than
		// raw loads. Operand order there is NATURAL (string then index) — the op_*
		// convention, unlike op_call_direct to a helper, which binds in reverse.
		// last_byte reads the receiver twice, so it goes through a temp local.
		{"first-byte-upper", "function main(): i32 { var s: string = \"Hello\"; return s.first_byte(); }", 72},
		{"first-byte-lower", "function main(): i32 { var s: string = \"abc\"; return s.first_byte(); }", 97},
		{"first-byte-single", "function main(): i32 { var s: string = \"z\"; return s.first_byte(); }", 122},
		{"last-byte-letter", "function main(): i32 { var s: string = \"abc\"; return s.last_byte(); }", 99},
		{"last-byte-punct", "function main(): i32 { var s: string = \"hi!\"; return s.last_byte(); }", 33},
		// Single char: first_byte and last_byte must agree.
		{"last-byte-single", "function main(): i32 { var s: string = \"z\"; return s.last_byte(); }", 122},
		// Fresh concat receiver exercises the temp local (evaluated once, read twice).
		{"last-byte-fresh-concat", "function main(): i32 { var a: string = \"ab\"; var b: string = \"cd\"; return (a + b).last_byte(); }", 100},
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
			if !strings.Contains(string(emittedAsm), ".Lssa_main_") {
				t.Fatalf("predicate did not route through the IR path (no `.Lssa_main_` label — the AST "+
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
