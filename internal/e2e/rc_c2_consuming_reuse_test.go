package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// C2 — true zero-alloc FBIP. A consuming match (`match (own xs) { Cons(h,t) =>
// Cons(g(h), self(t)), Nil => Nil }`) whose arm rebuilds a same-enum variant
// now hands the consumed scrutinee box STRAIGHT to that construction via the
// reuse token, instead of shallow-freeing it and allocating a fresh box (C1).
// Memory is already flat under C1 (the freelist recycles), so C2's win is
// avoiding the free→alloc round-trip; correctness is the contract these pin:
// value + no over-release on all backends, and reuse-on == reuse-off (C2 is a
// pure optimisation over the C1 baseline).

const c2ConsumingSrc = `enum List { Cons(i32, List), Nil }
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
        total = total + sum(map_inc(build(5)));   // [5..1] +1 each, sum = 20
        i = i + 1;
    }
    if (total != 4000) { return 999; }
    return __rc_underflow_count();
}`

func TestX86_64C2ConsumingReuse(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, c2ConsumingSrc); code != 0 {
		t.Errorf("C2 consuming reuse: got %d, want 0", code)
	}
}

func TestArm64C2ConsumingReuse(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, c2ConsumingSrc); code != 0 {
		t.Errorf("C2 consuming reuse: got %d, want 0", code)
	}
}

func TestWASMC2ConsumingReuse(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	if got := runWasm(t, c2ConsumingSrc); got != 0 {
		t.Errorf("C2 consuming reuse: got %d, want 0", got)
	}
}

// reuse-on == reuse-off: C2 is a pure optimisation over the C1 free+alloc
// baseline. Both must be value-correct with zero over-release.
func withReuse(v bool, fn func()) {
	prev := ast.RcReuseEnabled
	ast.RcReuseEnabled = v
	defer func() { ast.RcReuseEnabled = prev }()
	fn()
}

func TestX86_64C2ReuseMatchesNoReuse(t *testing.T) {
	var on, off int
	withReuse(true, func() { _, on = compileAndRunX86_64FreeOn(t, c2ConsumingSrc) })
	withReuse(false, func() { _, off = compileAndRunX86_64FreeOn(t, c2ConsumingSrc) })
	if on != off || on != 0 {
		t.Errorf("C2 reuse on=%d off=%d, want both 0", on, off)
	}
}

func TestArm64C2ReuseMatchesNoReuse(t *testing.T) {
	var on, off int
	withReuse(true, func() { _, on = compileAndRunArm64FreeOn(t, c2ConsumingSrc) })
	withReuse(false, func() { _, off = compileAndRunArm64FreeOn(t, c2ConsumingSrc) })
	if on != off || on != 0 {
		t.Errorf("C2 reuse on=%d off=%d, want both 0", on, off)
	}
}
