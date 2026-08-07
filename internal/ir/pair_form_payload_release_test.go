package ir_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// A pair-form Option/Result return hands back `(tag, payload)` in registers.
// When the payload is POINTER-shaped — isPairFormPayloadShape admits array,
// slice, struct and tuple — that register is the ONLY reference to a value the
// callee allocated: there is no box, so the box-reclaim path has nothing to
// free, and the match binds the pointer as a borrow. Nobody owns it, so
// `match (mk()) { Some(v) => { … } }` over a per-iteration-fresh `mk()` leaked
// the whole payload every iteration.
//
// Measured before the fix, `__heap_bump_bytes()` over 100 / 200 / 400 rounds:
// 3200 / 6400 / 12800 — exactly linear. After: a flat 32 at every round count,
// on x86-64, arm64 and wasm alike.
//
// These assert on the emitted op stream because the runtime symptom is a leak,
// which no suite gates (docs/TEST-GATES.md); the allocation-volume half of the
// contract is pinned separately in internal/e2e/alloc_scaling_test.go.

const pairPayloadSrc = `function mk(n: i32): Option[i32[]] {
    if (n == 0) { return None; }
    return Some([n, n + 1, n + 2]);
}
`

// releasesAfterMatch reports whether fn calls `helper` — the type-directed
// release emitOwnedSlotDrop picks for the binding — anywhere in its body.
func releasesAfterMatch(fn *ir.Func, helper string) bool {
	for _, op := range fn.Ops {
		if op.Kind == ir.OpCallDirect && op.Str == helper {
			return true
		}
	}
	return false
}

// The load-bearing case: the binding is only ever read through (`a[0]`), so
// the payload cannot outlive the arm and the arm releases it.
func TestPairFormPayloadReleasedWhenConfined(t *testing.T) {
	ip := lowerForTest(t, pairPayloadSrc+`
function main(): i32 {
    var t: i32 = 0;
    for i in 0..4 {
        match (mk(1)) { Some(a) => { t = t + a[0]; }, None => { }, }
    }
    return t;
}
`)
	if !ip.PairForm["mk"] {
		t.Fatal("mk is not pair-form; this test no longer covers the pair-form path")
	}
	if !releasesAfterMatch(funcByName(ip, "main"), "__fern_arr_dec") {
		t.Error("main: the array payload of `match (mk(1))` is never released — it is the only reference to a fresh allocation, so every iteration leaks it")
	}
}

// The binding escapes the arm, so the release must NOT happen: dropping here
// would free a buffer the caller still holds. A leak is the safe direction.
func TestPairFormPayloadKeptWhenBindingEscapes(t *testing.T) {
	ip := lowerForTest(t, pairPayloadSrc+`
function main(): i32 {
    var kept: i32[] = [];
    for i in 0..4 {
        match (mk(1)) { Some(a) => { kept = a; }, None => { }, }
    }
    return kept[0];
}
`)
	if !ip.PairForm["mk"] {
		t.Fatal("mk is not pair-form; this test no longer covers the pair-form path")
	}
	if releasesAfterMatch(funcByName(ip, "main"), "__fern_arr_dec") {
		t.Error("main: `kept = a` lets the payload outlive the arm, but the arm releases it anyway — a use-after-free")
	}
}

// A `return` of the binding is the other escape, and it is what makes the
// whitelist in pairFormPayloadConfined worth having: the name appears in a
// position the walk does not recognise, so the release is declined.
func TestPairFormPayloadKeptWhenBindingReturned(t *testing.T) {
	ip := lowerForTest(t, pairPayloadSrc+`
function keepit(n: i32): i32[] {
    match (mk(n)) { Some(a) => { return a; }, None => { return [0]; }, }
}

function main(): i32 { return keepit(7)[0]; }
`)
	if releasesAfterMatch(funcByName(ip, "keepit"), "__fern_arr_dec") {
		t.Error("keepit: the arm returns the payload, but the arm releases it first — the caller gets freed memory")
	}
}

// A callee whose payload may ALIAS a parameter is not proven fresh, so there
// is no reference to release. returnsNoParamEscape is what rules it out, and
// it carries the whole freshness argument here: with no box there is no
// return-transfer inc and no is_unique gate to fall back on.
func TestPairFormPayloadKeptWhenCalleeMayAliasParam(t *testing.T) {
	ip := lowerForTest(t, `
function pick(xs: i32[], n: i32): Option[i32[]] {
    if (n == 0) { return None; }
    return Some(xs);
}

// xs is a PARAMETER here, so it is borrowed and carries no exit-sweep drop
// of its own — any __fern_arr_dec in this function is the arm's release.
function consume(xs: i32[]): i32 {
    var t: i32 = 0;
    for i in 0..4 {
        match (pick(xs, 1)) { Some(a) => { t = t + a[0]; }, None => { }, }
    }
    return t;
}

function main(): i32 {
    var xs: i32[] = [1, 2, 3];
    return consume(xs);
}
`)
	if releasesAfterMatch(funcByName(ip, "consume"), "__fern_arr_dec") {
		t.Error("consume: pick returns its own parameter, so the arm is releasing xs — the next iteration reads freed memory")
	}
}
