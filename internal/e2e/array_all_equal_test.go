package e2e

import "testing"

// std/array's `all_equal[T: cmp.Eq](xs)` — true iff every element equals the
// first (≤ 1 distinct value); vacuously true for length < 2, short-circuits on
// the first differing element. The T: cmp.Eq bound supplies the compare (the
// type's structural equality); the return is boolean. A `@derive(Eq)` struct
// element is pinned by TestStdArrayEqBoundDeriveElement. This differential
// exercises i32 AND string
// arrays across interp / x86-64 / wasm / arm64. Returns 42 iff every check
// holds; each leg skips itself when its toolchain is absent.
const arrayAllEqualProg = `
import "std/array" as array;
function main(): i32 {
    if (![7, 7, 7].all_equal()) { return 1; }
    if ([1, 2, 3].all_equal()) { return 2; }        // all distinct
    if ([1, 1, 2].all_equal()) { return 3; }        // last differs
    if ([2, 1, 1].all_equal()) { return 4; }        // first differs
    var e: i32[] = [];
    if (!e.all_equal()) { return 5; }               // empty -> vacuously true
    if (![5].all_equal()) { return 6; }             // single -> true
    if (!array.all_equal([9, 9])) { return 7; }     // free fn
    // string elements
    if (!["a", "a", "a"].all_equal()) { return 8; }
    if (["a", "b"].all_equal()) { return 9; }
    return 42;
}
`

func TestArrayAllEqualInterp(t *testing.T) {
	if got := runInterpExit(t, arrayAllEqualProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestArrayAllEqualX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, arrayAllEqualProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestArrayAllEqualWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, arrayAllEqualProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestArrayAllEqualArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, arrayAllEqualProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
