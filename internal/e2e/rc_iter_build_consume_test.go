package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Regression: an ITERATIVELY-built list (`acc = Cons(v, acc)` in a loop) that is
// later passed to a CONSUMING (`own`) match used to over-release N-1 times. The
// loop's self-referential reassignment MOVES the old `acc` into the new node's
// tail (a pointer store, no retaining inc) but then ALSO dropped it on the
// overwrite, pushing every tail node to rc 0. The exit-free deep-drop tolerated
// rc 0, but the consuming match's is_unique-gated free missed it and dec'd to
// -1. Fixed by suppressing the overwrite drop when the local is moved into the
// RHS construction. These pin value-correctness + zero over-release on every
// backend, and a bounded high-water (no leak from over-suppressing the drop).

const iterBuildConsumeSrc = `enum List { Cons(i32, List), Nil }
function eat(own xs: List): i32 {
    match (xs) { Cons(h, t) => { return h + eat(t); }, Nil => { return 0; } }
}
function ib(n: i32): List {
    var acc: List = Nil;
    var i: i32 = 0;
    while (i < n) { acc = Cons(2, acc); i = i + 1; }   // n nodes, each value 2
    return acc;
}
function main(): i32 {
    var total: i32 = 0;
    var i: i32 = 0;
    while (i < 50) {
        total = total + eat(ib(20));   // 20 nodes * 2 = 40 per iter
        i = i + 1;
    }
    if (total != 2000) { return 999; }
    return __rc_underflow_count();
}`

func TestX86_64IterBuildConsume(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, iterBuildConsumeSrc); code != 0 {
		t.Errorf("iter-build consumed: got %d, want 0", code)
	}
}

func TestArm64IterBuildConsume(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, iterBuildConsumeSrc); code != 0 {
		t.Errorf("iter-build consumed: got %d, want 0", code)
	}
}

func TestWASMIterBuildConsume(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	if got := runWasm(t, iterBuildConsumeSrc); got != 0 {
		t.Errorf("iter-build consumed: got %d, want 0", got)
	}
	// No leak from over-suppressing the drop: building + consuming an iter-built
	// list in a loop holds a flat high-water (each node reclaimed once).
	bump := func(n string) string {
		return `enum List { Cons(i32, List), Nil }
function eat(own xs: List): i32 { match (xs) { Cons(h, t) => { return h + eat(t); }, Nil => { return 0; } } }
function ib(n: i32): List { var acc: List = Nil; var i: i32 = 0; while (i < n) { acc = Cons(2, acc); i = i + 1; } return acc; }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < ` + n + `) { var u: i32 = eat(ib(10)); i = i + 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
	}
	if small, large := runWasm(t, bump("500")), runWasm(t, bump("5000")); small != large {
		t.Errorf("iter-build consume should be bounded: N=500 -> %d, N=5000 -> %d", small, large)
	}
}
