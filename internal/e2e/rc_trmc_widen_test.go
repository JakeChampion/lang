package e2e

import "testing"

// TRMC beyond the canonical `match (xs) { Cons(h,t) => Cons(g(h), self(t)),
// Nil => Nil }` (#5344). Each function here exercises one of the widened
// detector shapes and is checked for VALUE correctness — the hole-passing
// rewrite reorders when each node's payload is written, so a mis-placed hole
// or a mis-ordered arm chain shows up as a wrong list rather than a crash.
//
// `withTrmc` + the MatchesNoTrmc legs are the real gate: the same source must
// produce the same answer with the transform off, so a widened shape that
// lowers wrong cannot hide behind a self-consistent expectation.
//
// build_signed(6) = [-5, 4, -3, 2, -1, 0] (sum -3).
const trmcWidenSrc = `enum List { Cons(i32, List), Nil }
enum Rev { Node(Rev, i32), End }

function build_signed(n: i32): List {
    var acc: List = Nil;
    var i: i32 = 0;
    var s: i32 = 1;
    while (i < n) { acc = Cons(i * s, acc); s = 0 - s; i = i + 1; }
    return acc;
}
function sum(l: List): i32 {
    var acc: i32 = 0;
    var cur: List = l;
    var go: boolean = true;
    while (go) { match (cur) { Cons(h, t) => { acc = acc + h; cur = t; }, Nil => { go = false; } } }
    return acc;
}
function sum_rev(r: Rev): i32 {
    var acc: i32 = 0;
    var cur: Rev = r;
    var go: boolean = true;
    while (go) { match (cur) { Node(nx, v) => { acc = acc + v; cur = nx; }, End => { go = false; } } }
    return acc;
}

// Setup statements before the match, one of them an early return.
function take_inc(xs: List, n: i32): List {
    var lim: i32 = n;
    if (lim <= 0) { return Nil; }
    match (xs) {
        Cons(h, t) => { return Cons(h + 1, take_inc(t, lim - 1)); },
        Nil => { return Nil; },
    }
}

// A guarded arm, statements before each arm tail, and a wildcard base arm.
function abs_scale(xs: List): List {
    match (xs) {
        Cons(h, t) when h < 0 => { var p: i32 = 0 - h; return Cons(p * 2, abs_scale(t)); },
        Cons(h, t) => { var d: i32 = h * 2; return Cons(d, abs_scale(t)); },
        _ => { return Nil; },
    }
}

// A tail if/else whose true leaf is a BARE self-call: the filter shape, where
// the hole stays put and only the parameters advance.
function drop_neg(xs: List): List {
    match (xs) {
        Cons(h, t) => { if (h < 0) { return drop_neg(t); } else { return Cons(h, drop_neg(t)); } },
        Nil => { return Nil; },
    }
}

// A guard clause inside the arm body, whose return has to reach the hole
// machinery rather than the function's real return.
function until_zero(xs: List): List {
    match (xs) {
        Cons(h, t) => { if (h == 0) { return Nil; } return Cons(h, until_zero(t)); },
        Nil => { return Nil; },
    }
}

// The hole in the FIRST payload rather than the last.
function to_rev(xs: List): Rev {
    match (xs) {
        Cons(h, t) => { return Node(to_rev(t), h + 1); },
        Nil => { return End; },
    }
}

function main(): i32 {
    var xs: List = build_signed(6);
    if (sum(xs) != 0 - 3) { return 1; }
    if (sum(abs_scale(xs)) != 30) { return 2; }        // [10,8,6,4,2,0]
    if (sum(drop_neg(xs)) != 6) { return 3; }          // [4,2,0]
    if (sum(take_inc(xs, 3)) != 0 - 1) { return 4; }   // [-4,5,-2]
    if (sum(until_zero(xs)) != 0 - 3) { return 5; }    // [-5,4,-3,2,-1]
    if (sum_rev(to_rev(xs)) != 3) { return 6; }        // [-4,5,-2,3,0,1]
    return __rc_underflow_count();
}`

func TestX86_64TrmcWidened(t *testing.T) {
	var on, off int
	withTrmc(true, func() { _, on = compileAndRunX86_64FreeOn(t, trmcWidenSrc) })
	withTrmc(false, func() { _, off = compileAndRunX86_64FreeOn(t, trmcWidenSrc) })
	if on != 0 || off != 0 {
		t.Errorf("widened TRMC on=%d off=%d, want both 0", on, off)
	}
}

func TestArm64TrmcWidened(t *testing.T) {
	var on, off int
	withTrmc(true, func() { _, on = compileAndRunArm64FreeOn(t, trmcWidenSrc) })
	withTrmc(false, func() { _, off = compileAndRunArm64FreeOn(t, trmcWidenSrc) })
	if on != 0 || off != 0 {
		t.Errorf("widened TRMC on=%d off=%d, want both 0", on, off)
	}
}

func TestWASMTrmcWidened(t *testing.T) {
	var on, off int
	withTrmc(true, func() { on = runWasm(t, trmcWidenSrc) })
	withTrmc(false, func() { off = runWasm(t, trmcWidenSrc) })
	if on != 0 || off != 0 {
		t.Errorf("widened TRMC on=%d off=%d, want both 0", on, off)
	}
}

// The O(1)-stack win has to reach the widened shapes too: `drop_neg` mixes a
// bare tail self-call with a constructor-wrapped one, and only the second
// builds a node — the loop must still hold the stack flat across both.
const trmcWidenDeepSrc = `enum List { Cons(i32, List), Nil }
function drop_neg(xs: List): List {
    match (xs) {
        Cons(h, t) => { if (h < 0) { return drop_neg(t); } else { return Cons(h + 1, drop_neg(t)); } },
        Nil => { return Nil; },
    }
}
function build(n: i32): List { var acc: List = Nil; var i: i32 = 0; while (i < n) { acc = Cons(1, acc); i = i + 1; } return acc; }
function sum(l: List): i32 { var acc: i32 = 0; var cur: List = l; var go: boolean = true; while (go) { match (cur) { Cons(h, t) => { acc = acc + h; cur = t; }, Nil => { go = false; } } } return acc; }
function main(): i32 {
    if (sum(drop_neg(build(300000))) != 600000) { return 1; }
    return 0;
}`

func TestX86_64TrmcWidenedDeepStack(t *testing.T) {
	var on, off int
	withTrmc(true, func() {
		bin, _ := compileX86_64FreeOn(t, trmcWidenDeepSrc)
		on = runWithStackLimit(t, 16*1024, bin)
	})
	withTrmc(false, func() {
		bin, _ := compileX86_64FreeOn(t, trmcWidenDeepSrc)
		off = runWithStackLimit(t, 16*1024, bin)
	})
	if on != 0 {
		t.Errorf("TRMC on: deep filter should succeed, got %d", on)
	}
	if off == 0 {
		t.Errorf("TRMC off: deep filter should overflow the stack, but got 0 (TRMC may not be the reason on succeeds)")
	}
}
