package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// --- The core/iter adapters strand the pair-form payload ---------------------
//
// Distinct from the projection leak next door in
// tuple_projection_leak_test.go, which is fixed. That one was a routing
// defect: the overwrite dec reached the non-reclaiming __fern_rc_dec.
// This one is a release that is never emitted at all.
//
// `ArrayIter.next` returns `Option[(T, Self)]`, which is PAIR-FORM — the
// enum has no box, so the tuple the arm binds to `t` is the only heap
// value in play besides the iterator itself. Nothing releases it: traced,
// the tuple boxes carry no inc, no dec, no uniqueness test and no free.
// Everything else follows from that one omission. `cur = t.1` retains the
// fresh iterator, so it sits at rc 2 with the second reference held by the
// tuple nobody drops; at the next overwrite its own drop finds rc 2, is
// not unique, and flat-decs to 1. Stranded one reference short.
//
// The whole combinator library is written on this shape — sum, count,
// fold, map, filter and take all do `Some(t) => { …; cur = t.1; }` — so
// this is the per-element cost of iterating anything, not two adapters.
//
// `reclaimablePairFormPayload` is the machinery for exactly this and
// refuses. Three of its four conditions pass, the binding included; only
// freshness fails, because `returnsNoParamEscape` asks whether anything
// REACHABLE FROM the result aliases a parameter, where releasing the
// buffer needs only whether the result POINTER ITSELF is fresh.
// `next` answers no to the first and yes to the second.
//
// FIXED. freshPairFormEnumResultType now asks whether the returned
// payload BOX is the callee's own, instead of whether anything reachable
// from the result aliases a parameter — and it identifies a user function
// by map membership rather than a "__" name prefix, which had been
// refusing every concrete method along with the builtins it was aimed at.
// The release itself did not change and is still the deep
// emitOwnedSlotDrop: the tuple owns the fresh iterator at rc 1, `cur =
// t.1` retains it to 2, and the deep drop takes it to 1 held by cur. A
// SHALLOW free would leave it at 2 and still leak, which is why the
// obvious analogy to emitOwnedConsumingArmDrop is the wrong one here.
//
// These rows were 15 / 15 / 15. They now pin the REPAIR: a regression in
// the credited case fails here, and so does one that starts crediting a
// shape it should not — the boundary row is in the table rather than
// beside it.
//
// docs/rc-log/2026-08-30-iter-adapter-pair-form-payload.md.

const (
	iterFilterSrc = `
import "core/iter" as iter;
function main(): i32 {
    var xs: i32[] = [5, 2, 8, 1, 4, 9, 6];
    return iter.filter(iter.of(xs), function(x: i32): boolean { return x % 2 == 0; }).len();
}
`
	iterMapSrc = `
import "core/iter" as iter;
function main(): i32 {
    var xs: i32[] = [5, 2, 8, 1, 4, 9, 6];
    return iter.map(iter.of(xs), function(x: i32): i32 { return x + 1; }).len();
}
`
	iterSumSrc = `
import "core/iter" as iter;
function main(): i32 {
    var xs: i32[] = [5, 2, 8, 1, 4, 9, 6];
    return iter.sum(iter.of(xs));
}
`
	// The boundary: no match arm binds the pair, so there is no
	// pair-form payload to strand.
	iterOfOnlySrc = `
import "core/iter" as iter;
function main(): i32 {
    var xs: i32[] = [5, 2, 8, 1, 4, 9, 6];
    var it = iter.of(xs);
    return it.idx + xs.len();
}
`
)

func TestIterAdapterPairFormPayloadLeaksX86_64(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles and runs; not a -short test")
	}
	gcc, runner, ok := e2eharness.LookupX86_64Tooling()
	if !ok {
		t.Skip("no x86-64 toolchain")
	}
	for _, tc := range []struct {
		name string
		src  string
		want int
	}{
		// Was 15 each: 7 elements x (one ArrayIter + one tuple) + the
		// source array the stranded iterators kept alive. Identical
		// across all three because it was the iteration that leaked —
		// `sum` builds no output array at all and still paid 15.
		{"filter", iterFilterSrc, 0},
		{"map", iterMapSrc, 0},
		{"sum", iterSumSrc, 0},
		// The boundary: binds no pair, so there is nothing to release.
		{"iter.of alone", iterOfOnlySrc, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := unpairedAllocs(t, gcc, runner, tc.src)
			if got != tc.want {
				t.Errorf("%d unpaired allocation(s), want %d — an iterator's next "+
					"returns a freshly allocated payload box, and the match arm that "+
					"binds it is meant to release it. A non-zero count here means "+
					"freshPairFormEnumResultType stopped crediting that box, and every "+
					"combinator went back to stranding two allocations per element",
					got, tc.want)
			}
		})
	}
}
