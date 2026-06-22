package e2e

import (
	"testing"
)

// TestSelfHostIRF64Eligible locks in the IR-coverage widening for f64: programs
// using f64 locals/arithmetic/comparison, i32<->f64 casts, AND f64 in a
// FREE-function signature (param/return) are eligible. It probes
// asm_ir.all_eligible (the unified driver's `elig` mode) and bit-packs the
// per-case results. Case (d) — an f64 METHOD signature — is now ALSO eligible
// (f64 methods lower through the IR; f64_ret_fns_of records methods keyed
// "<Type>.<method>"). i64 methods stay deferred until i64 struct fields land. So
// the want is 8+4+2+1 = 15 (bits 8/4/2/1).
func TestSelfHostIRF64Eligible(t *testing.T) {
	progs := []string{
		"function main(): i32 { var x: f64 = 1.5; var y: f64 = 2.25; var z: f64 = x + y; if (z > 3.0) { return 7; } return 0; }",
		"function main(): i32 { var n: i32 = 10; var x: f64 = n as f64; var y: f64 = x / 4.0; return y as i32; }",
		"function scale(x: f64, k: f64): f64 { return x * k; } function main(): i32 { var r: f64 = scale(3.0, 2.5); if (r > 7.0) { return 7; } return 0; }",
		"struct B { } function (b: B) half(): f64 { return 0.5; } function main(): i32 { return 0; }",
	}
	if got := eligBits(t, progs, []int{8, 4, 2, 1}); got != 15 {
		t.Errorf("f64 IR eligibility = %d, want 15 (f64 locals/casts/free-fn + f64 METHOD all eligible: bits 8/4/2/1)", got)
	}
}
