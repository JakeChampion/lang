package e2e

// Differential coverage for std/array generic `distinct` — every element
// once, in first-seen order (`distinct([3,1,3,2,1]) == [3,1,2]`). Unlike
// `dedup` (consecutive runs only) it removes duplicates anywhere; unlike a
// Set round-trip it preserves order. Returns 42 iff every check holds across
// interp / x86-64 / wasm / arm64.

import "testing"

const arrayDistinctProg = `
import "std/array";
function main(): i32 {
    var xs: i32[] = [3, 1, 3, 2, 1, 3];
    var d: i32[] = array.distinct(xs);
    if (d.len() != 3 || d[0] != 3 || d[1] != 1 || d[2] != 2) { return 1; }   // order preserved
    if (array.distinct([5, 5, 5]).len() != 1) { return 2; }
    var e: i32[] = [];
    if (array.distinct(e).len() != 0) { return 3; }
    // Already-distinct input is returned unchanged.
    var uniq: i32[] = [1, 2, 3, 4];
    var du: i32[] = array.distinct(uniq);
    if (du.len() != 4 || du[3] != 4) { return 4; }
    // Contrast with dedup, which only collapses consecutive runs.
    var y: i32[] = [1, 1, 2, 1];
    if (array.dedup(y).len() != 3) { return 5; }        // [1,2,1]
    if (array.distinct(y).len() != 2) { return 6; }     // [1,2]
    return 42;
}
`

func TestArrayDistinctInterp(t *testing.T) {
	if got := runInterpExit(t, arrayDistinctProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestArrayDistinctX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, arrayDistinctProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestArrayDistinctWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, arrayDistinctProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestArrayDistinctArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, arrayDistinctProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
