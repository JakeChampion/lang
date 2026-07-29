package ir_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ir"
)

// Phase 5e enum drop-reuse. A self-overwrite of an owned enum local with
// a freshly constructed, payload-carrying variant of the same enum
// lowers through __alloc_reuse when the enum's variants share one box
// size (uniformEnumBoxSize) — the box is reused in place when uniquely
// owned. Reuses allocReuseCount from struct_reuse_test.go.
//
// The reuse fires for FREE-ELIGIBLE enum locals; pointer-payload enums
// constructed from non-literal args (array literals, etc.) are eligible,
// which is the allocation-heavy case where reuse matters. Scalar enums
// built from literal args (`Fwd(0)`) are conservatively tainted by
// rhsTainted's default rule and so aren't eligible — they currently leak
// their box anyway, so not reusing them is safe (a missed optimisation,
// covered by TestEnumReuseSkipsLiteralScalar below).

// A uniform pointer-payload enum self-overwritten in a loop reuses its
// box once. The box is reclaimed in place every iteration instead of a
// fresh alloc + the baseline's orphaned old box.
func TestEnumReuseFiresForPointerPayload(t *testing.T) {
	ip := lowerForTest(t, `enum Bag { Keep(i32[]), Swap(i32[]) }
function churn(n: i32): i32 {
    var b: Bag = Keep([0, 0]);
    var i: i32 = 0;
    while (i < n) {
        b = Keep([i, i]);
        i = i + 1;
    }
    return 0;
}
function main(): i32 { return churn(3); }`)
	f := funcByName(ip, "churn")
	if f == nil {
		t.Fatal("no func churn")
	}
	if got := allocReuseCount(f); got != 1 {
		t.Errorf("uniform pointer-payload enum should emit one __alloc_reuse, got %d", got)
	}
}

// Cross-variant reuse is sound for a uniform enum: the old box may hold a
// different variant than the one being constructed, but uniformEnumBoxSize
// guarantees identical box size, so the new variant always fits. Here the
// loop alternates Keep / Swap and both reuse the same box shape.
func TestEnumReuseFiresAcrossVariants(t *testing.T) {
	ip := lowerForTest(t, `enum Bag { Keep(i32[]), Swap(i32[]) }
function churn(n: i32): i32 {
    var b: Bag = Keep([0, 0]);
    var i: i32 = 0;
    while (i < n) {
        b = Keep([i, i]);
        b = Swap([i, i]);
        i = i + 1;
    }
    return 0;
}
function main(): i32 { return churn(3); }`)
	f := funcByName(ip, "churn")
	if f == nil {
		t.Fatal("no func churn")
	}
	if got := allocReuseCount(f); got != 2 {
		t.Errorf("cross-variant uniform reuse should emit two __alloc_reuse, got %d", got)
	}
}

// Variants that disagree on box size (A carries one array, B carries two)
// have no statically known reuse size, so reuse stays off even though the
// local is free-eligible — proving the size gate, not eligibility, is
// what stops it here.
func TestEnumReuseSkipsNonUniformBoxSize(t *testing.T) {
	ip := lowerForTest(t, `enum V { A(i32[]), B(i32[], i32[]) }
function churn(n: i32): i32 {
    var v: V = A([0]);
    var i: i32 = 0;
    while (i < n) {
        v = A([i]);
        i = i + 1;
    }
    return 0;
}
function main(): i32 { return churn(3); }`)
	f := funcByName(ip, "churn")
	if f == nil {
		t.Fatal("no func churn")
	}
	if got := allocReuseCount(f); got != 0 {
		t.Errorf("non-uniform enum must not reuse, got %d __alloc_reuse", got)
	}
}

// Constructing a payloadless variant yields a shared static sentinel, not
// a heap box, so there's nothing to reuse (the RHS isn't even a
// constructor Call — it lowers as an EnumLit).
func TestEnumReuseSkipsPayloadlessConstruction(t *testing.T) {
	ip := lowerForTest(t, `enum Box2 { Full(i32[]), Empty }
function churn(n: i32): i32 {
    var o: Box2 = Full([0]);
    var i: i32 = 0;
    while (i < n) {
        o = Empty;
        i = i + 1;
    }
    return 0;
}
function main(): i32 { return churn(3); }`)
	f := funcByName(ip, "churn")
	if f == nil {
		t.Fatal("no func churn")
	}
	if got := allocReuseCount(f); got != 0 {
		t.Errorf("payloadless construction must not reuse, got %d __alloc_reuse", got)
	}
}

// A borrowed parameter can be rc==1 while the caller still holds it, so
// reusing its box in place would corrupt the caller's value. For a REASSIGNED
// param the guard is not "never emit the reuse" but the pair that makes it
// sound: computeConsumedParams promotes such a param, the promotion's entry inc
// holds the slot at rc>=2 for as long as the caller holds the value, and the
// reuse's runtime is_unique gate therefore declines and allocates fresh. The
// gate only opens from the second overwrite on — on a box the callee itself
// built, which is the loop-accumulator case reuse exists for.
//
// The same entry inc is what balances the reassignment's overwrite dec; without
// it that dec releases a reference the caller never handed over, and the
// caller's value is freed early through a live alias
// (TestX86_64UnionThreadedParam).
func TestEnumReuseParamGuardedByEntryInc(t *testing.T) {
	ip := lowerForTest(t, `enum Bag { Keep(i32[]), Swap(i32[]) }
function bump(b: Bag): Bag {
    b = Keep([1]);
    return b;
}
function main(): i32 { return 0; }`)
	f := funcByName(ip, "bump")
	if f == nil {
		t.Fatal("no func bump")
	}
	if got := allocReuseCount(f); got != 1 {
		t.Errorf("want the is_unique-gated reuse on the reassigned param, got %d __alloc_reuse", got)
	}
	gated, inced := false, false
	for _, op := range f.Ops {
		if op.Kind == ir.OpRcIsUnique {
			gated = true
		}
		if op.Kind == ir.OpRcInc {
			inced = true
		}
	}
	if !gated {
		t.Error("param reuse must be is_unique-gated at runtime; found no OpRcIsUnique")
	}
	if !inced {
		t.Error("promoted param must be entry-inc'd to balance its overwrite dec; found no OpRcInc")
	}
}

// A scalar enum constructed from a literal arg (`Keep(0)`) is tainted by
// rhsTainted's conservative default and so isn't free-eligible — reuse
// stays off. Documents the current eligibility boundary (safe: such
// boxes leak under the baseline too, so skipping reuse loses nothing).
func TestEnumReuseSkipsLiteralScalar(t *testing.T) {
	const src = `enum Step { Fwd(i32), Bwd(i32) }
function churn(n: i32): i32 {
    var s: Step = Fwd(0);
    var i: i32 = 0;
    while (i < n) {
        s = Fwd(i);
        i = i + 1;
    }
    return 0;
}
function main(): i32 { return churn(3); }`
	prev := ast.EnumRcPayloads
	defer func() { ast.EnumRcPayloads = prev }()

	// Move model: a scalar-payload enum is not free-eligible, so the
	// self-overwrite reuse is skipped.
	ast.EnumRcPayloads = false
	if got := allocReuseCount(funcByName(lowerForTest(t, src), "churn")); got != 0 {
		t.Errorf("move model: expected 0 reuse, got %d", got)
	}

	// EnumRcPayloads (Slice 1b): the enum now rc-counts its payloads, becoming
	// free-eligible, so `s = Fwd(i)` reuses its box in place each iteration —
	// the FBIP win the model unblocks. Sound: the byte-identical differential
	// gate pins rc-on == move-model output.
	ast.EnumRcPayloads = true
	if got := allocReuseCount(funcByName(lowerForTest(t, src), "churn")); got == 0 {
		t.Errorf("rc model: expected the eligible enum to reuse its box, got 0")
	}
}
