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

// A constructor in a bare `fip` body is no longer an E053 SHAPE violation
// (#9602). The checker cannot tell a rebuild that reuses a dead donor's box
// from one that allocates a fresh box, so it admits the shape and the IR layer
// decides: verifyFipAllocs counts the sites that lowered to a real OpAlloc and
// refuses the function with E068 when they exceed the allowance, which for a
// bare `fip` is zero. internal/ir/fip_verify_test.go owns that half.
//
// Rejecting the shape here left `fip` unable to carry struct state at all: a
// field write is E048, whose message names the rebuild `T { ...old, f: v }` as
// the remedy, and that rebuild was E053 — each diagnostic pointing at what the
// other forbade.
func TestFipAcceptsConstructorShape(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"struct rebuild from own", `struct P { x: i32, y: i32 }
fip function f(own p: P): P { return P { ...p, x: p.x + 1 }; }
function main(): i32 { return 0; }`},
		{"fresh struct literal", `struct P { x: i32, y: i32 }
fip function f(): P { return P { x: 1, y: 2 }; }
function main(): i32 { return 0; }`},
		{"tuple literal", `fip function f(): (i32, i32) { return (1, 2); }
function main(): i32 { return 0; }`},
		{"enum construction", `enum L { C(i32, L), N }
fip function f(t: L): L { return C(1, t); }
function main(): i32 { return 0; }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkSource(t, tc.src); err != nil {
				t.Errorf("expected no E053 for a constructor shape, got: %v", err)
			}
		})
	}
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

}

// The receiver root walk reaches through a field access and an index, so a
// `.with` on an `own` struct's array field is the same in-place set as one on
// the parameter itself. The self-host accepted only a bare identifier, which
// made structure-of-arrays — the shape `fbip` exists for — E053 there and
// clean here (#9699).
//
// It reaches through an earlier `.with` link too: a chain writes one array in
// place, in assignment position as in return position (#9702). Any other call
// stops it.
func TestFipWithReceiverRootWalk(t *testing.T) {
	wantNoErr(t, "with on an own struct's array field", `struct S { xs: i32[] }
fip function f(own s: S): i32[] { return s.xs.with(0, 1); }`)

	wantNoErr(t, "with on an own struct's nested array field", `struct Inner { xs: i32[] }
struct S { inner: Inner }
fip function f(own s: S): i32[] { return s.inner.xs.with(0, 1); }`)

	wantNoErr(t, "fbip struct update writing its own array field", `struct S { xs: i32[], n: i32 }
fbip function f(own s: S): S { return S { ...s, xs: s.xs.with(0, 1), n: s.n + 1 }; }`)

	// The claim follows ownership, not shape: a field receiver on a BORROWED
	// struct copies on write and stays E053.
	wantE053(t, "with on a non-own struct's array field", `struct S { xs: i32[] }
fip function f(s: S): i32[] { return s.xs.with(0, 1); }`)

	wantNoErr(t, "chained with on an own array",
		`fip function f(own b: i32[]): i32[] { return b.with(0, 1).with(1, 2); }`)

	wantNoErr(t, "chained with on an own array in assignment position",
		`fip function f(own b: i32[]): i32[] { b = b.with(0, 1).with(1, 2); return b; }`)

	wantNoErr(t, "chained with on an own struct's array field", `struct S { xs: i32[] }
fip function f(own s: S): i32[] { return s.xs.with(0, 1).with(1, 2); }`)

	// An outer link that reads the root runs after the inner write, so the
	// chain copies and is not admitted.
	wantE053(t, "chained with whose outer link reads the receiver",
		`fip function f(own b: i32[]): i32[] { return b.with(0, 1).with(1, b[0]); }`)

	// The claim still follows ownership through the chain.
	wantE053(t, "chained with on a borrowed array",
		`fip function f(b: i32[]): i32[] { return b.with(0, 1).with(1, 2); }`)

	// A chain rooted at a call that is not `.with` reaches no owner either.
	// `pick` is itself allocation-free, so the only thing left to report is
	// the `.with` whose receiver the walk could not root.
	wantE053(t, "chain rooted at a non-with call", `fip function pick(own a: i32[]): i32[] { return a; }
fip function f(own b: i32[]): i32[] { return pick(b).with(0, 1); }`)
}

// `recv.map(f)` on an `own` array is admitted the way `.with` is (#9733):
// whether the result is written through the donor is an IR fact — the
// receiver has to be the consumed parameter itself and `f` capture-free and
// effect-free — so the checker admits the shape and E068 counts the `map`
// R7 declines. The claim still follows ownership: a borrowed receiver has
// no donor and stays E053.
func TestFipMapOnOwnReceiver(t *testing.T) {
	const decl = "function (xs: i64[]) map(f: (i64) => i64): i64[] { return xs; }\n"
	wantNoErr(t, "map on an own array", decl+
		`fip function run(own xs: i64[]): i64[] { return xs.map((x: i64): i64 => x); }`)
	wantNoErr(t, "fbip map on an own array", decl+
		`fbip function run(own xs: i64[]): i64[] { return xs.map((x: i64): i64 => x); }`)
	// The element function is inside the claim: what it calls must be `fip`.
	wantE053(t, "map whose element function calls outside the claim", decl+
		`function g(x: i64): i64 { return x; }
fip function run(own xs: i64[]): i64[] { return xs.map((x: i64): i64 => g(x)); }`)
	wantE053(t, "map on a borrowed array", decl+
		`fip function run(xs: i64[]): i64[] { return xs.map((x: i64): i64 => x); }`)
	wantE053(t, "chained map on an own array", decl+
		`fip function run(own xs: i64[]): i64[] { return xs.map((x: i64): i64 => x).map((x: i64): i64 => x); }`)
}
