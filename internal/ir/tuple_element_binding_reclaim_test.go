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
