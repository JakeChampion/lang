package e2e

import "testing"

const setAlgebraProg = `
import "std/pset";
function main(): i32 {
    let a: pset.PSet[i32] = pset.of([1, 2, 3]);
    let b: pset.PSet[i32] = pset.of([2, 3, 4]);
    let sub: pset.PSet[i32] = pset.of([2, 3]);
    let empty: pset.PSet[i32] = pset.new();
    if (!a.is_superset(sub)) { return 1; }
    if (a.is_superset(b)) { return 2; }
    if (!a.is_superset(empty)) { return 3; }
    if (!a.is_superset(a)) { return 4; }
    if (a.is_disjoint(b)) { return 5; }
    if (!a.is_disjoint(pset.of([7, 8]))) { return 6; }
    if (!a.is_disjoint(empty)) { return 7; }
    if (!empty.is_disjoint(empty)) { return 8; }
    let sd: pset.PSet[i32] = a.symmetric_difference(b);
    if (sd.len() != 2 || !sd.contains(1) || !sd.contains(4) || sd.contains(2)) { return 9; }
    if (a.symmetric_difference(a).len() != 0) { return 10; }
    if (a.symmetric_difference(empty) != a) { return 11; }
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
