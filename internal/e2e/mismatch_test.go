package e2e

import "testing"

// #8791: `__mismatch(a, ao, b, bo, n)` is the offset of the first byte where
// a[ao..ao+n) and b[bo..bo+n) differ, or n when they are equal.
//
// The program is its own oracle. It sweeps every length 0..100 against every
// difference position, in both operand orders, and compares each answer with a
// reference loop written in Fern — so a wrong lowering fails on the exact
// (length, position) that broke rather than on one hand-picked case. The sweep
// reaches all five bands the native kernels have: the scalar remainder, the
// 4- and 8-byte overlapping windows, the 16-byte vector loop and (on x86-64)
// the 32-byte AVX2 loop.
//
// The windows are the half worth pinning hardest. A line-oriented utility
// compares SHORT ranges, and the first draft of this kernel measured 12 ns on
// the issue's 13-byte case precisely because 13 never reaches a 16-byte loop;
// the overlapping leading/trailing windows took it to 6 ns. They also carry
// the subtlest claim in the kernel — that a difference the TRAILING window
// reports is provably the first one, because the leading window already proved
// its own range equal — and lengths 8..15 are where a mistake in that shows.
const mismatchSweepSrc = `function ref(a: string, ao: i32, b: string, bo: i32, n: i32): i32 {
    var i: i32 = 0;
    while (i < n) {
        if (a[ao + i] != b[bo + i]) { return i; }
        i = i + 1;
    }
    return n;
}

function rep(c: string, n: i32): string {
    var s: string = "";
    var i: i32 = 0;
    while (i < n) { s = s + c; i = i + 1; }
    return s;
}

function main(): i32 {
    var n: i32 = 0;
    while (n <= 100) {
        var a: string = rep("a", n);
        if (__mismatch(a, 0, a, 0, n) != n) { return 1; }
        var d: i32 = 0;
        while (d < n) {
            var b: string = rep("a", d) + "b" + rep("a", n - d - 1);
            if (__mismatch(a, 0, b, 0, n) != ref(a, 0, b, 0, n)) { return 2; }
            if (__mismatch(b, 0, a, 0, n) != ref(b, 0, a, 0, n)) { return 3; }
            // Both ranges offset into a longer buffer, so a lowering that
            // ignores an operand's offset is caught rather than cancelling.
            var pa: string = "zz" + a;
            var pb: string = "qqq" + b;
            if (__mismatch(pa, 2, pb, 3, n) != ref(pa, 2, pb, 3, n)) { return 4; }
            d = d + 1;
        }
        n = n + 1;
    }
    // The clamps are the contract, not defensive tidying: n is reduced to what
    // both ranges actually hold, so a caller asking for more than exists gets
    // the shorter answer and its equality test against n correctly fails.
    if (__mismatch("ab", 0, "abcdef", 0, 6) != 2) { return 5; }
    if (__mismatch("abc", 0 - 4, "abc", 0, 3) != 3) { return 6; }
    if (__mismatch("abc", 99, "abc", 0, 3) != 0) { return 7; }
    return 42;
}`

// TestX86_64Mismatch drives the sweep through the AVX2 kernel.
func TestX86_64Mismatch(t *testing.T) {
	if _, got := compileAndRunX86_64(t, mismatchSweepSrc); got != 42 {
		t.Errorf("__mismatch sweep on x86-64: exit %d, want 42 (#8791)", got)
	}
}

// TestArm64Mismatch drives it through the NEON kernel.
func TestArm64Mismatch(t *testing.T) {
	if _, got := compileAndRunArm64(t, mismatchSweepSrc); got != 42 {
		t.Errorf("__mismatch sweep on arm64: exit %d, want 42 (#8791)", got)
	}
}

// TestWASMMismatch drives it through the wasm lowering.
func TestWASMMismatch(t *testing.T) {
	if got := runWasm(t, mismatchSweepSrc); got != 42 {
		t.Errorf("__mismatch sweep on wasm: got %d, want 42 (#8791)", got)
	}
}
