package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// annotateWidthCases extend the typed-IR annotation (#5531) to irlower's
// infer_expr_width: a checker-typed i64/u64-returning call is a 64-bit value.
// The annotate pass stamps ExprCall.ty ("i64"/"u64"); infer_expr_width's
// ExprCall arm reads it as a positive fast-path (→ 64) instead of re-deriving
// via is_i64_ret_fn. Each case uses the CALL RESULT in a 64-bit op whose answer
// a truncated-32 width would get wrong (the value exceeds 2^32), so the exit
// code is a direct oracle on the width decision.
var annotateWidthCases = []struct {
	name string
	src  string
}{
	// i64 call result mod: 5000000000 % 7 == 2 at 64-bit; a 32-bit truncation
	// (705032704) % 7 == 5 — so the exit code proves the call typed 64-bit.
	{"i64_call_mod", `function big(): i64 { return 5000000000; }
function main(): i32 { return (big() % 7) as i32; }`}, // 2
	// two i64 calls summed then divided — needs 64-bit through the call results.
	{"i64_call_sum_div", `function big(): i64 { return 5000000000; }
function main(): i32 { return ((big() + big()) / 1000000000) as i32; }`}, // 10
	// u64 call result mod (bit-63 set value): unsigned 64-bit.
	{"u64_call_mod", `function bu(): u64 { return 18000000000000000000 as u64; }
function main(): i32 { return (bu() % 1000000007 as u64) as i32; }`}, // 18000000000000000000 % 1000000007
	// A u64-SUFFIXED literal in a desugared if-expression branch. check_expr
	// typed every integer literal i32 whatever its suffix, so check_call_expr
	// stamped the IIFE i32 and expr_is_u64 — which trusts that tag ahead of its
	// structural walk — answered false: the chained `>>` took the SIGNED shift
	// and 0xFFFF… >> 60 came back -1 instead of 15. Only the direct-operand
	// position was exposed; through a local the slot carries the width.
	{"u64_suffix_if_expr_shift", `function shifted(c: boolean, p: u64): u64 { return (if (c) { 18446744073709551615u64 } else { p }) >> 60u64; }
function main(): i32 { return shifted(true, 1u64) as i32; }`}, // 15
	// The match-EXPRESSION spelling desugars to the same IIFE, and both branches
	// being literals is what makes check_call_expr's branch types agree — the
	// shape that stamped a confident, wrong i32.
	{"u64_suffix_match_expr_shift", `function d(c: i32): u64 { return (match (c) { 1 => 18446744073709551615u64, _ => 0u64 }) >> 60u64; }
function main(): i32 { return d(1) as i32; }`}, // 15
}

// TestSelfHostAnnotateWidthIR_X86_64 pins the checker-stamped result type feeding
// irlower's infer_expr_width through the IR path (#5531).
func TestSelfHostAnnotateWidthIR_X86_64(t *testing.T) {
	dir, mmc, stdlibRoot, gcc, interpBin := annotateF64ProjDir(t)

	for _, tc := range annotateWidthCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			route, derr := exec.Command(mmc, mainPath, stdlibRoot, "-decide").Output()
			if derr != nil {
				t.Fatalf("route decide: %v", derr)
			}
			if got := strings.TrimSpace(string(route)); got != "ir" {
				t.Fatalf("%s routed %q, want \"ir\" (case no longer exercises the IR annotate path)", tc.name, got)
			}
			asm, cerr := exec.Command(mmc, mainPath, stdlibRoot).Output()
			if cerr != nil {
				t.Fatalf("loader compile: %v", cerr)
			}
			if len(asm) == 0 {
				t.Fatal("loader emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "annwidth_"+tc.name, string(asm))
			cmd := exec.Command(progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s (IR annotate path) exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
