package e2e

// Differential coverage for std/array `take_last` / `drop_last` — the
// back-of-the-array mirrors of `take` / `drop`. `take_last(xs, n)` keeps the
// last n elements, `drop_last(xs, n)` keeps all but them; `n` is clamped so
// `n <= 0` and `n >= len` are the empty / whole-array cases, never a fault.
// Returns 42 iff every check holds across interp / x86-64 / wasm / arm64.

import "testing"

const arrayTakeDropLastProg = `
import "std/array";
function main(): i32 {
    var xs: i32[] = [1, 2, 3, 4, 5];
    var tl: i32[] = array.take_last(xs, 2);
    if (tl.len() != 2 || tl[0] != 4 || tl[1] != 5) { return 1; }
    if (array.take_last(xs, 10).len() != 5) { return 2; }
    if (array.take_last(xs, 0).len() != 0) { return 3; }
    if (array.take_last(xs, 0 - 3).len() != 0) { return 4; }
    var dl: i32[] = array.drop_last(xs, 2);
    if (dl.len() != 3 || dl[0] != 1 || dl[2] != 3) { return 5; }
    if (array.drop_last(xs, 10).len() != 0) { return 6; }
    if (array.drop_last(xs, 0).len() != 5) { return 7; }
    // take_last(n) and drop_last(n) partition the array: the first len-n
    // elements and the last n, with nothing shared or missing.
    var a: i32[] = array.take_last(xs, 2);
    var b: i32[] = array.drop_last(xs, 2);
    if (a.len() + b.len() != 5) { return 8; }
    if (b[0] != 1 || b[2] != 3 || a[0] != 4 || a[1] != 5) { return 9; }
    return 42;
}
`

func TestArrayTakeDropLastInterp(t *testing.T) {
	if got := runInterpExit(t, arrayTakeDropLastProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestArrayTakeDropLastX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, arrayTakeDropLastProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestArrayTakeDropLastWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, arrayTakeDropLastProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestArrayTakeDropLastArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, arrayTakeDropLastProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
