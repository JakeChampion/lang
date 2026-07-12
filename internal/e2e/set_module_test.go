package e2e

import "testing"

// std/set differential coverage. The module is a generic, value-
// semantic Set[T] built on a struct-wrapped array. Its correctness is
// backend-sensitive: a naive `add` that appends onto the receiver's
// field array in place passes on the interpreter but silently mutates
// a shared receiver once compiled (the copy-on-write aliasing hazard).
// std/set therefore copies the backing array on every mutating op, and
// these tests pin that contract on every backend that compiles the
// program (interp / x86-64 / wasm / arm64-qemu), each skipping itself
// when its toolchain is absent.

// setPurityProg exercises the load-bearing invariant: an operation
// returns a NEW set and never mutates its receiver. `before*100 +
// after*10 + clen` == 223 iff `a.add(3)` left `a` (len 2) untouched
// while producing a 3-element result. The naive in-place impl yields
// 233 (receiver grown to 3) when compiled.
const setPurityProg = `
import "std/set" as set;
function main(): i32 {
    var a: set.Set[i32] = set.set_of([1, 2]);
    var before: i32 = a.len();
    var c: set.Set[i32] = a.add(3);
    var after: i32 = a.len();
    return before * 100 + after * 10 + c.len();   // expect 223
}
`

// setOpsProg walks the full combinator surface (dedup, remove,
// union/intersect/difference, subset/equals, string elems) and returns
// 42 iff every check holds. Mirrors examples/tests/set_test.fern in a
// single exit-coded program so the compiled backends assert it too.
const setOpsProg = `
import "std/set" as set;
function main(): i32 {
    var a: set.Set[i32] = set.set_of([1, 2, 2, 3]);
    if (a.len() != 3) { return 1; }
    if (!a.contains(2) || a.contains(9)) { return 2; }
    a = a.add(4);
    a = a.remove(2);
    if (a.contains(2) || a.len() != 3) { return 3; }
    var b: set.Set[i32] = set.set_of([3, 4, 5]);
    if (a.union(b).len() != 4) { return 4; }
    if (a.intersect(b).len() != 2) { return 5; }
    var d: set.Set[i32] = a.difference(b);
    if (d.len() != 1 || !d.contains(1)) { return 6; }
    if (!set.set_of([3, 4]).is_subset(a)) { return 7; }
    if (!a.equals(set.set_of([4, 3, 1]))) { return 8; }
    var ss: set.Set[string] = set.set_of(["a", "b", "a"]);
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
