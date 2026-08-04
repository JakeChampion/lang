package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Array self-reassign reclamation — `a = a.append(x)` in a loop.
//
// Before this fix the array reassignment-overwrite in assign() only freed
// the OLD buffer when the local was `freeEligible`. A self-referential RHS
// (`a = a.append(a)`) taints `a` out of that conservative set (the call
// result may alias it), so the overwrite fell through to the catch-all flat
// __fern_rc_dec — which has NO free path — and every push *grow* orphaned
// its old buffer. A build-an-array loop was therefore O(N) heap, not O(1):
// the dominant churn in the self-host SSA build_func loops.
//
// The struct / enum reassignment-overwrite already had the
// selfReassignOwnedLocal escape hatch (the rc-gated deep-drop is balanced
// even when the call result aliases the local); this extends the identical
// reasoning to arrays. Combined with the two-tier freelist (blocks >2048 B
// now reclaim by power-of-two class instead of leaking), a push-grow loop
// over arrays of ANY size is O(1) heap.
//
// build(600) grows its buffer past 2048 B (cap reaches ~766 → 16+766*4 =
// 3080 B), so the probe exercises BOTH the small exact-fit tier and the
// large power-of-two tier. a[0] is always 0 (we append 0,1,2,…), so a
// value-correct run keeps `sum == 0`; a corruption / use-after-free would
// perturb it and trip the sentinel.
func arrSelfReassignSrc(iters string) string {
	return `
function build(k: i32): i32[] {
    var a: i32[] = [];
    var i: i32 = 0;
    while (i < k) { a = a.append(i); i = i + 1; }
    return a;
}
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    var sum: i32 = 0;
    while (j < ` + iters + `) {
        var a: i32[] = build(600);
        sum = sum + a[0];
        j = j + 1;
    }
    if (sum != 0) { return 201; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func TestX86_64ArraySelfReassignReclaim(t *testing.T) {
	ast.RcFreeEnabled = true
	small := mustRunX86_64FreeOn(t, arrSelfReassignSrc("20"))
	large := mustRunX86_64FreeOn(t, arrSelfReassignSrc("400"))
	if small == 201 || large == 201 {
		t.Fatalf("value-incorrect run (a[0] != 0): small=%d large=%d", small, large)
	}
	// Bounded heap: with the old/new buffer freed and the freelist
	// reclaiming every size class, the bump pointer reaches one build's
	// working set and stops — equal for 20 and 400 iterations. A leak
	// would grow the bump with the iteration count (small != large).
	if small != large {
		t.Errorf("array self-reassign push-grow must be O(1) heap, got iters=20 -> %d, iters=400 -> %d (leak)", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero working set for build(600); got 0 (probe not exercising the heap)")
	}
}
