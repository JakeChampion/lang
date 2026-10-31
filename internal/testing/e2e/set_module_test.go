package e2e

import "testing"

const setPurityProg = `
import "std/pset";
function main(): i32 {
    let a: pset.PSet[i32] = pset.of([1, 2]);
    let before: i32 = a.len();
    let c: pset.PSet[i32] = a.insert(3);
    let after: i32 = a.len();
    return before * 100 + after * 10 + c.len();   // expect 223
}
`

const setOpsProg = `
import "std/pset";
function main(): i32 {
    let a: pset.PSet[i32] = pset.of([1, 2, 2, 3]);
    if (a.len() != 3) { return 1; }
    if (!a.contains(2) || a.contains(9)) { return 2; }
    a = a.insert(4);
    a = a.remove(2);
    if (a.contains(2) || a.len() != 3) { return 3; }
    let b: pset.PSet[i32] = pset.of([3, 4, 5]);
    if (a.union(b).len() != 4) { return 4; }
    if (a.intersection(b).len() != 2) { return 5; }
    let d: pset.PSet[i32] = a.difference(b);
    if (d.len() != 1 || !d.contains(1)) { return 6; }
    if (!pset.of([3, 4]).is_subset(a)) { return 7; }
    if (a != pset.of([4, 3, 1])) { return 8; }
    let ss: pset.PSet[string] = pset.of(["a", "b", "a"]);
    if (ss.len() != 2 || !ss.contains("b")) { return 9; }
    ss = ss.remove("a");
    if (ss.contains("a")) { return 10; }
    return 42;
}
`

func TestSetPurityInterp(t *testing.T) {
	if got := runInterpExit(t, setPurityProg); got != 223 {
		t.Fatalf("interp got %d, want 223 (add mutated its receiver)", got)
	}
}

func TestSetPurityX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, setPurityProg); got != 223 {
		t.Fatalf("x86-64 got %d, want 223 (add mutated its receiver)", got)
	}
}

func TestSetPurityWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, setPurityProg); got != 223 {
		t.Fatalf("wasm got %d, want 223 (add mutated its receiver)", got)
	}
}

func TestSetPurityArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, setPurityProg); got != 223 {
		t.Fatalf("arm64 got %d, want 223 (add mutated its receiver)", got)
	}
}

func TestSetOpsInterp(t *testing.T) {
	if got := runInterpExit(t, setOpsProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestSetOpsX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, setOpsProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestSetOpsWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, setOpsProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestSetOpsArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, setOpsProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
