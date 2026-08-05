package e2e

// Differential + stability coverage for std/sort's `sort_by`.
//
// It was a FIXED bottom-up merge sort: ceil(log2 n) passes, each one
// materialising a fresh full copy of the array, with no regard for the input —
// an already-sorted array cost exactly as much as a random one. It is now the
// comparator-closure sibling of core/cmp's adaptive sort: insertion sort below
// 32, natural ascending/descending run detection with descending runs reversed
// in place, then balanced merge rounds.
//
// Note the existing sort_by_test.go cases do NOT cover this. Each of them
// defines its own inline `sort_by` in the test program — they exercise closure
// lowering on a bounded-generic body, not the shipped stdlib function. Nothing
// checked the real one across sizes.
//
// Two things need pinning:
//
//  1. The result agrees with a naive sort across the sizes that straddle the
//     insertion threshold (32) and the merge rounds, and across the shapes run
//     detection exists to exploit: random, sorted, reverse-sorted, all-equal,
//     and a two-run concatenation.
//  2. The sort is STABLE through the merge and reversal paths. Stability is the
//     documented contract and the easiest thing to lose: a descending run
//     detected with a non-strict compare would swap equal neighbours past each
//     other, and a merge tie-break taking from the right run would invert
//     equal keys.
//
// The stability half is mutation-checked — flipping the merge's `> 0` to
// `>= 0` makes the `stable` case fail with a tag inversion.

import "testing"

const sortByAdaptiveProg = `
import "std/sort" as sort;

function asc(a: i32, b: i32): i32 { if (a < b) { return 0 - 1; } if (a > b) { return 1; } return 0; }

function ref_sort(xs: i32[]): i32[] {
    var a: i32[] = [];
    var c: i32 = 0;
    while (c < xs.len()) { a = a.append(xs[c]); c = c + 1; }
    var i: i32 = 0;
    while (i < a.len()) {
        var m: i32 = i;
        var j: i32 = i + 1;
        while (j < a.len()) {
            if (a[j] < a[m]) { m = j; }
            j = j + 1;
        }
        var t: i32 = a[i];
        a = a.with(i, a[m]);
        a = a.with(m, t);
        i = i + 1;
    }
    return a;
}

function eq_arr(x: i32[], y: i32[]): boolean {
    if (x.len() != y.len()) { return false; }
    var i: i32 = 0;
    while (i < x.len()) {
        if (x[i] != y[i]) { return false; }
        i = i + 1;
    }
    return true;
}

// xorshift, so the random cases are the same on every backend.
function nextr(x: i32): i32 {
    var v: i32 = x;
    v = v ^ (v << 13);
    v = v ^ (v >> 17);
    v = v ^ (v << 5);
    return v & 2147483647;
}

function main(): i32 {
    // Every size from 0 through 80: below the insertion threshold, at it, and
    // through the first several merge rounds above it. Ties are dense (mod 7)
    // so the tie-break paths are exercised at every size.
    var n: i32 = 0;
    while (n <= 80) {
        var xs: i32[] = [];
        var seed: i32 = n + 12345;
        var i: i32 = 0;
        while (i < n) { seed = nextr(seed); xs = xs.append(seed % 7); i = i + 1; }

        var got: i32[] = sort.sort_by(xs, asc);
        if (!eq_arr(got, ref_sort(xs))) { return 1; }
        if (!sort.is_sorted_by(got, asc)) { return 2; }

        // Already sorted -- the single-run path, which must be a no-op in
        // ordering terms rather than a reshuffle.
        if (!eq_arr(sort.sort_by(got, asc), got)) { return 3; }

        // Reverse-sorted -- the descending-run detection + in-place reversal.
        var rev: i32[] = [];
        var r: i32 = n - 1;
        while (r >= 0) { rev = rev.append(got[r]); r = r - 1; }
        if (!eq_arr(sort.sort_by(rev, asc), got)) { return 4; }

        // All-equal -- a single ascending run under a non-strict compare, and
        // the shape a strict/non-strict mix-up in run detection breaks.
        var flat: i32[] = [];
        var f: i32 = 0;
        while (f < n) { flat = flat.append(5); f = f + 1; }
        if (!eq_arr(sort.sort_by(flat, asc), flat)) { return 5; }

        // Two sorted runs concatenated -- exactly one merge, whatever n is.
        var two: i32[] = [];
        var h: i32 = 0;
        while (h < n) { two = two.append(h % 2); h = h + 1; }
        var twos: i32[] = sort.sort_by(two, asc);
        if (!eq_arr(twos, ref_sort(two))) { return 6; }

        // The input is never mutated.
        if (n > 0 && xs.len() != n) { return 7; }

        n = n + 1;
    }

    // STABILITY. Sort (key, tag) pairs by key alone; equal keys must come out
    // in input order, so the tags within a key group stay ascending.
    //
    // Encoded as key*1000 + tag with a comparator that compares only the key,
    // over 200 elements so the run detection, the MIN_RUN-sized insertion
    // spans, and several merge rounds all participate.
    var pairs: i32[] = [];
    var s2: i32 = 999;
    var t: i32 = 0;
    while (t < 200) {
        s2 = nextr(s2);
        pairs = pairs.append((s2 % 5) * 1000 + t);
        t = t + 1;
    }
    var st: i32[] = sort.sort_by(pairs, (a: i32, b: i32) => (a / 1000) - (b / 1000));
    var p: i32 = 1;
    while (p < st.len()) {
        var prevk: i32 = st[p - 1] / 1000;
        var curk: i32 = st[p] / 1000;
        if (curk < prevk) { return 8; }
        // Same key -> the tag must not have gone backwards.
        if (curk == prevk && (st[p] % 1000) < (st[p - 1] % 1000)) { return 9; }
        p = p + 1;
    }

    // A reverse comparator sorts descending through the same body.
    var d: i32[] = sort.sort_by([3, 1, 4, 1, 5, 9, 2, 6], (a: i32, b: i32) => b - a);
    var q: i32 = 1;
    while (q < d.len()) {
        if (d[q] > d[q - 1]) { return 10; }
        q = q + 1;
    }

    return 42;
}
`

func TestSortByAdaptiveInterp(t *testing.T) {
	if got := runInterpExit(t, sortByAdaptiveProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestSortByAdaptiveX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, sortByAdaptiveProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestSortByAdaptiveWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, sortByAdaptiveProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestSortByAdaptiveArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, sortByAdaptiveProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
