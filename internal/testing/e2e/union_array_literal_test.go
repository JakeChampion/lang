package e2e

import (
	"testing"
)

const unionArrMixed = `struct A { x: i32 }
struct B { y: i32 }
type N = A | B;
function main(): i32 {
    let xs: N[] = [A { x: 5 }, B { y: 7 }, A { x: 11 }];
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < xs.len()) {
        t = t + match (xs[i]) { A(a) => a.x, B(b) => 100 + b.y };
        i = i + 1;
    }
    if (t != 123) { return 99; }
    return 0;
}`

const unionArrSame = `struct A { x: i32 }
struct B { y: i32 }
type N = A | B;
function ksum(xs: N[]): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < xs.len()) {
        t = t + match (xs[i]) { A(a) => a.x, B(b) => 100 + b.y };
        i = i + 1;
    }
    return t;
}
function main(): i32 {
    let xs: N[] = [A { x: 1 }, A { x: 2 }];
    if (ksum(xs) != 3) { return 99; }
    return 0;
}`

func checkUnionArrLit(t *testing.T, run func(*testing.T, string) (string, int)) {
	t.Helper()
	for _, c := range []struct{ name, src string }{
		{"mixed", unionArrMixed},
		{"same", unionArrSame},
	} {
		if _, code := run(t, c.src); code != 0 {
			t.Errorf("%s: code=%d (99=value mismatch / un-wrapped element)", c.name, code)
		}
	}
}

func TestX86_64UnionArrayLiteralWrap(t *testing.T) {
	checkUnionArrLit(t, compileAndRunX86_64FreeOn)
}

func TestArm64UnionArrayLiteralWrap(t *testing.T) {
	checkUnionArrLit(t, compileAndRunArm64FreeOn)
}

func TestWASMUnionArrayLiteralWrap(t *testing.T) {
	if got := runWasm(t, unionArrMixed); got != 0 {
		t.Errorf("mixed: %d (99=value mismatch / un-wrapped element)", got)
	}
	if got := runWasm(t, unionArrSame); got != 0 {
		t.Errorf("same: %d (99=value mismatch / un-wrapped element)", got)
	}
}
