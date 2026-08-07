package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Union-of-structs array literals must wrap each element into the union, the
// same way a single `var n: N = A { … }`, a `return`, or an `arr.append(A { … })`
// argument is. Before the fix the checker's ArrayLit case had no union-wrap:
// `var xs: N[] = [A { … }, B { … }]` stored bare, un-tagged structs, so a later
// `match` misfired (the interpreter reported "match scrutinee is *interp.Struct,
// expected enum value"; the native backends segfaulted). The `.push` path was
// unaffected because it coerces through the Call-argument path. The fix wraps
// each element when the array's element-type hint is a union enum — which also
// makes a mixed-variant literal `[A { … }, B { … }]` type-check rather than
// being rejected with E034 "array element type B, expected A".
//
// Each program reads every element back through a match and returns 0 only when
// all values are correct (a mismatch returns the 99 sentinel, surviving the
// 8-bit native exit code).

// Mixed variants in one literal — exercises both the wrap and the
// cross-variant element typing.
const unionArrMixed = `struct A { x: i32 }
struct B { y: i32 }
type N = A | B;
function main(): i32 {
    var xs: N[] = [A { x: 5 }, B { y: 7 }, A { x: 11 }];
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < xs.len()) {
        t = t + match (xs[i]) { A(a) => a.x, B(b) => 100 + b.y };
        i = i + 1;
    }
    if (t != 123) { return 99; }
    return 0;
}`

// Same-variant literal — the original repro shape (all elements type A, the
// checker infers A[] then coerces to N[]).
const unionArrSame = `struct A { x: i32 }
struct B { y: i32 }
type N = A | B;
function ksum(xs: N[]): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < xs.len()) {
        t = t + match (xs[i]) { A(a) => a.x, B(b) => 100 + b.y };
        i = i + 1;
    }
    return t;
}
function main(): i32 {
    var xs: N[] = [A { x: 1 }, A { x: 2 }];
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
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	if got := runWasm(t, unionArrMixed); got != 0 {
		t.Errorf("mixed: %d (99=value mismatch / un-wrapped element)", got)
	}
	if got := runWasm(t, unionArrSame); got != 0 {
		t.Errorf("same: %d (99=value mismatch / un-wrapped element)", got)
	}
}
