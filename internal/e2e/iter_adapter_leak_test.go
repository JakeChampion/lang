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
// So these numbers are a GAP LIST, not a contract: they are what the
// refusal currently costs. The fix drives them to zero, and the two
// clean neighbours are the boundary that says a fix went too far.
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
		// 15 = 7 elements x (one ArrayIter + one tuple) + the source
		// array, which the stranded iterators keep alive. Identical
		// across all three because it is the iteration that leaks:
		// `sum` builds no output array at all and still pays 15, and
		// filter's and map's output arrays ARE reclaimed.
		{"filter", iterFilterSrc, 15},
		{"map", iterMapSrc, 15},
		{"sum", iterSumSrc, 15},
		// The boundary. A fix that reaches this row has gone too far.
		{"iter.of alone", iterOfOnlySrc, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := unpairedAllocs(t, gcc, runner, tc.src)
			if got != tc.want {
				t.Errorf("%d unpaired allocation(s), want %d — this is a GAP LIST "+
					"pinned at what the pair-form payload refusal currently costs, so "+
					"a DROP here is the fix landing and wants this number lowered, "+
					"while a RISE is a new leak on the iteration path. The zero row "+
					"is the boundary and must stay zero either way",
					got, tc.want)
			}
		})
	}
}
