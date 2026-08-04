package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Tuple reclamation (RC-Perceus) — the sibling of rc_heap_bump_test.go
// for tuple loop-body vars. A tuple is heap-boxed with an rc header, so
// a `var t = (a, b)` re-declared in a loop reuses one slot across
// iterations; before this slice emitVarReinitDropOld SKIPPED TupleType,
// so every prior iteration's box (and its rc-tracked elements) leaked
// and the bump high-water grew linearly with N. The dec-on-reinit now
// routes a tuple through the exit sweep's deep-drop (via the generated
// __drop_tuple_<mangled> fn for rc-tracked elements, a plain box_free
// otherwise), so the mark stays FLAT regardless of iteration count.
//
// Two programs differing only in iteration count must report the SAME
// bump growth; a regression that stopped reclaiming tuple boxes would
// make the larger run grow proportionally and the counts diverge.

// tupleBumpGrowthSrc builds an n-iteration loop whose body re-declares a
// plain (i32, i32) tuple — exercises emitTupleSlotDrop's is_unique-gated
// box_free path (no rc-tracked element to deep-drop).
func tupleBumpGrowthSrc(n string) string {
	return `function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var sum: i32 = 0;
    while (i < ` + n + `) {
        var p: (i32, i32) = (i, i + 1);
        sum = sum + p.0 + p.1;
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// tupleArrBumpGrowthSrc re-declares a (i32[], i32) tuple — exercises the
// deep-drop path: the generated __drop_tuple_ fn frees the array buffer
// AND the tuple box on the box's last reference. Without the per-element
// deep drop this leaks the array buffer in addition to the box.
func tupleArrBumpGrowthSrc(n string) string {
	return `function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var sum: i32 = 0;
    while (i < ` + n + `) {
        var p: (i32[], i32) = ([i, i + 1, i + 2], i);
        sum = sum + p.1;
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func TestX86_64TupleHeapBumpBounded(t *testing.T) {
	small := mustRunX86_64FreeOn(t, tupleBumpGrowthSrc("50"))
	large := mustRunX86_64FreeOn(t, tupleBumpGrowthSrc("5000"))
	if small != large {
		t.Errorf("plain-tuple bump growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d (a leak would grow with N)", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water (one box), got 0")
	}
	asmall := mustRunX86_64FreeOn(t, tupleArrBumpGrowthSrc("50"))
	alarge := mustRunX86_64FreeOn(t, tupleArrBumpGrowthSrc("5000"))
	if asmall != alarge {
		t.Errorf("tuple-of-array bump growth should be bounded (deep-drop): N=50 -> %d, N=5000 -> %d", asmall, alarge)
	}
}

func TestArm64TupleHeapBumpBounded(t *testing.T) {
	small := mustRunArm64FreeOn(t, tupleBumpGrowthSrc("50"))
	large := mustRunArm64FreeOn(t, tupleBumpGrowthSrc("5000"))
	if small != large {
		t.Errorf("plain-tuple bump growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water (one box), got 0")
	}
	asmall := mustRunArm64FreeOn(t, tupleArrBumpGrowthSrc("50"))
	alarge := mustRunArm64FreeOn(t, tupleArrBumpGrowthSrc("5000"))
	if asmall != alarge {
		t.Errorf("tuple-of-array bump growth should be bounded (deep-drop): N=50 -> %d, N=5000 -> %d", asmall, alarge)
	}
}

func TestWASMTupleHeapBumpBounded(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	small := runWasm(t, tupleBumpGrowthSrc("50"))
	large := runWasm(t, tupleBumpGrowthSrc("5000"))
	if small != large {
		t.Errorf("plain-tuple bump growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water (one box), got 0")
	}
	asmall := runWasm(t, tupleArrBumpGrowthSrc("50"))
	alarge := runWasm(t, tupleArrBumpGrowthSrc("5000"))
	if asmall != alarge {
		t.Errorf("tuple-of-array bump growth should be bounded (deep-drop): N=50 -> %d, N=5000 -> %d", asmall, alarge)
	}
}
