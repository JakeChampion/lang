package ir_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// #4400 — Koka-style consuming match on an OWNED-BY-DEFAULT enum parameter:
// the per-arm scrutinee release (emitOwnedConsumingArmDrop) frees / decs the
// box inside each unguarded arm and then ZEROES the param slot so the exit
// sweep no-ops. The zero-store to the param's slot is the transform's
// unambiguous signature: nothing else stores a constant 0 to a param slot
// (params are never zero-inited, and these sources never reassign them), so
// counting `OpConstI32 0; OpStoreLocal <param>` pairs pins exactly which arms
// consume.

// zeroStoreCount counts `OpConstI32 0` immediately followed by
// `OpStoreLocal slot` — the consuming-arm "param is dead" stamp.
func zeroStoreCount(fn *ir.Func, slot int32) int {
	n := 0
	for i := 1; i < len(fn.Ops); i++ {
		if fn.Ops[i].Kind == ir.OpStoreLocal && fn.Ops[i].I32 == slot &&
			fn.Ops[i-1].Kind == ir.OpConstI32 && fn.Ops[i-1].I32 == 0 {
			n++
		}
	}
	return n
}

// step's param ESCAPES (its payloads flow into the returned construction), so
// borrow inference leaves it OWNED — the callee reclaims it. The match is the
// param's last use, outside any loop: both arms consume (the Cons arm frees /
// decs the heap box, the Nil arm's release no-ops on the sentinel via the
// is_unique + dec guards), so the param slot is zeroed twice.
const ownedStepSrc = `enum List { Cons(i32, List), Nil }
function step(l: List): List {
    match (l) {
        Cons(h, t) => { return Cons(h + 1, t); },
        Nil => { return Nil; },
    }
}
function main(): i32 { return 0; }`

func TestOwnedConsumingMatchFires(t *testing.T) {
	f := funcByName(lowerForTest(t, ownedStepSrc), "step")
	if f == nil {
		t.Fatal("no func step")
	}
	if got := zeroStoreCount(f, 0); got != 2 {
		t.Errorf("consuming owned match: both arms should zero the param slot, got %d zero-stores", got)
	}
}

// Inside a loop the release would re-run on an already-freed box, so the
// analysis refuses; the box stays with the exit sweep and the param slot is
// never zeroed.
func TestOwnedConsumingMatchBlockedInLoop(t *testing.T) {
	src := `enum List { Cons(i32, List), Nil }
function step(l: List): List {
    var i: i32 = 0;
    while (i < 1) {
        i = i + 1;
        match (l) {
            Cons(h, t) => { return Cons(h + 1, t); },
            Nil => { return Nil; },
        }
    }
    return Nil;
}
function main(): i32 { return 0; }`
	f := funcByName(lowerForTest(t, src), "step")
	if f == nil {
		t.Fatal("no func step")
	}
	if got := zeroStoreCount(f, 0); got != 0 {
		t.Errorf("match inside loop must not consume, got %d zero-stores", got)
	}
}

// A scrutinee read AFTER the match is not at its last use — consuming it (and
// zeroing the slot) would strand the later read, so the analysis refuses.
func TestOwnedConsumingMatchBlockedByLaterUse(t *testing.T) {
	src := `enum List { Cons(i32, List), Nil }
function peek(l: List): List {
    var n: i32 = 0;
    match (l) {
        Cons(h, t) => { n = h; },
        Nil => { n = 0; },
    }
    if (n > 3) { return l; }
    return Nil;
}
function main(): i32 { return 0; }`
	f := funcByName(lowerForTest(t, src), "peek")
	if f == nil {
		t.Fatal("no func peek")
	}
	if got := zeroStoreCount(f, 0); got != 0 {
		t.Errorf("scrutinee used after match must not consume, got %d zero-stores", got)
	}
}

// A GUARDED arm never releases (a guard-false fall-through must leave the box
// intact for the next arm), so its bindings stay borrows — and since it here
// re-binds the same `t` the unguarded arm needs as a counted owner, the name
// is inadmissible and the WHOLE match falls back to the exit sweep (a release
// with an untracked pointer payload would strand its count).
func TestOwnedConsumingMatchGuardedRebindBlocks(t *testing.T) {
	src := `enum List { Cons(i32, List), Nil }
function step(l: List): List {
    match (l) {
        Cons(h, t) when h > 3 => { return Cons(h + 1, t); },
        Cons(h, t) => { return Cons(h, t); },
        Nil => { return Nil; },
    }
}
function main(): i32 { return 0; }`
	f := funcByName(lowerForTest(t, src), "step")
	if f == nil {
		t.Fatal("no func step")
	}
	if got := zeroStoreCount(f, 0); got != 0 {
		t.Errorf("guarded re-bind of t must block the consuming match, got %d zero-stores", got)
	}
}

// A `_` at a pointer payload position discards the tail — releasing the box
// would strand the tail's whole sub-tree (today's exit sweep reclaims it), so
// the match must fall back.
func TestOwnedConsumingMatchWildcardPointerPayloadBlocks(t *testing.T) {
	src := `enum List { Cons(i32, List), Nil }
function head_only(l: List): List {
    match (l) {
        Cons(h, _) => { return Cons(h, Nil); },
        Nil => { return Nil; },
    }
}
function main(): i32 { return 0; }`
	f := funcByName(lowerForTest(t, src), "head_only")
	if f == nil {
		t.Fatal("no func head_only")
	}
	if got := zeroStoreCount(f, 0); got != 0 {
		t.Errorf("discarded pointer payload must block the consuming match, got %d zero-stores", got)
	}
}
