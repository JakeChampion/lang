package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// An if-EXPRESSION desugars to an IIFE — `(function(): RT { if (c) { return A; }
// else { return B; } })()` — and `if_expr_rt` picks that `RT` from a branch's
// shape. It called every non-float number literal "i32", so a 64-bit branch
// declared a 32-bit return over a 64-bit body and the module fell off the IR
// path entirely: `FERN_STRICT_IR: __lam_0`, on a program the interpreter runs
// fine (#6178, fernsmith seed 502).
//
// Two things were wrong and both are needed. The literal classifier now reads
// the suffix first and magnitude second, the same rule `mono_infer` already
// applied. And `parse_if_chain` combines the two branches' tags through
// `wider_rt` instead of taking the then-branch's and discarding the else's —
// without that, width that appears only in the else branch is still lost.
//
// Mutation-measured, by reverting each half and rebuilding the compiler. SIX of
// the eight cases fail without both halves, and they fail by REFUSING TO
// COMPILE, not by answering wrongly — including `small_shift_unaffected_by_masking`, which
// I had assumed was a direction-pin: the declared width is wrong whatever the
// shift amount, so the module leaves the IR path either way. Reverting only
// `wider_rt` and keeping the classifier narrows it to exactly one case,
// `wide_in_else_branch_only`. The two that pass either way are
// `narrow_stays_i32` (which is what the fix must not widen) and
// `plain_suffixed_shift` (no if-expression at all). Oracle: the interpreter.
var ifExprWidthCases = []struct {
	name string
	expr string
}{
	// Seed 502's shape reduced: both branches suffixed, shifted by 32. Declared
	// i32, the whole module refused to compile.
	{"suffixed_branches", "(((if (false) { 764i64 } else { 151i64 }) >> 32i64) as i32)"},
	{"suffixed_branches_u64", "(((if (true) { 764u64 } else { 151u64 }) >> 32u64) as i32)"},
	// Wide by MAGNITUDE rather than by suffix — the other half of the rule.
	{"wide_by_magnitude", "(((if (false) { 7640000000000i64 } else { 1510000000000i64 }) >> 32i64) as i32)"},
	// Width in the ELSE branch only. `if_expr_rt` is asked about both branches,
	// but parse_if_chain used to keep only the then-branch's answer, so this
	// stayed i32 no matter what the classifier said. This is the wider_rt case.
	{"wide_in_else_branch_only", "(((if (false) { 5 } else { 5000000000 }) >> 32i64) as i32)"},
	{"wide_in_then_branch_only", "(((if (true) { 5000000000 } else { 5 }) >> 32i64) as i32)"},

	// A shift below 32 cannot be affected by the shift-count masking, but it is
	// affected by this bug all the same: the IIFE's declared width contradicts
	// its body whatever the count is, so the module bails. Kept as its own case
	// because it is the one that shows the defect is about the DECLARATION, not
	// about the shift.
	{"small_shift_unaffected_by_masking", "(((if (true) { 764i64 } else { 151i64 }) >> 4i64) as i32)"},

	// A narrow if-expression must stay i32 — the fix must not widen everything.
	{"narrow_stays_i32", "((if (true) { 7 } else { 9 }) + 1)"},
	// A plain suffixed shift with no if-expression, to keep the neighbouring
	// literal-width rule (#6228) in frame.
	{"plain_suffixed_shift", "((807i64 >> 32i64) as i32)"},
}

// TestSelfHostIfExprWidthIR_X86_64 pins the declared return type of the IIFE an
// if-expression desugars to, for branches that are 64-bit (#6178), through the
// self-host IR path. A regression here is a REFUSAL to compile rather than a
// wrong answer, so the route check below is as load-bearing as the exit code.
func TestSelfHostIfExprWidthIR_X86_64(t *testing.T) {
	dir, mmc, stdlibRoot, gcc, interpBin := annotateF64ProjDir(t)
	_, runner := x86_64Tooling(t)

	for _, tc := range ifExprWidthCases {
		t.Run(tc.name, func(t *testing.T) {
			src := "function main(): i32 { return " + tc.expr + "; }\n"
			want := interpExit(t, interpBin, src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			route, derr := exec.Command(mmc, mainPath, stdlibRoot, "-decide").Output()
			if derr != nil {
				t.Fatalf("route decide: %v", derr)
			}
			if got := strings.TrimSpace(string(route)); got != "ir" {
				t.Fatalf("%s routed %q, want \"ir\" — the if-expression's IIFE declared a "+
					"width its body contradicts, so the module left the IR path", tc.name, got)
			}
			asm, cerr := exec.Command(mmc, mainPath, stdlibRoot).Output()
			if cerr != nil {
				t.Fatalf("loader compile: %v", cerr)
			}
			if len(asm) == 0 {
				t.Fatal("loader emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "ifexprw_"+tc.name, string(asm))
			argv := append(append([]string{}, runner...), progBin)
			cmd := exec.Command(argv[0], argv[1:]...)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle) — `%s`", tc.name, code, want, tc.expr)
			}
		})
	}
}
