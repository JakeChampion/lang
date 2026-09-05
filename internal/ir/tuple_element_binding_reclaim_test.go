package ir_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// Binding a tuple's STRUCT element to a local — `var q: P = p.1` — incs at the
// binding site. rhsTainted admits that same read out of a STRUCT-typed local as
// a counted alias, on the grounds that the binding incs and the container deep-
// drops its own contents at scope exit; both halves hold for a tuple too, but
// the tuple fell through to the conservative taint. The inc was therefore never
// balanced and the element leaked once per extraction, unbounded, growing with
// the ELEMENT's width (32 B at three fields, 80 B at fifteen) rather than the
// tuple's — which is what identified it.
//
// The allocation-volume half is gated in internal/e2e/alloc_scaling_test.go
// (`tuple-struct-element-binding`, measured 1.99x before and 1.00x after). This
// asserts the emitted release, because volume alone cannot say whether the drop
// landed on the element or somewhere incidental.

const tupleThreadSrc = `struct P { a: i32, b: i32, c: i32 }
function pull(s: P): (i32, P) { return (s.a, P { a: s.a + 1, b: s.b, c: s.c }); }
function churn(n: i32): i32 {
    var t: i32 = 0;
    var s: P = P { a: 0, b: 0, c: 0 };
    var i: i32 = 0;
    while (i < n) {
        var p: (i32, P) = pull(s);
        var q: P = p.1;
        t = t + q.a;
        s = q;
        i = i + 1;
    }
    return t % 7;
}

function main(): i32 { return churn(4); }
`

func callCount(fn *ir.Func, name string) int {
	n := 0
	for _, op := range fn.Ops {
		if op.Kind == ir.OpCallDirect && op.Str == name {
			n++
		}
	}
	return n
}

// cowSeamRetainCount counts COW-seam retains specifically, by their emitted
// SHAPE: emitMapCowRetainTest compares the result handle against the pre-COW
// receiver at pointer width and retains only under that branch.
//
// A plain rcIncCount cannot do this job. The function under test emits other
// retains — the projection's own binding-site inc among them — so counting
// __fern_rc_inc passes whether or not the seam retained anything, which is a
// test that holds for the wrong reason.
func cowSeamRetainCount(fn *ir.Func) int {
	n := 0
	for i := 0; i+3 < len(fn.Ops); i++ {
		if fn.Ops[i].Kind == ir.OpEq && fn.Ops[i].Width == ir.WidthPtr &&
			fn.Ops[i+1].Kind == ir.OpIf &&
			fn.Ops[i+2].Kind == ir.OpLoadLocal &&
			fn.Ops[i+3].Kind == ir.OpRcInc {
			n++
		}
	}
	return n
}

func TestTupleElementBindingIsReclaimed(t *testing.T) {
	fn := funcByName(lowerForTest(t, tupleThreadSrc), "churn")
	// One deep struct drop is the TUPLE's own — __drop_tuple_<…> reclaims its
	// elements, and that one was there before. The locals bound from the
	// element (`q`, and `s` threaded from it) are the ones that were stranded,
	// so the release landing means strictly more than that single drop.
	if got := callCount(fn, "__drop_struct_P"); got < 2 {
		t.Errorf("churn emits %d deep struct drops; the only one is the tuple's own, so the element bound out of it is never released — one leak per extraction", got)
	}
	if callCount(fn, "__drop_tuple__LP_i32_C_P_RP_") == 0 {
		t.Error("churn never drops the tuple itself; this test no longer covers the shape it describes")
	}
}

// A MAP element IS credited — but only in company. The counted-alias argument
// rests on the destination's drop being matched by a reference the container
// genuinely holds, and for the delete tuple that reference did not exist: the
// tuple stored the receiver's handle uncounted, so crediting the projection
// alone made `var m = t.0` on a `(Map, boolean)` deep-free a map the tuple
// still referenced, and segfaulted `map_delete_tuple_churn_free` on both
// natives. The COW-seam retain supplies the count; the two are a pair (#8276).
//
// So this pins BOTH halves, and it is the pairing that has to stay true rather
// than either number on its own:
//
//   - the loop reclaims the map (drops are emitted at all), which crediting
//     the projection is what buys;
//   - the seam retains it (mapCowBindSites reaches a whole-tuple `var`), which
//     is what makes those drops safe.
//
// Drop either and the shape is a use-after-free again, so a future change that
// removes one must fail here rather than quietly restore the 2026-09 segfault.
// The e2e corpus covers the crash and the bytes; this covers the DECISION, in
// a hundredth of the time.
func TestTupleMapElementIsCreditedWithTheSeamRetain(t *testing.T) {
	const src = `
import "core/int";
import "core/map";

function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var m: Map[i32, i32] = map_new(8);
        m = m.insert(1, 4);
        var t: (Map[i32, i32], boolean) = m.without(1);
        m = t.0;
        if (t.1) { acc = acc + 1; }
        i = i + 1;
    }
    return acc % 7;
}

function main(): i32 { return churn(4); }
`
	fn := funcByName(lowerForTest(t, src), "churn")

	// Half one: the map is released. Before #8276 this was zero — the
	// projection was refused ownership, so nothing in the loop ever dropped
	// the map and the whole table leaked once an iteration.
	if n := callCount(fn, "__fern_map_drop"); n == 0 {
		t.Error("churn emits no map drops: the tuple element is not credited, so the loop leaks its map every iteration (#8434)")
	}

	// Half two: the seam retained it, which is the only thing making half one
	// safe. Matched by SHAPE rather than by counting retains — see
	// cowSeamRetainCount for why a bare __fern_rc_inc count passes either way. `__map_cow_inplace` hands the receiver's own handle back on the
	// in-place branch, so without this the drops above free a live map.
	if n := cowSeamRetainCount(fn); n == 0 {
		t.Error("churn emits no retain: the delete tuple holds the receiver's handle uncounted, so the map drops above are a use-after-free — the COW-seam retain (mapCowBindSites) is what pairs with the credit (#8276)")
	}
}
