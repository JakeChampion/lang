package e2e

import "testing"

// std/array's `binary_search[T: cmp.Ord](xs, target) -> Option[i32]` — O(log n)
// search of an ascending-sorted array via the three-way `xs[mid].cmp(target)`.
// The `T: cmp.Ord` bound monomorphises the compare per element type (scalar for
// i32, lexicographic for string), same as `sort`; the return is Option[i32]
// (scalar payload) regardless of T, so — unlike the T[]/Option[T]-returning
// generics — it lowers on the self-host IR path for string elements too. This
// differential exercises i32 AND string arrays across interp / x86-64 / wasm /
// arm64. Returns 42 iff every check holds; each leg skips itself when its
// toolchain is absent.
const arrayBinarySearchProg = `
import "std/array" as array;
function idx(o: Option[i32]): i32 { match (o) { Some(v) => { return v; }, None => { return 0 - 1; } } }
function main(): i32 {
    var xs: i32[] = [1, 3, 5, 7, 9, 11];
    if (idx(xs.binary_search(7)) != 3) { return 1; }
    if (idx(xs.binary_search(1)) != 0) { return 2; }     // first
    if (idx(xs.binary_search(11)) != 5) { return 3; }    // last
    if (idx(xs.binary_search(4)) != 0 - 1) { return 4; } // absent, in range
    if (idx(xs.binary_search(0)) != 0 - 1) { return 5; } // below
    if (idx(xs.binary_search(20)) != 0 - 1) { return 6; }// above
    if (idx(array.binary_search(xs, 5)) != 2) { return 7; } // free fn
    var e: i32[] = [];
    if (idx(e.binary_search(1)) != 0 - 1) { return 8; }  // empty
    var one: i32[] = [42];
    if (idx(one.binary_search(42)) != 0) { return 9; }
    if (idx(one.binary_search(1)) != 0 - 1) { return 10; }
    // string elements, sorted lexicographically
    var ss: string[] = ["apple", "banana", "cherry", "date"];
    if (idx(ss.binary_search("cherry")) != 2) { return 11; }
    if (idx(ss.binary_search("apple")) != 0) { return 12; }
    if (idx(ss.binary_search("fig")) != 0 - 1) { return 13; }
    return 42;
}
`

func TestArrayBinarySearchInterp(t *testing.T) {
	if got := runInterpExit(t, arrayBinarySearchProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestArrayBinarySearchX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, arrayBinarySearchProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestArrayBinarySearchWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, arrayBinarySearchProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestArrayBinarySearchArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, arrayBinarySearchProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
