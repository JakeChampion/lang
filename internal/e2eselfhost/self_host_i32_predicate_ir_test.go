package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// i32PredicateIRCases exercise the builtin i32 PREDICATE methods —
// `n.is_zero/is_positive/is_negative/is_even/is_odd()` — on the IR path. The AST
// emitter (asm.fern) inlines each as a couple of arithmetic ops, and the IR path
// (irlower) previously had no lowering for them, so any program using one bailed
// the whole module to the AST emitter (an `ineligible-fn` blocker for retiring
// asm.fern, #3457). irlower now desugars each to the equivalent boolean
// ExprBinary (is_zero → n==0, is_positive → n>0, is_negative → n<0, is_odd →
// (n&1)==1, is_even → (n&1)==0) and recurses through the existing integer-op
// lowering — no runtime helper, so every backend gets them.
//
// These methods are a self-host-only surface (the NATIVE checker rejects
// `n.is_even()` with E043), so there is no native-interp oracle; the expected
// exit codes are pinned directly. The routing is proven by asm_pathprobe_run's
// "ir" verdict — the exit code alone would still pass if the module fell back to
// AST, which handles these methods too.
var i32PredicateIRCases = []struct {
	name     string
	main     string
	expected int
}{
	{"is-even-true", `function main(): i32 { if ((6).is_even()) { return 1; } return 0; }`, 1},
	{"is-even-false", `function main(): i32 { if ((7).is_even()) { return 1; } return 0; }`, 0},
	{"is-odd-true", `function main(): i32 { if ((7).is_odd()) { return 1; } return 0; }`, 1},
	{"is-odd-false", `function main(): i32 { if ((8).is_odd()) { return 1; } return 0; }`, 0},
	{"is-zero-true", `function main(): i32 { if ((0).is_zero()) { return 1; } return 0; }`, 1},
	{"is-zero-false", `function main(): i32 { if ((5).is_zero()) { return 1; } return 0; }`, 0},
	{"is-positive-true", `function main(): i32 { if ((5).is_positive()) { return 1; } return 0; }`, 1},
	{"is-positive-zero", `function main(): i32 { if ((0).is_positive()) { return 1; } return 0; }`, 0},
	{"is-negative-true", `function main(): i32 { if ((0 - 3).is_negative()) { return 1; } return 0; }`, 1},
	// A variable (not literal) i32 receiver, combining several predicates + `!`.
	// n=10: is_even → +1, is_positive → +2, !is_odd → +4  ⇒ 7.
	{"var-receiver-combined", `function main(): i32 {
  var n: i32 = 10; var r: i32 = 0;
  if (n.is_even()) { r = r + 1; }
  if (n.is_positive()) { r = r + 2; }
  if (!n.is_odd()) { r = r + 4; }
  return r;
}`, 7},
}

// TestSelfHostI32PredicateIRX86_64 builds asm_run + asm_pathprobe_run and asserts
// each case (1) routes the "ir" path and (2) exits with its pinned value.
func TestSelfHostI32PredicateIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"asm_run.fern", "asm_pathprobe_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range i32PredicateIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\" (irlower is not lowering the predicate)", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "i32pred_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}
