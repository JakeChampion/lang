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
// the IR path rather than bailing the module to the legacy AST emitter (#3457).
//
// asm.fern intercepted these (its ty_is_i32 block) and irlower had no case for
// them, so ANY program calling one sent its whole module to the AST emitter.
// Measuring the fallback put 105 of the reachable AST-emit cases in this
// "ineligible-fn" bucket — the largest category keeping asm.fern alive.
//
// Three shapes are covered, in increasing order of what they require:
//   - the zero-arg predicates: pure inline arithmetic over existing ops;
//   - abs / sign: inline too, but they need the receiver value twice, so it is
//     stored to a temp local and re-loaded rather than lowered twice;
//   - xs.sum() / xs.product(): HELPER-backed, the shape with the most moving
//     parts (see the comment on those cases below).
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
		// xs.sum() / xs.product() are HELPER-backed rather than inline arithmetic,
		// which needs four things beyond the lowering: an is_fern_helper entry (else
		// calls_only_known bails the module), a need mapping on the call target and a
		// matching emit_ir_runtime_fern_fn gate (else the body is never emitted and the
		// program calls an undefined symbol), and an emit_runtime_globls export (else a
		// library unit dangles at the per-module link).
		{"arr-sum", "function main(): i32 { var xs: i32[] = [1, 2, 3, 4, 5]; return xs.sum(); }", 15},
		{"arr-sum-empty", "function main(): i32 { var xs: i32[] = []; return xs.sum(); }", 0},
		{"arr-sum-mixed-signs", "function main(): i32 { var xs: i32[] = [10, 0 - 3, 0 - 2, 5]; return xs.sum(); }", 10},
		{"arr-product", "function main(): i32 { var xs: i32[] = [2, 3, 5]; return xs.product(); }", 30},
		{"arr-product-empty", "function main(): i32 { var xs: i32[] = []; return xs.product(); }", 1},
		{"arr-product-with-zero", "function main(): i32 { var xs: i32[] = [4, 0, 7]; return xs.product(); }", 0},
		// n.pow(k) is helper-backed AND takes an argument. The callee binds params in
		// reverse push order, so these cases are deliberately ASYMMETRIC: 3.pow(3)
		// passes even with the operands swapped and would hide the bug.
		{"pow-2-5", "function main(): i32 { var n: i32 = 2; return n.pow(5); }", 32},
		{"pow-zero-exponent", "function main(): i32 { var n: i32 = 7; return n.pow(0); }", 1},
		{"pow-unit-exponent", "function main(): i32 { var n: i32 = 5; return n.pow(1); }", 5},
		{"pow-symmetric", "function main(): i32 { var n: i32 = 3; return n.pow(3); }", 27},
		// Negative base, odd exponent: -8, shifted into exit-code range.
		{"pow-negative-base", "function main(): i32 { var n: i32 = 0 - 2; return n.pow(3) + 100; }", 92},
		{"pow-expr-receiver", "function main(): i32 { var b: i32 = 2; var e: i32 = 3; return (b + 1).pow(e); }", 27},
		// n.gcd(m) / n.lcm(m) — helper-backed with an argument, like pow, but with
		// one extra moving part: lcm's own Fern body calls `.gcd()`, so the lowering
		// added here is what that inner call goes through too. Before it existed,
		// that inner call fell to the generic primitive-receiver dispatch and emitted
		// an undefined `__fn_i32__gcd` — which the arm64 per-module UNIT path hit for
		// real, since i32_lcm is in all_runtime_need_roots and its entry unit emits
		// lcm unconditionally (#5940, the blocker on the arm64 over-budget rescue).
		//
		// gcd and lcm are both SYMMETRIC, so no case here can catch a swapped
		// operand order — the ordering is copied from pow, which the asymmetric
		// cases above do pin. What these do pin: the sign handling (both operands
		// are abs()'d), the zero conventions, and that the helper bodies link.
		{"gcd-48-18", "function main(): i32 { var n: i32 = 48; return n.gcd(18); }", 6},
		{"gcd-coprime", "function main(): i32 { var n: i32 = 9; return n.gcd(28); }", 1},
		{"gcd-negative-receiver", "function main(): i32 { var n: i32 = 0 - 48; return n.gcd(18); }", 6},
		{"gcd-negative-arg", "function main(): i32 { var n: i32 = 48; return n.gcd(0 - 18); }", 6},
		// gcd(a, 0) == abs(a) and gcd(0, 0) == 0 — the loop-never-runs arm.
		{"gcd-zero-arg", "function main(): i32 { var n: i32 = 7; return n.gcd(0); }", 7},
		{"gcd-zero-receiver", "function main(): i32 { var n: i32 = 0; return n.gcd(12); }", 12},
		{"gcd-both-zero", "function main(): i32 { var n: i32 = 0; return n.gcd(0); }", 0},
		{"gcd-expr-receiver", "function main(): i32 { var a: i32 = 40; var b: i32 = 8; return (a + b).gcd(18); }", 6},
		{"lcm-4-6", "function main(): i32 { var n: i32 = 4; return n.lcm(6); }", 12},
		{"lcm-coprime", "function main(): i32 { var n: i32 = 5; return n.lcm(7); }", 35},
		{"lcm-negative-receiver", "function main(): i32 { var n: i32 = 0 - 4; return n.lcm(6); }", 12},
		{"lcm-negative-arg", "function main(): i32 { var n: i32 = 4; return n.lcm(0 - 6); }", 12},
		// Either operand zero short-circuits to 0, before the divide by gcd == 0.
		{"lcm-zero-arg", "function main(): i32 { var n: i32 = 9; return n.lcm(0); }", 0},
		{"lcm-zero-receiver", "function main(): i32 { var n: i32 = 0; return n.lcm(9); }", 0},
		{"lcm-multiple", "function main(): i32 { var n: i32 = 3; return n.lcm(9); }", 9},
		{"lcm-expr-receiver", "function main(): i32 { var a: i32 = 2; var b: i32 = 2; return (a + b).lcm(6); }", 12},
		// xs.index_of(x) / xs.contains(x): helper-backed WITH an argument, so both the
		// need-mapping and the reverse operand order apply. The not-found cases shift
		// by +10 because the helper returns -1, which is not an exit code.
		{"index-of-found-mid", "function main(): i32 { var xs: i32[] = [7, 8, 9]; return xs.index_of(9); }", 2},
		{"index-of-found-first", "function main(): i32 { var xs: i32[] = [7, 8, 9]; return xs.index_of(7); }", 0},
		{"index-of-missing", "function main(): i32 { var xs: i32[] = [7, 8, 9]; return xs.index_of(4) + 10; }", 9},
		{"index-of-empty", "function main(): i32 { var xs: i32[] = []; return xs.index_of(1) + 10; }", 9},
		{"contains-true", "function main(): i32 { var xs: i32[] = [7, 8, 9]; if (xs.contains(8)) { return 1; } return 0; }", 1},
		{"contains-false", "function main(): i32 { var xs: i32[] = [7, 8, 9]; if (xs.contains(3)) { return 1; } return 0; }", 0},
		{"contains-single", "function main(): i32 { var xs: i32[] = [5]; if (xs.contains(5)) { return 1; } return 0; }", 1},
		// is_empty desugars to `len() == 0` and re-lowers, so it inherits the `.len()`
		// arm's receiver dispatch and its fresh-string-receiver reclaim. These cases
		// pin that it covers every receiver .len() does — the AST guard spans string,
		// int arrays and string[], so a string-only port would silently leave the
		// array forms on the AST path.
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
		// args_count() desugars to args().len(). The harness runs the inner binary with
		// no extra argv, so the count is 1 (argv[0] alone).
		{"args-count", "function main(): i32 { return args_count(); }", 1},
		{"args-count-compare", "function main(): i32 { if (args_count() >= 1) { return 7; } return 0; }", 7},
		// xs.min() / xs.max() are the first helper-backed builtins with a COMPOSITE
		// return (Option[i32]). The AST emitter open-codes the `len == 0` branch and
		// both 16-byte boxes; the IR path calls a helper that carries the guard and
		// the box in its Fern body. The EMPTY cases are the point of the design — a
		// non-empty-only test set would pass with the guard dropped entirely.
		{"arr-min", "function main(): i32 { var xs: i32[] = [5, 2, 8]; match (xs.min()) { Some(m) => { return m; }, None => { return 99; } } }", 2},
		{"arr-max", "function main(): i32 { var xs: i32[] = [5, 2, 8]; match (xs.max()) { Some(m) => { return m; }, None => { return 99; } } }", 8},
		{"arr-min-empty", "function main(): i32 { var xs: i32[] = []; match (xs.min()) { Some(m) => { return m; }, None => { return 99; } } }", 99},
		{"arr-max-empty", "function main(): i32 { var xs: i32[] = []; match (xs.max()) { Some(m) => { return m; }, None => { return 99; } } }", 99},
		// Single element: min and max must agree. The distinct-extreme cases above
		// are what keep the two from being wired to the same body.
		{"arr-min-single", "function main(): i32 { var xs: i32[] = [7]; match (xs.min()) { Some(m) => { return m; }, None => { return 99; } } }", 7},
		{"arr-max-single", "function main(): i32 { var xs: i32[] = [7]; match (xs.max()) { Some(m) => { return m; }, None => { return 99; } } }", 7},
		{"arr-min-negatives", "function main(): i32 { var xs: i32[] = [3, 0 - 5, 1]; match (xs.min()) { Some(m) => { return m + 10; }, None => { return 99; } } }", 5},
		{"arr-max-negatives", "function main(): i32 { var xs: i32[] = [0 - 9, 0 - 2, 0 - 5]; match (xs.max()) { Some(m) => { return m + 10; }, None => { return 99; } } }", 8},
		// An UNTYPED array literal receiver (inferred i32[]) takes the same guard.
		{"arr-min-untyped", "function main(): i32 { var a = [5, 3, 8, 1]; match (a.min()) { Some(m) => { return m; }, None => { return 99; } } }", 1},
		// The three scrutinee-type resolvers the helper's return type had to be
		// registered with, since opt_ret_fns_of cannot see a runtime helper: a
		// direct `match` scrutinee (above), an unannotated `var` binding matched
		// later, and the try operator.
		{"arr-max-var-binding", "function main(): i32 { var xs: i32[] = [4, 9, 1]; var o = xs.max(); match (o) { Some(m) => { return m; }, None => { return 99; } } }", 9},
		{"arr-min-try", "function f(xs: i32[]): Option[i32] { var m: i32 = xs.min()?; return Some(m + 1); } function main(): i32 { var xs: i32[] = [6, 2]; match (f(xs)) { Some(v) => { return v; }, None => { return 99; } } }", 3},
		{"arr-min-try-empty", "function f(xs: i32[]): Option[i32] { var m: i32 = xs.min()?; return Some(m + 1); } function main(): i32 { var xs: i32[] = []; match (f(xs)) { Some(v) => { return v; }, None => { return 99; } } }", 99},
		// Crossing a call boundary: the helper's Option box is returned by a user
		// function and matched in the caller.
		{"arr-min-through-fn", "function pick(xs: i32[]): Option[i32] { return xs.min(); } function main(): i32 { var xs: i32[] = [9, 3, 5]; match (pick(xs)) { Some(m) => { return m; }, None => { return 99; } } }", 3},
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
