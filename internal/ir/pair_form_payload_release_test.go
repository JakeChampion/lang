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

// releasesBoundPayload reports whether fn releases the slot holding the arm's
// BINDING, which is the arm dropping a payload that escaped it.
//
// "Any release anywhere" is too coarse to express that. When the binding
// escapes into a local, that local becomes an owner and reclaims its OWN prior
// value at the overwrite — a release with nothing to do with the binding, in a
// function that contains no arm release at all. Telling them apart needs the
// operand, and both shapes carry it in the op immediately around the call:
//
//	local.load <binding>; rc.inc                    the escaping assignment's inc
//	local.load <slot>; const.i32 <stride>; call     a release of <slot>
//
// The binding is the slot alias-inc'd as a SOURCE — the assignment incs what it
// reads, and decs what it overwrites, so the two slots are never the same one.
// A release of an inc'd-as-source slot is therefore the arm dropping the
// binding; a release of any other slot is a destination reclaiming itself.
func releasesBoundPayload(fn *ir.Func, helper string) bool {
	bindings := map[int32]bool{}
	for i, op := range fn.Ops {
		if op.Kind == ir.OpRcInc && i > 0 && fn.Ops[i-1].Kind == ir.OpLoadLocal {
			bindings[fn.Ops[i-1].I32] = true
		}
	}
	for i, op := range fn.Ops {
		if op.Kind != ir.OpCallDirect || op.Str != helper {
			continue
		}
		if i < 2 || fn.Ops[i-2].Kind != ir.OpLoadLocal {
			// Shape not recognised — report rather than pass silently.
			return true
		}
		if bindings[fn.Ops[i-2].I32] {
			return true
		}
	}
	return false
}

// releasesBoundPayload is a hand-written op-stream matcher, so its own
// discrimination is pinned here rather than assumed: a sharpened predicate that
// answered "no" to everything would make every escape test above vacuous.
func TestReleasesBoundPayloadDiscriminates(t *testing.T) {
	// `local.load 6; rc.inc` marks slot 6 as the binding.
	bind := []ir.Op{
		{Kind: ir.OpLoadLocal, I32: 6},
		{Kind: ir.OpRcInc, Str: "__fern_rc_inc"},
	}
	rel := func(slot int32) []ir.Op {
		return []ir.Op{
			{Kind: ir.OpLoadLocal, I32: slot},
			{Kind: ir.OpConstI32, I32: 4},
			{Kind: ir.OpCallDirect, Str: "__fern_arr_dec", I32: 2},
		}
	}
	armReleases := &ir.Func{Ops: append(append([]ir.Op{}, bind...), rel(6)...)}
	if !releasesBoundPayload(armReleases, "__fern_arr_dec") {
		t.Error("a release of the bound slot must be reported — the escape tests rest on this")
	}
	destReleases := &ir.Func{Ops: append(append([]ir.Op{}, bind...), rel(0)...)}
	if releasesBoundPayload(destReleases, "__fern_arr_dec") {
		t.Error("a release of the DESTINATION slot is that local reclaiming its own prior value, not an arm release")
	}
	unknownShape := &ir.Func{Ops: []ir.Op{{Kind: ir.OpCallDirect, Str: "__fern_arr_dec", I32: 2}}}
	if !releasesBoundPayload(unknownShape, "__fern_arr_dec") {
		t.Error("an unrecognised release shape must be reported, not passed silently")
	}
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
	if releasesBoundPayload(funcByName(ip, "main"), "__fern_arr_dec") {
		t.Error("main: `kept = a` lets the payload outlive the arm, but the arm releases it anyway — a use-after-free")
	}
}

// A `return` of the binding is the other escape, and it is what makes the
// whitelist in bindingConfinedToArm worth having: the name appears in a
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

// `a.len()` is `__method_Array_len(a)` after the checker's method rewrite, so
// the occurrence sits in an ARGUMENT list and matched neither read shape the
// whitelist recognised (#6409). A borrowing call cannot let the pointer
// outlive the arm, so it is excused and the payload is released.
func TestPairFormPayloadReleasedThroughBorrowingCall(t *testing.T) {
	ip := lowerForTest(t, pairPayloadSrc+`
function total(a: i32[]): i32 { var s: i32 = 0; for i in 0..a.len() { s = s + a[i]; } return s; }

function main(): i32 {
    var t: i32 = 0;
    for i in 0..4 {
        match (mk(1)) { Some(a) => { t = t + a.len(); }, None => { }, }
        match (mk(1)) { Some(a) => { t = t + total(a); }, None => { }, }
        match (mk(1)) { Some(a) => { t = t + a[0] + a.len(); }, None => { }, }
    }
    return t;
}
`)
	if !ip.PairForm["mk"] {
		t.Fatal("mk is not pair-form; this test no longer covers the pair-form path")
	}
	if !releasesAfterMatch(funcByName(ip, "main"), "__fern_arr_dec") {
		t.Error("main: an arm body that only borrows the payload through a call never releases it — every iteration leaks the whole array")
	}
}

// The other side of the same widening: a callee that hands the argument BACK
// is not a borrow, so the occurrence stays unexcused. `resultCannotAliasArg`
// is what rules it out, and it is the gate whose loosening segfaulted the
// differential oracle when the stage-(b) arg reclaim tried the same move.
func TestPairFormPayloadKeptWhenCallReturnsTheArgument(t *testing.T) {
	ip := lowerForTest(t, pairPayloadSrc+`
function ident(a: i32[]): i32[] { return a; }

function main(): i32 {
    var kept: i32[] = [];
    for i in 0..4 {
        match (mk(1)) { Some(a) => { kept = ident(a); }, None => { }, }
    }
    return kept[0];
}
`)
	if releasesAfterMatch(funcByName(ip, "main"), "__fern_arr_dec") {
		t.Error("main: ident hands the payload back into `kept`, but the arm releases it anyway — a use-after-free")
	}
}
