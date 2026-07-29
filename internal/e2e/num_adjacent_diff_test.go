package e2e

// Differential coverage for std/num `adjacent_diff` — the differences between
// consecutive elements (`out[i] == xs[i+1] - xs[i]`), the inverse of `cumsum`.
// Checks the deltas (including negatives), the `cumsum`/`adjacent_diff` inverse
// relationship, the empty / single-element inputs (which yield empty), and an
// i64 width. Returns 42 iff every check holds across interp / x86-64 / wasm /
// arm64.

import "testing"

const numAdjacentDiffProg = `
import "std/num";
function main(): i32 {
    var xs: i32[] = [1, 3, 6, 10];
    var d: i32[] = num.adjacent_diff(xs);
    if (d.len() != 3 || d[0] != 2 || d[1] != 3 || d[2] != 4) { return 1; }
    // Negative deltas.
    var ns: i32[] = [10, 3, 8, 8];
    var nd: i32[] = num.adjacent_diff(ns);
    if (nd[0] != (0 - 7) || nd[1] != 5 || nd[2] != 0) { return 2; }
    // Inverse of cumsum: adjacent_diff(cumsum(v)) == v (for len >= 1).
    var v: i32[] = [4, 1, 7, 2, 9];
    var round: i32[] = num.adjacent_diff(num.cumsum(v));
    // cumsum(v) = [4,5,12,14,23]; adjacent_diff = [1,7,2,9] == v[1..].
    if (round.len() != 4 || round[0] != 1 || round[1] != 7 || round[2] != 2 || round[3] != 9) { return 3; }
    // Empty / single -> empty.
    var e: i32[] = [];
    var one: i32[] = [42];
    if (num.adjacent_diff(e).len() != 0 || num.adjacent_diff(one).len() != 0) { return 4; }
    // i64 width.
    var ls: i64[] = [1000000000000, 1000000000003, 1000000000001];
    var ld: i64[] = num.adjacent_diff(ls);
    if (ld[0] != 3 || ld[1] != (0 - 2)) { return 5; }
    return 42;
}
`

func TestNumAdjacentDiffInterp(t *testing.T) {
	if got := runInterpExit(t, numAdjacentDiffProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestNumAdjacentDiffX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, numAdjacentDiffProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestNumAdjacentDiffWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, numAdjacentDiffProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestNumAdjacentDiffArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, numAdjacentDiffProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
