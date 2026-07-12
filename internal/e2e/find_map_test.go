package e2e

import "testing"

// Differential coverage for std/array.find_map across backends: the
// first-Some result, None when nothing maps, the empty array, an
// always-None projection, and short-circuiting at the first Some.
// Returns 42 iff every check holds. Each leg skips itself when its
// toolchain is absent.
const findMapProg = `
import "std/array" as array;
function even_x10(n: i32): Option[i32] {
    if (n % 2 == 0) { return Some(n * 10); }
    return None;
}
function always_none(n: i32): Option[i32] { return None; }
function opt(o: Option[i32], fb: i32): i32 {
    match (o) { Some(v) => { return v; }, None => { return fb; } }
}
function main(): i32 {
    if (opt(array.find_map([1, 3, 4, 6], even_x10), -1) != 40) { return 1; }
    if (opt(array.find_map([1, 3, 5], even_x10), -99) != -99) { return 2; }
    var empty: i32[] = [];
    if (opt(array.find_map(empty, even_x10), -99) != -99) { return 3; }
    if (opt(array.find_map([2, 4, 6], always_none), -99) != -99) { return 4; }
    if (opt(array.find_map([2, 4], even_x10), -1) != 20) { return 5; }
    return 42;
}
`

func TestFindMapInterp(t *testing.T) {
	if got := runInterpExit(t, findMapProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestFindMapX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, findMapProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestFindMapWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, findMapProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestFindMapArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, findMapProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
