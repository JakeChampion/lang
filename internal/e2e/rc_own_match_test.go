package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Consuming match on an `own` parameter — Perceus's headline FBIP result: a
// recursive traversal (`map`/`filter`/`length`) that reuses the structure's
// cells in place. Matching an owned scrutinee MOVES its pointer payloads into
// the arm bindings (reclaimed downstream) and shallow-frees the box, so the old
// input cell is freed each step and recycled by the freelist into the output
// cell — zero leak, bounded memory. `own`/consuming-match is opt-in and unused
// elsewhere, so these are the feature's correctness gate.

const ownMapSrc = `enum List { Cons(i32, List), Nil }
function map_inc(own xs: List): List {
    match (xs) {
        Cons(h, t) => { return Cons(h + 1, map_inc(t)); },
        Nil => { return Nil; },
    }
}
function sum(l: List): i32 {
    match (l) { Cons(h, t) => { return h + sum(t); }, Nil => { return 0; } }
}
function build(n: i32): List {
    if (n == 0) { return Nil; }
    return Cons(n, build(n - 1));
}
function main(): i32 {
    var total: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        total = total + sum(map_inc(build(5)));   // map+1 over [5..1] then sum = 20
        i = i + 1;
    }
    if (total != 4000) { return 999; }
    return __rc_underflow_count();
}`

func TestX86_64OwnConsumingMatch(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, ownMapSrc); code != 0 {
		t.Errorf("recursive map_inc: got %d, want 0", code)
	}
}

func TestArm64OwnConsumingMatch(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, ownMapSrc); code != 0 {
		t.Errorf("recursive map_inc: got %d, want 0", code)
	}
}

func TestWASMOwnConsumingMatch(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	if got := runWasm(t, ownMapSrc); got != 0 {
		t.Errorf("recursive map_inc: got %d, want 0", got)
	}
	// Heap-bump bound: the consuming traversal frees each input cell (recycled
	// into the output), so a build-map-sum loop holds a bounded high-water
	// instead of leaking a list per iteration.
	bump := func(n string) string {
		return `enum List { Cons(i32, List), Nil }
function map_inc(own xs: List): List { match (xs) { Cons(h,t) => { return Cons(h+1, map_inc(t)); }, Nil => { return Nil; } } }
function sum(l: List): i32 { match (l) { Cons(h,t) => { return h + sum(t); }, Nil => { return 0; } } }
function build(n: i32): List { if (n == 0) { return Nil; } return Cons(n, build(n - 1)); }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < ` + n + `) {
        var unused: i32 = sum(map_inc(build(8)));
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
	}
	small := runWasm(t, bump("2000"))
	large := runWasm(t, bump("20000"))
	if small != large {
		t.Errorf("consuming map should be bounded (cells recycled): N=2000 -> %d, N=20000 -> %d", small, large)
	}
}
