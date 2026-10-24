package e2ecompiler

import (
	"testing"
)

func TestSelfHostIRStructReturnEligible(t *testing.T) {
	progs := []string{
		"struct P { x: i32, y: i32 } function mk(): P { return P { x: 3, y: 4 }; } function main(): i32 { let p = mk(); return p.x * 10 + p.y; }",
		"struct P { x: i32, y: i32 } function mk(a: i32): P { return P { x: a, y: a + 1 }; } function main(): i32 { return mk(7).x + mk(7).y; }",
	}
	if got := eligBits(t, progs, []int{10, 1}); got != 11 {
		t.Errorf("struct-returning IR eligibility = %d, want 11 (each digit is one case's verdict)", got)
	}
}
