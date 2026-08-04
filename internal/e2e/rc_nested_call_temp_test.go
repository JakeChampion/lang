package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Nested-call owned-temp reclamation. A fresh owned temporary passed as a
// borrowed argument to a POINTER-returning callee — `sum(dup(build(n)))`, and
// its method form `sum(build(n).dup())` — used to leak the temp every call: the
// stage-(b) reclaim was gated to concrete-scalar-returning enclosing calls (a
// pointer result might alias the arg via an identity/projection return). The
// `returnsNoParamEscape` analysis proves the callee never lets a parameter
// escape into its result, so the temp is provably dead the instant the call
// returns and can be reclaimed. These pin value + zero over-release + a flat
// high-water on all backends.

const nestedFreeSrc = `enum List { Cons(i32, List), Nil }
function build(n: i32): List { if (n == 0) { return Nil; } return Cons(n, build(n - 1)); }
function sum(l: List): i32 { match (l) { Cons(h, t) => { return h + sum(t); }, Nil => { return 0; } } }
function dup(xs: List): List { match (xs) { Cons(h, t) => { return Cons(h, dup(t)); }, Nil => { return Nil; } } }
function main(): i32 {
    var total: i32 = 0;
    var i: i32 = 0;
    while (i < 100) { total = total + sum(dup(build(5))); i = i + 1; }   // 15 per iter
    if (total != 1500) { return 999; }
    return __rc_underflow_count();
}`

const nestedMethodSrc = `enum List { Cons(i32, List), Nil }
function build(n: i32): List { if (n == 0) { return Nil; } return Cons(n, build(n - 1)); }
function sum(l: List): i32 { match (l) { Cons(h, t) => { return h + sum(t); }, Nil => { return 0; } } }
function (xs: List) dup(): List { match (xs) { Cons(h, t) => { return Cons(h, t.dup()); }, Nil => { return Nil; } } }
function main(): i32 {
    var total: i32 = 0;
    var i: i32 = 0;
    while (i < 100) { total = total + sum(build(5).dup()); i = i + 1; }
    if (total != 1500) { return 999; }
    return __rc_underflow_count();
}`

func TestX86_64NestedCallTemp(t *testing.T) {
	for _, src := range []string{nestedFreeSrc, nestedMethodSrc} {
		if _, code := compileAndRunX86_64FreeOn(t, src); code != 0 {
			t.Errorf("nested-call temp: got %d, want 0", code)
		}
	}
}

func TestArm64NestedCallTemp(t *testing.T) {
	for _, src := range []string{nestedFreeSrc, nestedMethodSrc} {
		if _, code := compileAndRunArm64FreeOn(t, src); code != 0 {
			t.Errorf("nested-call temp: got %d, want 0", code)
		}
	}
}

func TestWASMNestedCallTemp(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	for _, src := range []string{nestedFreeSrc, nestedMethodSrc} {
		if got := runWasm(t, src); got != 0 {
			t.Errorf("nested-call temp: got %d, want 0", got)
		}
	}
	// Bounded: the inner build(n) temp is reclaimed after the pointer-returning
	// dup, so the pipeline holds a flat high-water instead of leaking a list per
	// call. (Without the fix: 128 B/iter.)
	bump := func(n string) string {
		return `enum List { Cons(i32, List), Nil }
function build(n: i32): List { if (n == 0) { return Nil; } return Cons(n, build(n - 1)); }
function sum(l: List): i32 { match (l) { Cons(h, t) => { return h + sum(t); }, Nil => { return 0; } } }
function dup(xs: List): List { match (xs) { Cons(h, t) => { return Cons(h, dup(t)); }, Nil => { return Nil; } } }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < ` + n + `) { var u: i32 = sum(dup(build(5))); i = i + 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
	}
	if small, large := runWasm(t, bump("500")), runWasm(t, bump("5000")); small != large {
		t.Errorf("nested-call temp should be bounded: N=500 -> %d, N=5000 -> %d", small, large)
	}
}
