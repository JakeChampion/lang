package e2e

import "testing"

const mismatchSweepSrc = `function ref(a: string, ao: i32, b: string, bo: i32, n: i32): i32 {
    let i: i32 = 0;
    while (i < n) {
        if (a[ao + i] != b[bo + i]) { return i; }
        i = i + 1;
    }
    return n;
}

function rep(c: string, n: i32): string {
    let s: string = "";
    let i: i32 = 0;
    while (i < n) { s = s + c; i = i + 1; }
    return s;
}

function main(): i32 {
    let n: i32 = 0;
    while (n <= 100) {
        let a: string = rep("a", n);
        if (__mismatch(a, 0, a, 0, n) != n) { return 1; }
        let d: i32 = 0;
        while (d < n) {
            let b: string = rep("a", d) + "b" + rep("a", n - d - 1);
            if (__mismatch(a, 0, b, 0, n) != ref(a, 0, b, 0, n)) { return 2; }
            if (__mismatch(b, 0, a, 0, n) != ref(b, 0, a, 0, n)) { return 3; }
            // Both ranges offset into a longer buffer, so a lowering that
            // ignores an operand's offset is caught rather than cancelling.
            let pa: string = "zz" + a;
            let pb: string = "qqq" + b;
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

func TestX86_64Mismatch(t *testing.T) {
	if _, got := compileAndRunX86_64(t, mismatchSweepSrc); got != 42 {
		t.Errorf("__mismatch sweep on x86-64: exit %d, want 42 (#8791)", got)
	}
}

func TestArm64Mismatch(t *testing.T) {
	if _, got := compileAndRunArm64(t, mismatchSweepSrc); got != 42 {
		t.Errorf("__mismatch sweep on arm64: exit %d, want 42 (#8791)", got)
	}
}

func TestWASMMismatch(t *testing.T) {
	if got := runWasm(t, mismatchSweepSrc); got != 42 {
		t.Errorf("__mismatch sweep on wasm: got %d, want 42 (#8791)", got)
	}
}
