package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Koka-style consuming match on an OWNED-BY-DEFAULT enum parameter (#4400) —
// the counted-model sibling of the `own`-param consuming match
// (rc_own_match_test.go). `step` repacks its input list's head, so its param
// escapes (borrow inference leaves it OWNED) and the match consumes it: each
// arm releases the scrutinee box in place of the exit sweep — shallow-freed
// when unique (the extracted bindings inherit the payload counts), dup'd +
// dec'd when shared — and the arm bindings become counted owners reclaimed by
// the exit sweep. These programs are the feature's correctness gates: the
// UNIQUE path must stay garbage-free (bounded high-water), and the SHARED
// path must dup the binding (without the dup the binding's exit-sweep dec
// over-releases the tail a live list still holds → the underflow detector
// trips).

const ownedConsumingSrc = `enum List { Cons(i32, List), Nil }
function step(l: List): List {
    match (l) {
        Cons(h, t) => { return Cons(h + 1, t); },
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
    // UNIQUE path: fresh build -> step consumes (frees the head box in the
    // arm, repacks) -> the loop-var reinit drop reclaims l2 next iteration.
    var total: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var l2: List = step(build(5));           // [5..1] -> [6,4,3,2,1]
        total = total + sum(l2);                 // 16
        i = i + 1;
    }
    if (total != 3200) { return 999; }
    // SHARED path: keep aliases the box (call-site retain inc -> rc 2), so
    // step's arm takes the dup + flat-dec branch: out shares keep's tail,
    // keep survives the call, and both lists stay readable.
    var keep: List = build(3);        // [3,2,1]
    var out: List = step(keep);       // [4,2,1] sharing keep's tail
    var a: i32 = sum(keep);           // 6
    var b: i32 = sum(out);            // 7
    if (a != 6) { return 90; }
    if (b != 7) { return 91; }
    return __rc_underflow_count();
}`

func TestX86_64OwnedConsumingMatch(t *testing.T) {
	if out, code := compileAndRunX86_64FreeOn(t, ownedConsumingSrc); code != 0 {
		t.Errorf("owned consuming match: got %d, want 0 (999=unique-path value, 90/91=shared-path value, else underflow)\n%s", code, out)
	}
}

func TestArm64OwnedConsumingMatch(t *testing.T) {
	if out, code := compileAndRunArm64FreeOn(t, ownedConsumingSrc); code != 0 {
		t.Errorf("owned consuming match: got %d, want 0 (999=unique-path value, 90/91=shared-path value, else underflow)\n%s", code, out)
	}
}

func TestWASMOwnedConsumingMatch(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	if got := runWasm(t, ownedConsumingSrc); got != 0 {
		t.Errorf("owned consuming match: got %d, want 0", got)
	}
	// Heap-bump bound: the consuming step frees each head box in the arm
	// (recycled into the repack) and drain consumes the rest, so the
	// build-step-drain loop holds a bounded high-water instead of growing
	// with the iteration count.
	bump := func(n string) string {
		return `enum List { Cons(i32, List), Nil }
function step(l: List): List { match (l) { Cons(h, t) => { return Cons(h + 1, t); }, Nil => { return Nil; } } }
function sum(l: List): i32 { match (l) { Cons(h, t) => { return h + sum(t); }, Nil => { return 0; } } }
function build(n: i32): List { if (n == 0) { return Nil; } return Cons(n, build(n - 1)); }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < ` + n + `) {
        var l2: List = step(build(8));
        var unused: i32 = sum(l2);
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
	}
	small := runWasm(t, bump("2000"))
	large := runWasm(t, bump("20000"))
	if small != large {
		t.Errorf("consuming step should be bounded (boxes recycled): N=2000 -> %d, N=20000 -> %d", small, large)
	}
}
