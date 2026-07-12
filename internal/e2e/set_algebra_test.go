package e2e

import "testing"

// Differential coverage for the std/set algebra completions across
// backends: is_superset (mirror of is_subset, incl. empty), is_disjoint
// (empty intersection), and symmetric_difference (elements in exactly
// one set, with union's ordering convention). Returns 42 iff every check
// holds. Each leg skips itself when its toolchain is absent.
const setAlgebraProg = `
import "std/set" as set;
function main(): i32 {
    var a: set.Set[i32] = set.set_of([1, 2, 3]);
    var b: set.Set[i32] = set.set_of([2, 3, 4]);
    var sub: set.Set[i32] = set.set_of([2, 3]);
    var empty: set.Set[i32] = set.set_new();
    if (!a.is_superset(sub)) { return 1; }
    if (a.is_superset(b)) { return 2; }
    if (!a.is_superset(empty)) { return 3; }
    if (!a.is_superset(a)) { return 4; }
    if (a.is_disjoint(b)) { return 5; }
    if (!a.is_disjoint(set.set_of([7, 8]))) { return 6; }
    if (!a.is_disjoint(empty)) { return 7; }
    if (!empty.is_disjoint(empty)) { return 8; }
    var sd: set.Set[i32] = a.symmetric_difference(b);
    if (sd.len() != 2 || !sd.contains(1) || !sd.contains(4) || sd.contains(2)) { return 9; }
    if (a.symmetric_difference(a).len() != 0) { return 10; }
    if (!a.symmetric_difference(empty).equals(a)) { return 11; }
    var arr: i32[] = sd.to_array();
    if (arr[0] != 1 || arr[1] != 4) { return 12; }
    return 42;
}
`

func TestSetAlgebraInterp(t *testing.T) {
	if got := runInterpExit(t, setAlgebraProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestSetAlgebraX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, setAlgebraProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestSetAlgebraWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, setAlgebraProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestSetAlgebraArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, setAlgebraProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
