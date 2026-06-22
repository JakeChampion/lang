package e2e

import (
	"testing"
)

// TestSelfHostIRF64ArrayEligible locks in that f64 arrays now lower through the
// IR. f64 array literals allocate an 8-byte-stride buffer (arr_make width 64);
// a[i] reads/writes an 8-byte f64 (arr_get/arr_set width 64 → f64.load/store on
// wasm; the register backends already used 8-byte slots). The slot binding
// tracks f64-array-ness (local_is_f64arr) so a[i] types as f64. It probes
// asm_ir.all_eligible (via the unified driver's `elig` mode) and bit-packs the
// per-case results:
//
//	(a) f64 array literal + indexed read  → ELIGIBLE (1)
//	(b) f64 array indexed write a[i] = v  → ELIGIBLE (1)
//	(c) f64[] param + indexed read        → ELIGIBLE (1)
//	(d) f64[]-RETURNING free function     → ELIGIBLE (1)  [f64arr_ret_fns lets
//	                                         the call site recover the width]
//
// Expected: a*8 + b*4 + c*2 + d == 8 + 4 + 2 + 1 == 15.
func TestSelfHostIRF64ArrayEligible(t *testing.T) {
	progs := []string{
		"function main(): i32 { var a: f64[] = [1.5, 2.5]; var x: f64 = a[0] + a[1]; if (x > 3.0) { return 7; } return 0; }",
		"function main(): i32 { var a: f64[] = [1.0, 2.0]; a[1] = 5.5; var x: f64 = a[0] + a[1]; if (x > 6.0) { return 8; } return 0; }",
		"function sum(a: f64[]): f64 { return a[0] + a[1]; } function main(): i32 { var arr: f64[] = [2.5, 4.0]; var r: f64 = sum(arr); if (r > 6.0) { return 5; } return 0; }",
		"function mk(): f64[] { return [1.5, 2.5]; } function main(): i32 { var a: f64[] = mk(); if (a[0] > 1.0) { return 4; } return 0; }",
	}
	if got := eligBits(t, progs, []int{8, 4, 2, 1}); got != 15 {
		t.Errorf("f64-array IR eligibility = %d, want 15 (literal/write/param/return all eligible)", got)
	}
}
