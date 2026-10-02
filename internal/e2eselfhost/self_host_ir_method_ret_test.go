package e2eselfhost

import (
	"testing"
)

// TestSelfHostIRMethodReturnEligible locks in the IR-coverage widening for
// methods returning structs / tuples. The struct/tuple return registries used
// to record only free functions, so `let p = obj.mk()` for a struct/tuple-
// returning method bailed (the call site couldn't type p, and a
// struct method-call result's `p.x` bails). It asks the path probe (semlower.verdict, via
// eligBits) on method-returning programs and encodes the
// per-case results in the exit code (a*10 + b == 11 when both are eligible).
func TestSelfHostIRMethodReturnEligible(t *testing.T) {
	progs := []string{
		"struct P { x: i32, y: i32 } struct B { } function (b: B) mk(): P { return P { x: 3, y: 4 }; } function main(): i32 { let b = B { }; let p = b.mk(); return p.x * 10 + p.y; }",
		"struct B { } function (b: B) pair(): (string, i32) { return (\"hi\", 5); } function main(): i32 { let b = B { }; let (s, n) = b.pair(); return s.len() + n; }",
	}
	if got := eligBits(t, progs, []int{10, 1}); got != 11 {
		t.Errorf("method-returning IR eligibility = %d, want 11 (each digit is one case's verdict)", got)
	}
}
