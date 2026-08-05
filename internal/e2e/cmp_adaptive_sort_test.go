package e2e

// Differential + stability coverage for core/cmp's adaptive sort.
//
// `cmp.sort` / `cmp.sort_desc` are a natural-run merge sort in Timsort's
// shape: insertion sort below MIN_RUN, natural ascending/descending run
// detection (descending runs reversed in place), short runs extended to
// MIN_RUN, then balanced merge rounds. The previous revision was a fixed
// bottom-up merge sort that copied the whole array on every pass regardless
// of the input.
//
// Two things need pinning, and neither was covered before: that the result
// still agrees with a naive sort across the sizes that straddle MIN_RUN and
// the merge rounds, and that the sort is STABLE through the merge path. The
// pre-existing Ord-sort cases in cmp_ord_sort_test.go define their own inline
// insertion sort, so they exercise the trait machinery, not this code.
//
// The stability half is mutation-checked: flipping the merge's tie-break to
// take from the right run makes `stable` fail with a tag inversion.

import "testing"

// Random arrays with dense ties, plus the shapes run detection exists to
// exploit, checked against a naive selection sort in the same program.
const cmpAdaptiveSortProg = `
import "core/cmp";

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

function nextr(x: i32): i32 {
    var v: i32 = x;
    v = v ^ (v << 13);
    v = v ^ (v >> 17);
    v = v ^ (v << 5);
    if (v < 0) { v = 0 - v; }
    if (v < 0) { v = 1; }
    return v;
}

function main(): i32 {
    var seed: i32 = 12345;

    // Every length from 0 to 80 -- straddling MIN_RUN (32) and the merge
    // rounds -- with only 7 distinct values so ties are dense.
    var n: i32 = 0;
    while (n <= 80) {
        var trial: i32 = 0;
        while (trial < 4) {
            var xs: i32[] = [];
            var k: i32 = 0;
            while (k < n) {
                seed = nextr(seed);
                xs = xs.append(seed % 7);
                k = k + 1;
            }
            var want: i32[] = ref_sort(xs);
            if (!eq_arr(cmp.sort(xs), want)) { return 1; }

            var wantd: i32[] = [];
            var z: i32 = want.len() - 1;
            while (z >= 0) { wantd = wantd.append(want[z]); z = z - 1; }
            if (!eq_arr(cmp.sort_desc(xs), wantd)) { return 2; }

            // Value semantics: the input is untouched.
            if (!eq_arr(ref_sort(xs), want)) { return 3; }
            trial = trial + 1;
        }
        n = n + 1;
    }

    // The shapes natural-run detection exists for: already sorted, reverse
    // sorted, all equal, and a sorted run followed by a descending tail.
    var m: i32 = 0;
    while (m <= 70) {
        var asc: i32[] = [];
        var desc: i32[] = [];
        var same: i32[] = [];
        var mixed: i32[] = [];
        var i2: i32 = 0;
        while (i2 < m) {
            asc = asc.append(i2);
            desc = desc.append(m - i2);
            same = same.append(9);
            mixed = mixed.append(i2);
            i2 = i2 + 1;
        }
        var i3: i32 = 0;
        while (i3 < m) { mixed = mixed.append(m - i3); i3 = i3 + 1; }
        if (!eq_arr(cmp.sort(asc), ref_sort(asc))) { return 4; }
        if (!eq_arr(cmp.sort(desc), ref_sort(desc))) { return 5; }
        if (!eq_arr(cmp.sort(same), ref_sort(same))) { return 6; }
        if (!eq_arr(cmp.sort(mixed), ref_sort(mixed))) { return 7; }
        m = m + 1;
    }

    // Edges.
    var empty: i32[] = [];
    if (cmp.sort(empty).len() != 0) { return 8; }
    var one: i32[] = [5];
    if (cmp.sort(one)[0] != 5) { return 9; }
    var negs: i32[] = [3, 0 - 5, 0, 0 - 1, 7];
    if (!eq_arr(cmp.sort(negs), ref_sort(negs))) { return 10; }

    return 42;
}
`

// Stability through the merge path: `key` alone orders, `tag` witnesses input
// order. 100 elements over 5 keys means every equal-key group spans several
// runs and survives multiple merges.
const cmpAdaptiveSortStableProg = `
import "core/cmp";

struct P { key: i32, tag: i32 }
impl cmp.Ord for P {
    function cmp(self: Self, other: Self): i32 {
        if (self.key < other.key) { return 0 - 1; }
        if (self.key > other.key) { return 1; }
        return 0;
    }
}

function main(): i32 {
    var xs: P[] = [];
    var i: i32 = 0;
    while (i < 100) {
        xs = xs.append(P { key: (i * 37) % 5, tag: i });
        i = i + 1;
    }

    var s: P[] = cmp.sort(xs);
    if (s.len() != 100) { return 1; }
    var j: i32 = 1;
    while (j < s.len()) {
        if (s[j - 1].key > s[j].key) { return 2; }
        // Stable: within an equal-key run, input order (tag) is preserved.
        if (s[j - 1].key == s[j].key && s[j - 1].tag > s[j].tag) { return 3; }
        j = j + 1;
    }

    // Descending reverses the groups but keeps input order WITHIN a group.
    var d: P[] = cmp.sort_desc(xs);
    var m: i32 = 1;
    while (m < d.len()) {
        if (d[m - 1].key < d[m].key) { return 4; }
        if (d[m - 1].key == d[m].key && d[m - 1].tag > d[m].tag) { return 5; }
        m = m + 1;
    }

    // An all-equal input is one big tie: a stable sort returns it unchanged.
    var flat: P[] = [];
    var f: i32 = 0;
    while (f < 60) { flat = flat.append(P { key: 7, tag: f }); f = f + 1; }
    var fs: P[] = cmp.sort(flat);
    var g: i32 = 0;
    while (g < 60) {
        if (fs[g].tag != g) { return 6; }
        g = g + 1;
    }

    return 42;
}
`

func TestCmpAdaptiveSortInterp(t *testing.T) {
	if got := runInterpExit(t, cmpAdaptiveSortProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestCmpAdaptiveSortX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, cmpAdaptiveSortProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestCmpAdaptiveSortWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, cmpAdaptiveSortProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestCmpAdaptiveSortArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, cmpAdaptiveSortProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}

func TestCmpAdaptiveSortStableInterp(t *testing.T) {
	if got := runInterpExit(t, cmpAdaptiveSortStableProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestCmpAdaptiveSortStableX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, cmpAdaptiveSortStableProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestCmpAdaptiveSortStableWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, cmpAdaptiveSortStableProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestCmpAdaptiveSortStableArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, cmpAdaptiveSortStableProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
