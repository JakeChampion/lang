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

// The string-element sibling (#6879), and the tuple half of the struct fix in
// #6499: the exit sweep's INLINE tuple arm released a native single-word
// string element with a bare __fern_rc_dec, which decrements and never frees,
// so the buffer's count went 1 -> 0 and it was stranded — 64 B a round on
// x86-64 while arm64 and wasm (two-word ABIs, __fern_str_dec) were already
// flat.
//
// The binding has to be in a CALLEE: a loop-scoped `var t` re-declared in the
// body reclaims through emitVarReinitDropOld, which routes to the generated
// __drop_tuple_<mangled> — and that body has always called __fern_str_dec, so
// the loop spelling was flat throughout. Only the function-exit sweep was
// short, which is why the two spellings measured apart is what identifies it.
//
// The program reports the per-round bytes as its exit code (64 pre-fix on
// x86-64, 0 after) rather than a bound, so a partial regression is legible.
const tupleStrElemChurnSrc = `import "std/i32";
function wide(k: i32): string { return "a-value-well-past-the-inline-threshold-" + k.to_string(); }
function probe(k: i32): i32 {
    var t: (string, i32) = (wide(k), k);
    return t.0.len();
}
function churn(n: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) { t = t + probe(i); i = i + 1; }
    return t;
}
function main(): i32 {
    var warm: i32 = churn(200);
    var before: i64 = __heap_bump_bytes();
    var again: i32 = churn(200);
    var per: i64 = (__heap_bump_bytes() - before) / 200;
    if (warm != again) { return 98; }
    if (warm <= 0) { return 97; }
    return (per as i32);
}`

func TestX86_64TupleStringElemReclaimed(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, tupleStrElemChurnSrc); code != 0 {
		t.Errorf("tuple string-element churn leaked %d bytes/round on x86-64, want 0", code)
	}
}

func TestArm64TupleStringElemReclaimed(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, tupleStrElemChurnSrc); code != 0 {
		t.Errorf("tuple string-element churn leaked %d bytes/round on arm64, want 0", code)
	}
}

func TestWASMTupleStringElemReclaimed(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	if got := runWasm(t, tupleStrElemChurnSrc); got != 0 {
		t.Errorf("tuple string-element churn leaked %d bytes/round on wasm, want 0", got)
	}
}
