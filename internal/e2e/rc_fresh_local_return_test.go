package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// A function that builds its result in a LOCAL and returns it — `var r =
// build(n); return r;` — is now proven param-free (returnsNoParamEscape's
// fresh-local extension), so an owned temp passed to it through a borrowed
// parameter is reclaimed rather than leaked, exactly like the direct-construction
// case. Pins value + zero over-release + a flat high-water on all backends.
const freshLocalSrc = `enum List { Cons(i32, List), Nil }
function build(n: i32): List { if (n == 0) { return Nil; } return Cons(n, build(n - 1)); }
function sum(l: List): i32 { match (l) { Cons(h, t) => { return h + sum(t); }, Nil => { return 0; } } }
function relay(xs: List): List { var r: List = build(4); return r; }   // ignores xs, returns a fresh local
function main(): i32 {
    var total: i32 = 0;
    var i: i32 = 0;
    while (i < 100) { total = total + sum(relay(build(3))); i = i + 1; }   // build(4) sum = 10
    if (total != 1000) { return 999; }
    return __rc_underflow_count();
}`

func TestX86_64FreshLocalReturn(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, freshLocalSrc); code != 0 {
		t.Errorf("fresh-local return: got %d, want 0", code)
	}
}

func TestArm64FreshLocalReturn(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, freshLocalSrc); code != 0 {
		t.Errorf("fresh-local return: got %d, want 0", code)
	}
}

func TestWASMFreshLocalReturn(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	if got := runWasm(t, freshLocalSrc); got != 0 {
		t.Errorf("fresh-local return: got %d, want 0", got)
	}
	// Bounded: the inner build(3) temp is reclaimed after relay (a pointer-
	// returning callee proven to return a fresh local), so the loop is flat.
	bump := func(n string) string {
		return `enum List { Cons(i32, List), Nil }
function build(n: i32): List { if (n == 0) { return Nil; } return Cons(n, build(n - 1)); }
function sum(l: List): i32 { match (l) { Cons(h, t) => { return h + sum(t); }, Nil => { return 0; } } }
function relay(xs: List): List { var r: List = build(4); return r; }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < ` + n + `) { var u: i32 = sum(relay(build(3))); i = i + 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
	}
	if small, large := runWasm(t, bump("500")), runWasm(t, bump("5000")); small != large {
		t.Errorf("fresh-local return should be bounded: N=500 -> %d, N=5000 -> %d", small, large)
	}
}
