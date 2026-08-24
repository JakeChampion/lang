package e2e

// String EQUALITY across every length class, checked against a byte-wise
// oracle in the same program.
//
// `__fern_strcmp` compares 8 bytes at a time on x86-64 and 4 at a time on
// arm64, and each ends with a final load anchored at the END of the operands
// that deliberately overlaps what the loop already read. That tail, and the
// sub-word classes below it, are the shapes a length table is least likely to
// cover by accident: the boundaries are 1/2/3, 4, 8, and the first length past
// a whole number of words. Every length from 0 to 41 is compared here — equal,
// differing at each individual position, and against its own prefix — so a
// mis-sized tail shows up as a disagreement with the oracle rather than as a
// rare wrong answer somewhere downstream.
//
// The equal operands are built two different ways (a slice of the alphabet vs
// a byte-at-a-time concatenation) so the two sides are never the same pointer
// and the comparison cannot pass on the identity fast path. Lengths up to 7
// additionally cross the inline/SSO seam, where one operand is packed in a
// register and has to be spilled to reach a byte pointer.

import "testing"

const stringEqualityLengthsProg = `
import "std/string";

function ref_eq(a: string, b: string): boolean {
    if (a.len() != b.len()) { return false; }
    var i: i32 = 0;
    while (i < a.len()) {
        if (a[i] != b[i]) { return false; }
        i = i + 1;
    }
    return true;
}

function main(): i32 {
    var alpha: string = "abcdefghijklmnopqrstuvwxyz0123456789ABCDE";
    var n: i32 = 0;
    while (n <= 41) {
        var a: string = slice_unchecked(alpha, 0, n).to_owned();
        var b: string = "";
        var i: i32 = 0;
        while (i < n) { b = b + slice_unchecked(alpha, i, i + 1).to_owned(); i = i + 1; }
        if (a.len() != n) { return 1; }
        if (b.len() != n) { return 2; }
        if (!ref_eq(a, b)) { return 3; }
        if (!(a == b)) { return 4; }
        if (a != b) { return 5; }
        // Differ at exactly one position, walked across the whole string so
        // the mismatch lands in the first word, a middle word, and the
        // overlapping tail in turn.
        var p: i32 = 0;
        while (p < n) {
            var c: string = slice_unchecked(alpha, 0, p).to_owned() + "!" + slice_unchecked(alpha, p + 1, n).to_owned();
            if (c.len() != n) { return 6; }
            if (ref_eq(a, c)) { return 7; }
            if (a == c) { return 8; }
            if (!(a != c)) { return 9; }
            p = p + 1;
        }
        // Length mismatch, both operand orders.
        if (n > 0) {
            var shorter: string = slice_unchecked(alpha, 0, n - 1).to_owned();
            if (a == shorter) { return 10; }
            if (shorter == a) { return 11; }
        }
        n = n + 1;
    }
    return 42;
}
`

func TestStringEqualityLengthsInterp(t *testing.T) {
	if got := runInterpExit(t, stringEqualityLengthsProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestStringEqualityLengthsX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, stringEqualityLengthsProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestStringEqualityLengthsArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, stringEqualityLengthsProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}

func TestStringEqualityLengthsWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, stringEqualityLengthsProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}
