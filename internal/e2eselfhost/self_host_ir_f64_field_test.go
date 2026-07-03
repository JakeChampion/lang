package e2eselfhost

import (
	"testing"
)

// TestSelfHostIRF64FieldEligible locks in the IR-coverage widening for f64
// struct fields: a struct with an f64 field is now leaf-safe and lowers through
// the IR (register backends already used 8-byte field slots; wasm widened its
// field layout to 8-byte stride with per-field f64/i32 load-store). It probes
// asm_ir.all_eligible (the unified driver's `elig` mode) and bit-packs per-case
// results: (a) read, (b) mixed i32/f64 fields, (c) field write — all eligible →
// 4+2+1 = 7.
func TestSelfHostIRF64FieldEligible(t *testing.T) {
	progs := []string{
		"struct P { x: f64, n: i32 } function main(): i32 { var p = P { x: 3.5, n: 2 }; var y: f64 = p.x + 1.0; if (y > 4.0) { return p.n; } return 0; }",
		"struct V { a: i32, d: f64, b: i32 } function main(): i32 { var v = V { a: 1, d: 2.5, b: 3 }; var s: f64 = v.d * 2.0; if (s > 4.0) { return v.a + v.b; } return 0; }",
		"struct P { x: f64, n: i32 } function main(): i32 { var p = P { x: 1.0, n: 4 }; p.x = 5.5; if (p.x > 5.0) { return p.n; } return 0; }",
	}
	if got := eligBits(t, progs, []int{4, 2, 1}); got != 7 {
		t.Errorf("f64-struct-field IR eligibility = %d, want 7 (read/mixed/write all eligible)", got)
	}
}
