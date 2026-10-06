package e2e

import (
	"testing"
)

// Tuple reclamation (RC-Perceus) — the sibling of rc_heap_bump_test.go
// for tuple loop-body vars. A tuple is heap-boxed with an rc header, so
// a `let t = (a, b)` re-declared in a loop reuses one slot across
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

// The string-element sibling (#6879), and the tuple half of the struct fix in
// #6499: the exit sweep's INLINE tuple arm released a native single-word
// string element with a bare __fern_rc_dec, which decrements and never frees,
// so the buffer's count went 1 -> 0 and it was stranded — 64 B a round on
// x86-64 while arm64 and wasm (two-word ABIs, __fern_str_dec) were already
// flat.
//
// The binding has to be in a CALLEE: a loop-scoped `let t` re-declared in the
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
    let t: (string, i32) = (wide(k), k);
    return t.0.len();
}
function churn(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) { t = t + probe(i); i = i + 1; }
    return t;
}
function main(): i32 {
    let warm: i32 = churn(200);
    let before: i64 = __heap_bump_bytes();
    let again: i32 = churn(200);
    let per: i64 = (__heap_bump_bytes() - before) / 200;
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
	if got := runWasm(t, tupleStrElemChurnSrc); got != 0 {
		t.Errorf("tuple string-element churn leaked %d bytes/round on wasm, want 0", got)
	}
}
