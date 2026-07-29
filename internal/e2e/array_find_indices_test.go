package e2e

// Differential coverage for std/array `find_indices` — the indices of every
// element satisfying the predicate (the "all matches" plural of `position`).
// Checks the matching indices, none-match (empty), all-match, and that the
// result agrees with `position` on the first hit. Returns 42 iff every check
// holds across interp / x86-64 / wasm / arm64.

import "testing"

const arrayFindIndicesProg = `
import "std/array";
function main(): i32 {
    var xs: i32[] = [10, 7, 4, 3, 8, 5, 2];
    var evens: i32[] = array.find_indices(xs, (x: i32) => x % 2 == 0);   // 0,2,4,6
    if (evens.len() != 4 || evens[0] != 0 || evens[1] != 2 || evens[2] != 4 || evens[3] != 6) { return 1; }
    // None match.
    var big: i32[] = array.find_indices(xs, (x: i32) => x > 100);
    if (big.len() != 0) { return 2; }
    // All match.
    var all: i32[] = array.find_indices(xs, (x: i32) => x >= 0);
    if (all.len() != 7) { return 3; }
    // Agrees with position on the first hit.
    match (array.position(xs, (x: i32) => x < 5)) {
        Some(p) => { var idx: i32[] = array.find_indices(xs, (x: i32) => x < 5); if (idx.len() == 0 || idx[0] != p) { return 4; } },
        None => { if (array.find_indices(xs, (x: i32) => x < 5).len() != 0) { return 5; } }
    }
    // Empty input.
    var e: i32[] = [];
    if (array.find_indices(e, (x: i32) => true).len() != 0) { return 6; }
    return 42;
}
`

func TestArrayFindIndicesInterp(t *testing.T) {
	if got := runInterpExit(t, arrayFindIndicesProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestArrayFindIndicesX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, arrayFindIndicesProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestArrayFindIndicesWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, arrayFindIndicesProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestArrayFindIndicesArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, arrayFindIndicesProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
