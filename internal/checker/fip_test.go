package checker

import (
	"strings"
	"testing"
)

// `fip function` is a Koka-style fully-in-place CHECKED guarantee: the checker
// (E053) verifies the body performs no heap allocation, as a sound conservative
// subset. In-place index writes to an `own` array parameter are allowed (the
// copy-on-write unique branch); allocating literals / string ops / non-fip
// calls are rejected. These pin both directions.

func wantE053(t *testing.T, name, src string) {
	t.Helper()
	err := checkSource(t, src)
	if err == nil {
		t.Fatalf("%s: expected E053 (fip allocation), got none", name)
	}
	if !strings.Contains(err.Error(), "`fip` function") {
		t.Errorf("%s: expected a fip-allocation error, got: %v", name, err)
	}
}

func wantNoErr(t *testing.T, name, src string) {
	t.Helper()
	if err := checkSource(t, src); err != nil {
		t.Errorf("%s: expected no error, got: %v", name, err)
	}
}

func TestFipAcceptsAllocationFree(t *testing.T) {
	// In-place insertion sort over an `own` array: `.with` on the unique
	// `arr` is the allocation-free CoW unique-in-place element set (the
	// value-returning replacement for the removed `arr[i] = v`, accepted
	// because the receiver root is `own`); `len` is whitelisted, the rest
	// is scalar.
	wantNoErr(t, "inplace sort", `fip function sort_inplace(own arr: i32[]): i32[] {
    var n: i32 = arr.len();
    var k: i32 = 1;
    while (k < n) {
        var key: i32 = arr[k];
        var j: i32 = k - 1;
        while (j >= 0 && arr[j] > key) { arr = arr.with(j + 1, arr[j]); j = j - 1; }
        arr = arr.with(j + 1, key);
        k = k + 1;
    }
    return arr;
}
function main(): i32 { return 0; }`)

	// `.with` on an `own` array is accepted (allocation-free in-place).
	wantNoErr(t, "with on own", `fip function set0(own a: i32[]): i32[] { return a.with(0, 9); }
function main(): i32 { return 0; }`)

	// Pure scalar arithmetic — trivially fip.
	wantNoErr(t, "scalar", `fip function add(a: i32, b: i32): i32 { return a + b; }
function main(): i32 { return 0; }`)

	// A fip function calling another fip function.
	wantNoErr(t, "fip calls fip", `fip function inc(x: i32): i32 { return x + 1; }
fip function twice(x: i32): i32 { return inc(inc(x)); }
function main(): i32 { return 0; }`)
}

func TestFipRejectsAllocation(t *testing.T) {
	wantE053(t, "array literal", `fip function f(): i32[] { return [1, 2, 3]; }
function main(): i32 { return 0; }`)

	wantE053(t, "struct literal", `struct P { x: i32, y: i32 }
fip function f(): P { return P { x: 1, y: 2 }; }
function main(): i32 { return 0; }`)

	wantE053(t, "tuple literal", `fip function f(): (i32, i32) { return (1, 2); }
function main(): i32 { return 0; }`)

	wantE053(t, "enum construction", `enum L { C(i32, L), N }
fip function f(t: L): L { return C(1, t); }
function main(): i32 { return 0; }`)

	wantE053(t, "string concat", `fip function f(a: string, b: string): string { return a + b; }
function main(): i32 { return 0; }`)

	wantE053(t, "calls non-fip", `function alloc(): i32[] { return [1]; }
fip function f(): i32 { var a: i32[] = alloc(); return a[0]; }
function main(): i32 { return 0; }`)

	// `.with` on a NON-`own` (shared/borrowed) array copies-on-write, so it
	// is not allocation-free — only `.with` on an `own` receiver is accepted.
	wantE053(t, "with on non-own array", `fip function f(arr: i32[]): i32[] { return arr.with(0, 9); }
function main(): i32 { return 0; }`)
}

// wantFbipE053 is wantE053's fbip sibling: the violation must be reported
// against the `fbip` keyword.
func wantFbipE053(t *testing.T, name, src string) {
	t.Helper()
	err := checkSource(t, src)
	if err == nil {
		t.Fatalf("%s: expected E053 (fbip shape violation), got none", name)
	}
	if !strings.Contains(err.Error(), "`fbip` function") {
		t.Errorf("%s: expected an fbip-shape error, got: %v", name, err)
	}
}

// `fbip` relaxes exactly the constructor rule of the E053 walk: struct /
// tuple literals and payload-carrying enum variants pass the CHECKER (the IR
// then verifies each site is reuse-paired — E068); everything else E053
// rejects stays rejected. Graded `fip(n)` gets the same constructor
// relaxation (the IR owns the count).
func TestFbipAcceptsConstructors(t *testing.T) {
	wantNoErr(t, "fbip struct literal", `struct P { x: i32, y: i32 }
fbip function f(a: i32): P { return P { x: a, y: a }; }
function main(): i32 { return 0; }`)

	wantNoErr(t, "fbip tuple literal", `fbip function f(a: i32): (i32, i32) { return (a, a + 1); }
function main(): i32 { return 0; }`)

	wantNoErr(t, "fbip enum construction", `enum L { C(i32, L), N }
fbip function f(own t: L): L { return C(1, t); }
function main(): i32 { return 0; }`)

	// The R4 consuming-match rebuild — the canonical fbip shape.
	wantNoErr(t, "fbip consuming match", `enum List { Cons(i32, List), Nil }
fbip function map_inc(own xs: List): List {
    match (xs) {
        Cons(h, t) => { return Cons(h + 1, map_inc(t)); },
        Nil => { return Nil; },
    }
}
function main(): i32 { return 0; }`)

	// Graded fip(n): constructors pass the checker too (the IR counts them).
	wantNoErr(t, "graded fip(1) struct literal", `struct P { x: i32, y: i32 }
fip(1) function f(a: i32): P { return P { x: a, y: a }; }
function main(): i32 { return 0; }`)

	// fbip may call fip: the callee's claim is strictly stronger.
	wantNoErr(t, "fbip calls fip", `fip function inc(x: i32): i32 { return x + 1; }
fbip function f(x: i32): i32 { return inc(x); }
function main(): i32 { return 0; }`)

	// fbip may call fbip.
	wantNoErr(t, "fbip calls fbip", `struct P { x: i32 }
fbip function g(a: i32): P { return P { x: a }; }
fbip function f(a: i32): P { return g(a); }
function main(): i32 { return 0; }`)
}

func TestFbipRejectsNonConstructorAllocation(t *testing.T) {
	// Array literals stay rejected — no array reuse pairing exists.
	wantFbipE053(t, "fbip array literal", `fbip function f(): i32[] { return [1, 2, 3]; }
function main(): i32 { return 0; }`)

	wantFbipE053(t, "fbip string concat", `fbip function f(a: string, b: string): string { return a + b; }
function main(): i32 { return 0; }`)

	wantFbipE053(t, "fbip string interpolation", `fbip function f(a: i32): string { return f"v={a}"; }
function main(): i32 { return 0; }`)

	wantFbipE053(t, "fbip calls unmarked", `function alloc(): i32[] { return [1]; }
fbip function f(): i32 { var a: i32[] = alloc(); return a[0]; }
function main(): i32 { return 0; }`)

	wantFbipE053(t, "fbip cow write", `struct H { v: i32 }
fbip function f(arr: i32[]): i32[] { return arr.with(0, 9); }
function main(): i32 { return 0; }`)

	// Bare fip stays strict: it may NOT call fbip (the weaker claim) …
	wantE053(t, "fip calls fbip", `struct P { x: i32 }
fbip function g(a: i32): P { return P { x: a }; }
fip function f(a: i32): i32 { var p: P = g(a); return p.x; }
function main(): i32 { return 0; }`)

	// … and bare fip(0) still rejects constructors at the checker.
	wantE053(t, "bare fip struct literal still rejected", `struct P { x: i32 }
fip function f(a: i32): P { return P { x: a } ; }
function main(): i32 { return 0; }`)
}
