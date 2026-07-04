package e2eselfhost

import (
	"testing"
)

// TestSelfHostIRTupleReturnEligible LOCKS IN the IR-coverage widening for
// tuple-returning functions. The differential gate (TestSelfHostAsmIRPath) can't
// prove a program actually takes the IR path, because irlower is built to emit
// asm byte-identical to the AST backend for the overlapping subset — so AST==IR
// holds whether or not the IR path was used. This test instead asserts
// asm_ir.all_eligible() directly (via the unified driver's `elig` mode) on
// tuple-returning programs and encodes the per-case results in the exit code.
// Before tuple-returning functions were lowered, lower_func bailed every `(...)`
// return, so all cases would be ineligible (exit 0).
func TestSelfHostIRTupleReturnEligible(t *testing.T) {
	progs := []string{
		"function three(): (i32, i32, i32) { return (4, 5, 6); } function main(): i32 { var (a, b, c) = three(); return a + b + c; }",
		"function pair(): (string, i32) { return (\"hi\", 5); } function main(): i32 { var (s, n) = pair(); return s.len() + n; }",
		"function trip(): (i32, i32, i32) { return (1, 2, 3); } function main(): i32 { var t = trip(); return t.0 + t.1 + t.2; }",
		"struct P { x: i32, y: i32 } function mk(): (P, i32) { return (P { x: 3, y: 4 }, 9); } function main(): i32 { var (p, n) = mk(); return p.x + p.y + n; }",
		"function mk(): (i64, f64) { return (20000000000, 2.5); } function main(): i32 { var (a, x) = mk(); if (a > 15000000000) { return 1; } if (x > 2.0) { return 2; } return 0; }",
	}
	if got := eligBits(t, progs, []int{16, 8, 4, 2, 1}); got != 31 {
		t.Errorf("tuple-returning IR eligibility = %d, want 31 (each bit is one case's all_eligible)", got)
	}
}
