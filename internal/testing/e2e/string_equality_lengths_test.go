package e2e

import "testing"

const stringEqualityLengthsProg = `
import "std/string";

function ref_eq(a: string, b: string): boolean {
    if (a.len() != b.len()) { return false; }
    let i: i32 = 0;
    while (i < a.len()) {
        if (a[i] != b[i]) { return false; }
        i = i + 1;
    }
    return true;
}

function main(): i32 {
    let alpha: string = "abcdefghijklmnopqrstuvwxyz0123456789ABCDE";
    let n: i32 = 0;
    while (n <= 41) {
        let a: string = slice_unchecked(alpha, 0, n).to_owned();
        let b: string = "";
        let i: i32 = 0;
        while (i < n) { b = b + slice_unchecked(alpha, i, i + 1).to_owned(); i = i + 1; }
        if (a.len() != n) { return 1; }
        if (b.len() != n) { return 2; }
        if (!ref_eq(a, b)) { return 3; }
        if (!(a == b)) { return 4; }
        if (a != b) { return 5; }
        // Differ at exactly one position, walked across the whole string so
        // the mismatch lands in the first word, a middle word, and the
        // overlapping tail in turn.
        let p: i32 = 0;
        while (p < n) {
            let c: string = slice_unchecked(alpha, 0, p).to_owned() + "!" + slice_unchecked(alpha, p + 1, n).to_owned();
            if (c.len() != n) { return 6; }
            if (ref_eq(a, c)) { return 7; }
            if (a == c) { return 8; }
            if (!(a != c)) { return 9; }
            p = p + 1;
        }
        // Length mismatch, both operand orders.
        if (n > 0) {
            let shorter: string = slice_unchecked(alpha, 0, n - 1).to_owned();
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
