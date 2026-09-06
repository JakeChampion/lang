package x86_64ssa_test

import "testing"

// `f64_bits` of a compile-time constant folds to a ConstInt in the SSA
// optimiser, and that constant has to keep all 64 bits: the lifter stamps
// Width 64 on the reinterpret so MovImm does not sign-extend the low half.
// The folded constant only becomes a MovImm when something the folder cannot
// see through consumes it — here an argument to a recursive, so uninlinable,
// function. The answer is bits 62:60 of a signalling NaN's pattern, which the
// truncated constant reads as 0, so a wrong lift cannot pass by coincidence.
func TestProgramReinterpretConstantKeepsAllBits(t *testing.T) {
	src := `function hi(b: i64, n: i32): i32 {
    if (n > 0) { return hi(b, n - 1); }
    return ((b >> 60) & 7) as i32;
}
function main(): i32 {
    var s: f64 = f64_from_bits(9218868437227405313);
    return hi(f64_bits(s), 1);
}`
	for _, n := range []int{1, 8} {
		programMatchesInterp(t, src, n)
	}
}
