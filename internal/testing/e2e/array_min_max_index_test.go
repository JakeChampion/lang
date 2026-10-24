package e2e

import "testing"

const arrayMinMaxIndexProg = `
import "std/array" as array;
function idx(o: Option[i32]): i32 { match (o) { Some(v) => { return v; }, None => { return 0 - 1; } } }
function main(): i32 {
    let xs: i32[] = [3, 1, 4, 1, 5, 9, 2, 6];
    if (idx(xs.max_index()) != 5) { return 1; }    // 9 at index 5
    if (idx(xs.min_index()) != 1) { return 2; }    // first 1 (tie -> first index)
    if (idx(array.max_index(xs)) != 5) { return 3; } // free fn
    if (idx([42].max_index()) != 0) { return 4; }  // single
    if (idx([42].min_index()) != 0) { return 5; }
    let e: i32[] = [];
    if (idx(e.max_index()) != 0 - 1) { return 6; } // empty -> None
    if (idx(e.min_index()) != 0 - 1) { return 7; }
    if (idx([7, 7, 7].max_index()) != 0) { return 8; } // all equal -> first
    // string elements, lexicographic ordering
    let ss: string[] = ["banana", "apple", "cherry"];
    if (idx(ss.max_index()) != 2) { return 9; }    // cherry
    if (idx(ss.min_index()) != 1) { return 10; }   // apple
    return 42;
}
`

func TestArrayMinMaxIndexInterp(t *testing.T) {
	if got := runInterpExit(t, arrayMinMaxIndexProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestArrayMinMaxIndexX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, arrayMinMaxIndexProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestArrayMinMaxIndexWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, arrayMinMaxIndexProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestArrayMinMaxIndexArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, arrayMinMaxIndexProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
