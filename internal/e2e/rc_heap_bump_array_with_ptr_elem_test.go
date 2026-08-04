package e2e

import "testing"

// TestX86_64ArrayWithPtrElemRecycles pins the pointer-element `.with`
// copy-on-write fix. A functional record update `r = upd(r, i, v)` whose
// body is `R { ops: r.ops.with(i, Op { .. }), p: r.p }` copies the ops
// array every iteration (the receiver `r` stays live across the `.with`,
// forcing the CoW copy branch). The array elements are rc-tracked struct
// pointers.
//
// The plain __fern_arr_cow_inplace memcpy'd those pointers WITHOUT an inc,
// so the fresh copy shared the receiver's element boxes at unchanged rc.
// When the previous `r` was dropped it freed those boxes out from under the
// new `r` — a use-after-free that also stranded the old array spine (its
// elements looked shared, so its precise drop mis-accounted), leaving the
// build-and-discard loop growing without bound. The pointer-aware
// __fern_arr_cow_inplace_ptr inc's each copied element (so both arrays own
// it) and emitArraySet drops the overwritten old element, so every
// iteration's old spine + replaced element recycle from the freelist and
// the loop stays flat.
//
// The probe reads __heap_bump_bytes() (bytes handed out but NOT recycled)
// after 100k iterations and asserts it is bounded (< 1 MiB). Leaked, it
// would be ~100k × the array-spine size (many MiB). The value guard
// (r.ops[0].a) additionally catches the UAF corruption directly.
func TestX86_64ArrayWithPtrElemRecycles(t *testing.T) {
	const src = `struct Op { a: i32, c: i32 }
struct R { ops: Op[], p: i32 }
function upd(r: R, i: i32, v: i32): R {
    return R { ops: r.ops.with(i, Op { a: v, c: 7 }), p: r.p };
}
function churn(n: i32): i32 {
    var ops: Op[] = [];
    var k: i32 = 0;
    while (k < 8) { ops = ops.append(Op { a: 0, c: 0 }); k = k + 1; }
    var r: R = R { ops: ops, p: 0 };
    var i: i32 = 0;
    while (i < n) { r = upd(r, i % 8, i); i = i + 1; }
    if (r.ops[0].a == 999999) { return 99; }
    if ((__heap_bump_bytes() as i32) < 1048576) { return 0; }
    return 1;
}
function main(): i32 { return churn(100000); }`
	if _, code := compileAndRunX86_64FreeOn(t, src); code != 0 {
		t.Errorf("pointer-element .with churn: got exit %d, want 0 (heap bump < 1 MiB — copied array spines + replaced elements must recycle)", code)
	}
}
